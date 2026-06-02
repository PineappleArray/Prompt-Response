package proxy

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestTranslateToAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "ignored-by-router",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi"},
			{"role": "user", "content": "again"}
		],
		"max_tokens": 256
	}`)

	got, err := translateToAnthropic(body, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("translateToAnthropic: %v", err)
	}
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6 (router controls model)", got.Model)
	}
	if got.System != "be terse" {
		t.Errorf("system = %q, want %q", got.System, "be terse")
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (system hoisted out)", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != "hello" {
		t.Errorf("first message = %+v, want user/hello", got.Messages[0])
	}
	if got.MaxTokens != 256 {
		t.Errorf("max_tokens = %d, want 256", got.MaxTokens)
	}
	if !got.Stream {
		t.Error("stream should be forced true")
	}
}

func TestTranslateToAnthropicDefaultMaxTokens(t *testing.T) {
	got, err := translateToAnthropic([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), "m")
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTokens != defaultAnthropicMaxTokens {
		t.Errorf("max_tokens = %d, want default %d", got.MaxTokens, defaultAnthropicMaxTokens)
	}
}

// TestTranslateAnthropicStream feeds a representative Anthropic SSE stream and
// asserts the translated OpenAI stream carries the role, the concatenated
// content, the finish reason, and a terminal [DONE].
func TestTranslateAnthropicStream(t *testing.T) {
	anthropicSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", world"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	pr, pw := io.Pipe()
	go translateAnthropicStream(strings.NewReader(anthropicSSE), pw)
	out, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("read translated stream: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "data: [DONE]") {
		t.Errorf("missing [DONE] terminator:\n%s", got)
	}

	// Reassemble content + role from the OpenAI chunks.
	var content strings.Builder
	roleSeen := false
	finishSeen := false
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk not valid JSON: %q (%v)", payload, err)
		}
		if len(chunk.Choices) == 0 {
			t.Fatalf("chunk missing choices: %q", payload)
		}
		d := chunk.Choices[0]
		if d.Delta.Role == "assistant" {
			roleSeen = true
		}
		content.WriteString(d.Delta.Content)
		if d.FinishReason != nil && *d.FinishReason == "stop" {
			finishSeen = true
		}
	}

	if !roleSeen {
		t.Error("expected an assistant role chunk")
	}
	if content.String() != "Hello, world" {
		t.Errorf("content = %q, want %q", content.String(), "Hello, world")
	}
	if !finishSeen {
		t.Error("expected a finish_reason=stop chunk")
	}
}

func TestMapStopReason(t *testing.T) {
	cases := map[string]string{
		"max_tokens":    "length",
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"weird":         "stop",
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("mapStopReason(%q) = %q, want %q", in, got, want)
		}
	}
}
