package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"forge.sarak.sh/saraksh/stalwart-jev/internal/jev"
)

type fake struct {
	got jev.Email
	v   jev.Verdict
	err error
}

func (f *fake) Classify(_ context.Context, e jev.Email) (jev.Verdict, error) {
	f.got = e
	return f.v, f.err
}

func handler(t *testing.T, f *fake) *Handler {
	t.Helper()
	h, err := NewHandler(f, 0.8, 0.5, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return h
}

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))
	return rec
}

// TestServeHTTP pins the response fields Stalwart refuses to parse without:
// id, object, created, model, and per choice index, finish_reason, role.
func TestServeHTTP(t *testing.T) {
	f := &fake{v: jev.Verdict{Category: "Harmful", Confidence: 0.93, Probabilities: map[string]float64{"Harmful": 0.951}}}
	rec := post(handler(t, f), `{"model":"jev","messages":[{"role":"user","content":"Classify.\n\nSubject: Win\n\nClick here"}],"temperature":0.5}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, jev.Email{Subject: "Win", Body: "Click here"}, f.got)

	var r map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	for _, k := range []string{"id", "object", "created", "model"} {
		require.Contains(t, r, k)
	}
	require.Equal(t, "jev", r["model"])
	c := r["choices"].([]any)[0].(map[string]any)
	require.Equal(t, float64(0), c["index"])
	require.Equal(t, "stop", c["finish_reason"])
	require.Equal(t, map[string]any{"role": "assistant", "content": "Harmful,High,p=0.95"}, c["message"])
}

func TestServeHTTPFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		method string
		body   string
		err    error
		code   int
	}{
		{"classifier error", http.MethodPost, `{"messages":[{"content":"x"}]}`, errors.New("boom"), http.StatusBadGateway},
		{"no messages", http.MethodPost, `{"messages":[]}`, nil, http.StatusBadRequest},
		{"not json", http.MethodPost, `nope`, nil, http.StatusBadRequest},
		{"get", http.MethodGet, ``, nil, http.StatusMethodNotAllowed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler(t, &fake{err: tt.err}).ServeHTTP(rec, httptest.NewRequest(tt.method, "/", strings.NewReader(tt.body)))
			require.Equal(t, tt.code, rec.Code)
		})
	}
}

func TestLevel(t *testing.T) {
	for c, want := range map[float64]string{0.99: "High", 0.8: "High", 0.79: "Medium", 0.5: "Medium", 0.49: "Low", 0: "Low"} {
		require.Equal(t, want, Level(c, 0.8, 0.5), "%v", c)
	}
}

func TestParseContent(t *testing.T) {
	for content, want := range map[string]jev.Email{
		"P.\n\nSubject: Hello\n\nBody\n\nmore": {Subject: "Hello", Body: "Body\n\nmore"},
		"P.\n\nSubject: \n\nBody":              {Subject: "", Body: "Body"},
		"P.\n\nSubject: Only":                  {Subject: "Only"},
		"no marker":                            {Body: "no marker"},
	} {
		require.Equal(t, want, ParseContent(content), "%q", content)
	}
}
