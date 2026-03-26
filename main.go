package main

import (
	"log"
	"net/http"

	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/proxy"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// store
	rdb := store.NewRedis(cfg.Redis.Addr)

	// poller
	poll := poller.New(cfg.Replicas)
	poll.Start()

	// scorer
	scor := scorer.New(
		cfg.Replicas,
		rdb,
		poll,
		cfg.Weights,
		cfg.AffinityTTL,
	)

	// classifier
	cls := classifier.NewHeuristic(classifier.HeuristicConfig{
		Weights: classifier.SignalWeights{
			Length:     0.25,
			Code:       0.35,
			Reasoning:  0.25,
			Complexity: 0.15,
		},
		Threshold: cfg.Threshold,
	})

	// proxy handler
	handler := proxy.New(scor, cls, cfg)

	log.Println("router listening on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}