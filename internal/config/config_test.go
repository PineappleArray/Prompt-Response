package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Minimal valid config that all tests can extend.
// Two tiers: small (with fallback) and large, so validation passes.
const minimalValid = `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 2
    routing:
      rules:
        - min_score: 0.60
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`

// ---------------------------------------------------------------------------
// Loading & defaults
// ---------------------------------------------------------------------------

func TestLoad_ValidConfig(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Models) != 2 {
		t.Errorf("expected 2 model tiers, got %d", len(cfg.Models))
	}
	if cfg.Models[0].Name != "small" {
		t.Errorf("expected first tier 'small', got %q", cfg.Models[0].Name)
	}
	if cfg.Models[1].Name != "large" {
		t.Errorf("expected second tier 'large', got %q", cfg.Models[1].Name)
	}
}

func TestLoad_Defaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
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
	if cfg.AffinityTTL.Minutes() != 5 {
		t.Errorf("expected default affinity_ttl 5m, got %s", cfg.AffinityTTL)
	}
	if cfg.PollInterval.Seconds() != 2 {
		t.Errorf("expected default poll_interval 2s, got %s", cfg.PollInterval)
	}
}

func TestLoad_ClassifierDefaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := cfg.Classifier
	if c.Creativity != 0.35 {
		t.Errorf("expected classifier creativity 0.35, got %f", c.Creativity)
	}
	if c.Reasoning != 0.25 {
		t.Errorf("expected classifier reasoning 0.25, got %f", c.Reasoning)
	}
	if c.Constraint != 0.15 {
		t.Errorf("expected classifier constraint 0.15, got %f", c.Constraint)
	}
	if c.Domain != 0.15 {
		t.Errorf("expected classifier domain 0.15, got %f", c.Domain)
	}
	if c.Length != 0.10 {
		t.Errorf("expected classifier length 0.10, got %f", c.Length)
	}
}

func TestLoad_CircuitDefaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
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
}

func TestLoad_RetryDefaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Retry.MaxRetries != 1 {
		t.Errorf("expected default max_retries 1, got %d", cfg.Retry.MaxRetries)
	}
	if cfg.Retry.Timeout.Seconds() != 30 {
		t.Errorf("expected default retry timeout 30s, got %s", cfg.Retry.Timeout)
	}
}

func TestLoad_RateLimitAndAuditDefaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimit.RequestsPerMinute != 60 {
		t.Errorf("expected default requests_per_minute 60, got %f", cfg.RateLimit.RequestsPerMinute)
	}
	if cfg.RateLimit.Burst != 10 {
		t.Errorf("expected default burst 10, got %d", cfg.RateLimit.Burst)
	}
	if cfg.Audit.BufferSize != 1000 {
		t.Errorf("expected default buffer_size 1000, got %d", cfg.Audit.BufferSize)
	}
}

func TestLoad_StreamDefaults(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stream.StallTimeout.Seconds() != 15 {
		t.Errorf("expected default stall_timeout 15s, got %s", cfg.Stream.StallTimeout)
	}
}

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

func TestLoad_AuthEnabledNoKeys(t *testing.T) {
	content := minimalValid + `
auth:
  enabled: true
  keys: []
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for auth enabled with no keys")
	}
}

func TestLoad_AuthWithKeys(t *testing.T) {
	content := minimalValid + `
auth:
  enabled: true
  keys:
    - key: "sk-test"
      tenant: "acme"
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Auth.Enabled {
		t.Error("expected auth enabled")
	}
	if len(cfg.Auth.Keys) != 1 {
		t.Errorf("expected 1 auth key, got %d", len(cfg.Auth.Keys))
	}
	if cfg.Auth.Keys[0].Tenant != "acme" {
		t.Errorf("expected tenant acme, got %s", cfg.Auth.Keys[0].Tenant)
	}
}

// ---------------------------------------------------------------------------
// Tier validation
// ---------------------------------------------------------------------------

func TestLoad_NoModels(t *testing.T) {
	content := `
Models: []
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for empty models")
	}
}

func TestLoad_DuplicatePriority(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 1
    routing:
      rules:
        - min_score: 0.50
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for duplicate priority")
	}
	if !containsString(err.Error(), "share priority") {
		t.Errorf("expected 'share priority' in error, got %q", err.Error())
	}
}

func TestLoad_DuplicateTierName(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: small
    priority: 2
    routing:
      rules:
        - min_score: 0.50
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for duplicate tier name")
	}
	if !containsString(err.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got %q", err.Error())
	}
}

func TestLoad_NoFallbackTier(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      rules:
        - max_score: 0.30
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 2
    routing:
      rules:
        - min_score: 0.50
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for no fallback tier")
	}
	if !containsString(err.Error(), "fallback") {
		t.Errorf("expected 'fallback' in error, got %q", err.Error())
	}
}

func TestLoad_MultipleFallbacks(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 2
    routing:
      fallback: true
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for multiple fallback tiers")
	}
	if !containsString(err.Error(), "only one tier can be fallback") {
		t.Errorf("expected 'only one tier can be fallback' in error, got %q", err.Error())
	}
}

func TestLoad_TierMissingName(t *testing.T) {
	content := `
Models:
  - priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for tier missing name")
	}
}

func TestLoad_TierNoModels(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models: []
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for tier with no models")
	}
}

func TestLoad_NegativePriority(t *testing.T) {
	content := `
Models:
  - name: small
    priority: -1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for negative priority")
	}
}

// ---------------------------------------------------------------------------
// Replica validation
// ---------------------------------------------------------------------------

func TestLoad_ReplicaMissingID(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing replica ID")
	}
}

func TestLoad_ReplicaMissingURL(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing replica URL")
	}
}

func TestLoad_ReplicaMissingModel(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing replica model")
	}
}

// ---------------------------------------------------------------------------
// Routing rules validation
// ---------------------------------------------------------------------------

func TestLoad_InvalidRuleScoreRange(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 2
    routing:
      rules:
        - min_score: 0.80
          max_score: 0.20
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for min_score >= max_score")
	}
	if !containsString(err.Error(), "min_score must be less than max_score") {
		t.Errorf("expected score range error, got %q", err.Error())
	}
}

func TestLoad_InvalidRuleReasoningRange(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
  - name: large
    priority: 2
    routing:
      rules:
        - min_reasoning: 0.90
          max_reasoning: 0.10
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for min_reasoning >= max_reasoning")
	}
}

// ---------------------------------------------------------------------------
// Redis validation
// ---------------------------------------------------------------------------

func TestLoad_MissingRedis(t *testing.T) {
	content := `
Models:
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing redis addr")
	}
}

// ---------------------------------------------------------------------------
// File errors
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Numeric bounds
// ---------------------------------------------------------------------------

func TestLoad_NumericBounds(t *testing.T) {
	tests := []struct {
		name    string
		extra   string
		wantErr string
	}{
		{
			name:    "threshold above 1",
			extra:   "threshold: 1.5",
			wantErr: "threshold must be in [0, 1]",
		},
		{
			name:    "threshold negative",
			extra:   "threshold: -0.2",
			wantErr: "threshold must be in [0, 1]",
		},
		{
			name:    "max_queue negative",
			extra:   "max_queue: -1",
			wantErr: "max_queue must be positive",
		},
		{
			name:    "negative scoring weight",
			extra:   "weights:\n  cache_affinity: -0.1\n  queue_depth: 0.3\n  kv_cache_pressure: 0.3\n  baseline: 0.3",
			wantErr: "scoring weights must be non-negative",
		},
		{
			name:    "circuit error_threshold above 1",
			extra:   "circuit:\n  error_threshold: 1.2",
			wantErr: "circuit error_threshold must be in [0, 1]",
		},
		{
			name:    "circuit error_threshold negative",
			extra:   "circuit:\n  error_threshold: -0.1",
			wantErr: "circuit error_threshold must be in [0, 1]",
		},
		{
			name:    "circuit window_size negative",
			extra:   "circuit:\n  window_size: -1s",
			wantErr: "circuit window_size must be positive",
		},
		{
			name:    "circuit min_samples negative",
			extra:   "circuit:\n  min_samples: -3",
			wantErr: "circuit min_samples must be positive",
		},
		{
			name:    "stream stall_timeout negative",
			extra:   "stream:\n  stall_timeout: -1s",
			wantErr: "stream stall_timeout must be non-negative",
		},
		{
			name:    "stream done_timeout negative",
			extra:   "stream:\n  done_timeout: -5s",
			wantErr: "stream done_timeout must be non-negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, minimalValid+"\n"+tc.extra)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !containsString(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ToReplicaList — model size parsing & tier detection
// ---------------------------------------------------------------------------

func TestToReplicaList_ParsesModelSize(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()

	for _, rep := range rl.Replicas {
		switch rep.ID {
		case "r1":
			if rep.ParamSize != 1_500_000_000 {
				t.Errorf("r1: expected 1.5B params, got %d", rep.ParamSize)
			}
		case "r2":
			if rep.ParamSize != 72_000_000_000 {
				t.Errorf("r2: expected 72B params, got %d", rep.ParamSize)
			}
		}
	}
}

func TestToReplicaList_DetectsCodeModel(t *testing.T) {
	content := `
Models:
  - name: code
    priority: 1
    routing:
      fallback: true
      code_signals: true
      task_types: ["Code Generation"]
    models:
      - id: c1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-Coder-7B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()
	codeTier := rl.CodeTier()
	if codeTier == nil {
		t.Fatal("expected code tier to be detected")
	}
	if codeTier.Name != "code" {
		t.Errorf("expected code tier name 'code', got %q", codeTier.Name)
	}
}

func TestToReplicaList_DetectsReasoningModel(t *testing.T) {
	content := `
Models:
  - name: reasoning
    priority: 1
    routing:
      fallback: true
      rules:
        - min_reasoning: 0.85
    models:
      - id: reason1
        url: http://localhost:8001
        model: Qwen/QwQ-32B-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()
	reasonTier := rl.ReasoningTier()
	if reasonTier == nil {
		t.Fatal("expected reasoning tier to be detected")
	}
	if reasonTier.Name != "reasoning" {
		t.Errorf("expected reasoning tier name 'reasoning', got %q", reasonTier.Name)
	}
}

func TestToReplicaList_SmallestAndLargest(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()

	smallest := rl.SmallestTier()
	if smallest != "small" {
		t.Errorf("expected smallest tier 'small', got %q", smallest)
	}

	largest := rl.LargestTier()
	if largest != "large" {
		t.Errorf("expected largest tier 'large', got %q", largest)
	}
}

func TestToReplicaList_SortedByPriority(t *testing.T) {
	// Intentionally list large before small in YAML
	content := `
Models:
  - name: large
    priority: 3
    routing:
      rules:
        - min_score: 0.60
    models:
      - id: r2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-72B-Instruct-AWQ
  - name: small
    priority: 1
    routing:
      fallback: true
    models:
      - id: r1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
redis:
  addr: localhost:6379
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()
	// First replica should be from the lowest priority tier
	if rl.Replicas[0].TierCfg.Priority != 1 {
		t.Errorf("expected first replica from priority 1, got %d", rl.Replicas[0].TierCfg.Priority)
	}
}

func TestToReplicaList_RoutingRulesCarried(t *testing.T) {
	path := writeTemp(t, minimalValid)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rl := cfg.ToReplicaList()

	// Small tier should have fallback
	smallTier := rl.TierByName("small")
	if smallTier == nil {
		t.Fatal("expected to find small tier")
	}
	if !smallTier.Routing.Fallback {
		t.Error("expected small tier to be fallback")
	}

	// Large tier should have routing rules
	largeTier := rl.TierByName("large")
	if largeTier == nil {
		t.Fatal("expected to find large tier")
	}
	if len(largeTier.Routing.Rules) != 1 {
		t.Errorf("expected 1 routing rule on large tier, got %d", len(largeTier.Routing.Rules))
	}
	if largeTier.Routing.Rules[0].MinScore == nil || *largeTier.Routing.Rules[0].MinScore != 0.60 {
		t.Error("expected large tier rule min_score 0.60")
	}
}

// ---------------------------------------------------------------------------
// Model size parsing edge cases
// ---------------------------------------------------------------------------

func TestParseModelSize(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected int64
	}{
		{"standard", "Qwen/Qwen2.5-7B-Instruct-AWQ", 7_000_000_000},
		{"decimal", "Qwen/Qwen2.5-1.5B-Instruct-AWQ", 1_500_000_000},
		{"large", "Qwen/Qwen2.5-72B-Instruct-AWQ", 72_000_000_000},
		{"lowercase", "meta-llama/llama-3.2-3b", 3_000_000_000},
		{"qwq", "Qwen/QwQ-32B-AWQ", 32_000_000_000},
		{"no_size", "unknown-model", 0},
		{"empty", "", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseModelSize(tc.model)
			if got != tc.expected {
				t.Errorf("parseModelSize(%q) = %d, want %d", tc.model, got, tc.expected)
			}
		})
	}
}

func TestIsCodeModel(t *testing.T) {
	if !isCodeModel("Qwen/Qwen2.5-Coder-7B-Instruct") {
		t.Error("expected Coder model to be detected as code")
	}
	if isCodeModel("Qwen/Qwen2.5-7B-Instruct") {
		t.Error("expected non-code model to not be detected as code")
	}
}

func TestIsReasoningModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"Qwen/QwQ-32B-AWQ", true},
		{"deepseek-r1-7b", true},
		{"o1-preview", true},
		{"Qwen/Qwen2.5-72B-Instruct", false},
	}

	for _, tc := range tests {
		got := isReasoningModel(tc.model)
		if got != tc.expected {
			t.Errorf("isReasoningModel(%q) = %v, want %v", tc.model, got, tc.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// Full config with all tiers and routing rules
// ---------------------------------------------------------------------------

func TestLoad_FullConfig(t *testing.T) {
	content := `
listen_addr: ":9090"

Models:
  - name: small
    priority: 1
    routing:
      rules:
        - task_types: ["QA", "Classification"]
          max_score: 0.15
          max_reasoning: 0.15
    models:
      - id: replica-small-1
        url: http://localhost:8001
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ
      - id: replica-small-2
        url: http://localhost:8002
        model: Qwen/Qwen2.5-1.5B-Instruct-AWQ

  - name: code
    priority: 2
    routing:
      task_types: ["Code Generation"]
      code_signals: true
    models:
      - id: replica-code-1
        url: http://localhost:8003
        model: Qwen/Qwen2.5-Coder-7B-Instruct-AWQ

  - name: medium
    priority: 3
    routing:
      fallback: true
    models:
      - id: replica-medium-1
        url: http://localhost:8004
        model: Qwen/Qwen2.5-14B-Instruct-AWQ

  - name: large
    priority: 4
    routing:
      rules:
        - min_reasoning: 0.70
          min_score: 0.55
        - min_score: 0.65
          min_domain: 0.80
          min_constraint: 0.60
    models:
      - id: replica-large-1
        url: http://localhost:8005
        model: Qwen/Qwen2.5-72B-Instruct-AWQ

  - name: reasoning
    priority: 5
    routing:
      rules:
        - min_reasoning: 0.85
          min_score: 0.75
    models:
      - id: replica-reasoning-1
        url: http://localhost:8006
        model: Qwen/QwQ-32B-AWQ

redis:
  addr: localhost:6379

weights:
  cache_affinity: 0.50
  queue_depth: 0.25
  kv_cache_pressure: 0.15
  baseline: 0.10

classifier:
  length: 0.20
  code: 0.30
  reasoning: 0.15
  complexity: 0.10
  conv_depth: 0.10
  output_length: 0.15

threshold: 0.35
max_queue: 20
affinity_ttl: 5m
poll_interval: 2s

auth:
  enabled: true
  keys:
    - key: "sk-prod-key"
      tenant: "prod"

ratelimit:
  enabled: true
  requests_per_minute: 120
  burst: 20

audit:
  enabled: true
  buffer_size: 5000

usage:
  enabled: true
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ListenAddr != ":9090" {
		t.Errorf("expected listen addr :9090, got %s", cfg.ListenAddr)
	}
	if len(cfg.Models) != 5 {
		t.Errorf("expected 5 model tiers, got %d", len(cfg.Models))
	}

	rl := cfg.ToReplicaList()
	if len(rl.Replicas) != 6 {
		t.Errorf("expected 6 replicas, got %d", len(rl.Replicas))
	}

	// Verify routing rules were carried through
	largeTier := rl.TierByName("large")
	if largeTier == nil {
		t.Fatal("expected large tier")
	}
	if len(largeTier.Routing.Rules) != 2 {
		t.Errorf("expected 2 routing rules on large, got %d", len(largeTier.Routing.Rules))
	}

	codeTier := rl.CodeTier()
	if codeTier == nil {
		t.Fatal("expected code tier")
	}
	if !codeTier.Routing.CodeSignals {
		t.Error("expected code tier to have code_signals enabled")
	}

	reasonTier := rl.ReasoningTier()
	if reasonTier == nil {
		t.Fatal("expected reasoning tier")
	}

	if cfg.RateLimit.RequestsPerMinute != 120 {
		t.Errorf("expected rpm 120, got %f", cfg.RateLimit.RequestsPerMinute)
	}
	if cfg.Audit.BufferSize != 5000 {
		t.Errorf("expected audit buffer 5000, got %d", cfg.Audit.BufferSize)
	}
}
