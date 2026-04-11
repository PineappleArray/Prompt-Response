package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"prompt-response/internal/circuit"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/metrics"
	"prompt-response/internal/scorer"
	"prompt-response/internal/types"
)

// hopByHop headers that must not be forwarded between client and upstream.
var hopByHop = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

type Handler struct {
	scorer     *scorer.Scorer
	classifier *classifier.HeuristicClassifier
	cfg        *config.Config
	circuit    *circuit.Registry
	client     *http.Client
}

func New(s *scorer.Scorer, c *classifier.HeuristicClassifier, cfg *config.Config, cr *circuit.Registry) *Handler {
	return &Handler{
		scorer:     s,
		classifier: c,
		cfg:        cfg,
		circuit:    cr,
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
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

	// Retry loop: attempt the request on the best replica, retrying on upstream
	// failures (5xx, connection errors) with a different replica each time.
	var resp *http.Response
	var chosenReplica config.Replica
	var cancelUpstream context.CancelFunc
	excluded := make(map[string]bool)
	maxAttempts := 1 + h.cfg.Retry.MaxRetries

	for attempt := 0; attempt < maxAttempts; attempt++ {
		replica := h.scorer.Pick(prefixHash, result.Tier, h.circuit, excluded)
		if replica.ID == "" {
			break
		}

		attemptCtx, cancel := context.WithTimeout(r.Context(), h.cfg.Retry.Timeout)
		upstream, err := h.doUpstream(attemptCtx, replica, body, r)

		if err == nil && upstream.StatusCode < 500 {
			resp = upstream
			chosenReplica = replica
			cancelUpstream = cancel // deferred until body is fully consumed
			if h.circuit != nil {
				h.circuit.RecordSuccess(replica.ID)
			}
			break
		}

		cancel() // safe to cancel failed attempts immediately

		// Record upstream failure
		if h.circuit != nil {
			h.circuit.RecordFailure(replica.ID)
		}
		metrics.UpstreamErrorsTotal.WithLabelValues(replica.ID).Inc()
		excluded[replica.ID] = true

		if upstream != nil {
			upstream.Body.Close()
		}

		if attempt < maxAttempts-1 {
			metrics.RetriesTotal.WithLabelValues(replica.ID).Inc()
			slog.Warn("upstream failed, retrying",
				"replica", replica.ID,
				"attempt", attempt+1,
				"err", err,
			)
		}
	}

	if resp == nil {
		writeError(w, r, http.StatusServiceUnavailable, "no_replicas", "no healthy replicas available")
		return
	}
	defer cancelUpstream()
	defer resp.Body.Close()

	cacheHit := "miss"
	if aff, ok := h.scorer.Store().GetAffinity(prefixHash); ok && aff == chosenReplica.ID {
		cacheHit = "hit"
	}

	slog.Info("routing request",
		"replica", chosenReplica.ID,
		"tier_requested", result.Tier,
		"tier_matched", chosenReplica.Tier,
		"classifier_score", result.Score,
		"prefix_hash", prefixHash,
		"cache_hit", cacheHit,
		"reason", result.Reason,
		"attempts", len(excluded)+1,
	)

	// Copy response headers to client, then stream body through interceptor.
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	start := time.Now()
	sw := newStreamInterceptor(w)
	streamBody(sw, resp.Body)
	totalDuration := time.Since(start)

	stats := sw.Stats()
	ttft := totalDuration
	if stats.Wrote {
		ttft = stats.FirstByteAt.Sub(start)
	}

	h.scorer.RecordHit(prefixHash, chosenReplica.ID)

	if h.circuit != nil {
		metrics.CircuitState.WithLabelValues(chosenReplica.ID).Set(float64(h.circuit.State(chosenReplica.ID)))
	}

	tier := string(result.Tier)
	metrics.RequestsTotal.WithLabelValues(tier, chosenReplica.ID, cacheHit).Inc()
	metrics.RequestDuration.WithLabelValues(tier, chosenReplica.ID).Observe(totalDuration.Seconds())
	metrics.TimeToFirstToken.WithLabelValues(tier, chosenReplica.ID).Observe(ttft.Seconds())

	// Stream-level metrics: output tokens, throughput, and inter-token latency.
	var tps float64
	var avgITLMs int64
	if stats.OutputTokens > 0 {
		metrics.OutputTokens.WithLabelValues(tier, chosenReplica.ID).Observe(float64(stats.OutputTokens))

		if streamDur := stats.LastTokenAt.Sub(stats.FirstByteAt).Seconds(); streamDur > 0 {
			tps = float64(stats.OutputTokens) / streamDur
			metrics.TokensPerSecond.WithLabelValues(tier, chosenReplica.ID).Observe(tps)
		}

		if stats.ChunkCount > 1 {
			avgITL := stats.InterTokenSum / time.Duration(stats.ChunkCount-1)
			avgITLMs = avgITL.Milliseconds()
			metrics.InterTokenLatency.WithLabelValues(tier, chosenReplica.ID).Observe(avgITL.Seconds())
		}
	}

	slog.Info("completed",
		"replica", chosenReplica.ID,
		"ttft_ms", ttft.Milliseconds(),
		"total_ms", totalDuration.Milliseconds(),
		"output_tokens", stats.OutputTokens,
		"tokens_per_sec", tps,
		"avg_itl_ms", avgITLMs,
		"cache_hit", cacheHit,
	)
}

// doUpstream sends the request body to the given replica and returns the
// raw response. The caller is responsible for closing resp.Body.
func (h *Handler) doUpstream(ctx context.Context, replica config.Replica, body []byte, orig *http.Request) (*http.Response, error) {
	upstreamURL := replica.URL + orig.URL.Path
	req, err := http.NewRequestWithContext(ctx, orig.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, orig.Header)
	req.Host = ""
	return h.client.Do(req)
}

// streamBody copies the upstream response body to the stream interceptor,
// flushing after each read to ensure SSE chunks are sent to the client
// immediately (equivalent to httputil.ReverseProxy FlushInterval=-1).
func streamBody(sw *streamInterceptor, body io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, wErr := sw.Write(buf[:n]); wErr != nil {
				break
			}
			sw.Flush()
		}
		if err != nil {
			break
		}
	}
}

// copyHeaders copies non-hop-by-hop headers from src to dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if hopByHop[http.CanonicalHeaderKey(k)] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
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
		Circuit     string  `json:"circuit"`
		ErrorRate   float64 `json:"error_rate"`
	}

	var replicas []replicaStatus
	healthyCount := 0
	for _, r := range h.cfg.Replicas {
		state := states[r.ID]
		if state.Healthy {
			healthyCount++
		}
		circuitState := "closed"
		errorRate := 0.0
		if h.circuit != nil {
			circuitState = h.circuit.State(r.ID).String()
			errorRate = h.circuit.ErrorRate(r.ID)
		}
		replicas = append(replicas, replicaStatus{
			ID:          r.ID,
			Model:       r.Model,
			Tier:        string(r.Tier),
			Healthy:     state.Healthy,
			QueueDepth:  state.QueueDepth,
			KVCacheUtil: state.KVCacheUtil,
			Running:     state.Running,
			Circuit:     circuitState,
			ErrorRate:   errorRate,
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
			"circuit": map[string]any{
				"error_threshold": h.cfg.Circuit.ErrorThreshold,
				"window_size":     h.cfg.Circuit.WindowSize.String(),
				"cooldown":        h.cfg.Circuit.Cooldown.String(),
				"min_samples":     h.cfg.Circuit.MinSamples,
			},
			"retry": map[string]any{
				"max_retries": h.cfg.Retry.MaxRetries,
				"timeout":     h.cfg.Retry.Timeout.String(),
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
