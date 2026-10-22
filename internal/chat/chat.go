// Package chat serves the OpenAI chat-completions subset that Stalwart's
// SpamLlm calls, answering from a Classifier.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/mikluko/stalwart-jev/internal/jev"
)

// Classifier returns the spam category of an email.
type Classifier interface {
	Classify(ctx context.Context, e jev.Email) (jev.Verdict, error)
}

// Handler answers POST requests in the chat-completions shape with
// "<Category>,<High|Medium|Low>,p=<probability>". Any failure is a non-2xx
// status, on which Stalwart leaves the message untagged.
type Handler struct {
	Classifier Classifier
	// High and Medium are the lowest Choice confidences reported as High and
	// Medium; anything below Medium is Low.
	High   float64
	Medium float64
	Log    *slog.Logger

	verdicts metric.Int64Counter
	failures metric.Int64Counter
	conf     metric.Float64Histogram
	tokens   metric.Int64Counter
}

// NewHandler returns a Handler with its instruments registered on the global
// meter provider.
func NewHandler(c Classifier, high, medium float64, log *slog.Logger) (*Handler, error) {
	m := otel.Meter("github.com/mikluko/stalwart-jev")
	h := &Handler{Classifier: c, High: high, Medium: medium, Log: log}
	var err error
	if h.verdicts, err = m.Int64Counter("stalwart_jev.verdicts", metric.WithDescription("Messages classified, by category and confidence level")); err != nil {
		return nil, err
	}
	if h.failures, err = m.Int64Counter("stalwart_jev.failures", metric.WithDescription("Requests answered without a verdict, by reason")); err != nil {
		return nil, err
	}
	if h.conf, err = m.Float64Histogram("stalwart_jev.confidence", metric.WithDescription("Choice confidence of verdicts"),
		metric.WithExplicitBucketBoundaries(0.3, 0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 0.99)); err != nil {
		return nil, err
	}
	if h.tokens, err = m.Int64Counter("stalwart_jev.input_tokens", metric.WithDescription("Billable input tokens spent")); err != nil {
		return nil, err
	}
	return h, nil
}

type request struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type choice struct {
	Index        int     `json:"index"`
	FinishReason string  `json:"finish_reason"`
	Message      message `json:"message"`
}

type response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
		h.fail(ctx, w, "bad_request", http.StatusBadRequest, fmt.Errorf("decode request: %v", err))
		return
	}
	e := ParseContent(req.Messages[len(req.Messages)-1].Content)
	v, err := h.Classifier.Classify(ctx, e)
	if err != nil {
		h.fail(ctx, w, "classifier", http.StatusBadGateway, err)
		return
	}
	level := Level(v.Confidence, h.High, h.Medium)
	h.record(ctx, v, level)
	content := fmt.Sprintf("%s,%s,p=%.2f", v.Category, level, v.Probabilities[v.Category])
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		ID:      "chatcmpl-" + trace.SpanContextFromContext(ctx).SpanID().String(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []choice{{FinishReason: "stop", Message: message{Role: "assistant", Content: content}}},
	})
}

func (h *Handler) record(ctx context.Context, v jev.Verdict, level string) {
	attrs := []attribute.KeyValue{attribute.String("category", v.Category), attribute.String("confidence", level)}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("spam.category", v.Category),
		attribute.String("spam.confidence", level),
		attribute.Float64("spam.choice_confidence", v.Confidence),
	)
	h.verdicts.Add(ctx, 1, metric.WithAttributes(attrs...))
	h.conf.Record(ctx, v.Confidence, metric.WithAttributes(attribute.String("category", v.Category)))
	h.tokens.Add(ctx, v.InputTokens)
	h.Log.InfoContext(ctx, "verdict", "category", v.Category, "confidence", v.Confidence, "level", level, "input_tokens", v.InputTokens)
}

func (h *Handler) fail(ctx context.Context, w http.ResponseWriter, reason string, code int, err error) {
	h.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
	trace.SpanFromContext(ctx).RecordError(err)
	h.Log.WarnContext(ctx, "no verdict", "reason", reason, "err", err)
	http.Error(w, http.StatusText(code), code)
}

// Level maps a Choice confidence to Stalwart's confidence names.
func Level(confidence, high, medium float64) string {
	switch {
	case confidence >= high:
		return "High"
	case confidence >= medium:
		return "Medium"
	default:
		return "Low"
	}
}

// ParseContent splits the prompt Stalwart builds,
// "<prompt>\n\nSubject: <subject>\n\n<body>", into subject and body. The
// configured prompt must not contain "\n\nSubject: ". Content without that
// marker is taken whole as the body.
func ParseContent(content string) jev.Email {
	const marker = "\n\nSubject: "
	i := strings.Index(content, marker)
	if i < 0 {
		return jev.Email{Body: content}
	}
	rest := content[i+len(marker):]
	subject, body, _ := strings.Cut(rest, "\n\n")
	return jev.Email{Subject: subject, Body: body}
}
