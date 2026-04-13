package classifier

import (
	"net/http"
	"time"
)

type ModelClassifier struct {
	weights   SignalWeights
	threshold float64
	keywords  KeywordSets
	http      *http.Client
	endpoint  string
}

type Connection struct {
	endpoint string
	http     *http.Client
}

type Result struct {
	Model      string  `json:"model"`
	TaskType   string  `json:"task_type"`
	Reasoning  float64 `json:"reasoning"`
	Complexity float64 `json:"complexity"`
}

func New(endpoint string, timeout time.Duration, weights SignalWeights, threshold float64, keywords KeywordSets) *ModelClassifier {
	return &ModelClassifier{
		endpoint: endpoint,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		weights:   weights,
		threshold: threshold,
		keywords:  keywords,
	}
}

func (mc *ModelClassifier) Classify(req Request) (Response, error) {

	return Response{}, nil
}
