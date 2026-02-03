//go:build integration

package standup_helper_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"standup-helper/logger"
)

func TestLogger_LogFileChange_Flush(t *testing.T) {
	logDir := t.TempDir()
	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	path := filepath.Join("/some", "project", "src.go")
	change := logger.FileChange{
		Path:      path,
		Action:    "modified",
		Timestamp: time.Now(),
		Diff:      "+ added line\n",
	}
	if err := log.LogFileChange(change); err != nil {
		t.Fatalf("LogFileChange: %v", err)
	}
	if err := log.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "standup-"+today+".md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "# Standup Log - ") {
		t.Error("log missing header")
	}
	if !strings.Contains(content, "## File Changes") {
		t.Error("log missing File Changes section")
	}
	if !strings.Contains(content, path) {
		t.Errorf("log missing path %q", path)
	}
	if !strings.Contains(content, "modified") {
		t.Error("log missing action")
	}
	if !strings.Contains(content, "+ added line") {
		t.Error("log missing diff content")
	}
}

func TestLogger_LogGitCommit_Flush(t *testing.T) {
	logDir := t.TempDir()
	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	commit := logger.GitCommit{
		Hash:      "abc123456789",
		Author:    "Test User",
		Message:   "fix: something",
		Timestamp: time.Now(),
		Files:     []string{"pkg/foo.go", "pkg/bar.go"},
	}
	if err := log.LogGitCommit(commit); err != nil {
		t.Fatalf("LogGitCommit: %v", err)
	}
	if err := log.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "standup-"+today+".md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## Git Commits") {
		t.Error("log missing Git Commits section")
	}
	if !strings.Contains(content, "abc1234") {
		t.Error("log missing short hash")
	}
	if !strings.Contains(content, "Test User") {
		t.Error("log missing author")
	}
	if !strings.Contains(content, "fix: something") {
		t.Error("log missing message")
	}
}

func TestLogger_GetCurrentDayLogContent(t *testing.T) {
	logDir := t.TempDir()
	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	if err := log.LogFileChange(logger.FileChange{Path: "/a/b.go", Action: "created", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := log.Flush(); err != nil {
		t.Fatal(err)
	}

	content, err := log.GetCurrentDayLogContent()
	if err != nil {
		t.Fatalf("GetCurrentDayLogContent: %v", err)
	}
	if !strings.Contains(content, "/a/b.go") || !strings.Contains(content, "created") {
		t.Errorf("GetCurrentDayLogContent missing written change: %s", content)
	}
}

func TestLogger_WriteDaySummary_HasWrittenAIDaySummary(t *testing.T) {
	logDir := t.TempDir()
	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	if log.HasWrittenAIDaySummary() {
		t.Error("HasWrittenAIDaySummary should be false before WriteDaySummary")
	}

	summary := "Test AI day summary content."
	if err := log.WriteDaySummary(summary); err != nil {
		t.Fatalf("WriteDaySummary: %v", err)
	}
	if !log.HasWrittenAIDaySummary() {
		t.Error("HasWrittenAIDaySummary should be true after WriteDaySummary")
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "standup-"+today+".md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## AI Day Summary") {
		t.Error("log missing AI Day Summary section")
	}
	if !strings.Contains(content, summary) {
		t.Errorf("log missing summary text: %s", content)
	}

	// Second WriteDaySummary should be no-op
	if err := log.WriteDaySummary("other"); err != nil {
		t.Fatalf("second WriteDaySummary: %v", err)
	}
	data2, _ := os.ReadFile(logPath)
	if strings.Contains(string(data2), "other") {
		t.Error("second WriteDaySummary should not append")
	}
}

func TestLogger_HasWrittenAIDaySummary_WithFixture(t *testing.T) {
	logDir := t.TempDir()
	today := time.Now().Format("2006-01-02")
	destPath := filepath.Join(logDir, "standup-"+today+".md")
	fixturePath := filepath.Join("testdata", "logs", "standup-with-ai-summary.md")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not found (run from tests dir): %v", err)
	}
	// Write fixture content with today's header so logger sees existing file
	content := string(fixture)
	content = strings.Replace(content, "# Standup Log - 2026-01-15", "# Standup Log - "+today, 1)
	if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	log, err := logger.NewLogger(logDir)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	defer log.Close()

	if !log.HasWrittenAIDaySummary() {
		t.Error("HasWrittenAIDaySummary should be true when log already contains AI Day Summary section")
	}
}
