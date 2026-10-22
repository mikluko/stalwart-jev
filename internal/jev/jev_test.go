package jev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func client(url string) *Client {
	return &Client{URL: url, Model: "jev-latest", Key: "k", HTTP: http.DefaultClient, MaxBody: 8, Retries: 2, Backoff: time.Millisecond}
}

func TestClassify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer k", r.Header.Get("Authorization"))
		var req request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "jev-latest", req.Model)
		require.Equal(t, Email{Subject: "Hi", Body: "abcdefgh"}, req.State, "body cut to MaxBody")
		q := req.Questions["category"]
		require.Equal(t, "choice", q.Type)
		require.Equal(t, Categories, q.Criteria)
		_, _ = w.Write([]byte(`{"answers":{"category":{"type":"choice","choice":"Harmful","confidence":0.93,"probabilities":{"Harmful":0.95,"Unsolicited":0.05}}},"usage":{"input_tokens":445}}`))
	}))
	defer srv.Close()

	v, err := client(srv.URL).Classify(context.Background(), Email{Subject: "Hi", Body: "abcdefghijk"})
	require.NoError(t, err)
	require.Equal(t, Verdict{Category: "Harmful", Confidence: 0.93, Probabilities: map[string]float64{"Harmful": 0.95, "Unsolicited": 0.05}, InputTokens: 445}, v)
}

func TestClassifyUnknownChoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"category":{"choice":"Other","confidence":1}}}`))
	}))
	defer srv.Close()
	_, err := client(srv.URL).Classify(context.Background(), Email{})
	require.ErrorContains(t, err, `unknown choice "Other"`)
}

func TestClassifyRetries(t *testing.T) {
	for _, tt := range []struct {
		name  string
		codes []int
		calls int32
		want  int
	}{
		{"429 then success", []int{429, 200}, 2, 0},
		{"529 exhausts retries", []int{529, 529, 529, 529}, 3, 529},
		{"400 is not retried", []int{400, 200}, 1, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				code := tt.codes[n.Add(1)-1]
				if code != 200 {
					http.Error(w, "nope", code)
					return
				}
				_, _ = w.Write([]byte(`{"answers":{"category":{"choice":"Legitimate","confidence":1}}}`))
			}))
			defer srv.Close()
			_, err := client(srv.URL).Classify(context.Background(), Email{})
			require.Equal(t, tt.calls, n.Load())
			if tt.want == 0 {
				require.NoError(t, err)
				return
			}
			var se *StatusError
			require.True(t, errors.As(err, &se))
			require.Equal(t, tt.want, se.Code)
		})
	}
}

func TestTruncate(t *testing.T) {
	require.Equal(t, "abc", truncate("abc", 0))
	require.Equal(t, "abc", truncate("abc", 5))
	require.Equal(t, "ab", truncate("abc", 2))
	require.Equal(t, "€", truncate("€€", 5), "a rune is never split")
	require.Equal(t, "", truncate("€€", 2))
}
