package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Message is a single OpenAI chat-completion message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// StreamMsg is one event from the SSE stream: either a content delta or
// a terminal error. The producing goroutine closes the channel after
// the stream ends.
type StreamMsg struct {
	Delta string
	Err   error
}

// Send POSTs the messages to the router's chat-completions endpoint with
// streaming enabled and returns a channel of StreamMsg events. The
// caller drains the channel; it is closed when the upstream emits
// `data: [DONE]` or the connection ends.
func Send(ctx context.Context, url, model string, msgs []Message) (<-chan StreamMsg, error) {
	body, err := json.Marshal(chatRequest{Model: model, Messages: msgs, Stream: true})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ch := make(chan StreamMsg, 64)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		parseSSEStream(resp.Body,
			func(s string) { ch <- StreamMsg{Delta: s} },
			func(e error) { ch <- StreamMsg{Err: e} },
		)
	}()
	return ch, nil
}

// parseSSEStream reads SSE events from r and invokes deltas() for each
// content delta, errs() once if scanning fails. It returns when the
// stream emits `data: [DONE]` or hits EOF.
func parseSSEStream(r io.Reader, deltas func(string), errs func(error)) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			return
		}
		var c sseChunk
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			continue
		}
		for _, choice := range c.Choices {
			if choice.Delta.Content != "" {
				deltas(choice.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		errs(err)
	}
}
