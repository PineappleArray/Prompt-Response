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
)
