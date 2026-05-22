package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"prompt-response/internal/circuit"
	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/poller"
	"prompt-response/internal/scorer"
	"prompt-response/internal/store"
	"prompt-response/internal/types"

	"github.com/cespare/xxhash/v2"
)

// stubClassifier is a Classifier whose response is fixed by the test. It
// records the CurrentTier of every request so tests can assert the handler
// forwards the pinned tier to the classifier.
type stubClassifier struct {
	mu    sync.Mutex
	resp  *classifier.ClassifyResponse
	calls []types.ModelTier
}

func (s *stubClassifier) Classify(_ context.Context, req classifier.Request) (*classifier.ClassifyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req.CurrentTier)
	return s.resp, nil
}

func (s *stubClassifier) currentTiers() []types.ModelTier {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]types.ModelTier(nil), s.calls...)
}

// tierReplica builds a replica with its TierCfg populated, mirroring what
// config.ToReplicaList produces at runtime. Priority sets the escalation
// rank (higher priority = more capable tier).
func tierReplica(id, url, model string, tier types.ModelTier, priority int) config.Replica {
	return config.Replica{
		ID: id, URL: url, Model: model, Tier: tier,
		TierCfg: types.TierConfig{Name: tier, Priority: priority},
	}
}

func newConvTestHandler(replicas []config.Replica, cls classifier.Classifier) *Handler {
	mem := store.NewMemory()
	poll := poller.New(replicas, 0)
	cfg := &config.Config{
		Replicas: replicas,
		Weights: config.Weights{
			CacheAffinity: 0.50, QueueDepth: 0.25, KVCachePressure: 0.15, Baseline: 0.10,
		},
		AffinityTTL: 5 * time.Minute,
		MaxQueue:    20,
		Circuit: config.Circuit{
			ErrorThreshold: 0.5, WindowSize: 10 * time.Second,
			Cooldown: 30 * time.Second, MinSamples: 5,
		},
		Retry: config.Retry{MaxRetries: 1, Timeout: 30 * time.Second},
	}
	scor := scorer.New(replicas, mem, poll, cfg.Weights, cfg.AffinityTTL, cfg.MaxQueue)
	cr := circuit.NewRegistry(circuit.Config{
		ErrorThreshold: cfg.Circuit.ErrorThreshold, WindowSize: cfg.Circuit.WindowSize,
		Cooldown: cfg.Circuit.Cooldown, MinSamples: cfg.Circuit.MinSamples,
	})
	return New(scor, cls, cfg, cr, nil, nil)
}

// tierEchoBackend is a mock vLLM replica that streams its own label so a
// test can tell which replica served a request.
func tierEchoBackend(label string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", label)
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func chatRequest(convID, prompt string) *http.Request {
	payload := fmt.Sprintf(
		`{"messages":[{"role":"system","content":"sys"},{"role":"user","content":%q}]}`,
		prompt)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	if convID != "" {
		r.Header.Set("X-Conversation-Id", convID)
	}
	return r
}

// TestUpTierOnly_NoDowngrade verifies that once a conversation is pinned to a
// high tier, a later turn that classifies lower still routes to the high tier.
func TestUpTierOnly_NoDowngrade(t *testing.T) {
	smallBackend := tierEchoBackend("SMALL")
	defer smallBackend.Close()
	largeBackend := tierEchoBackend("LARGE")
	defer largeBackend.Close()

	replicas := []config.Replica{
		tierReplica("small-1", smallBackend.URL, "small-model", types.TierSmall, 1),
		tierReplica("large-1", largeBackend.URL, "large-model", types.TierLarge, 4),
	}
	// Classifier wants the small tier this turn.
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierSmall, Score: 0.05}}
	h := newConvTestHandler(replicas, cls)

	// Pin the conversation to the large tier from an earlier turn.
	convID := "conv-downgrade"
	h.scorer.Store().SetConversation(
		xxhash.Sum64String("cid:"+convID),
		types.ConvState{Tier: types.TierLarge, Model: "large-model"},
		5*time.Minute,
	)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, chatRequest(convID, "trivial follow-up"))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "LARGE") {
		t.Errorf("expected request to stay on the large tier, got body %q", body)
	}
	if got := cls.currentTiers(); len(got) != 1 || got[0] != types.TierLarge {
		t.Errorf("classifier should receive current_tier=large, got %v", got)
	}
}

// TestUpTierOnly_Escalates verifies a conversation does follow the classifier
// up to a higher tier.
func TestUpTierOnly_Escalates(t *testing.T) {
	smallBackend := tierEchoBackend("SMALL")
	defer smallBackend.Close()
	largeBackend := tierEchoBackend("LARGE")
	defer largeBackend.Close()

	replicas := []config.Replica{
		tierReplica("small-1", smallBackend.URL, "small-model", types.TierSmall, 1),
		tierReplica("large-1", largeBackend.URL, "large-model", types.TierLarge, 4),
	}
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierLarge, Score: 0.9}}
	h := newConvTestHandler(replicas, cls)

	convID := "conv-escalate"
	h.scorer.Store().SetConversation(
		xxhash.Sum64String("cid:"+convID),
		types.ConvState{Tier: types.TierSmall, Model: "small-model"},
		5*time.Minute,
	)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, chatRequest(convID, "now something hard"))

	if body := w.Body.String(); !strings.Contains(body, "LARGE") {
		t.Errorf("expected escalation to the large tier, got body %q", body)
	}
}

// TestConversationTierPinnedAcrossTurns verifies the first turn pins the
// conversation and the second turn forwards that tier to the classifier.
func TestConversationTierPinnedAcrossTurns(t *testing.T) {
	backend := tierEchoBackend("OK")
	defer backend.Close()

	replicas := []config.Replica{
		tierReplica("large-1", backend.URL, "large-model", types.TierLarge, 4),
	}
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierLarge, Score: 0.8}}
	h := newConvTestHandler(replicas, cls)

	convID := "conv-multiturn"

	// Turn 1: no prior state — classifier sees an empty current_tier.
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, chatRequest(convID, "first question"))
	if w1.Code != http.StatusOK {
		t.Fatalf("turn 1: expected 200, got %d", w1.Code)
	}

	st, ok := h.scorer.Store().GetConversation(xxhash.Sum64String("cid:" + convID))
	if !ok || st.Tier != types.TierLarge {
		t.Fatalf("turn 1 should pin the conversation to large, got %+v ok=%v", st, ok)
	}

	// Turn 2: the pinned tier must reach the classifier.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, chatRequest(convID, "second question"))
	if w2.Code != http.StatusOK {
		t.Fatalf("turn 2: expected 200, got %d", w2.Code)
	}

	got := cls.currentTiers()
	if len(got) != 2 {
		t.Fatalf("expected 2 classifier calls, got %d", len(got))
	}
	if got[0] != "" {
		t.Errorf("turn 1 current_tier should be empty, got %q", got[0])
	}
	if got[1] != types.TierLarge {
		t.Errorf("turn 2 current_tier should be large, got %q", got[1])
	}
}
