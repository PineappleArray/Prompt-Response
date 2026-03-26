package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Replicas  []Replica `yaml:"replicas"`
	Redis     Redis     `yaml:"redis"`
	Weights   Weights   `yaml:"weights"`
	PrefixLen int       `yaml:"prefix_len"`
	AffinityTTL time.Duration `yaml:"affinity_ttl"`
	Threshold float64   `yaml:"threshold"`
}

type Replica struct {
	ID    string `yaml:"id"`
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
	Tier  string `yaml:"tier"`
}

type Redis struct {
	Addr string `yaml:"addr"`
}

type Weights struct {
	W1 float64 `yaml:"w1"`
	W2 float64 `yaml:"w2"`
	W3 float64 `yaml:"w3"`
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
	return &cfg, nil
}