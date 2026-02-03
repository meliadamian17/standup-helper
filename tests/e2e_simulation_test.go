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
	"standup-helper/summarizer"
)

func TestE2E_FileSystemAndGit_Simulation(t *testing.T) {
	repoDir := t.TempDir()
	logDir := t.TempDir()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH, skipping e2e simulation test")
	}

	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(repoDir, "init")
	git(repoDir, "config", "user.name", "Test User")
	git(repoDir, "config", "user.email", "test@example.com")

	// Two commits so monitor has a baseline
	if err := os.WriteFile(filepath.Join(repoDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "a.txt")
	git(repoDir, "commit", "-m", "first")
	if err := os.WriteFile(filepath.Join(repoDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "b.txt")
	git(repoDir, "commit", "-m", "second")

	cfg := &config.Config{
		Directories: []string{repoDir},
		Exclusions:  []string{"node_modules", ".git"},
		Git:         config.GitConfig{PollInterval: 80 * time.Millisecond, TrackCommits: true},
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
	gitMonitor := monitor.NewGitMonitor(cfg, log)

	if err := fsMonitor.Start(); err != nil {
		t.Fatalf("FileSystem Start: %v", err)
	}
	if err := gitMonitor.Start(); err != nil {
		t.Fatalf("Git Start: %v", err)
	}

	// Trigger file change: create a new file
	e2ePath := filepath.Join(repoDir, "e2e.txt")
	if err := os.WriteFile(e2ePath, []byte("e2e"), 0644); err != nil {
		t.Fatal(err)
	}

	// New commit so git monitor logs it
	git(repoDir, "add", "e2e.txt")
	git(repoDir, "commit", "-m", "e2e third commit")

	time.Sleep(250 * time.Millisecond)
	if err := log.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := fsMonitor.Stop(); err != nil {
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
	if !strings.Contains(content, "## File Changes") {
		t.Errorf("log missing File Changes section: %s", content)
	}
	if !strings.Contains(content, "e2e.txt") {
		t.Errorf("log missing e2e.txt in File Changes: %s", content)
	}
	if !strings.Contains(content, "## Git Commits") {
		t.Errorf("log missing Git Commits section: %s", content)
	}
	if !strings.Contains(content, "e2e third commit") {
		t.Errorf("log missing e2e third commit message: %s", content)
	}
}

// TestE2E_Summarizer_RealOllama runs an e2e simulation with the summarizer enabled
// and uses the real Ollama service. Skips if Ollama is not available or the model is not loaded.
func TestE2E_Summarizer_RealOllama(t *testing.T) {
	repoDir := t.TempDir()
	logDir := t.TempDir()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH, skipping summarizer e2e test")
	}

	git := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git(repoDir, "init")
	git(repoDir, "config", "user.name", "Test User")
	git(repoDir, "config", "user.email", "test@example.com")

	// One committed file so we can modify it and get a diff for summarization
	codePath := filepath.Join(repoDir, "code.go")
	initialContent := []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	if err := os.WriteFile(codePath, initialContent, 0644); err != nil {
		t.Fatal(err)
	}
	git(repoDir, "add", "code.go")
	git(repoDir, "commit", "-m", "initial")

	model := "llama3.2"
	if m := os.Getenv("STANDUP_HELPER_TEST_OLLAMA_MODEL"); m != "" {
		model = m
	}
	cfg := &config.Config{
		Directories: []string{repoDir},
		Exclusions:  []string{"node_modules", ".git"},
		Git:         config.GitConfig{PollInterval: 30 * time.Second, TrackCommits: false},
		Filesystem:  config.FSConfig{Debounce: 100 * time.Millisecond, TrackDiffs: true},
		Summarizer:  config.SummarizerConfig{Enabled: true, Model: model, KeepAlive: "0"},
	}

	summ := summarizer.NewSummarizer("", model, true, "0")
	if err := summ.EnsureModelLoaded(); err != nil {
		t.Skipf("Ollama not available or model %q not loaded: %v", model, err)
	}

	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	fsMonitor, err := monitor.NewFileSystemMonitor(cfg, log, summ)
	if err != nil {
		t.Fatalf("NewFileSystemMonitor: %v", err)
	}

	if err := fsMonitor.Start(); err != nil {
		t.Fatalf("FileSystem Start: %v", err)
	}

	// Modify file so there is a diff for the summarizer
	modifiedContent := []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n\tprintln(\"world\")\n}\n")
	if err := os.WriteFile(codePath, modifiedContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce then allow time for Ollama to summarize (can be 10–60s depending on model)
	time.Sleep(200 * time.Millisecond)
	const ollamaWait = 60 * time.Second
	deadline := time.Now().Add(ollamaWait)
	for time.Now().Before(deadline) {
		if err := log.Flush(); err != nil {
			t.Fatal(err)
		}
		today := time.Now().Format("2006-01-02")
		data, _ := os.ReadFile(filepath.Join(logDir, "standup-"+today+".md"))
		content := string(data)
		if strings.Contains(content, "Summary:") && strings.Contains(content, "Full diff:") {
			break
		}
		time.Sleep(500 * time.Millisecond)
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
	if !strings.Contains(content, "code.go") {
		t.Errorf("log missing code.go: %s", content)
	}
	if !strings.Contains(content, "Summary:") {
		t.Errorf("log missing Summary (summarizer output): %s", content)
	}
	if !strings.Contains(content, "Full diff:") {
		t.Errorf("log missing Full diff section: %s", content)
	}
}
