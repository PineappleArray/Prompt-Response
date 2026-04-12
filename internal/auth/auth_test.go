package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKeystore_Validate(t *testing.T) {
	tests := []struct {
		name    string
		entries []KeyEntry
		key     string
		wantOK  bool
		wantID  string
	}{
		{
			name:    "valid key",
			entries: []KeyEntry{{Key: "sk-test-123", Tenant: "acme"}},
			key:     "sk-test-123",
			wantOK:  true,
			wantID:  "acme",
		},
		{
			name:    "invalid key",
			entries: []KeyEntry{{Key: "sk-test-123", Tenant: "acme"}},
			key:     "sk-wrong",
			wantOK:  false,
		},
		{
			name:    "empty key",
			entries: []KeyEntry{{Key: "sk-test-123", Tenant: "acme"}},
			key:     "",
			wantOK:  false,
		},
		{
			name: "multiple keys second match",
			entries: []KeyEntry{
				{Key: "sk-key-1", Tenant: "tenant-a"},
				{Key: "sk-key-2", Tenant: "tenant-b"},
			},
			key:    "sk-key-2",
			wantOK: true,
			wantID: "tenant-b",
		},
		{
			name:    "empty keystore",
			entries: nil,
			key:     "sk-anything",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ks := NewKeystore(tt.entries)
			tenant, ok := ks.Validate(tt.key)
			if ok != tt.wantOK {
				t.Errorf("Validate() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && tenant.ID != tt.wantID {
				t.Errorf("Validate() tenant.ID = %q, want %q", tenant.ID, tt.wantID)
			}
		})
	}
}

func TestKeystore_Len(t *testing.T) {
	ks := NewKeystore([]KeyEntry{
		{Key: "k1", Tenant: "t1"},
		{Key: "k2", Tenant: "t2"},
	})
	if got := ks.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

func TestTenantContext_RoundTrip(t *testing.T) {
	tenant := Tenant{ID: "acme"}
	ctx := ContextWithTenant(context.Background(), tenant)

	got, ok := TenantFromContext(ctx)
	if !ok {
		t.Fatal("TenantFromContext() returned false")
	}
	if got.ID != "acme" {
		t.Errorf("TenantFromContext() ID = %q, want %q", got.ID, "acme")
	}
}

func TestTenantContext_Missing(t *testing.T) {
	_, ok := TenantFromContext(context.Background())
	if ok {
		t.Error("TenantFromContext() should return false for empty context")
	}
}

func TestMiddleware_BearerToken(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})

	var gotTenant Tenant
	var gotOK bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenant, gotOK = TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(ks)(inner)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-valid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !gotOK || gotTenant.ID != "acme" {
		t.Errorf("expected tenant acme, got %v (ok=%v)", gotTenant, gotOK)
	}
}

func TestMiddleware_XAPIKey(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(ks)(inner)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-API-Key", "sk-valid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("inner handler was not called")
	}
}

func TestMiddleware_MissingKey(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})
	handler := Middleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var body authErrorResponse
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "api_key_required" {
		t.Errorf("expected code api_key_required, got %s", body.Error.Code)
	}
}

func TestMiddleware_InvalidKey(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})
	handler := Middleware(ks)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-invalid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var body authErrorResponse
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "invalid_api_key" {
		t.Errorf("expected code invalid_api_key, got %s", body.Error.Code)
	}
}

func TestMiddleware_SkipsHealthz(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(ks)(inner)

	// No auth header — should still pass through for health endpoints
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("inner handler was not called for /healthz")
	}
}

func TestMiddleware_SkipsReadyz(t *testing.T) {
	ks := NewKeystore([]KeyEntry{{Key: "sk-valid", Tenant: "acme"}})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(ks)(inner)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("inner handler was not called for /readyz")
	}
}

func TestExtractKey(t *testing.T) {
	tests := []struct {
		name         string
		authHeader   string
		apiKeyHeader string
		want         string
	}{
		{
			name:       "bearer token",
			authHeader: "Bearer sk-123",
			want:       "sk-123",
		},
		{
			name:         "x-api-key header",
			apiKeyHeader: "sk-456",
			want:         "sk-456",
		},
		{
			name:         "bearer preferred over x-api-key",
			authHeader:   "Bearer sk-bearer",
			apiKeyHeader: "sk-header",
			want:         "sk-bearer",
		},
		{
			name: "no auth headers",
			want: "",
		},
		{
			name:       "basic auth ignored",
			authHeader: "Basic dXNlcjpwYXNz",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.apiKeyHeader != "" {
				req.Header.Set("X-API-Key", tt.apiKeyHeader)
			}
			got := extractKey(req)
			if got != tt.want {
				t.Errorf("extractKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSafePrefix(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "long key", key: "sk-1234567890abcdef", want: "sk-12345..."},
		{name: "short key", key: "abc", want: "a..."},
		{name: "exactly 8", key: "12345678", want: "1234..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safePrefix(tt.key)
			if got != tt.want {
				t.Errorf("safePrefix(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
