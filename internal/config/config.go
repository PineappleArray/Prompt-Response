package config

import (
	"fmt"
	"os"
	"time"

	"prompt-response/internal/types"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr   string            `yaml:"listen_addr"`
	Replicas     []Replica         `yaml:"replicas"`
	Redis        Redis             `yaml:"redis"`
	Weights      Weights           `yaml:"weights"`
	Classifier   ClassifierWeights `yaml:"classifier"`
	Circuit      Circuit           `yaml:"circuit"`
	Retry        Retry             `yaml:"retry"`
	Auth         Auth              `yaml:"auth"`
	RateLimit    RateLimit         `yaml:"ratelimit"`
	Audit        Audit             `yaml:"audit"`
	PrefixLen    int               `yaml:"prefix_len"`
	AffinityTTL  time.Duration     `yaml:"affinity_ttl"`
	Threshold    float64           `yaml:"threshold"`
	MaxQueue     float64           `yaml:"max_queue"`
	PollInterval time.Duration     `yaml:"poll_interval"`
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

type Replica struct {
	ID    string          `yaml:"id"`
	URL   string          `yaml:"url"`
	Model string          `yaml:"model"`
	Tier  types.ModelTier `yaml:"tier"`
}

type Redis struct {
	Addr string `yaml:"addr"`
}

type Weights struct {
	CacheAffinity   float64 `yaml:"cache_affinity"`
	QueueDepth      float64 `yaml:"queue_depth"`
	KVCachePressure float64 `yaml:"kv_cache_pressure"`
	Baseline        float64 `yaml:"baseline"`
}

// Auth controls API key authentication.
type Auth struct {
	Enabled bool      `yaml:"enabled"`
	Keys    []AuthKey `yaml:"keys"`
}

// AuthKey maps an API key to a tenant identifier.
type AuthKey struct {
	Key    string `yaml:"key"`
	Tenant string `yaml:"tenant"`
}

// RateLimit controls per-tenant request rate limiting.
type RateLimit struct {
	Enabled           bool    `yaml:"enabled"`
	RequestsPerMinute float64 `yaml:"requests_per_minute"`
	Burst             int     `yaml:"burst"`
}

// Audit controls the request routing decision audit trail.
type Audit struct {
	Enabled    bool `yaml:"enabled"`
	BufferSize int  `yaml:"buffer_size"`
}

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
	// Classifier weight defaults
	c := &cfg.Classifier
	if c.Length == 0 && c.Code == 0 && c.Reasoning == 0 {
		c.Length = 0.20
		c.Code = 0.30
		c.Reasoning = 0.15
		c.Complexity = 0.10
		c.ConvDepth = 0.10
		c.OutputLength = 0.15
	}
	// Circuit breaker defaults
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
	// Retry defaults
	if cfg.Retry.MaxRetries == 0 {
		cfg.Retry.MaxRetries = 1
	}
	if cfg.Retry.Timeout == 0 {
		cfg.Retry.Timeout = 30 * time.Second
	}
	// Rate limit defaults
	if cfg.RateLimit.RequestsPerMinute == 0 {
		cfg.RateLimit.RequestsPerMinute = 60
	}
	if cfg.RateLimit.Burst == 0 {
		cfg.RateLimit.Burst = 10
	}
	// Audit defaults
	if cfg.Audit.BufferSize == 0 {
		cfg.Audit.BufferSize = 1000
	}
}

func validate(cfg *Config) error {
	if len(cfg.Replicas) == 0 {
		return fmt.Errorf("at least one replica required")
	}
	for _, r := range cfg.Replicas {
		if r.ID == "" || r.URL == "" {
			return fmt.Errorf("replica must have id and url")
		}
		if !types.ValidTier(r.Tier) {
			return fmt.Errorf("replica %s: invalid tier %q (valid: small, medium, large)", r.ID, r.Tier)
		}
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
	return nil
}
