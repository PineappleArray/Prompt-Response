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

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/middleware"
	"prompt-response/internal/poller"
	"prompt-response/internal/proxy"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"
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

	handler := proxy.New(scor, cls, cfg)

	wrapped := middleware.RequestID(
		middleware.RequestTimeout(30*time.Second,
			middleware.MaxBodySize(1<<20, handler),
		),
	)

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
