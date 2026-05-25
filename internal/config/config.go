package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"prompt-response/internal/types"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Top-level config
// ---------------------------------------------------------------------------

type Config struct {
	ListenAddr   string            `yaml:"listen_addr"`
	Models       []ModelTier       `yaml:"Models"`
	Redis        Redis             `yaml:"redis"`
	Weights      Weights           `yaml:"weights"`
	Classifier   ClassifierWeights `yaml:"classifier"`
	Circuit      Circuit           `yaml:"circuit"`
	Retry        Retry             `yaml:"retry"`
	Auth         Auth              `yaml:"auth"`
	RateLimit    RateLimit         `yaml:"ratelimit"`
	Audit        Audit             `yaml:"audit"`
	Usage        Usage             `yaml:"usage"`
	Repetition   Repetition        `yaml:"repetition"`
	Stream       Stream            `yaml:"stream"`
	PrefixLen    int               `yaml:"prefix_len"`
	AffinityTTL  time.Duration     `yaml:"affinity_ttl"`
	Threshold    float64           `yaml:"threshold"`
	MaxQueue     float64           `yaml:"max_queue"`
	PollInterval time.Duration     `yaml:"poll_interval"`
	Keywords     KeywordSets       `yaml:"keywords"`

	// Replicas is the flattened runtime replica list, built from Models by
	// Load via ToReplicaList. It is not read from YAML.
	Replicas []types.Replica `yaml:"-"`
}

// ---------------------------------------------------------------------------
// Sub-configs
// ---------------------------------------------------------------------------

type KeywordSets struct {
	Code       []string `yaml:"code"`
	Reasoning  []string `yaml:"reasoning"`
	Complexity []string `yaml:"complexity"`
}

type Repetition struct {
	FrequencyPenalty *float64 `yaml:"frequency_penalty"`
	PresencePenalty  *float64 `yaml:"presence_penalty"`
}

type Circuit struct {
	ErrorThreshold float64       `yaml:"error_threshold"`
	WindowSize     time.Duration `yaml:"window_size"`
	Cooldown       time.Duration `yaml:"cooldown"`
	MinSamples     int           `yaml:"min_samples"`
}

type Retry struct {
	MaxRetries int           `yaml:"max_retries"`
	Timeout    time.Duration `yaml:"timeout"`
}

type ClassifierWeights struct {
	Length       float64 `yaml:"length"`
	Code         float64 `yaml:"code"`
	Reasoning    float64 `yaml:"reasoning"`
	Complexity   float64 `yaml:"complexity"`
	ConvDepth    float64 `yaml:"conv_depth"`
	OutputLength float64 `yaml:"output_length"`
}

type Redis struct {
	Addr string `yaml:"addr"`
}

type Weights struct {
	CacheAffinity   float64 `yaml:"cache_affinity"`
	QueueDepth      float64 `yaml:"queue_depth"`
	KVCachePressure float64 `yaml:"kv_cache_pressure"`
	Baseline        float64 `yaml:"baseline"`
	// MissPenalty is subtracted from a candidate's score when the prefix
	// cache does not point to that replica. Raising it makes the scorer
	// more reluctant to send a request to a replica that would have to
	// recompute the prefix from scratch.
	MissPenalty float64 `yaml:"miss_penalty"`
}

type Auth struct {
	Enabled bool      `yaml:"enabled"`
	Keys    []AuthKey `yaml:"keys"`
}

type AuthKey struct {
	Key    string `yaml:"key"`
	Tenant string `yaml:"tenant"`
}

type RateLimit struct {
	Enabled           bool    `yaml:"enabled"`
	RequestsPerMinute float64 `yaml:"requests_per_minute"`
	Burst             int     `yaml:"burst"`
}

type Audit struct {
	Enabled    bool `yaml:"enabled"`
	BufferSize int  `yaml:"buffer_size"`
}

type Usage struct {
	Enabled  bool          `yaml:"enabled"`
	Postgres UsagePostgres `yaml:"postgres"`
}

// UsagePostgres configures the optional Postgres-backed usage sink that
// mirrors the in-memory Tracker so totals survive restarts.
type UsagePostgres struct {
	Enabled       bool          `yaml:"enabled"`
	DSN           string        `yaml:"dsn"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	BatchSize     int           `yaml:"batch_size"`
	BufferSize    int           `yaml:"buffer_size"`
}

type Stream struct {
	StallTimeout time.Duration `yaml:"stall_timeout"`
	DoneTimeout  time.Duration `yaml:"done_timeout"`
}

// ---------------------------------------------------------------------------
// Model tier + replica config (YAML representation)
// ---------------------------------------------------------------------------

type ModelTier struct {
	Name     string          `yaml:"name"`
	Priority int             `yaml:"priority"`
	Routing  TierRouting     `yaml:"routing"`
	Models   []ReplicaConfig `yaml:"models"`
}

type ReplicaConfig struct {
	ID    string `yaml:"id"`
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

// Replica is the runtime replica type. It aliases types.Replica so callers
// in this package and its dependents can refer to config.Replica.
type Replica = types.Replica

// ---------------------------------------------------------------------------
// Routing rules — configurable tier selection based on classifier signals
//
// Evaluation order:
//   1. Tiers are evaluated in priority order (lowest priority number first)
//   2. Within a tier, task_types and code_signals are checked first
//   3. Then each rule in rules[] is evaluated (OR logic between rules)
//   4. Within a single rule, all non-nil fields must match (AND logic)
//   5. If no tier matches, the fallback tier is selected
// ---------------------------------------------------------------------------

type TierRouting struct {
	Rules       []RoutingRule `yaml:"rules"`
	TaskTypes   []string      `yaml:"task_types"`
	CodeSignals bool          `yaml:"code_signals"`
	Fallback    bool          `yaml:"fallback"`
}

// RoutingRule defines a set of threshold conditions on classifier output.
// All non-nil fields must be satisfied for the rule to match (AND logic).
// Multiple rules on a tier use OR logic — any single rule matching selects the tier.
type RoutingRule struct {
	MinScore      *float64 `yaml:"min_score"`
	MaxScore      *float64 `yaml:"max_score"`
	MinReasoning  *float64 `yaml:"min_reasoning"`
	MaxReasoning  *float64 `yaml:"max_reasoning"`
	MinDomain     *float64 `yaml:"min_domain"`
	MaxDomain     *float64 `yaml:"max_domain"`
	MinCreativity *float64 `yaml:"min_creativity"`
	MaxCreativity *float64 `yaml:"max_creativity"`
	MinConstraint *float64 `yaml:"min_constraint"`
	MaxConstraint *float64 `yaml:"max_constraint"`
	TaskTypes     []string `yaml:"task_types"`
}

// ---------------------------------------------------------------------------
// Model size parsing
// ---------------------------------------------------------------------------

var modelSizeRe = regexp.MustCompile(`(?i)(\d+\.?\d*)\s*[Bb]`)

// parseModelSize extracts the parameter count from a model name string.
//
//	"Qwen/Qwen2.5-1.5B-Instruct-AWQ"  -> 1_500_000_000
//	"Qwen/Qwen2.5-72B-Instruct-AWQ"   -> 72_000_000_000
//	"meta-llama/Llama-3.2-3B"          -> 3_000_000_000
//	"unknown-model"                     -> 0
func parseModelSize(modelName string) int64 {
	match := modelSizeRe.FindStringSubmatch(modelName)
	if match == nil {
		return 0
	}
	size, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}
	return int64(size * 1e9)
}

func isCodeModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	return strings.Contains(lower, "code") || strings.Contains(lower, "coder")
}

func isReasoningModel(modelName string) bool {
	lower := strings.ToLower(modelName)
	return strings.Contains(lower, "qwq") ||
		strings.Contains(lower, "deepseek-r1") ||
		strings.Contains(lower, "o1") ||
		strings.Contains(lower, "reasoning")
}

// ---------------------------------------------------------------------------
// Config loading
// ---------------------------------------------------------------------------

func Load(path string) (*Config, error) {
	f, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(f, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}
	cfg.Replicas = cfg.ToReplicaList().Replicas
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8080"
	}
	if cfg.MaxQueue == 0 {
		cfg.MaxQueue = 20.0
	}
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.35
	}
	if cfg.AffinityTTL == 0 {
		cfg.AffinityTTL = 5 * time.Minute
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	c := &cfg.Classifier
	if c.Length == 0 && c.Code == 0 && c.Reasoning == 0 {
		c.Length = 0.20
		c.Code = 0.30
		c.Reasoning = 0.15
		c.Complexity = 0.10
		c.ConvDepth = 0.10
		c.OutputLength = 0.15
	}
	if cfg.Circuit.ErrorThreshold == 0 {
		cfg.Circuit.ErrorThreshold = 0.5
	}
	if cfg.Circuit.WindowSize == 0 {
		cfg.Circuit.WindowSize = 10 * time.Second
	}
	if cfg.Circuit.Cooldown == 0 {
		cfg.Circuit.Cooldown = 30 * time.Second
	}
	if cfg.Circuit.MinSamples == 0 {
		cfg.Circuit.MinSamples = 5
	}
	if cfg.Retry.MaxRetries == 0 {
		cfg.Retry.MaxRetries = 1
	}
	if cfg.Retry.Timeout == 0 {
		cfg.Retry.Timeout = 30 * time.Second
	}
	if cfg.RateLimit.RequestsPerMinute == 0 {
		cfg.RateLimit.RequestsPerMinute = 60
	}
	if cfg.RateLimit.Burst == 0 {
		cfg.RateLimit.Burst = 10
	}
	if cfg.Audit.BufferSize == 0 {
		cfg.Audit.BufferSize = 1000
	}
	if cfg.Stream.StallTimeout == 0 {
		cfg.Stream.StallTimeout = 15 * time.Second
	}
	if cfg.Repetition.FrequencyPenalty == nil {
		defaultFreq := 0.2
		cfg.Repetition.FrequencyPenalty = &defaultFreq
	}
	if cfg.Repetition.PresencePenalty == nil {
		defaultPres := 0.1
		cfg.Repetition.PresencePenalty = &defaultPres
	}
	if cfg.Usage.Postgres.Enabled {
		if cfg.Usage.Postgres.FlushInterval == 0 {
			cfg.Usage.Postgres.FlushInterval = 5 * time.Second
		}
		if cfg.Usage.Postgres.BatchSize == 0 {
			cfg.Usage.Postgres.BatchSize = 100
		}
		if cfg.Usage.Postgres.BufferSize == 0 {
			cfg.Usage.Postgres.BufferSize = 4096
		}
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func validate(cfg *Config) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("at least one model tier required")
	}

	priorities := make(map[int]string)
	names := make(map[string]bool)
	hasFallback := false

	for _, t := range cfg.Models {
		if t.Name == "" {
			return fmt.Errorf("model tier must have a name")
		}
		if t.Priority < 0 {
			return fmt.Errorf("model tier %s: priority must be non-negative", t.Name)
		}
		if len(t.Models) == 0 {
			return fmt.Errorf("model tier %s: must have at least one model", t.Name)
		}
		if existing, ok := priorities[t.Priority]; ok {
			return fmt.Errorf("model tiers %s and %s share priority %d", existing, t.Name, t.Priority)
		}
		if names[t.Name] {
			return fmt.Errorf("duplicate model tier name: %s", t.Name)
		}
		priorities[t.Priority] = t.Name
		names[t.Name] = true

		if t.Routing.Fallback {
			if hasFallback {
				return fmt.Errorf("model tier %s: only one tier can be fallback", t.Name)
			}
			hasFallback = true
		}

		// Validate routing rules
		for i, rule := range t.Routing.Rules {
			if rule.MinScore != nil && rule.MaxScore != nil && *rule.MinScore >= *rule.MaxScore {
				return fmt.Errorf("model tier %s rule %d: min_score must be less than max_score", t.Name, i)
			}
			if rule.MinReasoning != nil && rule.MaxReasoning != nil && *rule.MinReasoning >= *rule.MaxReasoning {
				return fmt.Errorf("model tier %s rule %d: min_reasoning must be less than max_reasoning", t.Name, i)
			}
			if rule.MinDomain != nil && rule.MaxDomain != nil && *rule.MinDomain >= *rule.MaxDomain {
				return fmt.Errorf("model tier %s rule %d: min_domain must be less than max_domain", t.Name, i)
			}
			if rule.MinCreativity != nil && rule.MaxCreativity != nil && *rule.MinCreativity >= *rule.MaxCreativity {
				return fmt.Errorf("model tier %s rule %d: min_creativity must be less than max_creativity", t.Name, i)
			}
			if rule.MinConstraint != nil && rule.MaxConstraint != nil && *rule.MinConstraint >= *rule.MaxConstraint {
				return fmt.Errorf("model tier %s rule %d: min_constraint must be less than max_constraint", t.Name, i)
			}
		}

		// Validate replicas
		for _, m := range t.Models {
			if m.ID == "" {
				return fmt.Errorf("model tier %s: replica missing id", t.Name)
			}
			if m.URL == "" {
				return fmt.Errorf("model tier %s: replica %s missing url", t.Name, m.ID)
			}
			if m.Model == "" {
				return fmt.Errorf("model tier %s: replica %s missing model", t.Name, m.ID)
			}
		}
	}

	if !hasFallback {
		return fmt.Errorf("exactly one model tier must have routing.fallback: true")
	}

	if cfg.Redis.Addr == "" {
		return fmt.Errorf("redis addr required")
	}
	if cfg.Auth.Enabled && len(cfg.Auth.Keys) == 0 {
		return fmt.Errorf("auth enabled but no keys configured")
	}
	if cfg.RateLimit.Enabled {
		if cfg.RateLimit.RequestsPerMinute <= 0 {
			return fmt.Errorf("ratelimit requests_per_minute must be positive")
		}
		if cfg.RateLimit.Burst <= 0 {
			return fmt.Errorf("ratelimit burst must be positive")
		}
	}
	if cfg.Audit.Enabled && cfg.Audit.BufferSize <= 0 {
		return fmt.Errorf("audit buffer_size must be positive")
	}
	if cfg.Threshold < 0 || cfg.Threshold > 1 {
		return fmt.Errorf("threshold must be in [0, 1], got %v", cfg.Threshold)
	}
	if cfg.MaxQueue <= 0 {
		return fmt.Errorf("max_queue must be positive, got %v", cfg.MaxQueue)
	}
	if cfg.Weights.CacheAffinity < 0 || cfg.Weights.QueueDepth < 0 ||
		cfg.Weights.KVCachePressure < 0 || cfg.Weights.Baseline < 0 ||
		cfg.Weights.MissPenalty < 0 {
		return fmt.Errorf("scoring weights must be non-negative")
	}
	if cfg.Circuit.ErrorThreshold < 0 || cfg.Circuit.ErrorThreshold > 1 {
		return fmt.Errorf("circuit error_threshold must be in [0, 1], got %v", cfg.Circuit.ErrorThreshold)
	}
	if cfg.Circuit.WindowSize <= 0 {
		return fmt.Errorf("circuit window_size must be positive, got %v", cfg.Circuit.WindowSize)
	}
	if cfg.Circuit.MinSamples <= 0 {
		return fmt.Errorf("circuit min_samples must be positive, got %d", cfg.Circuit.MinSamples)
	}
	if cfg.Stream.StallTimeout < 0 {
		return fmt.Errorf("stream stall_timeout must be non-negative, got %v", cfg.Stream.StallTimeout)
	}
	if cfg.Stream.DoneTimeout < 0 {
		return fmt.Errorf("stream done_timeout must be non-negative, got %v", cfg.Stream.DoneTimeout)
	}
	if cfg.Repetition.FrequencyPenalty != nil {
		fp := *cfg.Repetition.FrequencyPenalty
		if fp < -2.0 || fp > 2.0 {
			return fmt.Errorf("repetition frequency_penalty must be in [-2.0, 2.0], got %v", fp)
		}
	}
	if cfg.Repetition.PresencePenalty != nil {
		pp := *cfg.Repetition.PresencePenalty
		if pp < -2.0 || pp > 2.0 {
			return fmt.Errorf("repetition presence_penalty must be in [-2.0, 2.0], got %v", pp)
		}
	}
	if cfg.Usage.Postgres.Enabled {
		if cfg.Usage.Postgres.DSN == "" {
			return fmt.Errorf("usage.postgres enabled but dsn is empty")
		}
		if cfg.Usage.Postgres.FlushInterval < 0 {
			return fmt.Errorf("usage.postgres flush_interval must be non-negative, got %v", cfg.Usage.Postgres.FlushInterval)
		}
		if cfg.Usage.Postgres.BatchSize < 0 || cfg.Usage.Postgres.BufferSize < 0 {
			return fmt.Errorf("usage.postgres batch_size and buffer_size must be non-negative")
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// ToReplicaList builds the runtime replica list with auto-detected model
// sizes and compiled routing rules.
// ---------------------------------------------------------------------------

func (c *Config) ToReplicaList() types.ReplicaList {
	// Sort model tiers by priority (lowest first = evaluated first)
	sorted := make([]ModelTier, len(c.Models))
	copy(sorted, c.Models)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})

	var replicas []types.Replica
	for _, t := range sorted {
		// Detect tier-level properties from model names
		var maxSize int64
		tierIsCode := false
		tierIsReasoning := false

		for _, m := range t.Models {
			size := parseModelSize(m.Model)
			if size > maxSize {
				maxSize = size
			}
			if isCodeModel(m.Model) {
				tierIsCode = true
			}
			if isReasoningModel(m.Model) {
				tierIsReasoning = true
			}
		}

		// Convert config routing rules to runtime types
		rules := make([]types.RoutingRule, len(t.Routing.Rules))
		for i, r := range t.Routing.Rules {
			rules[i] = types.RoutingRule{
				MinScore:      r.MinScore,
				MaxScore:      r.MaxScore,
				MinReasoning:  r.MinReasoning,
				MaxReasoning:  r.MaxReasoning,
				MinDomain:     r.MinDomain,
				MaxDomain:     r.MaxDomain,
				MinCreativity: r.MinCreativity,
				MaxCreativity: r.MaxCreativity,
				MinConstraint: r.MinConstraint,
				MaxConstraint: r.MaxConstraint,
				TaskTypes:     r.TaskTypes,
			}
		}

		tierCfg := types.TierConfig{
			Name:        types.ModelTier(t.Name),
			Priority:    t.Priority,
			MaxSize:     maxSize,
			IsCode:      tierIsCode,
			IsReasoning: tierIsReasoning,
			Routing: types.TierRouting{
				Rules:       rules,
				TaskTypes:   t.Routing.TaskTypes,
				CodeSignals: t.Routing.CodeSignals,
				Fallback:    t.Routing.Fallback,
			},
		}

		for _, m := range t.Models {
			replicas = append(replicas, types.Replica{
				ID:        m.ID,
				URL:       m.URL,
				Model:     m.Model,
				Tier:      types.ModelTier(t.Name),
				TierCfg:   tierCfg,
				ParamSize: parseModelSize(m.Model),
			})
		}
	}

	return types.ReplicaList{Replicas: replicas}
}
