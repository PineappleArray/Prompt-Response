package scorer

import (
	"testing"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/store"
	"prompt-response/internal/types"
)

func BenchmarkPick(b *testing.B) {
	replicas := []config.Replica{
		{ID: "s1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "s2", URL: "http://s2", Tier: types.TierSmall},
		{ID: "l1", URL: "http://l1", Tier: types.TierLarge},
		{ID: "l2", URL: "http://l2", Tier: types.TierLarge},
	}
	mem := store.NewMemory()
	mem.SetAffinity(42, "s1", 5*time.Minute)
	poll := poller.New(replicas, 0)
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Pick(42, types.TierSmall, nil, nil)
	}
}

func BenchmarkScore(b *testing.B) {
	s := &Scorer{
		weights:  defaultWeights(),
		maxQueue: 20,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.score(true, 5, 0.45)
	}
}
