package usage

import (
	"sync"
	"testing"
	"time"
)

func TestRecord_NewTenant(t *testing.T) {
	fixed := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	tr := newTrackerForTest(func() time.Time { return fixed })

	tr.Record("acme", 100, 50)

	u, ok := tr.Get("acme")
	if !ok {
		t.Fatal("expected tenant acme to exist")
	}
	if u.InputTokens != 100 {
		t.Errorf("InputTokens=%d, want 100", u.InputTokens)
	}
	if u.OutputTokens != 50 {
		t.Errorf("OutputTokens=%d, want 50", u.OutputTokens)
	}
	if u.Requests != 1 {
		t.Errorf("Requests=%d, want 1", u.Requests)
	}
	if !u.FirstSeen.Equal(fixed) {
		t.Errorf("FirstSeen=%v, want %v", u.FirstSeen, fixed)
	}
	if !u.LastSeen.Equal(fixed) {
		t.Errorf("LastSeen=%v, want %v", u.LastSeen, fixed)
	}
}

func TestRecord_ExistingTenant_AccumulatesAndUpdatesLastSeen(t *testing.T) {
	t0 := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)

	clock := t0
	tr := newTrackerForTest(func() time.Time { return clock })

	tr.Record("acme", 100, 50)
	clock = t1
	tr.Record("acme", 200, 75)

	u, _ := tr.Get("acme")
	if u.InputTokens != 300 {
		t.Errorf("InputTokens=%d, want 300", u.InputTokens)
	}
	if u.OutputTokens != 125 {
		t.Errorf("OutputTokens=%d, want 125", u.OutputTokens)
	}
	if u.Requests != 2 {
		t.Errorf("Requests=%d, want 2", u.Requests)
	}
	if !u.FirstSeen.Equal(t0) {
		t.Errorf("FirstSeen=%v, want %v (should be immutable)", u.FirstSeen, t0)
	}
	if !u.LastSeen.Equal(t1) {
		t.Errorf("LastSeen=%v, want %v", u.LastSeen, t1)
	}
}

func TestRecord_NegativeCountsClampedToZero(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", -5, -10)

	u, _ := tr.Get("acme")
	if u.InputTokens != 0 {
		t.Errorf("InputTokens=%d, want 0", u.InputTokens)
	}
	if u.OutputTokens != 0 {
		t.Errorf("OutputTokens=%d, want 0", u.OutputTokens)
	}
	if u.Requests != 1 {
		t.Errorf("Requests=%d, want 1 (request still counted)", u.Requests)
	}
}

func TestRecord_MultipleTenantsIsolated(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", 100, 50)
	tr.Record("globex", 200, 100)
	tr.Record("acme", 10, 5)

	cases := []struct {
		name                      string
		tenant                    string
		wantIn, wantOut, wantReqs int64
	}{
		{"acme", "acme", 110, 55, 2},
		{"globex", "globex", 200, 100, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := tr.Get(tc.tenant)
			if !ok {
				t.Fatalf("tenant %s missing", tc.tenant)
			}
			if u.InputTokens != tc.wantIn {
				t.Errorf("InputTokens=%d, want %d", u.InputTokens, tc.wantIn)
			}
			if u.OutputTokens != tc.wantOut {
				t.Errorf("OutputTokens=%d, want %d", u.OutputTokens, tc.wantOut)
			}
			if u.Requests != tc.wantReqs {
				t.Errorf("Requests=%d, want %d", u.Requests, tc.wantReqs)
			}
		})
	}
}

func TestGet_MissingTenant(t *testing.T) {
	tr := NewTracker()
	_, ok := tr.Get("nobody")
	if ok {
		t.Error("expected Get to return false for missing tenant")
	}
}

func TestAll_ReturnsIndependentCopy(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", 100, 50)

	snapshot := tr.All()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(snapshot))
	}

	// Mutating the snapshot must not affect the tracker.
	u := snapshot["acme"]
	u.InputTokens = 99999
	snapshot["acme"] = u
	delete(snapshot, "acme")

	live, ok := tr.Get("acme")
	if !ok {
		t.Fatal("tenant acme disappeared after mutating snapshot")
	}
	if live.InputTokens != 100 {
		t.Errorf("InputTokens=%d, want 100 (tracker state leaked via snapshot)", live.InputTokens)
	}
}

func TestReset_SingleTenant(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", 100, 50)
	tr.Record("globex", 200, 100)

	n := tr.Reset("acme")
	if n != 1 {
		t.Errorf("Reset returned %d, want 1", n)
	}
	if _, ok := tr.Get("acme"); ok {
		t.Error("acme should be removed")
	}
	if _, ok := tr.Get("globex"); !ok {
		t.Error("globex should remain")
	}
}

func TestReset_AllTenants(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", 100, 50)
	tr.Record("globex", 200, 100)

	n := tr.Reset("")
	if n != 2 {
		t.Errorf("Reset(\"\") returned %d, want 2", n)
	}
	if tr.Len() != 0 {
		t.Errorf("Len()=%d, want 0", tr.Len())
	}
}

func TestReset_MissingTenantReturnsZero(t *testing.T) {
	tr := NewTracker()
	tr.Record("acme", 100, 50)
	if n := tr.Reset("nobody"); n != 0 {
		t.Errorf("Reset of missing tenant returned %d, want 0", n)
	}
	if tr.Len() != 1 {
		t.Errorf("Len()=%d, want 1 (unrelated reset should not touch other tenants)", tr.Len())
	}
}

func TestLen(t *testing.T) {
	tr := NewTracker()
	if tr.Len() != 0 {
		t.Errorf("Len()=%d, want 0", tr.Len())
	}
	tr.Record("a", 1, 1)
	tr.Record("b", 1, 1)
	tr.Record("a", 1, 1) // duplicate tenant
	if tr.Len() != 2 {
		t.Errorf("Len()=%d, want 2", tr.Len())
	}
}

func TestRecord_EmptyTenantTrackedLiterally(t *testing.T) {
	tr := NewTracker()
	tr.Record("", 10, 5)
	u, ok := tr.Get("")
	if !ok {
		t.Fatal("empty tenant should be tracked literally")
	}
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Errorf("got %+v", u)
	}
}

func TestRecord_ConcurrentSafe(t *testing.T) {
	tr := NewTracker()
	const goroutines = 50
	const perGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tr.Record("shared", 1, 2)
			}
		}()
	}

	// Concurrent readers to exercise RWMutex paths.
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(2)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = tr.Get("shared")
				_ = tr.All()
				_ = tr.Len()
			}
		}
	}()
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = tr.All()
			}
		}
	}()

	wg.Wait()
	close(stop)
	readerWG.Wait()

	u, _ := tr.Get("shared")
	wantReqs := int64(goroutines * perGoroutine)
	if u.Requests != wantReqs {
		t.Errorf("Requests=%d, want %d", u.Requests, wantReqs)
	}
	if u.InputTokens != wantReqs {
		t.Errorf("InputTokens=%d, want %d", u.InputTokens, wantReqs)
	}
	if u.OutputTokens != wantReqs*2 {
		t.Errorf("OutputTokens=%d, want %d", u.OutputTokens, wantReqs*2)
	}
}

func TestNewTracker_UsesWallClock(t *testing.T) {
	tr := NewTracker()
	before := time.Now()
	tr.Record("acme", 1, 1)
	after := time.Now()

	u, _ := tr.Get("acme")
	if u.FirstSeen.Before(before) || u.FirstSeen.After(after) {
		t.Errorf("FirstSeen=%v not within [%v, %v]", u.FirstSeen, before, after)
	}
}
