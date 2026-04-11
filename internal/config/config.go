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
	PrefixLen    int               `yaml:"prefix_len"`
	AffinityTTL  time.Duration     `yaml:"affinity_ttl"`
	Threshold    float64           `yaml:"threshold"`
	MaxQueue     float64           `yaml:"max_queue"`
	PollInterval time.Duration     `yaml:"poll_interval"`
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
	return nil
}
