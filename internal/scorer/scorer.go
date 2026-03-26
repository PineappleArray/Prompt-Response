package scorer

import (
	"math"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/store"
)

const maxQueue = 20.0

type Scorer struct {
	replicas []config.Replica
	store    store.Store
	poller   *poller.Poller
	weights  config.Weights
	ttl      time.Duration
}

func New(
	replicas []config.Replica,
	store store.Store,
	poller *poller.Poller,
	weights config.Weights,
	ttl time.Duration,
) *Scorer {
	return &Scorer{
		replicas: replicas,
		store:    store,
		poller:   poller,
		weights:  weights,
		ttl:      ttl,
	}
}

func (s *Scorer) Pick(prefixHash uint64) config.Replica {
	affinityID, hasAffinity := s.store.GetAffinity(prefixHash)
	states := s.poller.Snapshot()

	var best config.Replica
	bestScore := -1.0

	for _, r := range s.replicas {
		state, ok := states[r.ID]
		if !ok || !state.Healthy {
			continue
		}

		hit := hasAffinity && affinityID == r.ID
		score := s.score(hit, state.QueueDepth)

		if score > bestScore {
			bestScore = score
			best = r
		}
	}

	return best
}

func (s *Scorer) RecordHit(prefixHash uint64, replicaID string) {
	s.store.SetAffinity(prefixHash, replicaID, s.ttl)
}

func (s *Scorer) score(hit bool, queueDepth int) float64 {
	cacheScore := 0.0
	if hit {
		cacheScore = 1.0
	}
	queueScore := math.Max(0, 1-float64(queueDepth)/maxQueue)
	return s.weights.W1*cacheScore + s.weights.W2*queueScore + s.weights.W3*0.5
}