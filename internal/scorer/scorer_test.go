package scorer

import (
	"testing"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/store"
	"prompt-response/internal/types"
)

func defaultWeights() config.Weights {
	return config.Weights{
		CacheAffinity:   0.50,
		QueueDepth:      0.25,
		KVCachePressure: 0.15,
		Baseline:        0.10,
	}
}

type stubPoller struct {
	states map[string]poller.State
}

func (s *stubPoller) Snapshot() map[string]poller.State { return s.states }

func TestPick_TierMatchedPreferred(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "large-1", URL: "http://l1", Tier: types.TierLarge},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)

	// Manually set both healthy with same queue
	snap := poll.Snapshot()
	_ = snap // poller starts all healthy

	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)
	got := s.Pick(123, types.TierSmall, nil, nil)
	if got.ID != "small-1" {
		t.Errorf("expected small-1, got %s", got.ID)
	}

	got = s.Pick(123, types.TierLarge, nil, nil)
	if got.ID != "large-1" {
		t.Errorf("expected large-1, got %s", got.ID)
	}
}

func TestPick_FallbackWhenNoTierMatch(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)

	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)
	got := s.Pick(123, types.TierLarge, nil, nil) // no large tier exists
	if got.ID != "small-1" {
		t.Errorf("should fall back to small-1, got %s", got.ID)
	}
}

func TestPick_UnhealthySkipped(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "small-2", URL: "http://s2", Tier: types.TierSmall},
	}
	poll := poller.New(replicas, 0)

	// Mark small-1 as unhealthy via 3 failures
	for i := 0; i < 3; i++ {
		poll.SimulateFailure("small-1")
	}

	mem := store.NewMemory()
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)
	got := s.Pick(123, types.TierSmall, nil, nil)
	if got.ID != "small-2" {
		t.Errorf("expected small-2 (small-1 unhealthy), got %s", got.ID)
	}
}

func TestPick_CacheAffinityWins(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "small-2", URL: "http://s2", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	mem.SetAffinity(999, "small-2", 5*time.Minute)
	poll := poller.New(replicas, 0)

	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)
	got := s.Pick(999, types.TierSmall, nil, nil)
	if got.ID != "small-2" {
		t.Errorf("expected small-2 (cache affinity), got %s", got.ID)
	}
}

func TestPick_HighKVCachePressurePenalized(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "small-2", URL: "http://s2", Tier: types.TierSmall},
	}
	poll := poller.New(replicas, 0)

	// small-1 has cache affinity but 95% KV cache pressure
	poll.SetState("small-1", poller.State{Healthy: true, QueueDepth: 0, KVCacheUtil: 0.95})
	// small-2 has no affinity but low pressure
	poll.SetState("small-2", poller.State{Healthy: true, QueueDepth: 0, KVCacheUtil: 0.10})

	mem := store.NewMemory()
	mem.SetAffinity(999, "small-1", 5*time.Minute)

	// With heavy KV pressure weight, small-2 should win despite no affinity
	heavyKVWeights := config.Weights{
		CacheAffinity:   0.30,
		QueueDepth:      0.10,
		KVCachePressure: 0.50, // emphasize KV pressure
		Baseline:        0.10,
	}
	s := New(replicas, mem, poll, heavyKVWeights, 5*time.Minute, 20)
	got := s.Pick(999, types.TierSmall, nil, nil)
	if got.ID != "small-2" {
		t.Errorf("expected small-2 (low KV pressure), got %s", got.ID)
	}
}

func TestPick_AllUnhealthy(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
	}
	poll := poller.New(replicas, 0)
	for i := 0; i < 3; i++ {
		poll.SimulateFailure("small-1")
	}

	mem := store.NewMemory()
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)
	got := s.Pick(123, types.TierSmall, nil, nil)
	if got.ID != "" {
		t.Errorf("expected empty replica, got %s", got.ID)
	}
}

func TestRecordHit(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)

	s.RecordHit(42, "small-1")

	id, ok := mem.GetAffinity(42)
	if !ok || id != "small-1" {
		t.Errorf("expected affinity small-1, got %s (ok=%v)", id, ok)
	}
}

// stubCircuit implements CircuitChecker for testing.
type stubCircuit struct {
	blocked map[string]bool
}

func (sc *stubCircuit) Allow(id string) bool {
	return !sc.blocked[id]
}

func TestPick_CircuitOpenSkipped(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "small-2", URL: "http://s2", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)

	cc := &stubCircuit{blocked: map[string]bool{"small-1": true}}
	got := s.Pick(123, types.TierSmall, cc, nil)
	if got.ID != "small-2" {
		t.Errorf("expected small-2 (small-1 circuit open), got %s", got.ID)
	}
}

func TestPick_ExcludedSkipped(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
		{ID: "small-2", URL: "http://s2", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)

	excluded := map[string]bool{"small-1": true}
	got := s.Pick(123, types.TierSmall, nil, excluded)
	if got.ID != "small-2" {
		t.Errorf("expected small-2 (small-1 excluded), got %s", got.ID)
	}
}

func TestPick_AllExcluded(t *testing.T) {
	replicas := []config.Replica{
		{ID: "small-1", URL: "http://s1", Tier: types.TierSmall},
	}
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	s := New(replicas, mem, poll, defaultWeights(), 5*time.Minute, 20)

	excluded := map[string]bool{"small-1": true}
	got := s.Pick(123, types.TierSmall, nil, excluded)
	if got.ID != "" {
		t.Errorf("expected empty replica when all excluded, got %s", got.ID)
	}
}
