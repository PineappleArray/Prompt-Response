package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"prompt-response/internal/config"
)

// doUpstreamOpenAI forwards the request to an OpenAI-compatible API endpoint.
// No request/response translation is needed because OpenAI's chat-completions
// format is the same as what vLLM (and the router) speak natively.
//
// Two adjustments are made relative to a plain vLLM forward:
//   - The model field in the request body is overridden with replica.Model so
//     routing controls which OpenAI model is used, not whatever the client sent.
//   - An Authorization: Bearer header is added using the resolved API key.
func (h *Handler) doUpstreamOpenAI(ctx context.Context, replica config.Replica, body []byte, orig *http.Request) (*http.Response, error) {
	// Override the model field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode request body: %w", err)
	}
	modelJSON, err := json.Marshal(replica.Model)
	if err != nil {
		return nil, err
	}
	raw["model"] = modelJSON
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}

	url := replica.URL + orig.URL.Path
	req, err := http.NewRequestWithContext(ctx, orig.Method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	copyHeaders(req.Header, orig.Header)
	req.Host = ""
	if replica.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+replica.APIKey)
	}

	return h.client.Do(req)
}
