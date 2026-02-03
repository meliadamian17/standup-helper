package monitor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"standup-helper/config"
	"standup-helper/logger"
	"standup-helper/summarizer"

	"github.com/fsnotify/fsnotify"
)

// pathCooldown prevents re-processing the same path within this window (e.g. repeated fsnotify events).
const pathCooldown = 2 * time.Minute

// FileSystemMonitor monitors file system changes
type FileSystemMonitor struct {
	config        *config.Config
	logger        *logger.Logger
	summarizer    *summarizer.Summarizer
	watcher       *fsnotify.Watcher
	debouncer     map[string]*time.Timer
	debouncerMu   sync.Mutex
	lastProcessed map[string]time.Time
	processMu     sync.Mutex
	stopChan      chan struct{}
	doneChan      chan struct{}
}

// NewFileSystemMonitor creates a new file system monitor. If summ is nil, a summarizer is created from cfg.
func NewFileSystemMonitor(cfg *config.Config, log *logger.Logger, summ *summarizer.Summarizer) (*FileSystemMonitor, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	if summ == nil {
		summ = summarizer.NewSummarizer(
			cfg.Summarizer.BaseURL,
			cfg.Summarizer.Model,
			cfg.Summarizer.Enabled,
			cfg.Summarizer.KeepAlive,
		)
	}

	return &FileSystemMonitor{
		config:        cfg,
		logger:        log,
		summarizer:    summ,
		watcher:       watcher,
		debouncer:     make(map[string]*time.Timer),
		lastProcessed: make(map[string]time.Time),
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
	}, nil
}

// Start begins monitoring configured directories
func (m *FileSystemMonitor) Start() error {
	// Add all configured directories to watcher
	for _, dir := range m.config.Directories {
		if err := m.addDirectory(dir); err != nil {
			return fmt.Errorf("failed to add directory %s: %w", dir, err)
		}
	}

	// Start event processing goroutine
	go m.processEvents()

	return nil
}

// addDirectory recursively adds a directory to the watcher, excluding configured patterns
func (m *FileSystemMonitor) addDirectory(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		// Skip excluded paths
		if m.config.ShouldExclude(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Only watch directories (fsnotify watches directories, not individual files)
		if info.IsDir() {
			if err := m.watcher.Add(path); err != nil {
				// Log but don't fail on individual directory errors
				return nil
			}
		}

		return nil
	})
}

// processEvents processes file system events
func (m *FileSystemMonitor) processEvents() {
	defer close(m.doneChan)

	for {
		select {
		case event, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			m.handleEvent(event)

		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			// Log error but continue monitoring
			fmt.Fprintf(os.Stderr, "File watcher error: %v\n", err)

		case <-m.stopChan:
			return
		}
	}
}

// handleEvent handles a file system event with debouncing
func (m *FileSystemMonitor) handleEvent(event fsnotify.Event) {
	// Skip excluded files
	if m.config.ShouldExclude(event.Name) {
		return
	}

	// Skip directory events (we only care about file changes)
	info, err := os.Stat(event.Name)
	if err != nil {
		return // File might have been deleted
	}
	if info.IsDir() {
		return
	}

	// Determine action
	action := "modified"
	if event.Op&fsnotify.Create == fsnotify.Create {
		action = "created"
	} else if event.Op&fsnotify.Remove == fsnotify.Remove {
		action = "deleted"
	}

	// Capture for closure so the timer callback uses this event, not a later one
	filePath := event.Name
	act := action

	m.debouncerMu.Lock()
	defer m.debouncerMu.Unlock()

	// Cancel existing timer for this file
	if timer, exists := m.debouncer[filePath]; exists {
		timer.Stop()
	}

	timer := time.AfterFunc(m.config.Filesystem.Debounce, func() {
		m.processFileChange(filePath, act)
		m.debouncerMu.Lock()
		delete(m.debouncer, filePath)
		m.debouncerMu.Unlock()
	})

	m.debouncer[filePath] = timer
}

// processFileChange processes a file change after debouncing
func (m *FileSystemMonitor) processFileChange(filePath string, action string) {
	m.processMu.Lock()
	if last, ok := m.lastProcessed[filePath]; ok && time.Since(last) < pathCooldown {
		m.processMu.Unlock()
		return
	}
	m.lastProcessed[filePath] = time.Now() // Block concurrent timers for same path
	m.processMu.Unlock()

	// Get diff if configured and file exists
	diff := ""
	if m.config.Filesystem.TrackDiffs && action != "deleted" {
		if d, err := m.getFileDiff(filePath); err == nil {
			diff = d
		}
	}

	// Summarize diff if summarizer is enabled
	if diff != "" && m.summarizer != nil {
		fmt.Fprintf(os.Stderr, "[DEBUG] Attempting to summarize diff for %s (diff length: %d)\n", filePath, len(diff))
		summary, err := m.summarizer.SummarizeDiff(diff, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] Summarization failed for %s: %v\n", filePath, err)
		} else if summary != "" {
			fmt.Fprintf(os.Stderr, "[DEBUG] Summarization successful for %s (summary length: %d)\n", filePath, len(summary))
			// Use summary instead of full diff
			diff = fmt.Sprintf("Summary: %s\n\nFull diff:\n%s", summary, diff)
		} else {
			fmt.Fprintf(os.Stderr, "[DEBUG] Summarization returned empty summary for %s\n", filePath)
		}
	} else {
		if diff == "" {
			fmt.Fprintf(os.Stderr, "[DEBUG] No diff available for %s (track_diffs: %v, action: %s)\n", filePath, m.config.Filesystem.TrackDiffs, action)
		} else if m.summarizer == nil {
			fmt.Fprintf(os.Stderr, "[DEBUG] Summarizer is nil for %s\n", filePath)
		}
	}

	change := logger.FileChange{
		Path:      filePath,
		Action:    action,
		Timestamp: time.Now(),
		Diff:      diff,
	}

	if err := m.logger.LogFileChange(change); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to log file change: %v\n", err)
	}
}

// getFileDiff gets the diff for a file using git if available, otherwise returns empty
func (m *FileSystemMonitor) getFileDiff(filePath string) (string, error) {
	// Try to find git repository root
	gitRoot := m.findGitRoot(filePath)
	if gitRoot == "" {
		return "", fmt.Errorf("not in a git repository")
	}

	// Get relative path from git root
	relPath, err := filepath.Rel(gitRoot, filePath)
	if err != nil {
		return "", err
	}

	// Check git status first
	cmd := exec.Command("git", "status", "--porcelain", relPath)
	cmd.Dir = gitRoot
	statusOutput, err := cmd.Output()
	status := strings.TrimSpace(string(statusOutput))
	
	// Try different diff strategies based on git status
	var diff string
	
	if strings.HasPrefix(status, "??") {
		// New untracked file, show file content as diff
		content, err := os.ReadFile(filePath)
		if err == nil {
			lines := strings.Split(string(content), "\n")
			diffLines := make([]string, 0, len(lines))
			for _, line := range lines {
				diffLines = append(diffLines, "+"+line)
			}
			diff = strings.Join(diffLines, "\n")
			return diff, nil
		}
		return "", fmt.Errorf("failed to read new file")
	}
	
	// Try git diff (staged changes)
	cmd = exec.Command("git", "diff", "--no-color", "--cached", relPath)
	cmd.Dir = gitRoot
	output, err := cmd.Output()
	if err == nil {
		diff = strings.TrimSpace(string(output))
		if diff != "" {
			return diff, nil
		}
	}
	
	// Try git diff HEAD (unstaged changes vs HEAD)
	cmd = exec.Command("git", "diff", "--no-color", "HEAD", relPath)
	cmd.Dir = gitRoot
	output, err = cmd.Output()
	if err == nil {
		diff = strings.TrimSpace(string(output))
		if diff != "" {
			return diff, nil
		}
	}
	
	// Try git diff (working directory changes)
	cmd = exec.Command("git", "diff", "--no-color", relPath)
	cmd.Dir = gitRoot
	output, err = cmd.Output()
	if err == nil {
		diff = strings.TrimSpace(string(output))
		if diff != "" {
			return diff, nil
		}
	}
	
	// If file exists in git, try to show what changed in the last commit
	cmd = exec.Command("git", "log", "-1", "--pretty=format:", "--name-only", relPath)
	cmd.Dir = gitRoot
	output, err = cmd.Output()
	if err == nil && strings.TrimSpace(string(output)) != "" {
		// File was in last commit, show diff from that commit
		cmd = exec.Command("git", "diff", "--no-color", "HEAD~1", "HEAD", "--", relPath)
		cmd.Dir = gitRoot
		output, err = cmd.Output()
		if err == nil {
			diff = strings.TrimSpace(string(output))
			if diff != "" {
				return diff, nil
			}
		}
	}
	
	// No diff available
	return "", fmt.Errorf("no diff available")
}

// findGitRoot finds the git repository root for a given file path
func (m *FileSystemMonitor) findGitRoot(filePath string) string {
	dir := filepath.Dir(filePath)
	for {
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}
		dir = parent
	}
	return ""
}

// Stop stops the monitor
func (m *FileSystemMonitor) Stop() error {
	close(m.stopChan)

	// Stop all debounce timers
	m.debouncerMu.Lock()
	for _, timer := range m.debouncer {
		timer.Stop()
	}
	m.debouncer = make(map[string]*time.Timer)
	m.debouncerMu.Unlock()

	// Close watcher
	if err := m.watcher.Close(); err != nil {
		return err
	}

	// Wait for processing to finish
	<-m.doneChan

	return nil
}
