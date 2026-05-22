package store

import (
	"testing"
	"time"

	"prompt-response/internal/types"
)

func TestMemoryStoreAffinity(t *testing.T) {
	tests := []struct {
		name      string
		setHash   uint64
		setID     string
		setTTL    time.Duration
		getHash   uint64
		wantID    string
		wantFound bool
	}{
		{"hit", 42, "replica-a", time.Minute, 42, "replica-a", true},
		{"miss unknown hash", 42, "replica-a", time.Minute, 99, "", false},
		{"expired", 42, "replica-a", -time.Second, 42, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemory()
			m.SetAffinity(tc.setHash, tc.setID, tc.setTTL)
			gotID, gotFound := m.GetAffinity(tc.getHash)
			if gotID != tc.wantID || gotFound != tc.wantFound {
				t.Errorf("GetAffinity(%d) = (%q, %v), want (%q, %v)",
					tc.getHash, gotID, gotFound, tc.wantID, tc.wantFound)
			}
		})
	}
}

func TestMemoryStoreConversation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	state := types.ConvState{
		Tier:      types.TierLarge,
		Model:     "Qwen/Qwen2.5-72B-Instruct-AWQ",
		Bucket:    "b3",
		Turns:     4,
		UpdatedAt: now,
	}

	tests := []struct {
		name      string
		setID     uint64
		ttl       time.Duration
		getID     uint64
		wantFound bool
	}{
		{"hit", 7, time.Minute, 7, true},
		{"miss unknown id", 7, time.Minute, 8, false},
		{"expired", 7, -time.Second, 7, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemory()
			m.SetConversation(tc.setID, state, tc.ttl)
			got, found := m.GetConversation(tc.getID)
			if found != tc.wantFound {
				t.Fatalf("GetConversation(%d) found = %v, want %v", tc.getID, found, tc.wantFound)
			}
			if found && got != state {
				t.Errorf("GetConversation(%d) = %+v, want %+v", tc.getID, got, state)
			}
		})
	}
}

func TestMemoryStoreConversationEscalation(t *testing.T) {
	m := NewMemory()
	m.SetConversation(1, types.ConvState{Tier: types.TierSmall, Turns: 1}, time.Minute)

	prev, ok := m.GetConversation(1)
	if !ok || prev.Tier != types.TierSmall {
		t.Fatalf("expected pinned small tier, got %+v ok=%v", prev, ok)
	}

	// A later turn escalates the conversation.
	m.SetConversation(1, types.ConvState{Tier: types.TierLarge, Turns: 2}, time.Minute)
	got, _ := m.GetConversation(1)
	if got.Tier != types.TierLarge || got.Turns != 2 {
		t.Errorf("after escalation = %+v, want tier=large turns=2", got)
	}
}
