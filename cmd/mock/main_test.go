package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleStream_SSEFormat(t *testing.T) {
	body := strings.NewReader(`{"model":"sonnet","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/stream", body)
	w := httptest.NewRecorder()

	handleStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	out := w.Body.String()
	if !strings.HasPrefix(out, "data: ") {
		t.Errorf("stream should start with %q, got %q", "data: ", out[:min(12, len(out))])
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "data: [DONE]") {
		t.Errorf("stream should terminate with the [DONE] sentinel")
	}

	// Every data frame other than [DONE] must be a valid OpenAI chunk, and the
	// concatenated content must be non-empty.
	var content strings.Builder
	for _, frame := range strings.Split(out, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" || frame == "data: [DONE]" {
			continue
		}
		payload := strings.TrimPrefix(frame, "data: ")
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("frame is not valid JSON: %q: %v", payload, err)
		}
		if len(chunk.Choices) == 0 {
			t.Fatalf("frame has no choices: %q", payload)
		}
		content.WriteString(chunk.Choices[0].Delta.Content)
	}
	if content.Len() == 0 {
		t.Error("expected non-empty streamed content")
	}
}

func TestHandleStream_RejectsGET(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/stream", nil)
	w := httptest.NewRecorder()

	handleStream(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", w.Code)
	}
}

func TestMetricsEndpoints_JSON(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		handler http.HandlerFunc
		keys    []string
	}{
		{"usage", "/v1/router/usage", handleUsage, []string{"tenants", "count", "enabled"}},
		{"status", "/v1/router/status", handleStatus, []string{"status", "total_replicas", "healthy_count", "replicas"}},
		{"tiers", "/v1/router/tiers", handleTiers, []string{"tiers", "total_requests", "total_savings"}},
		{"models", "/v1/models", handleModels, []string{"object", "data"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()

			tc.handler(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected application/json, got %q", ct)
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			for _, k := range tc.keys {
				if _, ok := got[k]; !ok {
					t.Errorf("response missing key %q", k)
				}
			}
		})
	}
}

func TestHandleUsage_SingleTenant(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/router/usage?tenant=acme-corp", nil)
	w := httptest.NewRecorder()

	handleUsage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["tenant"] != "acme-corp" {
		t.Errorf("expected tenant acme-corp, got %v", got["tenant"])
	}

	// Unknown tenant → 404.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/router/usage?tenant=nope", nil)
	w2 := httptest.NewRecorder()
	handleUsage(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown tenant, got %d", w2.Code)
	}
}

func TestSplitTokens_RoundTrips(t *testing.T) {
	in := "hello there world"
	got := strings.Join(splitTokens(in), "")
	if got != in {
		t.Errorf("expected %q, got %q", in, got)
	}
}
