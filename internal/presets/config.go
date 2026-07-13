package presets

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// KafkaPreset fills a Weave Kafka trigger's brokers + auth secret from a
// pre-provisioned cluster known to this unit.
type KafkaPreset struct {
	Name      string   `yaml:"name" json:"name"`
	Brokers   []string `yaml:"brokers" json:"brokers"`
	SecretRef string   `yaml:"secretRef,omitempty" json:"secretRef,omitempty"`
}

// SecretPreset fills a raw Kubernetes Secret name (and optional key) field —
// used for authSecretRef, webhook secrets, git token secrets, volume mounts, etc.
type SecretPreset struct {
	Name       string `yaml:"name" json:"name"`
	SecretName string `yaml:"secretName" json:"secretName"`
	SecretKey  string `yaml:"secretKey,omitempty" json:"secretKey,omitempty"`
}

// Config is the top-level structure loaded from presets.yaml.
type Config struct {
	Kafka   []KafkaPreset  `yaml:"kafka" json:"kafka"`
	Secrets []SecretPreset `yaml:"secrets" json:"secrets"`
}

// LoadConfig reads and parses the YAML config at path. Unlike rbac.yaml,
// presets.yaml is optional — most units won't configure every preset kind —
// so a missing file yields an empty Config rather than an error.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("presets: read config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("presets: parse config: %w", err)
	}
	if cfg.Kafka == nil {
		cfg.Kafka = []KafkaPreset{}
	}
	if cfg.Secrets == nil {
		cfg.Secrets = []SecretPreset{}
	}
	return &cfg, nil
}
