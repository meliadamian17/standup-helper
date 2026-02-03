//go:build integration

package standup_helper_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"standup-helper/config"
	"standup-helper/logger"
	"standup-helper/monitor"
)

func TestFileSystemMonitor_Simulation(t *testing.T) {
	monitoredDir := t.TempDir()
	logDir := t.TempDir()

	// Ensure monitored dir has at least one file so the directory is watched
	seedPath := filepath.Join(monitoredDir, "seed.txt")
	if err := os.WriteFile(seedPath, []byte("seed"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Directories: []string{monitoredDir},
		Exclusions:  []string{"node_modules", ".git"},
		Git:         config.GitConfig{PollInterval: 30 * time.Second, TrackCommits: false},
		Filesystem:  config.FSConfig{Debounce: 80 * time.Millisecond, TrackDiffs: false},
		Summarizer:  config.SummarizerConfig{Enabled: false, Model: "llama3.2"},
	}

	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	fsMonitor, err := monitor.NewFileSystemMonitor(cfg, log, nil)
	if err != nil {
		t.Fatalf("NewFileSystemMonitor: %v", err)
	}

	if err := fsMonitor.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Create a new file so we get a create event
	targetPath := filepath.Join(monitoredDir, "foo.txt")
	if err := os.WriteFile(targetPath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce and a bit more for event processing
	time.Sleep(200 * time.Millisecond)
	if err := log.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := fsMonitor.Stop(); err != nil {
		t.Fatal(err)
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "standup-"+today+".md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## File Changes") {
		t.Errorf("log missing File Changes section: %s", content)
	}
	if !strings.Contains(content, "foo.txt") {
		t.Errorf("log missing expected file foo.txt: %s", content)
	}
}
