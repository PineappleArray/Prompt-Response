// Package audit provides a request routing decision audit trail.
// Every routed request produces a Record capturing the full decision
// breakdown: classifier signals, scorer choice, cache hit, retry
// attempts, and response metrics. Records are stored in a fixed-size
// ring buffer and exposed via the /v1/router/audit endpoint.
package audit

import "time"

// Record captures the complete routing decision for a single request.
type Record struct {
	Timestamp    time.Time          `json:"timestamp"`
	RequestID    string             `json:"request_id"`
	Tenant       string             `json:"tenant,omitempty"`
	Tier         string             `json:"tier"`
	ClassScore   float64            `json:"class_score"`
	Signals      map[string]float64 `json:"signals,omitempty"`
	ReplicaID    string             `json:"replica_id"`
	ReplicaTier  string             `json:"replica_tier"`
	CacheHit     bool               `json:"cache_hit"`
	Attempts     int                `json:"attempts"`
	TTFTMs       int64              `json:"ttft_ms"`
	TotalMs      int64              `json:"total_ms"`
	OutputTokens int                `json:"output_tokens"`
	StatusCode   int                `json:"status_code"`
	Reason       string             `json:"reason"`
}
