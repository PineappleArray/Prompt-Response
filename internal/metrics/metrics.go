package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	RequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_requests_total",
			Help: "Total requests routed, by tier, replica, and cache hit status",
		},
		[]string{"tier", "replica", "cache_hit"},
	)

	RequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_request_duration_seconds",
			Help:    "Total request duration in seconds",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"tier", "replica"},
	)

	TimeToFirstToken = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_ttft_seconds",
			Help:    "Time to first SSE token in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2},
		},
		[]string{"tier", "replica"},
	)

	ClassifierScore = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_classifier_score",
			Help:    "Classifier complexity score distribution",
			Buckets: []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
		},
		[]string{"tier"},
	)

	ReplicaKVCacheUtil = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "router_replica_kv_cache_utilization",
			Help: "Last observed KV cache utilization per replica",
		},
		[]string{"replica"},
	)

	// Inter-token latency: the key metric for detecting inference stalls.
	// High ITL indicates KV cache thrashing, batch preemption, or GPU contention.
	InterTokenLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_inter_token_latency_seconds",
			Help:    "Inter-token latency (time between consecutive SSE chunks with content)",
			Buckets: []float64{.005, .01, .02, .05, .1, .2, .5, 1},
		},
		[]string{"tier", "replica"},
	)

	// Output tokens per request — enables cost tracking and capacity planning.
	OutputTokens = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_output_tokens",
			Help:    "Output tokens per request (estimated from SSE stream content)",
			Buckets: []float64{10, 50, 100, 250, 500, 1000, 2000},
		},
		[]string{"tier", "replica"},
	)

	// Tokens per second — the throughput metric operators care about most.
	TokensPerSecond = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_tokens_per_second",
			Help:    "Output token throughput per request",
			Buckets: []float64{5, 10, 20, 50, 100, 200},
		},
		[]string{"tier", "replica"},
	)

	// Circuit breaker state per replica (0=closed, 1=half_open, 2=open).
	CircuitState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "router_circuit_state",
			Help: "Circuit breaker state per replica (0=closed, 1=half_open, 2=open)",
		},
		[]string{"replica"},
	)

	// Retry attempts triggered by upstream failures.
	RetriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_retries_total",
			Help: "Total retry attempts by replica that triggered the retry",
		},
		[]string{"replica"},
	)

	// Upstream errors (5xx or connection failures) per replica.
	UpstreamErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_upstream_errors_total",
			Help: "Total upstream errors per replica",
		},
		[]string{"replica"},
	)

	// Dead replica detections — replicas that accepted the request but failed
	// to emit the SSE [DONE] terminator within the stall window. The phase
	// label distinguishes pre_first_byte (no body bytes ever arrived; request
	// was rerouted) from mid_stream (some bytes streamed but [DONE] missing;
	// connection aborted).
	DeadReplicasTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_dead_replicas_total",
			Help: "Total dead-replica detections (no terminating character)",
		},
		[]string{"replica", "phase"},
	)

	// Prompt-to-terminator latency: total wall-clock time from the moment
	// the router begins handling a request until the upstream emits the SSE
	// [DONE] sentinel. Measures end-to-end completion latency including
	// classifier, replica selection, retries, and the full streamed body.
	PromptToDoneDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "router_prompt_to_done_seconds",
			Help:    "Latency from prompt start to SSE [DONE] terminator",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120},
		},
		[]string{"tier", "replica"},
	)

	// Authentication failures by reason (missing_key, invalid_key).
	AuthFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_auth_failures_total",
			Help: "Total authentication failures by reason",
		},
		[]string{"reason"},
	)

	// Rate limit rejections by tenant.
	RateLimitRejectsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_ratelimit_rejects_total",
			Help: "Total requests rejected by rate limiter",
		},
		[]string{"tenant"},
	)

	// Tokens consumed per tenant and direction (input/output).
	// Enables cost attribution and chargeback across tenants.
	TokensConsumedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_tokens_consumed_total",
			Help: "Total tokens consumed per tenant and direction (input/output)",
		},
		[]string{"tenant", "direction"},
	)

	// Tier escalations: a conversation promoted from its pinned tier to a
	// higher one. Conversations only ever escalate, never downgrade.
	TierEscalationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "router_tier_escalations_total",
			Help: "Total conversation tier escalations, by source and destination tier",
		},
		[]string{"from", "to"},
	)
)
