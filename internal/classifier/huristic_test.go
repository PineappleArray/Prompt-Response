package classifier

import (
	"testing"
)

func TestClassify(t *testing.T) {
	c := newHeuristic(HeuristicConfig{Weights: SignalWeights{Length: 0.25, Code: 0.35, Reasoning: 0.25, Complexity: 0.15}, Threshold: 0.5})

	cases := []struct {
		name     string
		req      Request
		wantTier ModelTier
	}{
		{
			name:     "simple factual → small",
			req:      Request{UserMessage: "what is 2+2", TokenCount: 5},
			wantTier: TierSmall,
		},
		{
			name:     "code with backticks → large",
			req:      Request{UserMessage: "fix this", HasCode: true, TokenCount: 80},
			wantTier: TierLarge,
		},
		{
			name: "implement algorithm → large",
			req: Request{
				UserMessage: "implement a red-black tree",
				TokenCount:  12,
			},
			wantTier: TierLarge,
		},
		{
			name:     "short reasoning → small",
			req:      Request{UserMessage: "why is the sky blue", TokenCount: 8},
			wantTier: TierSmall,
		},
		{
			name: "long complex reasoning → large",
			req: Request{
				UserMessage: "explain the tradeoffs between " +
					"distributed consensus algorithms at scale",
				TokenCount: 95,
			},
			wantTier: TierLarge,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := c.Classify(tc.req)
			if got.Tier != tc.wantTier {
				t.Errorf("score=%.2f reason=%q", got.Score, got.Reason)
			}
		})
	}
}
