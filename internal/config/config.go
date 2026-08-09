package config

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration, loaded from a YAML file.
type Config struct {
	Server ServerConfig `yaml:"server"`
	Scrape ScrapeConfig `yaml:"scrape"`
	TFE    TFEConfig    `yaml:"tfe"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
}

type ScrapeConfig struct {
	Interval Duration `yaml:"interval"`
	Targets  []Target `yaml:"targets"`
}

type TFEConfig struct {
	Enabled      bool     `yaml:"enabled"`
	Address      string   `yaml:"address"`
	Organization string   `yaml:"organization"`
	Interval     Duration `yaml:"interval"`
}

type Target struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// Duration lets YAML strings like "15s" decode into a time.Duration.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Load reads, parses, defaults, and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaultsAndValidate() error {
	if c.Server.Addr == "" {
		c.Server.Addr = ":2112"
	}
	if c.Scrape.Interval == 0 {
		c.Scrape.Interval = Duration(15 * time.Second)
	}
	for i, t := range c.Scrape.Targets {
		if t.Name == "" {
			return fmt.Errorf("scrape target %d: name is required", i)
		}
		if _, err := url.ParseRequestURI(t.URL); err != nil {
			return fmt.Errorf("scrape target %q: invalid url %q: %w", t.Name, t.URL, err)
		}
	}
	if c.TFE.Enabled {
		if c.TFE.Interval == 0 {
			c.TFE.Interval = Duration(30 * time.Second)
		}
		if _, err := url.ParseRequestURI(c.TFE.Address); err != nil {
			return fmt.Errorf("tfe: invalid address %q: %w", c.TFE.Address, err)
		}
		if c.TFE.Organization == "" {
			return fmt.Errorf("tfe: organization is required when enabled")
		}
	}
	return nil
}
