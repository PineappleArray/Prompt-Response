package config

import (
	"os"
	"path/filepath"
	"testing"

	"prompt-response/internal/types"
)

// writeTempConfig writes yaml to a temp file and returns its path.
func writeTempConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

// TestHybridConfigResolvesAPIReplica verifies an Anthropic replica needs no
// url (it is resolved from ANTHROPIC_BASE_URL) and gets its key from the env.
func TestHybridConfigResolvesAPIReplica(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.example/")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-123")

	yaml := `
listen_addr: ":8080"
redis:
  addr: localhost:6379
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: replica-small-1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 4
    routing:
      rules:
        - min_reasoning: 0.70
          min_score: 0.55
    models:
      - id: claude-large
        provider: anthropic
        model: claude-sonnet-4-6
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var claude *types.Replica
	for i := range cfg.Replicas {
		if cfg.Replicas[i].ID == "claude-large" {
			claude = &cfg.Replicas[i]
		}
	}
	if claude == nil {
		t.Fatal("claude-large replica not found")
	}
	if claude.Provider != types.ProviderAnthropic {
		t.Errorf("provider = %q, want anthropic", claude.Provider)
	}
	if !claude.IsAPI() {
		t.Error("IsAPI() = false, want true")
	}
	if claude.URL != "https://gateway.example" {
		t.Errorf("url = %q, want resolved+trimmed base url", claude.URL)
	}
	if claude.APIKey != "sk-test-123" {
		t.Errorf("api key = %q, want sk-test-123", claude.APIKey)
	}
}

// TestAPIOnlyConfig proves the router validates with no vLLM replicas at all.
func TestAPIOnlyConfig(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	yaml := `
redis:
  addr: localhost:6379
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: claude-haiku
        provider: anthropic
        model: claude-haiku-4-5
        api_key_env: ANTHROPIC_API_KEY
`
	cfg, err := Load(writeTempConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load (api-only): %v", err)
	}
	if len(cfg.Replicas) != 1 || !cfg.Replicas[0].IsAPI() {
		t.Fatalf("expected a single API replica, got %+v", cfg.Replicas)
	}
}

// TestUnknownProviderRejected ensures a typo'd provider fails validation.
func TestUnknownProviderRejected(t *testing.T) {
	yaml := `
redis:
  addr: localhost:6379
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: bad
        provider: gemini
        model: gemini-pro
`
	if _, err := Load(writeTempConfig(t, yaml)); err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}
