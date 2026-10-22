// Command stalwart-jev answers Stalwart's LLM spam classifier from TypeSafe
// Jev, posing as an OpenAI chat-completions endpoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"forge.sarak.sh/saraksh/stalwart-jev/internal/chat"
	"forge.sarak.sh/saraksh/stalwart-jev/internal/jev"
	"forge.sarak.sh/saraksh/stalwart-jev/internal/telemetry"
)

func main() {
	log := slog.New(telemetry.Handler{Handler: slog.NewJSONHandler(os.Stdout, nil)})
	if err := run(log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdown, err := telemetry.Setup(ctx, "stalwart-jev", log)
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer telemetry.Flush(ctx, shutdown, log)

	client := &jev.Client{
		URL:     cfg.url,
		Model:   cfg.model,
		Key:     cfg.key,
		HTTP:    &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport), Timeout: cfg.timeout},
		MaxBody: cfg.maxBody,
		Retries: 2,
		Backoff: 250 * time.Millisecond,
	}
	h, err := chat.NewHandler(client, cfg.high, cfg.medium, log)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", h)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           otelhttp.NewHandler(mux, "stalwart-jev", otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" })),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.listen, "model", cfg.model)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type config struct {
	listen, url, model, key string
	maxBody                 int
	high, medium            float64
	timeout                 time.Duration
}

// loadConfig reads the configuration from the environment; TYPESAFE_API_KEY
// is required, everything else has a default.
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		listen:  or(getenv("LISTEN_ADDR"), ":8080"),
		url:     or(getenv("TYPESAFE_URL"), "https://api.typesafe.ai/v1/systemone"),
		model:   or(getenv("TYPESAFE_MODEL"), "jev-latest"),
		key:     getenv("TYPESAFE_API_KEY"),
		maxBody: 32 << 10,
		high:    0.8,
		medium:  0.5,
		timeout: 20 * time.Second,
	}
	if c.key == "" {
		return c, errors.New("TYPESAFE_API_KEY is not set")
	}
	var err error
	if v := getenv("MAX_BODY_BYTES"); v != "" {
		if c.maxBody, err = strconv.Atoi(v); err != nil {
			return c, fmt.Errorf("MAX_BODY_BYTES: %w", err)
		}
	}
	if v := getenv("CONFIDENCE_HIGH"); v != "" {
		if c.high, err = strconv.ParseFloat(v, 64); err != nil {
			return c, fmt.Errorf("CONFIDENCE_HIGH: %w", err)
		}
	}
	if v := getenv("CONFIDENCE_MEDIUM"); v != "" {
		if c.medium, err = strconv.ParseFloat(v, 64); err != nil {
			return c, fmt.Errorf("CONFIDENCE_MEDIUM: %w", err)
		}
	}
	if v := getenv("TYPESAFE_TIMEOUT"); v != "" {
		if c.timeout, err = time.ParseDuration(v); err != nil {
			return c, fmt.Errorf("TYPESAFE_TIMEOUT: %w", err)
		}
	}
	if c.medium > c.high {
		return c, fmt.Errorf("CONFIDENCE_MEDIUM %v is above CONFIDENCE_HIGH %v", c.medium, c.high)
	}
	return c, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
