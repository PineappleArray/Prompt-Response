package types

type ModelTier string

type TierConfig struct {
    Name       string          `yaml:"name"`
    Priority   int             `yaml:"priority"`
    ScoreRange [2]float64      `yaml:"score_range"`
    Models     []ReplicaConfig `yaml:"models"`
}

type ReplicaConfig struct {
    ID    string `yaml:"id"`
    URL   string `yaml:"url"`
    Model string `yaml:"model"`
}

func (t ModelTier) String() string {
	return string(t)
}

func 

func ValidTier(t ModelTier) bool {
	switch t {
	case TierSmall, TierMedium, TierLarge, TierCode:
		return true
	}
	return false
}
