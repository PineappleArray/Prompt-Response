package scorer

import (
	"log/slog"
	"math"
	"sort"
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
// the requested tier. If no healthy replica exists at the requested tier,
// escalates to tiers with higher priority (more compute). Never downgrades.
// Replicas in the excluded set or with an open circuit breaker are skipped.
func (s *Scorer) Pick(prefixHash uint64, tier types.ModelTier, cc CircuitChecker, excluded map[string]bool) config.Replica {
	affinityID, hasAffinity := s.store.GetAffinity(prefixHash)
	states := s.poller.Snapshot()

	// Build ascending-priority tier list starting from the requested tier.
	// We only escalate upward (higher compute), never downgrade.
	tiersToTry := s.tiersAtOrAbove(tier)

	for _, t := range tiersToTry {
		var best config.Replica
		bestScore := -1.0
		for _, r := range s.replicas {
			if r.Tier != t {
				continue
			}
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
			score := s.scoreReplica(r, hit, state)
			if score > bestScore {
				bestScore = score
				best = r
			}
		}
		if best.ID != "" {
			if t != tier {
				slog.Warn("no replicas at requested tier, escalating to higher tier",
					"requested_tier", tier,
					"actual_tier", t,
				)
			}
			return best
		}
	}

	return config.Replica{} // all tiers at or above requested tier are down
}

// tiersAtOrAbove returns tier names in ascending priority order, starting
// with the requested tier and only including tiers at or above it.
// This ensures fallback always escalates to more capable models.
//
// When no replica carries a non-zero TierCfg.Priority (e.g. in unit tests that
// construct bare config.Replica values), escalation is disabled and only the
// exact requested tier is returned.
func (s *Scorer) tiersAtOrAbove(tier types.ModelTier) []types.ModelTier {
	type tierPri struct {
		name     types.ModelTier
		priority int
	}

	// Collect unique tiers with their priorities.
	seen := make(map[types.ModelTier]int)
	requestedPri := 0
	for _, r := range s.replicas {
		if _, ok := seen[r.Tier]; !ok {
			seen[r.Tier] = r.TierCfg.Priority
		}
		if r.Tier == tier && r.TierCfg.Priority > requestedPri {
			requestedPri = r.TierCfg.Priority
		}
	}

	// If no priority information is available (all zeros), escalation cannot
	// be performed safely — just try the exact requested tier.
	if requestedPri == 0 {
		return []types.ModelTier{tier}
	}

	// Filter to tiers at or above the requested priority.
	var candidates []tierPri
	for name, pri := range seen {
		if pri >= requestedPri {
			candidates = append(candidates, tierPri{name, pri})
		}
	}

	// Sort ascending by priority so we try the closest tier first.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return string(candidates[i].name) < string(candidates[j].name)
	})

	result := make([]types.ModelTier, len(candidates))
	for i, t := range candidates {
		result[i] = t.name
	}
	return result
}

// scoreReplica computes a selection score for a replica given its current state.
func (s *Scorer) scoreReplica(r config.Replica, hit bool, state poller.State) float64 {
	return s.score(hit, state.QueueDepth, state.KVCacheUtil)
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
	missPenalty := s.weights.MissPenalty
	if hit {
		cacheScore = 1.0
		missPenalty = 0
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
		s.weights.Baseline*0.5 -
		missPenalty
}
