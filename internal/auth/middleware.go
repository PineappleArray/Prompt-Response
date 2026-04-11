package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"prompt-response/internal/metrics"
)

// skipPaths are endpoints that bypass authentication (health probes).
var skipPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// Middleware returns HTTP middleware that validates API keys.
// Keys are extracted from the Authorization: Bearer <key> or X-API-Key header.
// Health check endpoints (/healthz, /readyz) bypass authentication.
func Middleware(ks *Keystore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			key := extractKey(r)
			if key == "" {
				metrics.AuthFailuresTotal.WithLabelValues("missing_key").Inc()
				writeAuthError(w, http.StatusUnauthorized, "api_key_required",
					"API key is required. Set Authorization: Bearer <key> or X-API-Key header.")
				return
			}

			tenant, ok := ks.Validate(key)
			if !ok {
				metrics.AuthFailuresTotal.WithLabelValues("invalid_key").Inc()
				slog.Warn("auth: invalid API key", "key_prefix", safePrefix(key))
				writeAuthError(w, http.StatusUnauthorized, "invalid_api_key",
					"the provided API key is not valid")
				return
			}

			ctx := ContextWithTenant(r.Context(), tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractKey pulls the API key from the Authorization or X-API-Key header.
func extractKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return auth[len(prefix):]
		}
	}
	return r.Header.Get("X-API-Key")
}

// safePrefix returns a truncated key prefix safe for logging.
func safePrefix(key string) string {
	if len(key) <= 8 {
		return key[:len(key)/2] + "..."
	}
	return key[:8] + "..."
}

type authErrorResponse struct {
	Error authErrorBody `json:"error"`
}

type authErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeAuthError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(authErrorResponse{
		Error: authErrorBody{
			Message: msg,
			Type:    "authentication_error",
			Code:    code,
		},
	})
}
