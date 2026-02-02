package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger handles daily log file rotation and markdown formatting
type Logger struct {
	logDir      string
	currentDate string
	currentFile *os.File
	mu          sync.Mutex
	fileChanges []FileChange
	gitCommits  []GitCommit
}

// FileChange represents a file system change
type FileChange struct {
	Path      string
	Action    string // "created", "modified", "deleted"
	Timestamp time.Time
	Diff      string
}

// GitCommit represents a git commit
type GitCommit struct {
	Hash      string
	Author    string
	Message   string
	Timestamp time.Time
	Files     []string
}

// NewLogger creates a new logger instance
func NewLogger(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logger := &Logger{
		logDir:      logDir,
		currentDate: time.Now().Format("2006-01-02"),
		fileChanges: make([]FileChange, 0),
		gitCommits:  make([]GitCommit, 0),
	}

	if err := logger.ensureLogFile(); err != nil {
		return nil, err
	}

	return logger, nil
}

// ensureLogFile ensures the log file for the current date is open
func (l *Logger) ensureLogFile() error {
	today := time.Now().Format("2006-01-02")

	if l.currentDate == today && l.currentFile != nil {
		return nil // Already have the correct file open
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Close previous file if different date
	if l.currentFile != nil && l.currentDate != today {
		l.currentFile.Close()
		l.currentFile = nil
	}

	// Open new file if needed
	if l.currentFile == nil {
		logPath := filepath.Join(l.logDir, fmt.Sprintf("standup-%s.md", today))
		file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}

		// Write header if file is new
		stat, err := file.Stat()
		if err == nil && stat.Size() == 0 {
			if _, err := file.WriteString(fmt.Sprintf("# Standup Log - %s\n\n", today)); err != nil {
				file.Close()
				return fmt.Errorf("failed to write log header: %w", err)
			}
		}

		l.currentFile = file
		l.currentDate = today
	}

	return nil
}

// LogFileChange logs a file system change
func (l *Logger) LogFileChange(change FileChange) error {
	if err := l.ensureLogFile(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.fileChanges = append(l.fileChanges, change)
	return nil
}

// LogGitCommit logs a git commit
func (l *Logger) LogGitCommit(commit GitCommit) error {
	if err := l.ensureLogFile(); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.gitCommits = append(l.gitCommits, commit)
	return nil
}

// Flush writes all pending changes to the log file
func (l *Logger) Flush() error {
	if err := l.ensureLogFile(); err != nil {
		return err
	}

	l.mu.Lock()
	// Make copies of the slices to avoid holding lock during I/O
	fileChanges := make([]FileChange, len(l.fileChanges))
	copy(fileChanges, l.fileChanges)
	gitCommits := make([]GitCommit, len(l.gitCommits))
	copy(gitCommits, l.gitCommits)
	l.mu.Unlock()

	if len(fileChanges) == 0 && len(gitCommits) == 0 {
		return nil // Nothing to flush
	}

	// Re-acquire lock for file writing
	l.mu.Lock()
	defer l.mu.Unlock()

	// Write file changes section if we have any
	if len(fileChanges) > 0 {
		if _, err := l.currentFile.WriteString("## File Changes\n\n"); err != nil {
			return err
		}

		for _, change := range fileChanges {
			timestamp := change.Timestamp.Format("15:04:05")
			line := fmt.Sprintf("- `%s` (%s at %s)\n", change.Path, change.Action, timestamp)
			if _, err := l.currentFile.WriteString(line); err != nil {
				return err
			}

			if change.Diff != "" {
				diffBlock := fmt.Sprintf("  ```diff\n%s\n  ```\n", change.Diff)
				if _, err := l.currentFile.WriteString(diffBlock); err != nil {
					return err
				}
			}
		}

		if _, err := l.currentFile.WriteString("\n"); err != nil {
			return err
		}
	}

	// Write git commits section if we have any
	if len(gitCommits) > 0 {
		if _, err := l.currentFile.WriteString("## Git Commits\n\n"); err != nil {
			return err
		}

		for _, commit := range gitCommits {
			timestamp := commit.Timestamp.Format("15:04:05")
			line := fmt.Sprintf("- Commit: `%s` by %s at %s\n", commit.Hash[:7], commit.Author, timestamp)
			if _, err := l.currentFile.WriteString(line); err != nil {
				return err
			}

			if commit.Message != "" {
				msg := fmt.Sprintf("  Message: %s\n", commit.Message)
				if _, err := l.currentFile.WriteString(msg); err != nil {
					return err
				}
			}

			if len(commit.Files) > 0 {
				files := fmt.Sprintf("  Files: %s\n", joinFiles(commit.Files))
				if _, err := l.currentFile.WriteString(files); err != nil {
					return err
				}
			}
		}

		if _, err := l.currentFile.WriteString("\n"); err != nil {
			return err
		}
	}

	// Write summary
	summary := l.generateSummaryLocked(fileChanges, gitCommits)
	if _, err := l.currentFile.WriteString("## Summary\n\n"); err != nil {
		return err
	}
	if _, err := l.currentFile.WriteString(summary); err != nil {
		return err
	}
	if _, err := l.currentFile.WriteString("\n"); err != nil {
		return err
	}

	// Clear buffers
	l.fileChanges = l.fileChanges[:0]
	l.gitCommits = l.gitCommits[:0]

	// Sync to disk
	return l.currentFile.Sync()
}

// generateSummaryLocked generates a summary of changes (must be called with lock held)
func (l *Logger) generateSummaryLocked(fileChanges []FileChange, gitCommits []GitCommit) string {
	modified := 0
	created := 0
	deleted := 0

	for _, change := range fileChanges {
		switch change.Action {
		case "modified":
			modified++
		case "created":
			created++
		case "deleted":
			deleted++
		}
	}

	summary := ""
	if modified > 0 {
		summary += fmt.Sprintf("- %d file(s) modified\n", modified)
	}
	if created > 0 {
		summary += fmt.Sprintf("- %d file(s) created\n", created)
	}
	if deleted > 0 {
		summary += fmt.Sprintf("- %d file(s) deleted\n", deleted)
	}
	if len(gitCommits) > 0 {
		summary += fmt.Sprintf("- %d commit(s)\n", len(gitCommits))
	}

	if summary == "" {
		summary = "- No changes recorded\n"
	}

	return summary
}

// joinFiles joins file paths with commas, truncating if too long
func joinFiles(files []string) string {
	const maxLen = 200
	result := ""
	for i, file := range files {
		if i > 0 {
			result += ", "
		}
		if len(result)+len(file) > maxLen {
			result += fmt.Sprintf("... and %d more", len(files)-i)
			break
		}
		result += file
	}
	return result
}

// Close closes the log file
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.currentFile != nil {
		// Flush any remaining data (without holding lock to avoid deadlock)
		l.mu.Unlock()
		_ = l.Flush()
		l.mu.Lock()
		err := l.currentFile.Close()
		l.currentFile = nil
		return err
	}
	return nil
}
