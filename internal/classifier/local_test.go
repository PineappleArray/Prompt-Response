package classifier

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"prompt-response/internal/config"
	"prompt-response/internal/types"
)

// TestBasePick ports the cases from app/model_select_test.py so the Go
// selection heuristic stays bug-for-bug compatible with the original Python.
func TestBasePick(t *testing.T) {

	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	classifierCfg := InitConfig(*cfg)
	tests := []struct {
		name string
		sig  signals
		text string
		want types.ModelTier
	}{
		{
			name: "simple QA routes small",
			sig:  signals{taskType: "QA", score: 0.05, reasoning: 0.05},
			want: types.TierSmall,
		},
		{
			name: "hard reasoning routes large",
			sig:  signals{taskType: "Open QA", score: 0.60, reasoning: 0.75},
			want: types.TierLarge,
		},
		{
			name: "code generation task routes code",
			sig:  signals{taskType: "Code Generation", score: 0.30},
			want: types.TierCode,
		},
		{
			name: "high complexity + domain + constraint routes large",
			sig:  signals{taskType: "Open QA", score: 0.70, domain: 0.85, constraint: 0.65},
			want: types.TierLarge,
		},
		{
			name: "default routes reasoning",
			sig:  signals{taskType: "Brainstorming", score: 0.40, reasoning: 0.30},
			want: types.TierReasoning,
		},
		{
			name: "fenced code in text routes code when score low",
			sig:  signals{taskType: "Open QA", score: 0.30},
			text: "fix this ```go\nfunc main(){}\n```",
			want: types.TierCode,
		},
		{
			name: "fenced code ignored when score high",
			sig:  signals{taskType: "Open QA", score: 0.80, reasoning: 0.2},
			text: "design ```go``` system",
			want: types.TierReasoning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifierCfg.basePick(tt.sig, tt.text); got != tt.want {
				t.Errorf("basePick() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSelectTierClampUp covers up-tier-only escalation (port of the tier
// escalation tests in model_select_test.py): a conversation's tier only rises.
func TestSelectTierClampUp(t *testing.T) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	classifierCfg := InitConfig(*cfg)
	tests := []struct {
		name    string
		sig     signals
		current types.ModelTier
		want    types.ModelTier
	}{
		{
			name:    "pinned higher tier is kept",
			sig:     signals{taskType: "QA", score: 0.05, reasoning: 0.05}, // base -> small
			current: types.TierLarge,
			want:    types.TierLarge,
		},
		{
			name:    "hard request escalates above lower pin",
			sig:     signals{taskType: "Open QA", score: 0.60, reasoning: 0.75}, // base -> large
			current: types.TierSmall,
			want:    types.TierLarge,
		},
		{
			name:    "equal tier stays equal",
			sig:     signals{taskType: "Open QA", score: 0.60, reasoning: 0.75}, // base -> large
			current: types.TierLarge,
			want:    types.TierLarge,
		},
		{
			name:    "no pin uses base pick",
			sig:     signals{taskType: "QA", score: 0.05, reasoning: 0.05},
			current: "",
			want:    types.TierSmall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifierCfg.selectTier(tt.sig, "", tt.current); got != tt.want {
				t.Errorf("selectTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLocalClassify exercises the full in-process path end to end.
func TestLocalClassify(t *testing.T) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	classifierCfg := InitConfig(*cfg)
	c := NewLocalClassifier(classifierCfg)

	tests := []struct {
		name     string
		req      Request
		wantTier types.ModelTier
	}{
		{
			name:     "trivial question -> small",
			req:      Request{UserMessage: "what is 2+2?", TokenCount: 4},
			wantTier: types.TierSmall,
		},
		{
			name:     "code request -> code",
			req:      Request{UserMessage: "write a function to reverse a linked list", HasCode: false, TokenCount: 10},
			wantTier: types.TierCode,
		},
		{
			name:     "conversation pinned large stays large",
			req:      Request{UserMessage: "thanks!", TokenCount: 2, CurrentTier: types.TierLarge},
			wantTier: types.TierLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := c.Classify(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Classify() error = %v", err)
			}
			if resp.Tier != tt.wantTier {
				t.Errorf("tier = %q, want %q (reason %q, score %.2f)", resp.Tier, tt.wantTier, resp.BuildReason, resp.Score)
			}
			if resp.Signals == nil {
				t.Error("expected non-nil signals map")
			}
		})
	}
}

// BenchmarkLocalClassify guards the in-process hot path: classification must be
// allocation-light and fast (it replaced a multi-millisecond network call).
func BenchmarkLocalClassify(b *testing.B) {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	classifierCfg := InitConfig(*cfg)
	c := NewLocalClassifier(classifierCfg)
	req := Request{
		UserMessage: "Explain the tradeoffs between optimistic and pessimistic locking in a distributed database, with examples.",
		TokenCount:  24,
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Classify(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}
