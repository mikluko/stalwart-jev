// Package jev classifies email into Stalwart's spam categories with a TypeSafe
// System One Choice question.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"
)

// Categories are the options of the Choice question, named as Stalwart's
// SpamLlm expects them by default.
var Categories = map[string]string{
	"Unsolicited": "Spam the recipient never asked for: scams, bulk junk, unknown senders pushing offers.",
	"Commercial":  "Marketing or newsletters from a business the recipient plausibly has a relationship with.",
	"Harmful":     "Phishing, credential theft, malware, fraud or extortion.",
	"Legitimate":  "Personal, transactional or otherwise wanted mail.",
}

const instructions = "Classify this email received by a personal mailbox."

// Email is the part of a message the classifier sees.
type Email struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// Verdict is the answer to the Choice question.
type Verdict struct {
	Category      string
	Confidence    float64
	Probabilities map[string]float64
	InputTokens   int64
}

// StatusError is a non-2xx answer from the API.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("typesafe: status %d: %s", e.Code, e.Body)
}

// Client asks the TypeSafe System One API. It is safe for concurrent use.
type Client struct {
	URL   string
	Model string
	Key   string
	HTTP  *http.Client
	// MaxBody caps the body sent, in bytes, cut at a rune boundary.
	MaxBody int
	// Retries is how many times a 429 or 529 is retried, with doubling
	// backoff from Backoff.
	Retries int
	Backoff time.Duration
}

type request struct {
	Model     string              `json:"model"`
	State     Email               `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type response struct {
	Answers map[string]struct {
		Choice        string             `json:"choice"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	} `json:"answers"`
	Usage struct {
		InputTokens int64 `json:"input_tokens"`
	} `json:"usage"`
}

// Classify returns the category of e. Errors from the API are *StatusError.
func (c *Client) Classify(ctx context.Context, e Email) (Verdict, error) {
	e.Body = truncate(e.Body, c.MaxBody)
	body, err := json.Marshal(request{
		Model: c.Model,
		State: e,
		Questions: map[string]question{
			"category": {Type: "choice", Instructions: instructions, Criteria: Categories},
		},
	})
	if err != nil {
		return Verdict{}, err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return Verdict{}, err
	}
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return Verdict{}, fmt.Errorf("typesafe: decode: %w", err)
	}
	a, ok := r.Answers["category"]
	if !ok {
		return Verdict{}, fmt.Errorf("typesafe: no answer to category")
	}
	if _, ok := Categories[a.Choice]; !ok {
		return Verdict{}, fmt.Errorf("typesafe: unknown choice %q", a.Choice)
	}
	return Verdict{
		Category:      a.Choice,
		Confidence:    a.Confidence,
		Probabilities: a.Probabilities,
		InputTokens:   r.Usage.InputTokens,
	}, nil
}

func (c *Client) post(ctx context.Context, body []byte) ([]byte, error) {
	backoff := c.Backoff
	for attempt := 0; ; attempt++ {
		raw, err := c.once(ctx, body)
		var se *StatusError
		if err == nil || attempt >= c.Retries || !errors.As(err, &se) || !retryable(se.Code) {
			return raw, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
}

func (c *Client) once(ctx context.Context, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &StatusError{Code: resp.StatusCode, Body: string(raw)}
	}
	return raw, nil
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code == 529
}

// truncate cuts s to at most n bytes without splitting a rune; n <= 0 keeps s.
func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
