//go:build integration

package standup_helper_test

import (
	"os"
	"path/filepath"
	"testing"

	"standup-helper/config"
	"gopkg.in/yaml.v3"
)

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeConfigWithDir(t, cfgPath, dir)

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Directories) != 1 || cfg.Directories[0] != dir {
		t.Errorf("expected directories [%s], got %v", dir, cfg.Directories)
	}
}

func TestLoadConfig_EmptyDirectories_Fails(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	writeYAML(t, cfgPath, map[string]interface{}{
		"directories": []string{},
		"exclusions":  []string{".git"},
		"git":         map[string]interface{}{"poll_interval": "30s", "track_commits": true},
		"filesystem":  map[string]interface{}{"debounce": "2s", "track_diffs": true},
		"summarizer":  map[string]interface{}{"enabled": false, "model": "llama3.2"},
	})

	_, err := config.LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("LoadConfig with empty directories should fail")
	}
}

func TestCreateDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	if err := config.CreateDefaultConfig(cfgPath); err != nil {
		t.Fatalf("CreateDefaultConfig: %v", err)
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("config file is empty")
	}

	// CreateDefaultConfig writes empty directories; LoadConfig will fail on Validate
	_, err = config.LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("default config has empty directories; LoadConfig should fail")
	}
}

func TestCreateDefaultConfig_NoOverwrite(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := config.CreateDefaultConfig(cfgPath); err != nil {
		t.Fatalf("CreateDefaultConfig: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Errorf("CreateDefaultConfig should not overwrite; got %q", string(data))
	}
}

func TestShouldExclude(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	writeConfigWithDir(t, cfgPath, dir)

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	tests := []struct {
		path   string
		expect bool
	}{
		{filepath.Join(dir, "node_modules"), true},  // base matches "node_modules"
		{filepath.Join(dir, ".git"), true},          // base matches ".git"
		{filepath.Join(dir, "src", "main.go"), false},
		{filepath.Join(dir, "foo.log"), true},       // base matches "*.log"
	}
	for _, tt := range tests {
		got := cfg.ShouldExclude(tt.path)
		if got != tt.expect {
			t.Errorf("ShouldExclude(%q) = %v, want %v", tt.path, got, tt.expect)
		}
	}
}

func writeConfigWithDir(t *testing.T, cfgPath, dir string) {
	t.Helper()
	writeYAML(t, cfgPath, map[string]interface{}{
		"directories": []string{dir},
		"exclusions":  []string{"node_modules", ".git", "*.log"},
		"git":         map[string]interface{}{"poll_interval": "30s", "track_commits": true},
		"filesystem":  map[string]interface{}{"debounce": "2s", "track_diffs": true},
		"summarizer":  map[string]interface{}{"enabled": false, "model": "llama3.2"},
	})
}

func writeYAML(t *testing.T, path string, v interface{}) {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
