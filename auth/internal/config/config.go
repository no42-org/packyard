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
	Name       string `yaml:"name"`
	Visibility string `yaml:"visibility"` // "public" | "private" (default: "private")
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

// PublicComponents returns the set of component names whose visibility is "public".
func (c *Config) PublicComponents() map[string]bool {
	set := make(map[string]bool)
	for _, comp := range c.Components {
		if name := strings.TrimSpace(comp.Name); name != "" && comp.Visibility == "public" {
			set[name] = true
		}
	}
	return set
}

// ComponentVisibility returns a map of component name to visibility string.
// Components with no visibility set default to "private".
func (c *Config) ComponentVisibility() map[string]string {
	m := make(map[string]string, len(c.Components))
	for _, comp := range c.Components {
		name := strings.TrimSpace(comp.Name)
		if name == "" {
			continue
		}
		vis := comp.Visibility
		if vis == "" {
			vis = "private"
		}
		m[name] = vis
	}
	return m
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
	hasValid := false
	seen := map[string]bool{}
	for _, comp := range cfg.Components {
		name := strings.TrimSpace(comp.Name)
		if name == "" {
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("config %s: duplicate component name %q", path, name)
		}
		seen[name] = true
		hasValid = true
		if vis := comp.Visibility; vis != "" && vis != "public" && vis != "private" {
			return nil, fmt.Errorf("config %s: component %q has invalid visibility %q (must be \"public\" or \"private\")", path, comp.Name, vis)
		}
	}
	if !hasValid {
		return nil, fmt.Errorf("config %s: no components defined", path)
	}
	return &cfg, nil
}
