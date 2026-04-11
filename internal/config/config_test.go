package config

import (
	"os"
	"path/filepath"
	"testing"

	"prompt-response/internal/types"
)

func TestLoad_ValidConfig(t *testing.T) {
	content := `
replicas:
  - id: r1
    url: http://localhost:8001
    model: test
    tier: small
redis:
  addr: localhost:6379
weights:
  cache_affinity: 0.5
  queue_depth: 0.3
  kv_cache_pressure: 0.1
  baseline: 0.1
threshold: 0.35
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Replicas) != 1 {
		t.Errorf("expected 1 replica, got %d", len(cfg.Replicas))
	}
	if cfg.Replicas[0].Tier != types.TierSmall {
		t.Errorf("expected tier small, got %s", cfg.Replicas[0].Tier)
	}
}

func TestLoad_Defaults(t *testing.T) {
	content := `
replicas:
  - id: r1
    url: http://localhost:8001
    model: test
    tier: small
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected default listen addr :8080, got %s", cfg.ListenAddr)
	}
	if cfg.MaxQueue != 20.0 {
		t.Errorf("expected default max_queue 20, got %f", cfg.MaxQueue)
	}
	if cfg.Threshold != 0.35 {
		t.Errorf("expected default threshold 0.35, got %f", cfg.Threshold)
	}
}

func TestLoad_CircuitDefaults(t *testing.T) {
	content := `
replicas:
  - id: r1
    url: http://localhost:8001
    model: test
    tier: small
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Circuit.ErrorThreshold != 0.5 {
		t.Errorf("expected default error_threshold 0.5, got %f", cfg.Circuit.ErrorThreshold)
	}
	if cfg.Circuit.WindowSize.Seconds() != 10 {
		t.Errorf("expected default window_size 10s, got %s", cfg.Circuit.WindowSize)
	}
	if cfg.Circuit.Cooldown.Seconds() != 30 {
		t.Errorf("expected default cooldown 30s, got %s", cfg.Circuit.Cooldown)
	}
	if cfg.Circuit.MinSamples != 5 {
		t.Errorf("expected default min_samples 5, got %d", cfg.Circuit.MinSamples)
	}
	if cfg.Retry.MaxRetries != 1 {
		t.Errorf("expected default max_retries 1, got %d", cfg.Retry.MaxRetries)
	}
	if cfg.Retry.Timeout.Seconds() != 30 {
		t.Errorf("expected default retry timeout 30s, got %s", cfg.Retry.Timeout)
	}
}

func TestLoad_NoReplicas(t *testing.T) {
	content := `
replicas: []
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for empty replicas")
	}
}

func TestLoad_InvalidTier(t *testing.T) {
	content := `
replicas:
  - id: r1
    url: http://localhost:8001
    model: test
    tier: gigantic
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid tier")
	}
}

func TestLoad_MissingRedis(t *testing.T) {
	content := `
replicas:
  - id: r1
    url: http://localhost:8001
    model: test
    tier: small
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing redis addr")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTemp(t, "not: [valid: yaml: content")
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_MissingReplicaID(t *testing.T) {
	content := `
replicas:
  - url: http://localhost:8001
    model: test
    tier: small
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing replica ID")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
