package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"prompt-response/internal/config"
)

// anthropicVersion is the API version header value the Messages API requires.
const anthropicVersion = "2023-06-01"

// defaultMaxTokens is used when the incoming OpenAI request omits max_tokens,
// which Anthropic requires.
const defaultAnthropicMaxTokens = 1024

// ---------------------------------------------------------------------------
// Request translation: OpenAI chat-completions -> Anthropic Messages API
// ---------------------------------------------------------------------------

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaChatRequest struct {
	Model       string      `json:"model"`
	Messages    []oaMessage `json:"messages"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature *float64    `json:"temperature,omitempty"`
	Stream      bool        `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	Stream      bool               `json:"stream"`
}

// translateToAnthropic converts an OpenAI chat-completions body into an
// Anthropic Messages request. System messages are hoisted into the top-level
// system field (Anthropic disallows a system role inside messages); user and
// assistant messages are passed through in order. The model is forced to the
// replica's configured model so routing controls which Claude model is used.
func translateToAnthropic(body []byte, model string) (anthropicRequest, error) {
	var in oaChatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return anthropicRequest{}, fmt.Errorf("decode openai request: %w", err)
	}

	var systemParts []string
	msgs := make([]anthropicMessage, 0, len(in.Messages))
	for _, m := range in.Messages {
		if strings.EqualFold(m.Role, "system") {
			systemParts = append(systemParts, m.Content)
			continue
		}
		msgs = append(msgs, anthropicMessage{Role: strings.ToLower(m.Role), Content: m.Content})
	}

	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultAnthropicMaxTokens
	}

	return anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		System:      strings.Join(systemParts, "\n\n"),
		Messages:    msgs,
		Temperature: in.Temperature,
		Stream:      true,
	}, nil
}

// ---------------------------------------------------------------------------
// Upstream call
// ---------------------------------------------------------------------------

// doUpstreamAnthropic sends the request to the Anthropic Messages API and
// returns a response whose body is an OpenAI-compatible SSE stream, so the rest
// of the proxy (stream interceptor, token counting, client) is unchanged.
//
// On a non-2xx upstream status the original response is returned unmodified so
// the handler's existing retry/circuit logic applies (5xx retried, 4xx surfaced
// to the client).
func (h *Handler) doUpstreamAnthropic(ctx context.Context, replica config.Replica, body []byte) (*http.Response, error) {
	areq, err := translateToAnthropic(body, replica.Model)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(areq)
	if err != nil {
		return nil, fmt.Errorf("encode anthropic request: %w", err)
	}

	url := replica.URL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("anthropic-version", anthropicVersion)
	if replica.APIKey != "" {
		req.Header.Set("x-api-key", replica.APIKey)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Let the handler treat this like any other upstream non-2xx.
		return resp, nil
	}

	// Translate the Anthropic SSE stream into OpenAI SSE on the fly.
	pr, pw := io.Pipe()
	go func() {
		translateAnthropicStream(resp.Body, pw)
		resp.Body.Close()
	}()

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header: http.Header{
			"Content-Type":  []string{"text/event-stream"},
			"Cache-Control": []string{"no-cache"},
		},
		Body: pr,
	}, nil
}

// ---------------------------------------------------------------------------
// Response translation: Anthropic SSE -> OpenAI chat.completion.chunk SSE
// ---------------------------------------------------------------------------

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	ContentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content_block"`
}

// translateAnthropicStream reads Anthropic SSE events from src and writes an
// OpenAI-compatible SSE stream to dst, terminating with `data: [DONE]`. It
// always closes dst before returning. Write errors (client disconnect) stop
// translation promptly.
func translateAnthropicStream(src io.Reader, dst *io.PipeWriter) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	roleSent := false
	finish := "stop"

	emit := func(delta map[string]any, finishReason any) bool {
		chunk := map[string]any{
			"object": "chat.completion.chunk",
			"choices": []map[string]any{
				{"index": 0, "delta": delta, "finish_reason": finishReason},
			},
		}
		b, _ := json.Marshal(chunk)
		if _, err := dst.Write([]byte("data: ")); err != nil {
			return false
		}
		if _, err := dst.Write(b); err != nil {
			return false
		}
		_, err := dst.Write([]byte("\n\n"))
		return err == nil
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue // skip blank lines and `event:` lines
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var ev anthropicStreamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			if !roleSent {
				roleSent = true
				if !emit(map[string]any{"role": "assistant"}, nil) {
					dst.Close()
					return
				}
			}
		case "content_block_start":
			if ev.ContentBlock.Text != "" {
				if !emit(map[string]any{"content": ev.ContentBlock.Text}, nil) {
					dst.Close()
					return
				}
			}
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				if !emit(map[string]any{"content": ev.Delta.Text}, nil) {
					dst.Close()
					return
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				finish = mapStopReason(ev.Delta.StopReason)
			}
		case "message_stop":
			emit(map[string]any{}, finish)
			dst.Write([]byte("data: [DONE]\n\n"))
			dst.Close()
			return
		}
	}

	// Stream ended without an explicit message_stop (EOF or read error): still
	// terminate the OpenAI stream cleanly so the client and interceptor finish.
	emit(map[string]any{}, finish)
	dst.Write([]byte("data: [DONE]\n\n"))
	dst.Close()
}

// mapStopReason converts an Anthropic stop_reason to the OpenAI finish_reason.
func mapStopReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
