package types

import "testing"

func TestValidTier(t *testing.T) {
	tests := []struct {
		name string
		tier ModelTier
		want bool
	}{
		{"small", TierSmall, true},
		{"medium", TierMedium, true},
		{"large", TierLarge, true},
		{"code", TierCode, true},
		{"empty", "", false},
		{"unknown", "gigantic", false},
		{"case mismatch", "Small", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidTier(tc.tier); got != tc.want {
				t.Errorf("ValidTier(%q) = %v, want %v", tc.tier, got, tc.want)
			}
		})
	}
}
