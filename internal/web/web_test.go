package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantStatus  int
		wantContent string
		wantBody    string
	}{
		{"index", "/dashboard/", http.StatusOK, "text/html", "<title>Prompt-Response"},
		{"index html canonicalized", "/dashboard/index.html", http.StatusMovedPermanently, "", ""},
		{"script", "/dashboard/app.js", http.StatusOK, "javascript", "router dashboard"},
		{"stylesheet", "/dashboard/style.css", http.StatusOK, "css", "--bg"},
		{"missing asset", "/dashboard/nope.js", http.StatusNotFound, "", ""},
	}

	h := Handler()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantContent != "" && !strings.Contains(rec.Header().Get("Content-Type"), tc.wantContent) {
				t.Errorf("Content-Type = %q, want substring %q", rec.Header().Get("Content-Type"), tc.wantContent)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body missing substring %q", tc.wantBody)
			}
		})
	}
}

func TestHandlerNoCacheOnShell(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		wantNoCache bool
	}{
		{"index no-cache", "/dashboard/", true},
		{"script no-cache", "/dashboard/app.js", true},
		{"stylesheet cacheable", "/dashboard/style.css", false},
	}

	h := Handler()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			gotNoCache := rec.Header().Get("Cache-Control") == "no-cache"
			if gotNoCache != tc.wantNoCache {
				t.Errorf("Cache-Control no-cache = %v, want %v", gotNoCache, tc.wantNoCache)
			}
		})
	}
}
