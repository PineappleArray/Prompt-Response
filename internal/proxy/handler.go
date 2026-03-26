package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/scorer"
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
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	// parse messages
	var req openAIRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// extract system prompt + user message
	systemPrompt, userMessage := extractMessages(req)

	// hash the system prompt for cache affinity
	prefixHash := xxhash.Sum64String(systemPrompt)

	// classify the request
	classReq := classifier.Request{
		SystemPrompt: systemPrompt,
		UserMessage:  userMessage,
		TokenCount:   estimateTokens(systemPrompt + userMessage),
		HasCode:      hasCodeBlock(userMessage),
	}
	result, _ := h.classifier.Classify(classReq)

	// pick the best replica
	replica := h.scorer.Pick(prefixHash)
	if replica.ID == "" {
		http.Error(w, "no healthy replicas", http.StatusServiceUnavailable)
		return
	}

	log.Printf("routing: replica=%s tier=%s score_reason=%s hash=%d",
		replica.ID, result.Tier, result.Reason, prefixHash)

	// reverse proxy to chosen replica
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

	start := time.Now()
	proxy.ServeHTTP(w, r)
	ttft := time.Since(start)

	// record affinity after response
	h.scorer.RecordHit(prefixHash, replica.ID)

	log.Printf("completed: replica=%s ttft=%dms", replica.ID, ttft.Milliseconds())
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

func estimateTokens(text string) int {
	// rough estimate: 1 token ≈ 4 characters
	return len(text) / 4
}

func hasCodeBlock(text string) bool {
	return strings.Contains(text, "```") ||
		strings.Contains(text, "func ") ||
		strings.Contains(text, "def ") ||
		strings.Contains(text, "class ")
}