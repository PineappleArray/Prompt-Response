package classifier

import (
	"prompt-response/internal/config"
)

func LoadClassifierSettings(cfg *config.Config) (ClassifierWeights, KeywordSets) {
	return loadModelWeights(cfg), loadKeywordSets(cfg)
}

func classifyPrompt() {

}
