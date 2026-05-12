package classifier

import (
	"context"
)

type Classifier interface {
	Classify(ctx context.Context, req Request) (*ClassifyResponse, error)
}
