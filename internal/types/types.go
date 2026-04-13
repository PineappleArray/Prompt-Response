package types

type ModelTier string

const (
	TierSmall  ModelTier = "small"
	TierMedium ModelTier = "medium"
	TierLarge  ModelTier = "large"
)

func ValidTier(t ModelTier) bool {
	switch t {
	case TierSmall, TierMedium, TierLarge:
		return true
	}
	return false
}
