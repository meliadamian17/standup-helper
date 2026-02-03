package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration
type Config struct {
	Directories []string      `yaml:"directories"`
	Exclusions  []string      `yaml:"exclusions"`
	Git         GitConfig     `yaml:"git"`
	Filesystem  FSConfig      `yaml:"filesystem"`
	Summarizer  SummarizerConfig `yaml:"summarizer"`
}

// GitConfig contains git monitoring settings
type GitConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	TrackCommits bool          `yaml:"track_commits"`
}

// FSConfig contains filesystem monitoring settings
type FSConfig struct {
	Debounce  time.Duration `yaml:"debounce"`
	TrackDiffs bool          `yaml:"track_diffs"`
}

// SummarizerConfig contains summarization settings
type SummarizerConfig struct {
	Enabled          bool   `yaml:"enabled"`
	BaseURL          string `yaml:"base_url,omitempty"` // Optional - will be auto-detected if empty
	Model            string `yaml:"model"`
	KeepAlive        string `yaml:"keep_alive,omitempty"`         // e.g. "0" to unload after each request, "5m" for default
	EndOfDaySummary  bool   `yaml:"end_of_day_summary,omitempty"` // Generate AI day summary at end_of_day_time
	EndOfDayTime     string `yaml:"end_of_day_time,omitempty"`    // Time of day for daily summary, e.g. "18:00"
}

// LoadConfig loads configuration from the specified path
func LoadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Expand tilde paths in directories
	if err := config.expandPaths(); err != nil {
		return nil, fmt.Errorf("failed to expand paths: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// expandPaths expands tilde (~) paths to absolute paths
func (c *Config) expandPaths() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	expanded := make([]string, 0, len(c.Directories))
	for _, dir := range c.Directories {
		// Expand ~ to home directory
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(homeDir, dir[2:])
		} else if dir == "~" {
			dir = homeDir
		}
		
		// Convert to absolute path
		if !filepath.IsAbs(dir) {
			absPath, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("failed to resolve path %s: %w", dir, err)
			}
			dir = absPath
		}
		
		expanded = append(expanded, dir)
	}
	
	c.Directories = expanded
	return nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if len(c.Directories) == 0 {
		return fmt.Errorf("at least one directory must be configured")
	}

	for _, dir := range c.Directories {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("directory path must be absolute: %s", dir)
		}
	}

	if c.Git.PollInterval <= 0 {
		c.Git.PollInterval = 30 * time.Second
	}

	if c.Filesystem.Debounce <= 0 {
		c.Filesystem.Debounce = 2 * time.Second
	}

	// Set default summarizer config
	// BaseURL will be auto-detected by the summarizer if empty
	if c.Summarizer.Model == "" {
		c.Summarizer.Model = "llama3.2" // Default to a common lightweight model
	}
	if c.Summarizer.KeepAlive == "" {
		c.Summarizer.KeepAlive = "0" // Unload model after each request to avoid context buildup
	}
	if c.Summarizer.EndOfDayTime == "" {
		c.Summarizer.EndOfDayTime = "18:00"
	}

	return nil
}

// GetConfigPath returns the default config file path
func GetConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(homeDir, ".standup-helper", "config.yaml")
}

// CreateDefaultConfig creates a default configuration file if it doesn't exist
func CreateDefaultConfig(configPath string) error {
	// Check if config already exists
	if _, err := os.Stat(configPath); err == nil {
		return nil // Config already exists
	}

	// Create directory if it doesn't exist
	configDir := filepath.Dir(configPath)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create default config
	defaultConfig := Config{
		Directories: []string{},
		Exclusions: []string{
			"node_modules",
			".git",
			".standup-helper", // Avoid watching our own log/config directory
			"dist",
			"build",
			".DS_Store",
			"*.log",
		},
		Git: GitConfig{
			PollInterval: 30 * time.Second,
			TrackCommits: true,
		},
		Filesystem: FSConfig{
			Debounce:  2 * time.Second,
			TrackDiffs: true,
		},
		Summarizer: SummarizerConfig{
			Enabled:         false,  // Disabled by default
			BaseURL:         "",     // Auto-detected if empty
			Model:           "llama3.2",
			KeepAlive:       "0",    // Unload after each request to avoid context buildup
			EndOfDaySummary: false,
			EndOfDayTime:    "18:00",
		},
	}

	data, err := yaml.Marshal(&defaultConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal default config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write default config: %w", err)
	}

	return nil
}

// ShouldExclude checks if a file path should be excluded based on configuration
func (c *Config) ShouldExclude(path string) bool {
	base := filepath.Base(path)
	for _, exclusion := range c.Exclusions {
		matched, err := filepath.Match(exclusion, base)
		if err == nil && matched {
			return true
		}
		// Also check if the exclusion appears anywhere in the path
		if filepath.Base(exclusion) == exclusion {
			// Simple pattern match
			if matched, _ := filepath.Match(exclusion, base); matched {
				return true
			}
		}
	}
	return false
}
