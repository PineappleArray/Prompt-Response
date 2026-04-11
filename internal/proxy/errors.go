package proxy

import (
	"encoding/json"
	"net/http"

	"prompt-response/internal/middleware"
)

// apiError follows the OpenAI error response format for client compatibility.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      string `json:"code"`
	RequestID string `json:"request_id,omitempty"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	reqID := middleware.GetRequestID(r.Context())
	errType := "invalid_request_error"
	if status >= 500 {
		errType = "server_error"
	}
	if status == http.StatusServiceUnavailable {
		errType = "service_unavailable"
	}
	if status == http.StatusTooManyRequests {
		errType = "rate_limit_error"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiError{
		Error: apiErrorBody{
			Message:   msg,
			Type:      errType,
			Code:      code,
			RequestID: reqID,
		},
	})
}
