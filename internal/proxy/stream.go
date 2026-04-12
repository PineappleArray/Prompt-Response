package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

// streamStats holds accumulated metrics from intercepting an SSE response stream.
type streamStats struct {
	FirstByteAt   time.Time
	LastTokenAt   time.Time
	OutputTokens  int
	ChunkCount    int
	InterTokenSum time.Duration // sum of inter-token gaps for ITL calculation
	Wrote         bool
}

// streamInterceptor wraps http.ResponseWriter to parse SSE chunks in real-time,
// counting output tokens and measuring inter-token latency (ITL) without
// modifying the stream. Subsumes ttftWriter's TTFT measurement.
type streamInterceptor struct {
	http.ResponseWriter
	stats streamStats
	buf   []byte // partial SSE line buffer for fragmented writes
}

func newStreamInterceptor(w http.ResponseWriter) *streamInterceptor {
	return &streamInterceptor{ResponseWriter: w}
}

func (si *streamInterceptor) Write(b []byte) (int, error) {
	now := time.Now()
	if !si.stats.Wrote {
		si.stats.FirstByteAt = now
		si.stats.Wrote = true
	}

	// Write bytes to the client first — never delay the stream.
	n, err := si.ResponseWriter.Write(b)

	// Parse SSE chunks from the written data for observability.
	si.buf = append(si.buf, b...)
	si.parseSSE(now)

	return n, err
}

func (si *streamInterceptor) Flush() {
	if f, ok := si.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Stats returns the accumulated stream statistics.
func (si *streamInterceptor) Stats() streamStats {
	return si.stats
}

// parseSSE extracts complete SSE events from the buffer and processes them.
// SSE events are delimited by double newlines (\n\n).
func (si *streamInterceptor) parseSSE(now time.Time) {
	for {
		idx := bytes.Index(si.buf, []byte("\n\n"))
		if idx == -1 {
			break
		}
		event := si.buf[:idx]
		si.buf = si.buf[idx+2:]
		si.processEvent(event, now)
	}
}

// sseChunk is the minimal structure needed to extract token content from an
// OpenAI-compatible SSE streaming response.
type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// processEvent handles a single SSE event line.
func (si *streamInterceptor) processEvent(event []byte, now time.Time) {
	// SSE format: "data: {json}" or "data: [DONE]"
	line := bytes.TrimSpace(event)
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return
	}
	payload := bytes.TrimPrefix(line, []byte("data: "))

	// [DONE] sentinel marks end of stream — not a token.
	if bytes.Equal(payload, []byte("[DONE]")) {
		return
	}

	var chunk sseChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return // malformed JSON — skip silently
	}

	if len(chunk.Choices) == 0 {
		return
	}

	content := chunk.Choices[0].Delta.Content
	if content == "" {
		return
	}

	// Count tokens using the same heuristic as input-side estimation:
	// 1 token ~ 4 characters. Minimum 1 token for any non-empty content.
	tokens := len(content) / 4
	if tokens < 1 {
		tokens = 1
	}
	si.stats.OutputTokens += tokens
	si.stats.ChunkCount++

	// Inter-token latency: time since the last chunk with content.
	if si.stats.ChunkCount > 1 {
		si.stats.InterTokenSum += now.Sub(si.stats.LastTokenAt)
	}
	si.stats.LastTokenAt = now
}
