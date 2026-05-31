package classifier

import (
	"context"
)

//type Classifier struct {
//    session *ort.DynamicAdvancedSession
//}
//ort "github.com/yalue/onnxruntime_go"

type Classifier interface {
	Classify(ctx context.Context, req Request) (*ClassifyResponse, error)
}
