package audit

import (
	"sync"
	"testing"
	"time"
)

func TestTrail_RecordAndRecent(t *testing.T) {
	trail := NewTrail(10)

	trail.Record(Record{RequestID: "r1", Tier: "small"})
	trail.Record(Record{RequestID: "r2", Tier: "large"})

	records := trail.Recent(2)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	// Newest first
	if records[0].RequestID != "r2" {
		t.Errorf("expected newest first (r2), got %s", records[0].RequestID)
	}
	if records[1].RequestID != "r1" {
		t.Errorf("expected second record r1, got %s", records[1].RequestID)
	}
}

func TestTrail_ReverseChronological(t *testing.T) {
	trail := NewTrail(100)

	for i := 0; i < 5; i++ {
		trail.Record(Record{
			RequestID: string(rune('a' + i)),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	records := trail.Recent(5)
	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d", len(records))
	}

	// Should be e, d, c, b, a
	expected := []string{"e", "d", "c", "b", "a"}
	for i, rec := range records {
		if rec.RequestID != expected[i] {
			t.Errorf("records[%d].RequestID = %q, want %q", i, rec.RequestID, expected[i])
		}
	}
}

func TestTrail_Wraparound(t *testing.T) {
	trail := NewTrail(3)

	trail.Record(Record{RequestID: "r1"})
	trail.Record(Record{RequestID: "r2"})
	trail.Record(Record{RequestID: "r3"})
	trail.Record(Record{RequestID: "r4"}) // overwrites r1
	trail.Record(Record{RequestID: "r5"}) // overwrites r2

	if trail.Len() != 3 {
		t.Errorf("expected len 3, got %d", trail.Len())
	}

	records := trail.Recent(3)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}

	// Should be r5, r4, r3 (newest first, r1 and r2 overwritten)
	expected := []string{"r5", "r4", "r3"}
	for i, rec := range records {
		if rec.RequestID != expected[i] {
			t.Errorf("records[%d].RequestID = %q, want %q", i, rec.RequestID, expected[i])
		}
	}
}

func TestTrail_RecentMoreThanAvailable(t *testing.T) {
	trail := NewTrail(10)
	trail.Record(Record{RequestID: "r1"})

	records := trail.Recent(100)
	if len(records) != 1 {
		t.Errorf("expected 1 record, got %d", len(records))
	}
}

func TestTrail_Empty(t *testing.T) {
	trail := NewTrail(10)

	records := trail.Recent(5)
	if records != nil {
		t.Errorf("expected nil for empty trail, got %v", records)
	}
	if trail.Len() != 0 {
		t.Errorf("expected len 0, got %d", trail.Len())
	}
}

func TestTrail_Cap(t *testing.T) {
	trail := NewTrail(42)
	if trail.Cap() != 42 {
		t.Errorf("expected cap 42, got %d", trail.Cap())
	}
}

func TestTrail_RecordPreservesAllFields(t *testing.T) {
	trail := NewTrail(10)

	rec := Record{
		Timestamp:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		RequestID:    "req-123",
		Tenant:       "acme",
		Tier:         "large",
		ClassScore:   0.72,
		Signals:      map[string]float64{"code": 0.8, "length": 0.6},
		ReplicaID:    "replica-large-1",
		ReplicaTier:  "large",
		CacheHit:     true,
		Attempts:     2,
		TTFTMs:       45,
		TotalMs:      1200,
		OutputTokens: 256,
		StatusCode:   200,
		Reason:       "code present, reasoning keywords",
	}
	trail.Record(rec)

	got := trail.Recent(1)[0]
	if got.RequestID != rec.RequestID {
		t.Errorf("RequestID = %q, want %q", got.RequestID, rec.RequestID)
	}
	if got.Tenant != rec.Tenant {
		t.Errorf("Tenant = %q, want %q", got.Tenant, rec.Tenant)
	}
	if got.ClassScore != rec.ClassScore {
		t.Errorf("ClassScore = %f, want %f", got.ClassScore, rec.ClassScore)
	}
	if got.CacheHit != rec.CacheHit {
		t.Errorf("CacheHit = %v, want %v", got.CacheHit, rec.CacheHit)
	}
	if got.Attempts != rec.Attempts {
		t.Errorf("Attempts = %d, want %d", got.Attempts, rec.Attempts)
	}
	if got.OutputTokens != rec.OutputTokens {
		t.Errorf("OutputTokens = %d, want %d", got.OutputTokens, rec.OutputTokens)
	}
	if len(got.Signals) != 2 {
		t.Errorf("expected 2 signals, got %d", len(got.Signals))
	}
}

func TestTrail_ConcurrentAccess(t *testing.T) {
	trail := NewTrail(100)

	var wg sync.WaitGroup
	// 10 writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				trail.Record(Record{RequestID: "w"})
			}
		}(i)
	}
	// 5 readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				trail.Recent(10)
				trail.Len()
			}
		}()
	}
	wg.Wait()

	// Should have recorded 500 writes, capped at 100
	if trail.Len() != 100 {
		t.Errorf("expected len 100, got %d", trail.Len())
	}
}

func TestTrail_SingleElement(t *testing.T) {
	trail := NewTrail(1)

	trail.Record(Record{RequestID: "r1"})
	trail.Record(Record{RequestID: "r2"}) // overwrites r1

	records := trail.Recent(1)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RequestID != "r2" {
		t.Errorf("expected r2, got %s", records[0].RequestID)
	}
}
