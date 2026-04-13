package classifier

import (
	"testing"

	"prompt-response/internal/types"
)

func newTestClassifier() *HeuristicClassifier {
	return NewHeuristic(HeuristicConfig{
		Weights: SignalWeights{
			Length:       0.20,
			Code:         0.30,
			Reasoning:    0.15,
			Complexity:   0.10,
			ConvDepth:    0.10,
			OutputLength: 0.15,
		},
		Threshold: 0.35,
	})
}

func TestClassify(t *testing.T) {
	c := newTestClassifier()

	cases := []struct {
		name     string
		req      Request
		wantTier types.ModelTier
	}{
		{
			name:     "simple factual → small",
			req:      Request{UserMessage: "what is 2+2", TokenCount: 5},
			wantTier: types.TierSmall,
		},
		{
			name:     "code with backticks → large",
			req:      Request{UserMessage: "fix this", HasCode: true, TokenCount: 80},
			wantTier: types.TierLarge,
		},
		{
			name: "implement algorithm → large",
			req: Request{
				UserMessage: "implement a red-black tree",
				TokenCount:  12,
			},
			wantTier: types.TierLarge,
		},
		{
			name:     "short reasoning → small",
			req:      Request{UserMessage: "why is the sky blue", TokenCount: 8},
			wantTier: types.TierSmall,
		},
		{
			name: "long complex reasoning → large",
			req: Request{
				UserMessage: "explain the tradeoffs between " +
					"distributed consensus algorithms at scale",
				TokenCount: 95,
			},
			wantTier: types.TierLarge,
		},
		{
			name: "deep conversation with context → large",
			req: Request{
				UserMessage: "explain why the previous distributed architecture design won't scale",
				TokenCount:  80,
				ConvTurns:   8,
			},
			wantTier: types.TierLarge,
		},
		{
			name:     "short answer expected → small",
			req:      Request{UserMessage: "what is the capital of France", TokenCount: 10},
			wantTier: types.TierSmall,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Classify(tc.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Tier != tc.wantTier {
				t.Errorf("got tier=%s score=%.2f reason=%q; want tier=%s",
					got.Tier, got.Score, got.Reason, tc.wantTier)
			}
		})
	}
}

func TestClassifySignalsPresent(t *testing.T) {
	c := newTestClassifier()
	got, _ := c.Classify(Request{
		UserMessage: "implement a distributed algorithm step by step",
		TokenCount:  50,
		HasCode:     true,
		ConvTurns:   3,
	})

	expectedSignals := []string{"length", "code", "reasoning", "complexity", "conv_depth", "output_length"}
	for _, name := range expectedSignals {
		if _, ok := got.Signals[name]; !ok {
			t.Errorf("missing signal %q in response", name)
		}
	}
}
