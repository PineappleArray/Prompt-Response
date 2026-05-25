package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"prompt-response/internal/classifier"
	"prompt-response/internal/config"
	"prompt-response/internal/types"

	"github.com/cespare/xxhash/v2"
)

// TestMain silences slog for the whole proxy test binary so per-request
// INFO lines don't pollute the benchmark output. Test failures still
// surface via t.Error/t.Fatal.
func TestMain(m *testing.M) {
	testing.Init()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// BenchmarkServeHTTP_SingleTurn measures end-to-end cost of one POST
// through the handler (parse, classify, pick, proxy, stream, record).
// No prior conversation, no header — the cold path.
func BenchmarkServeHTTP_SingleTurn(b *testing.B) {
	backend := tierEchoBackend("ok")
	defer backend.Close()

	replicas := []config.Replica{
		tierReplica("small-1", backend.URL, "small-model", types.TierSmall, 1),
	}
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierSmall, Score: 0.1}}
	h := newConvTestHandler(replicas, cls)

	payload := []byte(`{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"hello"}]}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
		h.ServeHTTP(w, r)
	}
}

// BenchmarkServeHTTP_MultiTurnPinned measures the warm path: a
// conversation already pinned in the store, so every iteration takes
// the extra GetConversation/SetConversation round-trip that the
// conversation pinning added.
func BenchmarkServeHTTP_MultiTurnPinned(b *testing.B) {
	backend := tierEchoBackend("ok")
	defer backend.Close()

	replicas := []config.Replica{
		tierReplica("small-1", backend.URL, "small-model", types.TierSmall, 1),
		tierReplica("large-1", backend.URL, "large-model", types.TierLarge, 4),
	}
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierSmall, Score: 0.1}}
	h := newConvTestHandler(replicas, cls)

	convID := "bench-conv"
	h.scorer.Store().SetConversation(
		xxhash.Sum64String("cid:"+convID),
		types.ConvState{Tier: types.TierSmall, Model: "small-model"},
		5*time.Minute,
	)

	payload := []byte(`{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"hello"}]}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
		r.Header.Set("X-Conversation-Id", convID)
		h.ServeHTTP(w, r)
	}
}

// BenchmarkServeHTTP_Parallel saturates the handler from many goroutines
// to approximate Poisson-arrival behaviour under load. Pairs with the
// per-iteration benchmarks above to highlight contention costs.
func BenchmarkServeHTTP_Parallel(b *testing.B) {
	backend := tierEchoBackend("ok")
	defer backend.Close()

	replicas := []config.Replica{
		tierReplica("small-1", backend.URL, "small-model", types.TierSmall, 1),
	}
	cls := &stubClassifier{resp: &classifier.ClassifyResponse{Tier: types.TierSmall, Score: 0.1}}
	h := newConvTestHandler(replicas, cls)

	payload := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
			h.ServeHTTP(w, r)
		}
	})
}

// BenchmarkHashing_FirstUserMessage measures the cost of hashing a
// short first user message — the typical hot-path input after this
// branch's hashing refactor.
func BenchmarkHashing_FirstUserMessage(b *testing.B) {
	const um = "what is the capital of france?"
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = xxhash.Sum64String(um)
	}
}

// BenchmarkHashing_SystemPrompt measures the cost of hashing a typical
// ~1.5KB system prompt — what the router used to hash before this
// branch.
func BenchmarkHashing_SystemPrompt(b *testing.B) {
	sp := strings.Repeat("You are a helpful assistant. ", 50)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = xxhash.Sum64String(sp)
	}
}
