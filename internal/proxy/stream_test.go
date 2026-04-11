package proxy

import (
	"net/http/httptest"
	"testing"
	"time"
)

func sseChunkData(content string) []byte {
	return []byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + content + "\"}}]}\n\n")
}

func TestStreamInterceptor_BasicSSE(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	si.Write(sseChunkData("Hello"))
	si.Write(sseChunkData(" world"))
	si.Write([]byte("data: [DONE]\n\n"))

	stats := si.Stats()
	if stats.ChunkCount != 2 {
		t.Errorf("expected 2 content chunks, got %d", stats.ChunkCount)
	}
	if stats.OutputTokens < 2 {
		t.Errorf("expected at least 2 tokens, got %d", stats.OutputTokens)
	}
	if !stats.Wrote {
		t.Error("expected Wrote to be true")
	}
	// Verify data passed through to client unchanged
	if w.Body.Len() == 0 {
		t.Error("expected response body to contain proxied data")
	}
}

func TestStreamInterceptor_EmptyContent(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	// Empty delta.content should not count as tokens
	si.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"\"}}]}\n\n"))
	si.Write(sseChunkData("actual"))

	stats := si.Stats()
	if stats.ChunkCount != 1 {
		t.Errorf("expected 1 content chunk (empty skipped), got %d", stats.ChunkCount)
	}
}

func TestStreamInterceptor_DoneSignal(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	si.Write(sseChunkData("token"))
	si.Write([]byte("data: [DONE]\n\n"))

	stats := si.Stats()
	if stats.ChunkCount != 1 {
		t.Errorf("expected 1 chunk ([DONE] excluded), got %d", stats.ChunkCount)
	}
}

func TestStreamInterceptor_TTFT(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	before := time.Now()
	si.Write(sseChunkData("first"))
	after := time.Now()

	stats := si.Stats()
	if stats.FirstByteAt.Before(before) || stats.FirstByteAt.After(after) {
		t.Errorf("FirstByteAt %v not between %v and %v", stats.FirstByteAt, before, after)
	}
}

func TestStreamInterceptor_ITL(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	si.Write(sseChunkData("first"))
	time.Sleep(10 * time.Millisecond)
	si.Write(sseChunkData("second"))
	time.Sleep(10 * time.Millisecond)
	si.Write(sseChunkData("third"))

	stats := si.Stats()
	if stats.ChunkCount != 3 {
		t.Fatalf("expected 3 chunks, got %d", stats.ChunkCount)
	}
	// InterTokenSum should be at least ~20ms (2 gaps × ~10ms each)
	if stats.InterTokenSum < 15*time.Millisecond {
		t.Errorf("expected InterTokenSum >= 15ms, got %v", stats.InterTokenSum)
	}
}

func TestStreamInterceptor_FragmentedWrite(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	// Split a single SSE event across two Write calls
	full := sseChunkData("fragmented")
	mid := len(full) / 2
	si.Write(full[:mid])
	si.Write(full[mid:])

	stats := si.Stats()
	if stats.ChunkCount != 1 {
		t.Errorf("expected 1 chunk from fragmented writes, got %d", stats.ChunkCount)
	}
	if stats.OutputTokens < 1 {
		t.Errorf("expected at least 1 token, got %d", stats.OutputTokens)
	}
}

func TestStreamInterceptor_NonSSE(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	// Plain JSON response (non-streaming) should pass through without panic
	si.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))

	stats := si.Stats()
	if stats.ChunkCount != 0 {
		t.Errorf("expected 0 chunks for non-SSE response, got %d", stats.ChunkCount)
	}
	if stats.OutputTokens != 0 {
		t.Errorf("expected 0 tokens for non-SSE response, got %d", stats.OutputTokens)
	}
}

func TestStreamInterceptor_Flush(t *testing.T) {
	w := httptest.NewRecorder()
	si := newStreamInterceptor(w)

	si.Write(sseChunkData("test"))
	// Flush should not panic — httptest.ResponseRecorder implements http.Flusher
	si.Flush()

	if !w.Flushed {
		t.Error("expected underlying writer to be flushed")
	}
}
