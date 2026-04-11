package ratelimit

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"

	"prompt-response/internal/auth"
	"prompt-response/internal/metrics"
)

// skipPaths are endpoints that bypass rate limiting (health probes).
var skipPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// Middleware returns HTTP middleware that enforces per-tenant rate limits.
// When auth is enabled, uses tenant ID as the rate limit key.
// When auth is disabled, falls back to client IP address.
func Middleware(reg *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			key := clientKey(r)
			allowed, remaining := reg.Allow(key)

			limit := fmt.Sprintf("%.0f", reg.Limit())
			w.Header().Set("X-RateLimit-Limit", limit)
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%.0f", remaining))

			if !allowed {
				metrics.RateLimitRejectsTotal.WithLabelValues(key).Inc()
				retryAfter := int(math.Ceil(60.0 / reg.Limit()))
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeRateLimitError(w)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// clientKey determines the rate limit key from the request.
// Prefers tenant ID from auth context, falls back to client IP.
func clientKey(r *http.Request) string {
	if t, ok := auth.TenantFromContext(r.Context()); ok {
		return t.ID
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if host == "" {
		return "unknown"
	}
	return host
}

type rateLimitErrorResponse struct {
	Error rateLimitErrorBody `json:"error"`
}

type rateLimitErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func writeRateLimitError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(rateLimitErrorResponse{
		Error: rateLimitErrorBody{
			Message: "rate limit exceeded, please retry after the Retry-After period",
			Type:    "rate_limit_error",
			Code:    "rate_limit_exceeded",
		},
	})
}
