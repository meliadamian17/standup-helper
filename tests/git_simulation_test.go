//go:build integration

package standup_helper_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"standup-helper/config"
	"standup-helper/logger"
	"standup-helper/monitor"
)

func TestGitMonitor_Simulation(t *testing.T) {
	repoDir := t.TempDir()
	logDir := t.TempDir()

	// Require git
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH, skipping git simulation test")
	}

	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(repoDir, "init")
	git(repoDir, "config", "user.name", "Test User")
	git(repoDir, "config", "user.email", "test@example.com")

	// First commit
	if err := os.WriteFile(filepath.Join(repoDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "a.txt")
	git(repoDir, "commit", "-m", "first")

	// Second commit (so monitor has a non-empty lastCommit when it starts)
	if err := os.WriteFile(filepath.Join(repoDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "b.txt")
	git(repoDir, "commit", "-m", "second")

	cfg := &config.Config{
		Directories: []string{repoDir},
		Exclusions:  []string{"node_modules", ".git"},
		Git:         config.GitConfig{PollInterval: 80 * time.Millisecond, TrackCommits: true},
		Filesystem:  config.FSConfig{Debounce: 2 * time.Second, TrackDiffs: false},
		Summarizer:  config.SummarizerConfig{Enabled: false, Model: "llama3.2"},
	}

	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	gitMonitor := monitor.NewGitMonitor(cfg, log)
	if err := gitMonitor.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Third commit after monitor has started (so it will be logged)
	if err := os.WriteFile(filepath.Join(repoDir, "c.txt"), []byte("c"), 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "c.txt")
	git(repoDir, "commit", "-m", "third")

	// Wait for at least one poll
	time.Sleep(150 * time.Millisecond)
	if err := log.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := gitMonitor.Stop(); err != nil {
		t.Fatal(err)
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "standup-"+today+".md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## Git Commits") {
		t.Errorf("log missing Git Commits section: %s", content)
	}
	if !strings.Contains(content, "third") {
		t.Errorf("log missing third commit message: %s", content)
	}
}
