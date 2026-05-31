package classifier

import (
	"context"
	"prompt-response/internal/types"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

type Classifier interface {
	Classify(ctx context.Context, req Request) (*Response, error)
}

type Pipeline struct {
	session  *hugot.Session
	pipeline *pipelines.TextClassificationPipeline
}

type Request struct {
	SystemPrompt string          `json:"system_prompt"`
	UserMessage  string          `json:"user_message"`
	TokenCount   int             `json:"token_count"`
	HasCode      bool            `json:"has_code"`
	ConvTurns    int             `json:"conv_turns"`
	CurrentTier  types.ModelTier `json:"current_tier"`
}

type Response struct {
	Tier    types.ModelTier    // routing decision
	Score   float64            // raw composite score 0–1
	Signals map[string]float64 // per-signal breakdown
	Reason  string             // human-readable explanation
}

func NewPipeline(modelName string) (*Pipeline, error) {
	session, err := hugot.NewORTSession()
	if err != nil {
		return nil, err
	}

	config := hugot.TextClassificationConfig{
		ModelPath: "./deberta-int8",
		Name:      "router-classifier",
	}

	pipeline, err := pipelines.NewTextClassificationPipeline(config, modelName)
	if err != nil {
		return nil, err
	}

	return &Pipeline{
		session:  session,
		pipeline: pipeline,
	}, nil
}
