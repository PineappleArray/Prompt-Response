package classifier

import "prompt-response/internal/types"

type Classifier interface {
	Classify(req Request) (Response, error)
}

type Request struct {
	SystemPrompt string // extracted from messages[role=system]
	UserMessage  string // latest messages[role=user]
	TokenCount   int    // pre-counted by proxy handler
	HasCode      bool   // true if ``` or code keywords found
	ConvTurns    int    // number of prior messages in thread
}

type Response struct {
	Tier    types.ModelTier    // routing decision
	Score   float64            // raw composite score 0–1
	Signals map[string]float64 // per-signal breakdown
	Reason  string             // human-readable explanation
}
