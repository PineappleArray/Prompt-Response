// Command mock is a dependency-free stand-in for the production router
// (cmd/router). It serves the same HTTP surface the React frontend talks to,
// but never calls a real upstream: chat completions are streamed from a small
// set of canned replies, and the metrics endpoints return static seed data
// (see data.go). This lets my-llm-ui be developed and demoed without vLLM
// replicas, Redis, or Postgres running.
//
// Run it with:
//
//	go run ./cmd/mock           # listens on :8080
//	LISTEN_ADDR=:9000 go run ./cmd/mock
//
// The frontend's Vite dev server proxies /v1 to localhost:8080, so the
// defaults line up with `npm run dev` in my-llm-ui.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// tokenDelay is the simulated pause between streamed SSE chunks, giving the
// frontend a realistic token cadence to render.
const tokenDelay = 35 * time.Millisecond

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	addr := ":8080"
	if a := os.Getenv("LISTEN_ADDR"); a != "" {
		addr = a
	}

	slog.Info("starting prompt-response MOCK router (no upstream calls)", "addr", addr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("mock router listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully", "timeout", "5s")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	slog.Info("shutdown complete")
}

// replyCounter rotates through cannedReplies so successive requests vary.
var replyCounter atomic.Uint64

// newMux wires the mock HTTP routes. Kept separate from main so tests can
// exercise the handlers via httptest without binding a socket.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Chat streaming — registered under both the path the frontend currently
	// uses (/v1/stream) and the OpenAI-compatible path the real router serves
	// (/v1/chat/completions), so the mock works regardless of which the UI hits.
	mux.HandleFunc("/v1/stream", handleStream)
	mux.HandleFunc("/v1/chat/completions", handleStream)

	// Metrics surface consumed by the frontend's usage page.
	mux.HandleFunc("/v1/router/usage", handleUsage)
	mux.HandleFunc("/v1/router/status", handleStatus)
	mux.HandleFunc("/v1/router/tiers", handleTiers)
	mux.HandleFunc("/v1/models", handleModels)

	// Probes.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
	})

	return mux
}

// chatRequest is the minimal subset of the OpenAI chat-completions body the
// mock needs. The actual message content is ignored; the reply is canned.
type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// handleStream emits an OpenAI-compatible SSE stream, byte-for-byte matching
// the framing the production streamInterceptor produces (internal/proxy/
// stream.go) so the existing useChat.ts parser handles it unchanged:
//
//	data: {"choices":[{"index":0,"delta":{"content":"..."}}]}\n\n
//	...
//	data: [DONE]\n\n
func handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed", "invalid_request_error"))
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid JSON body", "invalid_request_error"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorBody("streaming unsupported", "server_error"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	reply := cannedReplies[replyCounter.Add(1)%uint64(len(cannedReplies))]
	pieces := splitTokens(reply)

	ctx := r.Context()
	for i, piece := range pieces {
		select {
		case <-ctx.Done():
			// Client aborted (e.g. the Stop button) — stop cleanly mid-stream.
			return
		default:
		}

		delta := map[string]string{"content": piece}
		if i == 0 {
			delta["role"] = "assistant"
		}
		writeSSEChunk(w, delta)
		flusher.Flush()

		select {
		case <-ctx.Done():
			return
		case <-time.After(tokenDelay):
		}
	}

	w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

// writeSSEChunk serializes a single OpenAI-compatible streaming chunk and
// writes it as one SSE event.
func writeSSEChunk(w http.ResponseWriter, delta map[string]string) {
	chunk := map[string]any{
		"choices": []map[string]any{
			{"index": 0, "delta": delta, "finish_reason": nil},
		},
	}
	b, _ := json.Marshal(chunk)
	w.Write([]byte("data: "))
	w.Write(b)
	w.Write([]byte("\n\n"))
}

// splitTokens breaks text into small streaming pieces, keeping the trailing
// space on each word so the reassembled content reads naturally.
func splitTokens(text string) []string {
	words := strings.Fields(text)
	pieces := make([]string, 0, len(words))
	for i, word := range words {
		if i < len(words)-1 {
			pieces = append(pieces, word+" ")
		} else {
			pieces = append(pieces, word)
		}
	}
	return pieces
}

// handleUsage mirrors /v1/router/usage on the real router.
func handleUsage(w http.ResponseWriter, r *http.Request) {
	all := mockUsage()
	if tenant := r.URL.Query().Get("tenant"); tenant != "" {
		u, ok := all[tenant]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{"message": "tenant not found", "type": "not_found", "tenant": tenant},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tenant": tenant, "usage": u, "enabled": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenants": all,
		"count":   len(all),
		"enabled": true,
	})
}

// handleStatus mirrors /v1/router/status on the real router.
func handleStatus(w http.ResponseWriter, _ *http.Request) {
	replicas := mockReplicas()
	healthy := 0
	for _, rep := range replicas {
		if rep.Healthy {
			healthy++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "running",
		"total_replicas": len(replicas),
		"healthy_count":  healthy,
		"replicas":       replicas,
		"config": map[string]any{
			"threshold":    0.35,
			"affinity_ttl": "5m0s",
			"max_queue":    20.0,
			"weights": map[string]float64{
				"cache_affinity":    0.50,
				"queue_depth":       0.25,
				"kv_cache_pressure": 0.15,
				"baseline":          0.10,
			},
		},
	})
}

// handleTiers exposes the per-tier cost/savings breakdown. This is mock-only
// (the real router surfaces equivalent data via Prometheus), giving the
// frontend a convenient pre-aggregated source for its bar charts.
func handleTiers(w http.ResponseWriter, _ *http.Request) {
	tiers := mockTierUsage()
	var totalReq int
	var totalSavings, totalRouted, totalBaseline float64
	for _, t := range tiers {
		totalReq += t.Requests
		totalSavings += t.Savings
		totalRouted += t.RoutedCost
		totalBaseline += t.BaselineCost
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tiers":          tiers,
		"total_requests": totalReq,
		"total_savings":  totalSavings,
		"total_routed":   totalRouted,
		"total_baseline": totalBaseline,
	})
}

// handleModels mirrors /v1/models on the real router.
func handleModels(w http.ResponseWriter, _ *http.Request) {
	seen := map[string]bool{}
	var models []map[string]string
	for _, rep := range mockReplicas() {
		if seen[rep.Model] {
			continue
		}
		seen[rep.Model] = true
		models = append(models, map[string]string{
			"id":       rep.Model,
			"object":   "model",
			"owned_by": "prompt-response",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func errorBody(msg, typ string) map[string]any {
	return map[string]any{"error": map[string]any{"message": msg, "type": typ}}
}
