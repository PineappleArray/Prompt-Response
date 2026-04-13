package classifier

import (
	"fmt"
	"math"
	"strings"

	"prompt-response/internal/types"
)

type ModelClassifier struct {
	weights   SignalWeights
	threshold float64
	keywords  KeywordSets
}

type ModelConfig struct {
	Weights   SignalWeights
	Threshold float64
	keywords  KeywordSets
}

