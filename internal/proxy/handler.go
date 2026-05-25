package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"prompt-response/internal/audit"
	"prompt-response/internal/auth"
	"prompt-response/internal/circuit"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/metrics"
	"prompt-response/internal/middleware"
	"prompt-response/internal/scorer"
	"prompt-response/internal/types"
	"prompt-response/internal/usage"

	"github.com/cespare/xxhash/v2"
)

// anonymousTenant is the tenant key used for unauthenticated requests when
// recording token usage. Keeps the tenant label cardinality bounded and
// distinguishes unauthenticated traffic in billing reports.
const anonymousTenant = "anonymous"

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
	classifier classifier.Classifier
	cfg        *config.Config
	replicas   types.ReplicaList
	circuit    *circuit.Registry
	audit      *audit.Trail
	usage      *usage.Tracker
	usageSink  usage.Sink
	client     *http.Client
}

// SetUsageSink attaches an async usage Sink (e.g. PostgresSink). Nil is
// treated as "no sink" — usage continues to be tracked in memory only.
func (h *Handler) SetUsageSink(s usage.Sink) { h.usageSink = s }

func New(s *scorer.Scorer, c classifier.Classifier, cfg *config.Config, cr *circuit.Registry, trail *audit.Trail, tracker *usage.Tracker) *Handler {
	return &Handler{
		scorer:     s,
		classifier: c,
		cfg:        cfg,
		replicas:   types.ReplicaList{Replicas: cfg.Replicas},
		circuit:    cr,
		audit:      trail,
		usage:      tracker,
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
	case "/v1/router/audit":
		h.handleAudit(w, r)
		return
	case "/v1/router/usage":
		h.handleUsage(w, r)
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

	body = h.injectDefaults(body)

	if len(req.Messages) == 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "messages array is required and must not be empty")
		return
	}

	systemPrompt, userMessage := extractMessages(req)

	// Hash the first user message (not the system prompt). The first user
	// message is the only one guaranteed to be present and unchanged on
	// every turn of a conversation, so all turns route to the same replica
	// and warm the same KV cache. Hashing the system prompt would collide
	// many distinct conversations onto one replica; hashing the last user
	// message would churn the prefix cache turn to turn.
	firstUser := firstUserMessage(req)
	prefixHash := xxhash.Sum64String(firstUser)

	// Conversation tier lock: look up the tier this conversation is already
	// pinned to (if any) so the classifier can only escalate, never downgrade.
	convHash := conversationHash(r, firstUser)
	prevConv, hasConv := h.scorer.Store().GetConversation(convHash)

	classReq := classifier.Request{
		SystemPrompt: systemPrompt,
		UserMessage:  userMessage,
		TokenCount:   estimateTokens(systemPrompt + userMessage),
		HasCode:      hasCodeBlock(userMessage),
		ConvTurns:    countTurns(req),
		CurrentTier:  prevConv.Tier,
	}

	classResult, err := h.classifier.Classify(r.Context(), classReq)
	if err != nil {
		slog.Error("classification failed, defaulting to small", "err", err)
		classResult = &classifier.ClassifyResponse{
			Tier:        types.ModelTier(types.TierSmall),
			BuildReason: "classification error fallback",
		}
	}

	// map to the fields the rest of the handler uses
	result := classifier.Response{
		Tier:    classResult.Tier,
		Score:   classResult.Score,
		Signals: classResult.Signals,
		Reason:  classResult.BuildReason,
	}

	// Up-tier-only enforcement: a conversation's tier never decreases. If a
	// later turn classifies lower than the conversation is pinned to, keep
	// the pin. The Python pick() applies the same clamp; this re-clamp is
	// defense-in-depth in case the conversation state changed concurrently.
	if hasConv {
		finalTier := h.replicas.HigherTier(prevConv.Tier, result.Tier)
		if finalTier != prevConv.Tier {
			metrics.TierEscalationsTotal.
				WithLabelValues(string(prevConv.Tier), string(finalTier)).Inc()
			slog.Info("conversation escalated to higher tier",
				"conv_hash", convHash,
				"from_tier", prevConv.Tier,
				"to_tier", finalTier,
				"classifier_tier", result.Tier,
			)
		}
		result.Tier = finalTier
	}

	metrics.ClassifierScore.WithLabelValues(string(result.Tier)).Observe(result.Score)

	// Retry loop: attempt the request on the best replica, retrying on upstream
	// failures (5xx, connection errors, or pre-first-byte stalls indicating a
	// dead replica) with a different replica each time.
	requestStart := time.Now()
	var resp *http.Response
	var bodyReader io.Reader
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
			// Dead-replica detection: peek the upstream body before
			// committing response headers to the client. If the replica
			// accepted the request but never produces a single byte
			// within the stall window, treat it as dead and retry on a
			// different replica — the client never sees this attempt.
			if stall := h.cfg.Stream.StallTimeout; stall > 0 {
				peek := peekFirstByte(upstream.Body, stall)
				if peek.err != nil {
					cancel()
					upstream.Body.Close()
					if h.circuit != nil {
						h.circuit.RecordFailure(replica.ID)
					}
					metrics.UpstreamErrorsTotal.WithLabelValues(replica.ID).Inc()
					metrics.DeadReplicasTotal.WithLabelValues(replica.ID, "pre_first_byte").Inc()
					excluded[replica.ID] = true
					if attempt < maxAttempts-1 {
						metrics.RetriesTotal.WithLabelValues(replica.ID).Inc()
						slog.Warn("dead replica detected (no first byte), retrying",
							"replica", replica.ID,
							"stall_timeout", stall.String(),
							"attempt", attempt+1,
						)
					}
					continue
				}
				bodyReader = peek.reader
			} else {
				bodyReader = upstream.Body
			}

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
		if h.audit != nil {
			h.audit.Record(audit.Record{
				Timestamp:  time.Now(),
				RequestID:  middleware.GetRequestID(r.Context()),
				Tenant:     tenantID(r),
				Tier:       string(result.Tier),
				ClassScore: result.Score,
				Signals:    result.Signals,
				Attempts:   len(excluded),
				StatusCode: http.StatusServiceUnavailable,
				Reason:     result.Reason,
			})
		}
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

	// Mid-stream stall watchdog: if the replica goes silent after streaming
	// has begun, the connection is aborted and counted as a dead replica.
	// Reroute is no longer possible — bytes have already reached the client —
	// but the failure is recorded against the circuit breaker so future
	// requests avoid this replica.
	var midStreamStall atomic.Bool
	var watchdog *stallWatchdog
	if stall := h.cfg.Stream.StallTimeout; stall > 0 {
		watchdog = newStallWatchdog(stall, sw.LastActivity, func() {
			midStreamStall.Store(true)
			cancelUpstream()
		})
	}
	streamBody(sw, bodyReader)
	if watchdog != nil {
		watchdog.Stop()
	}
	totalDuration := time.Since(start)

	stats := sw.Stats()
	ttft := totalDuration
	if stats.Wrote {
		ttft = stats.FirstByteAt.Sub(start)
	}

	if midStreamStall.Load() || !stats.DoneSeen {
		phase := "missing_done"
		if midStreamStall.Load() {
			phase = "mid_stream"
		}
		metrics.DeadReplicasTotal.WithLabelValues(chosenReplica.ID, phase).Inc()
		if h.circuit != nil {
			h.circuit.RecordFailure(chosenReplica.ID)
		}
		slog.Warn("dead replica during stream",
			"replica", chosenReplica.ID,
			"phase", phase,
			"output_tokens", stats.OutputTokens,
			"elapsed_ms", totalDuration.Milliseconds(),
		)
	} else {
		// Prompt-to-terminator latency: end-to-end time from request entry
		// (requestStart, before classification and replica selection) to
		// the moment the SSE [DONE] sentinel is observed. Only recorded
		// for clean completions so the distribution reflects healthy
		// end-to-end behavior.
		promptToDone := time.Since(requestStart)
		metrics.PromptToDoneDuration.
			WithLabelValues(string(result.Tier), chosenReplica.ID).
			Observe(promptToDone.Seconds())
	}

	h.scorer.RecordHit(prefixHash, chosenReplica.ID)

	// Pin the conversation to the tier it was just served. Subsequent turns
	// read this back and can only escalate from here. Sharing affinity_ttl
	// keeps the conversation pin alive exactly as long as the prefix cache.
	h.scorer.Store().SetConversation(convHash, types.ConvState{
		Tier:      result.Tier,
		Model:     chosenReplica.Model,
		Bucket:    types.ScoreBucket(result.Score),
		Turns:     countTurns(req),
		UpdatedAt: time.Now(),
	}, h.cfg.AffinityTTL)

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

	if h.audit != nil {
		h.audit.Record(audit.Record{
			Timestamp:    time.Now(),
			RequestID:    middleware.GetRequestID(r.Context()),
			Tenant:       tenantID(r),
			Tier:         string(result.Tier),
			ClassScore:   result.Score,
			Signals:      result.Signals,
			ReplicaID:    chosenReplica.ID,
			ReplicaTier:  string(chosenReplica.Tier),
			CacheHit:     cacheHit == "hit",
			Attempts:     len(excluded) + 1,
			TTFTMs:       ttft.Milliseconds(),
			TotalMs:      totalDuration.Milliseconds(),
			OutputTokens: stats.OutputTokens,
			StatusCode:   resp.StatusCode,
			Reason:       result.Reason,
		})
	}

	if h.usage != nil {
		tenant := tenantID(r)
		if tenant == "" {
			tenant = anonymousTenant
		}
		h.usage.Record(tenant, classReq.TokenCount, stats.OutputTokens)
		metrics.TokensConsumedTotal.WithLabelValues(tenant, "input").Add(float64(classReq.TokenCount))
		metrics.TokensConsumedTotal.WithLabelValues(tenant, "output").Add(float64(stats.OutputTokens))

		if h.usageSink != nil {
			h.usageSink.Enqueue(usage.UsageEvent{
				Tenant: tenant,
				In:     classReq.TokenCount,
				Out:    stats.OutputTokens,
				At:     time.Now(),
			})
		}
	}
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
// immediately (equivalent to httputil.ReverseProxy FlushInterval=-1). The
// body is an io.Reader so the caller may layer a peeked-byte prefix in
// front of the upstream response for dead-replica detection.
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

// handleAudit returns recent routing decisions from the audit trail.
func (h *Handler) handleAudit(w http.ResponseWriter, r *http.Request) {
	if h.audit == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"records": []any{},
			"count":   0,
			"enabled": false,
		})
		return
	}

	n := 50
	if qs := r.URL.Query().Get("limit"); qs != "" {
		if parsed, err := strconv.Atoi(qs); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 200 {
		n = 200
	}

	records := h.audit.Recent(n)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"records": records,
		"count":   len(records),
		"enabled": true,
	})
}

// handleUsage returns per-tenant token consumption. With ?tenant=X, returns
// just that tenant's usage; otherwise returns all tenants.
func (h *Handler) handleUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.usage == nil {
		json.NewEncoder(w).Encode(map[string]any{
			"tenants": map[string]any{},
			"count":   0,
			"enabled": false,
		})
		return
	}

	if tenant := r.URL.Query().Get("tenant"); tenant != "" {
		u, ok := h.usage.Get(tenant)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "tenant not found",
					"type":    "not_found",
					"tenant":  tenant,
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"tenant":  tenant,
			"usage":   u,
			"enabled": true,
		})
		return
	}

	all := h.usage.All()
	json.NewEncoder(w).Encode(map[string]any{
		"tenants": all,
		"count":   len(all),
		"enabled": true,
	})
}

func tenantID(r *http.Request) string {
	if t, ok := auth.TenantFromContext(r.Context()); ok {
		return t.ID
	}
	return ""
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

// conversationHash derives a stable key for a multi-turn conversation. An
// explicit X-Conversation-Id header wins; otherwise the conversation is
// identified by its first user message, which every turn of a standard
// chat-completions exchange resends unchanged.
func conversationHash(r *http.Request, firstUser string) uint64 {
	if id := r.Header.Get("X-Conversation-Id"); id != "" {
		return xxhash.Sum64String("cid:" + id)
	}
	return xxhash.Sum64String(firstUser)
}

// firstUserMessage returns the content of the earliest user message, which
// anchors the conversation identity.
func firstUserMessage(req openAIRequest) string {
	for _, m := range req.Messages {
		if m.Role == "user" {
			return m.Content
		}
	}
	return ""
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

func (h *Handler) injectDefaults(body []byte) []byte {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	modified := false
	if _, ok := parsed["frequency_penalty"]; !ok && h.cfg.Repetition.FrequencyPenalty != nil {
		parsed["frequency_penalty"] = *h.cfg.Repetition.FrequencyPenalty
		modified = true
	}
	if _, ok := parsed["presence_penalty"]; !ok && h.cfg.Repetition.PresencePenalty != nil {
		parsed["presence_penalty"] = *h.cfg.Repetition.PresencePenalty
		modified = true
	}

	if !modified {
		return body
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return out
}
