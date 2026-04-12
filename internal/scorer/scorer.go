package scorer

import (
	"log/slog"
	"math"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/store"
	"prompt-response/internal/types"
)

// CircuitChecker determines if a replica should receive traffic.
// Returns true if the circuit is closed (healthy) or half-open (probing).
type CircuitChecker interface {
	Allow(replicaID string) bool
}

type Scorer struct {
	replicas []config.Replica
	store    store.Store
	poller   *poller.Poller
	weights  config.Weights
	ttl      time.Duration
	maxQueue float64
}

func New(
	replicas []config.Replica,
	store store.Store,
	poller *poller.Poller,
	weights config.Weights,
	ttl time.Duration,
	maxQueue float64,
) *Scorer {
	return &Scorer{
		replicas: replicas,
		store:    store,
		poller:   poller,
		weights:  weights,
		ttl:      ttl,
		maxQueue: maxQueue,
	}
}

// Pick selects the best replica for a request, preferring replicas that match
// the requested tier. Falls back to any healthy replica if no tier match exists.
// Replicas in the excluded set or with an open circuit breaker are skipped.
func (s *Scorer) Pick(prefixHash uint64, tier types.ModelTier, cc CircuitChecker, excluded map[string]bool) config.Replica {
	affinityID, hasAffinity := s.store.GetAffinity(prefixHash)
	states := s.poller.Snapshot()

	var best config.Replica
	bestScore := -1.0
	var fallback config.Replica
	fallbackScore := -1.0

	for _, r := range s.replicas {
		state, ok := states[r.ID]
		if !ok || !state.Healthy {
			continue
		}
		if excluded != nil && excluded[r.ID] {
			continue
		}
		if cc != nil && !cc.Allow(r.ID) {
			continue
		}

		hit := hasAffinity && affinityID == r.ID
		score := s.score(hit, state.QueueDepth, state.KVCacheUtil)

		if r.Tier == tier {
			if score > bestScore {
				bestScore = score
				best = r
			}
		} else if score > fallbackScore {
			fallbackScore = score
			fallback = r
		}
	}

	if best.ID != "" {
		return best
	}
	if fallback.ID != "" {
		slog.Warn("no tier-matched replica, falling back",
			"requested_tier", tier,
			"fallback_replica", fallback.ID,
			"fallback_tier", fallback.Tier,
		)
	}
	return fallback
}

func (s *Scorer) RecordHit(prefixHash uint64, replicaID string) {
	s.store.SetAffinity(prefixHash, replicaID, s.ttl)
}

func (s *Scorer) Store() store.Store {
	return s.store
}

func (s *Scorer) PollerSnapshot() map[string]poller.State {
	return s.poller.Snapshot()
}

func (s *Scorer) score(hit bool, queueDepth int, kvCacheUtil float64) float64 {
	cacheScore := 0.0
	if hit {
		cacheScore = 1.0
	}
	queueScore := math.Max(0, 1-float64(queueDepth)/s.maxQueue)

	// GPU KV cache pressure: penalize replicas nearing cache exhaustion.
	// At 90%+ utilization, vLLM evicts cached prefixes and preempts running
	// requests. Routing more traffic to a pressured replica destroys the
	// prefix cache hits that the affinity system worked to build.
	kvPressureScore := math.Max(0, 1.0-kvCacheUtil)

	return s.weights.CacheAffinity*cacheScore +
		s.weights.QueueDepth*queueScore +
		s.weights.KVCachePressure*kvPressureScore +
		s.weights.Baseline*0.5
}
