package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"prompt-response/internal/audit"
	"prompt-response/internal/auth"
	"prompt-response/internal/circuit"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/middleware"
	"prompt-response/internal/poller"
	"prompt-response/internal/proxy"
	"prompt-response/internal/ratelimit"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("starting prompt-response router",
		"version", version,
		"go_version", runtime.Version(),
		"build_time", buildTime,
	)

	cfgPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	slog.Info("config loaded",
		"replicas", len(cfg.Replicas),
		"threshold", cfg.Threshold,
		"affinity_ttl", cfg.AffinityTTL.String(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := store.NewRedis(cfg.Redis.Addr)

	// verify Redis connectivity at startup
	pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx); err != nil {
		slog.Warn("redis not available at startup, affinity cache will be degraded", "addr", cfg.Redis.Addr, "err", err)
	} else {
		slog.Info("redis connected", "addr", cfg.Redis.Addr)
	}

	poll := poller.New(cfg.Replicas, cfg.PollInterval)
	poll.Start(ctx)

	scor := scorer.New(
		cfg.Replicas,
		rdb,
		poll,
		cfg.Weights,
		cfg.AffinityTTL,
		cfg.MaxQueue,
	)

	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights: classifier.SignalWeights{
			Length:       cfg.Classifier.Length,
			Code:         cfg.Classifier.Code,
			Reasoning:    cfg.Classifier.Reasoning,
			Complexity:   cfg.Classifier.Complexity,
			ConvDepth:    cfg.Classifier.ConvDepth,
			OutputLength: cfg.Classifier.OutputLength,
		},
		Threshold: cfg.Threshold,
	})

	cb := circuit.NewRegistry(circuit.Config{
		ErrorThreshold: cfg.Circuit.ErrorThreshold,
		WindowSize:     cfg.Circuit.WindowSize,
		Cooldown:       cfg.Circuit.Cooldown,
		MinSamples:     cfg.Circuit.MinSamples,
	})
	slog.Info("circuit breaker initialized",
		"error_threshold", cfg.Circuit.ErrorThreshold,
		"window_size", cfg.Circuit.WindowSize.String(),
		"cooldown", cfg.Circuit.Cooldown.String(),
		"max_retries", cfg.Retry.MaxRetries,
	)

	var trail *audit.Trail
	if cfg.Audit.Enabled {
		trail = audit.NewTrail(cfg.Audit.BufferSize)
		slog.Info("audit trail enabled", "buffer_size", cfg.Audit.BufferSize)
	}

	handler := proxy.New(scor, cls, cfg, cb, trail)

	// Build middleware chain: inner → timeout → body limit → [ratelimit] → [auth] → request ID
	var inner http.Handler = middleware.RequestTimeout(30*time.Second,
		middleware.MaxBodySize(1<<20, handler),
	)

	if cfg.RateLimit.Enabled {
		rl := ratelimit.NewRegistry(ratelimit.Config{
			RequestsPerMinute: cfg.RateLimit.RequestsPerMinute,
			Burst:             cfg.RateLimit.Burst,
		})
		inner = ratelimit.Middleware(rl)(inner)
		slog.Info("rate limiting enabled",
			"requests_per_minute", cfg.RateLimit.RequestsPerMinute,
			"burst", cfg.RateLimit.Burst,
		)
	}

	if cfg.Auth.Enabled {
		entries := make([]auth.KeyEntry, len(cfg.Auth.Keys))
		for i, k := range cfg.Auth.Keys {
			entries[i] = auth.KeyEntry{Key: k.Key, Tenant: k.Tenant}
		}
		ks := auth.NewKeystore(entries)
		inner = auth.Middleware(ks)(inner)
		slog.Info("authentication enabled", "keys", ks.Len())
	}

	wrapped := middleware.RequestID(inner)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", wrapped)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("router listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully", "timeout", "10s")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("shutdown complete")
}

func init() {
	if v := os.Getenv("VERSION"); v != "" {
		version = v
	}
	if bt := os.Getenv("BUILD_TIME"); bt != "" {
		buildTime = bt
	}
}
