// Package config loads and parses the packyard-auth runtime configuration.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v2"
)

type ComponentConfig struct {
	Name string `yaml:"name"`
}

type Config struct {
	Components []ComponentConfig `yaml:"components"`
}

// ComponentSet returns the set of valid component names as a map for O(1) lookup.
// Entries with empty or whitespace-only names are silently skipped; names are trimmed.
func (c *Config) ComponentSet() map[string]bool {
	set := make(map[string]bool, len(c.Components))
	for _, comp := range c.Components {
		if name := strings.TrimSpace(comp.Name); name != "" {
			set[name] = true
		}
	}
	return set
}

func (c *Config) ComponentNames() []string {
	names := make([]string, 0, len(c.Components))
	for _, comp := range c.Components {
		if name := strings.TrimSpace(comp.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (c *Config) ComponentList() string {
	return strings.Join(c.ComponentNames(), ", ")
}

// Load reads and parses the YAML config file at path.
// Returns an error if the file cannot be read, is not valid YAML, or defines no components.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	for _, comp := range cfg.Components {
		if strings.TrimSpace(comp.Name) != "" {
			return &cfg, nil
		}
	}
	return nil, fmt.Errorf("config %s: no components defined", path)
}
