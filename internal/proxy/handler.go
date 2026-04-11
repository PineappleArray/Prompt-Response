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
	// health/readiness endpoints
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
		return
	case "/readyz":
		h.handleReadiness(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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
		http.Error(w, "no healthy replicas", http.StatusServiceUnavailable)
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
		http.Error(w, "bad replica URL", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // flush SSE chunks immediately

	r.Host = target.Host
	r.URL.Host = target.Host
	r.URL.Scheme = target.Scheme
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Wrap response writer to measure true time-to-first-token.
	// TTFT = time from request start to when the first SSE byte is written,
	// NOT total response time. These have different optimization strategies.
	start := time.Now()
	tw := &ttftWriter{ResponseWriter: w}
	proxy.ServeHTTP(tw, r)
	totalDuration := time.Since(start)

	ttft := totalDuration
	if tw.wrote {
		ttft = tw.firstByte.Sub(start)
	}

	h.scorer.RecordHit(prefixHash, replica.ID)

	metrics.RequestsTotal.WithLabelValues(string(result.Tier), replica.ID, cacheHit).Inc()
	metrics.RequestDuration.WithLabelValues(string(result.Tier), replica.ID).Observe(totalDuration.Seconds())
	metrics.TimeToFirstToken.WithLabelValues(string(result.Tier), replica.ID).Observe(ttft.Seconds())

	slog.Info("completed",
		"replica", replica.ID,
		"ttft_ms", ttft.Milliseconds(),
		"total_ms", totalDuration.Milliseconds(),
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

// ttftWriter wraps http.ResponseWriter to capture the time of the first Write
// call, enabling true time-to-first-token measurement for SSE streams.
type ttftWriter struct {
	http.ResponseWriter
	firstByte time.Time
	wrote     bool
}

func (w *ttftWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.firstByte = time.Now()
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *ttftWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
