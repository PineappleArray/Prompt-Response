package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/metrics"
	"prompt-response/internal/scorer"
	"prompt-response/internal/types"
)

type Handler struct {
	scorer     *scorer.Scorer
	classifier *classifier.HeuristicClassifier
	cfg        *config.Config
}

func New(s *scorer.Scorer, c *classifier.HeuristicClassifier, cfg *config.Config) *Handler {
	return &Handler{scorer: s, classifier: c, cfg: cfg}
}

type openAIRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
		return
	case "/readyz":
		h.handleReadiness(w)
		return
	case "/v1/models":
		h.handleModels(w)
		return
	case "/v1/router/status":
		h.handleRouterStatus(w)
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported for chat completions")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read_error", "failed to read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) == 0 {
		writeError(w, r, http.StatusBadRequest, "empty_body", "request body is empty")
		return
	}

	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "request body is not valid JSON")
		return
	}

	if len(req.Messages) == 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "messages array is required and must not be empty")
		return
	}

	systemPrompt, userMessage := extractMessages(req)
	prefixHash := xxhash.Sum64String(systemPrompt)

	classReq := classifier.Request{
		SystemPrompt: systemPrompt,
		UserMessage:  userMessage,
		TokenCount:   estimateTokens(systemPrompt + userMessage),
		HasCode:      hasCodeBlock(userMessage),
		ConvTurns:    countTurns(req),
	}
	result, err := h.classifier.Classify(classReq)
	if err != nil {
		slog.Error("classification failed, defaulting to small", "err", err)
		result = classifier.Response{Tier: types.TierSmall, Reason: "classification error fallback"}
	}

	metrics.ClassifierScore.WithLabelValues(string(result.Tier)).Observe(result.Score)

	replica := h.scorer.Pick(prefixHash, result.Tier)
	if replica.ID == "" {
		writeError(w, r, http.StatusServiceUnavailable, "no_replicas", "no healthy replicas available")
		return
	}

	cacheHit := "miss"
	if aff, ok := h.scorer.Store().GetAffinity(prefixHash); ok && aff == replica.ID {
		cacheHit = "hit"
	}

	slog.Info("routing request",
		"replica", replica.ID,
		"tier_requested", result.Tier,
		"tier_matched", replica.Tier,
		"classifier_score", result.Score,
		"prefix_hash", prefixHash,
		"cache_hit", cacheHit,
		"reason", result.Reason,
	)

	target, err := url.Parse(replica.URL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal routing error")
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // flush SSE chunks immediately

	r.Host = target.Host
	r.URL.Host = target.Host
	r.URL.Scheme = target.Scheme
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Stream interceptor: measures TTFT, inter-token latency, output token
	// count, and tokens-per-second by parsing SSE chunks in real-time.
	start := time.Now()
	sw := newStreamInterceptor(w)
	proxy.ServeHTTP(sw, r)
	totalDuration := time.Since(start)

	stats := sw.Stats()
	ttft := totalDuration
	if stats.Wrote {
		ttft = stats.FirstByteAt.Sub(start)
	}

	h.scorer.RecordHit(prefixHash, replica.ID)

	tier := string(result.Tier)
	metrics.RequestsTotal.WithLabelValues(tier, replica.ID, cacheHit).Inc()
	metrics.RequestDuration.WithLabelValues(tier, replica.ID).Observe(totalDuration.Seconds())
	metrics.TimeToFirstToken.WithLabelValues(tier, replica.ID).Observe(ttft.Seconds())

	// Stream-level metrics: output tokens, throughput, and inter-token latency.
	var tps float64
	var avgITLMs int64
	if stats.OutputTokens > 0 {
		metrics.OutputTokens.WithLabelValues(tier, replica.ID).Observe(float64(stats.OutputTokens))

		if streamDur := stats.LastTokenAt.Sub(stats.FirstByteAt).Seconds(); streamDur > 0 {
			tps = float64(stats.OutputTokens) / streamDur
			metrics.TokensPerSecond.WithLabelValues(tier, replica.ID).Observe(tps)
		}

		if stats.ChunkCount > 1 {
			avgITL := stats.InterTokenSum / time.Duration(stats.ChunkCount-1)
			avgITLMs = avgITL.Milliseconds()
			metrics.InterTokenLatency.WithLabelValues(tier, replica.ID).Observe(avgITL.Seconds())
		}
	}

	slog.Info("completed",
		"replica", replica.ID,
		"ttft_ms", ttft.Milliseconds(),
		"total_ms", totalDuration.Milliseconds(),
		"output_tokens", stats.OutputTokens,
		"tokens_per_sec", tps,
		"avg_itl_ms", avgITLMs,
		"cache_hit", cacheHit,
	)
}

func (h *Handler) handleReadiness(w http.ResponseWriter) {
	states := h.scorer.PollerSnapshot()
	healthy := false
	replicas := make(map[string]any)
	for id, state := range states {
		replicas[id] = map[string]any{
			"healthy":       state.Healthy,
			"kv_cache_util": state.KVCacheUtil,
			"queue_depth":   state.QueueDepth,
		}
		if state.Healthy {
			healthy = true
		}
	}

	status := "not_ready"
	code := http.StatusServiceUnavailable
	if healthy {
		status = "ready"
		code = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"status":   status,
		"replicas": replicas,
	})
}

// handleModels returns an OpenAI-compatible list of available models.
func (h *Handler) handleModels(w http.ResponseWriter) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}

	seen := make(map[string]bool)
	var models []model
	for _, r := range h.cfg.Replicas {
		if !seen[r.Model] {
			seen[r.Model] = true
			models = append(models, model{
				ID:      r.Model,
				Object:  "model",
				OwnedBy: "prompt-response",
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   models,
	})
}

// handleRouterStatus returns a detailed view of routing state for debugging.
func (h *Handler) handleRouterStatus(w http.ResponseWriter) {
	states := h.scorer.PollerSnapshot()

	type replicaStatus struct {
		ID          string  `json:"id"`
		Model       string  `json:"model"`
		Tier        string  `json:"tier"`
		Healthy     bool    `json:"healthy"`
		QueueDepth  int     `json:"queue_depth"`
		KVCacheUtil float64 `json:"kv_cache_util"`
		Running     int     `json:"running"`
	}

	var replicas []replicaStatus
	healthyCount := 0
	for _, r := range h.cfg.Replicas {
		state := states[r.ID]
		if state.Healthy {
			healthyCount++
		}
		replicas = append(replicas, replicaStatus{
			ID:          r.ID,
			Model:       r.Model,
			Tier:        string(r.Tier),
			Healthy:     state.Healthy,
			QueueDepth:  state.QueueDepth,
			KVCacheUtil: state.KVCacheUtil,
			Running:     state.Running,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":         "running",
		"total_replicas": len(h.cfg.Replicas),
		"healthy_count":  healthyCount,
		"replicas":       replicas,
		"config": map[string]any{
			"threshold":    h.cfg.Threshold,
			"affinity_ttl": h.cfg.AffinityTTL.String(),
			"max_queue":    h.cfg.MaxQueue,
			"weights": map[string]float64{
				"cache_affinity":    h.cfg.Weights.CacheAffinity,
				"queue_depth":       h.cfg.Weights.QueueDepth,
				"kv_cache_pressure": h.cfg.Weights.KVCachePressure,
				"baseline":          h.cfg.Weights.Baseline,
			},
		},
	})
}

func extractMessages(req openAIRequest) (system, user string) {
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			user = m.Content
		}
	}
	return
}

// countTurns returns the number of user/assistant message pairs,
// which indicates conversation depth for KV cache sizing.
func countTurns(req openAIRequest) int {
	turns := 0
	for _, m := range req.Messages {
		if m.Role == "user" {
			turns++
		}
	}
	return turns
}

func estimateTokens(text string) int {
	// rough estimate: 1 token ~ 4 characters
	return len(text) / 4
}

func hasCodeBlock(text string) bool {
	return strings.Contains(text, "```") ||
		strings.Contains(text, "func ") ||
		strings.Contains(text, "def ") ||
		strings.Contains(text, "class ")
}
