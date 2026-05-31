package main

import (
	"time"

	"prompt-response/internal/usage"
)

// This file holds the static, in-memory data the mock server serves. The
// numbers are hand-tuned to echo the routing/cost story captured in the
// repo's cost_results.json sample (80 routed prompts, ~$0.0079 saved versus
// always using the largest model, with most traffic absorbed by the small
// tier). Nothing here calls a real upstream — it is purely illustrative so
// the frontend can be developed and demoed without vLLM, Redis, or Postgres.

// replicaStatus mirrors the JSON shape emitted by the real router's
// /v1/router/status handler (internal/proxy/handler.go).
type replicaStatus struct {
	ID          string  `json:"id"`
	Model       string  `json:"model"`
	Tier        string  `json:"tier"`
	Healthy     bool    `json:"healthy"`
	QueueDepth  int     `json:"queue_depth"`
	KVCacheUtil float64 `json:"kv_cache_util"`
	Running     int     `json:"running"`
	Circuit     string  `json:"circuit"`
	ErrorRate   float64 `json:"error_rate"`
}

// mockReplicas returns the per-replica view shown on /v1/router/status. The
// IDs, models, and tiers match config.yaml so the page would render the same
// against the real router.
func mockReplicas() []replicaStatus {
	return []replicaStatus{
		{"replica-small-1", "Qwen/Qwen2.5-1.5B-Instruct-AWQ", "small", true, 3, 0.42, 5, "closed", 0.0},
		{"replica-small-2", "Qwen/Qwen2.5-1.5B-Instruct-AWQ", "small", true, 2, 0.38, 4, "closed", 0.0},
		{"replica-code-1", "Qwen/Qwen2.5-Coder-7B-Instruct-AWQ", "code", true, 1, 0.55, 2, "closed", 0.0},
		{"replica-medium-1", "Qwen/Qwen2.5-14B-Instruct-AWQ", "medium", true, 0, 0.21, 1, "closed", 0.0},
		{"replica-large-1", "Qwen/Qwen2.5-72B-Instruct-AWQ", "large", true, 0, 0.18, 0, "closed", 0.0},
		{"replica-reasoning-1", "Qwen/QwQ-32B-AWQ", "reasoning", false, 0, 0.0, 0, "open", 0.65},
	}
}

// mockUsage returns the per-tenant token consumption shown on
// /v1/router/usage, using the same usage.Usage struct as the real tracker so
// the JSON shape is identical.
func mockUsage() map[string]usage.Usage {
	now := time.Now()
	day := 24 * time.Hour
	return map[string]usage.Usage{
		"acme-corp": {
			InputTokens:  182_400,
			OutputTokens: 421_900,
			Requests:     1_284,
			FirstSeen:    now.Add(-12 * day),
			LastSeen:     now.Add(-3 * time.Minute),
		},
		"globex": {
			InputTokens:  74_120,
			OutputTokens: 168_540,
			Requests:     503,
			FirstSeen:    now.Add(-9 * day),
			LastSeen:     now.Add(-21 * time.Minute),
		},
		"initech": {
			InputTokens:  41_780,
			OutputTokens: 96_220,
			Requests:     312,
			FirstSeen:    now.Add(-6 * day),
			LastSeen:     now.Add(-2 * time.Hour),
		},
		"umbrella": {
			InputTokens:  9_640,
			OutputTokens: 22_010,
			Requests:     71,
			FirstSeen:    now.Add(-2 * day),
			LastSeen:     now.Add(-44 * time.Minute),
		},
	}
}

// tierUsage summarizes routed traffic and cost by model tier. The request
// counts follow the routing distribution in cost_results.json (small 53,
// code 11, medium/large smaller), scaled up for a fuller dashboard.
type tierUsage struct {
	Tier          string  `json:"tier"`
	Model         string  `json:"model"`
	Requests      int     `json:"requests"`
	RoutedCost    float64 `json:"routed_cost"`
	BaselineCost  float64 `json:"baseline_cost"`
	Savings       float64 `json:"savings"`
	AvgTTFTMillis int     `json:"avg_ttft_ms"`
}

// mockTierUsage returns the per-tier cost/savings breakdown shown on the
// metrics page. Totals roughly track cost_results.json's $0.0079 of savings.
func mockTierUsage() []tierUsage {
	return []tierUsage{
		{"small", "Qwen/Qwen2.5-1.5B-Instruct-AWQ", 1_392, 0.00138, 0.01070, 0.00932, 38},
		{"code", "Qwen/Qwen2.5-Coder-7B-Instruct-AWQ", 289, 0.00061, 0.00222, 0.00161, 95},
		{"medium", "Qwen/Qwen2.5-14B-Instruct-AWQ", 118, 0.00044, 0.00091, 0.00047, 140},
		{"large", "Qwen/Qwen2.5-72B-Instruct-AWQ", 105, 0.00121, 0.00121, 0.00000, 320},
		{"reasoning", "Qwen/QwQ-32B-AWQ", 66, 0.00079, 0.00079, 0.00000, 410},
	}
}

// cannedReplies are streamed back token-by-token by the mock chat handler.
// One is chosen per request (rotating) so repeated sends look varied.
var cannedReplies = []string{
	"Sure! Here's a quick rundown. The router inspects each prompt, scores its " +
		"complexity across a handful of signals, and forwards it to the smallest " +
		"model tier that can handle it well. Simple chit-chat lands on the small tier; " +
		"code and reasoning get escalated. This keeps latency low and cost down.",
	"Great question. In this mock build there is no real model behind the stream — " +
		"these tokens are generated locally to demonstrate the end-to-end SSE flow. " +
		"The frontend parses each `data:` frame, appends the delta, and shows the " +
		"blinking cursor until it sees `[DONE]`.",
	"Let me walk through it. Prefix-cache affinity routes follow-up turns back to the " +
		"replica that already holds the conversation's KV cache, so repeated context " +
		"isn't recomputed. The metrics page visualizes how traffic spreads across tiers " +
		"and how much that routing saves versus always using the largest model.",
	"Hash map insertion is O(1) average, O(n) worst case when everything collides into one bucket. " +
		"A balanced BST like a red-black tree is O(log n) guaranteed. " +
		"Pick a hash map when you need fast lookups and don't care about ordering. " +
		"Pick a BST when you need sorted iteration, range queries, or guaranteed worst-case " +
		"performance. Hash maps also struggle when the hash function is poor or the keys are adversarial " +
		"— a BST doesn't have that problem.",
}
