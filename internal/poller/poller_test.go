package poller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"prompt-response/internal/config"
	"prompt-response/internal/types"
)

// helper — builds a poller with one replica pointing at a test server URL
func newTestPoller(url string) *Poller {
	replicas := []config.Replica{
		{ID: "replica-1", URL: url, Model: "test", Tier: types.TierSmall},
	}
	return New(replicas, 0)
}

// Test 1 — healthy replica stays healthy
func TestCheckHealth_HealthyReplica(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newTestPoller(server.URL)
	p.checkHealth(p.replicas[0])

	state := p.Snapshot()["replica-1"]
	if !state.Healthy {
		t.Error("replica should be healthy after a 200 response")
	}
	if p.failures["replica-1"] != 0 {
		t.Errorf("failure count should be 0, got %d", p.failures["replica-1"])
	}
}

// Test 2 — one failure doesn't mark unhealthy
func TestCheckHealth_OneFailureNotUnhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := newTestPoller(server.URL)
	p.checkHealth(p.replicas[0])

	state := p.Snapshot()["replica-1"]
	if !state.Healthy {
		t.Error("one failure should not mark replica unhealthy")
	}
	if p.failures["replica-1"] != 1 {
		t.Errorf("failure count should be 1, got %d", p.failures["replica-1"])
	}
}

// Test 3 — three consecutive failures marks unhealthy
func TestCheckHealth_ThreeFailuresMarksUnhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := newTestPoller(server.URL)
	p.checkHealth(p.replicas[0])
	p.checkHealth(p.replicas[0])
	p.checkHealth(p.replicas[0])

	state := p.Snapshot()["replica-1"]
	if state.Healthy {
		t.Error("replica should be unhealthy after 3 consecutive failures")
	}
}

// Test 4 — recovery after being unhealthy
func TestCheckHealth_RecoveryAfterFailures(t *testing.T) {
	failing := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	p := newTestPoller(server.URL)

	// fail 3 times → unhealthy
	p.checkHealth(p.replicas[0])
	p.checkHealth(p.replicas[0])
	p.checkHealth(p.replicas[0])

	if p.Snapshot()["replica-1"].Healthy {
		t.Fatal("should be unhealthy after 3 failures, test setup wrong")
	}

	// one success → healthy again
	failing = false
	p.checkHealth(p.replicas[0])

	state := p.Snapshot()["replica-1"]
	if !state.Healthy {
		t.Error("replica should recover to healthy after one successful check")
	}
	if p.failures["replica-1"] != 0 {
		t.Errorf("failure count should reset to 0 on recovery, got %d", p.failures["replica-1"])
	}
}

// Test 5 — timeout counts as a failure
func TestCheckHealth_TimeoutCountsAsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // longer than the 2s timeout
	}))
	defer server.Close()

	p := newTestPoller(server.URL)
	p.checkHealth(p.replicas[0])

	if p.failures["replica-1"] != 1 {
		t.Errorf("timeout should increment failure count, got %d", p.failures["replica-1"])
	}
}

// Test 6 — two failures then success resets counter
func TestCheckHealth_FailureCountResetsOnSuccess(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	p := newTestPoller(server.URL)
	p.checkHealth(p.replicas[0]) // fail — count: 1
	p.checkHealth(p.replicas[0]) // fail — count: 2
	p.checkHealth(p.replicas[0]) // success — count should reset to 0

	if p.failures["replica-1"] != 0 {
		t.Errorf("failure count should reset to 0 after success, got %d", p.failures["replica-1"])
	}
	if !p.Snapshot()["replica-1"].Healthy {
		t.Error("replica should still be healthy — never hit 3 consecutive failures")
	}
}

// Test 7 — multiple replicas fail independently
func TestCheckHealth_MultipleReplicasIndependent(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer sick.Close()

	p := New([]config.Replica{
		{ID: "replica-1", URL: healthy.URL, Model: "test", Tier: types.TierSmall},
		{ID: "replica-2", URL: sick.URL, Model: "test", Tier: types.TierSmall},
	}, 0)

	// run 3 checks on both
	for i := 0; i < 3; i++ {
		p.checkHealth(p.replicas[0])
		p.checkHealth(p.replicas[1])
	}

	states := p.Snapshot()
	if !states["replica-1"].Healthy {
		t.Error("replica-1 should remain healthy")
	}
	if states["replica-2"].Healthy {
		t.Error("replica-2 should be unhealthy after 3 failures")
	}
}
