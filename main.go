package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfgPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		cfgPath = p
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := store.NewRedis(cfg.Redis.Addr)

	poll := poller.New(cfg.Replicas)
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
			Length:       0.20,
			Code:         0.30,
			Reasoning:    0.15,
			Complexity:   0.10,
			ConvDepth:    0.10,
			OutputLength: 0.15,
		},
		Threshold: cfg.Threshold,
	})

	handler := proxy.New(scor, cls, cfg)

	// middleware chain: request ID → timeout → body size limit → handler
	wrapped := middleware.RequestID(
		middleware.RequestTimeout(30*time.Second,
			middleware.MaxBodySize(1<<20, handler),
		),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", wrapped)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	go func() {
		slog.Info("router listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}
