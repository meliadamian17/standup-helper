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
)

// GitMonitor monitors git commits in configured directories
type GitMonitor struct {
	config      *config.Config
	logger      *logger.Logger
	repos       map[string]*gitRepoState
	reposMu     sync.Mutex
	stopChan    chan struct{}
	doneChan    chan struct{}
	ticker      *time.Ticker
}

// gitRepoState tracks the state of a git repository
type gitRepoState struct {
	path        string
	lastCommit  string
	lastChecked time.Time
}

// NewGitMonitor creates a new git monitor
func NewGitMonitor(cfg *config.Config, log *logger.Logger) *GitMonitor {
	return &GitMonitor{
		config:   cfg,
		logger:   log,
		repos:    make(map[string]*gitRepoState),
		stopChan: make(chan struct{}),
		doneChan: make(chan struct{}),
		ticker:   time.NewTicker(cfg.Git.PollInterval),
	}
}

// Start begins monitoring git repositories
func (m *GitMonitor) Start() error {
	// Discover git repositories in configured directories
	if err := m.discoverRepos(); err != nil {
		return fmt.Errorf("failed to discover repositories: %w", err)
	}

	// Start polling goroutine
	go m.pollRepos()

	return nil
}

// discoverRepos finds all git repositories in configured directories
func (m *GitMonitor) discoverRepos() error {
	m.reposMu.Lock()
	defer m.reposMu.Unlock()

	for _, dir := range m.config.Directories {
		if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Continue on errors
			}

			// Check if this is a .git directory
			if info.IsDir() && info.Name() == ".git" {
				repoPath := filepath.Dir(path)
				if _, exists := m.repos[repoPath]; !exists {
					// Get current HEAD commit
					lastCommit, _ := m.getCurrentCommit(repoPath)
					m.repos[repoPath] = &gitRepoState{
						path:        repoPath,
						lastCommit:  lastCommit,
						lastChecked: time.Now(),
					}
				}
				return filepath.SkipDir // Don't walk into .git
			}

			// Skip excluded directories
			if info.IsDir() && m.config.ShouldExclude(path) {
				return filepath.SkipDir
			}

			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

// pollRepos periodically checks for new commits
func (m *GitMonitor) pollRepos() {
	defer close(m.doneChan)

	// Initial check
	m.checkAllRepos()

	for {
		select {
		case <-m.ticker.C:
			m.checkAllRepos()

		case <-m.stopChan:
			return
		}
	}
}

// checkAllRepos checks all repositories for new commits
func (m *GitMonitor) checkAllRepos() {
	m.reposMu.Lock()
	repos := make([]*gitRepoState, 0, len(m.repos))
	for _, repo := range m.repos {
		repos = append(repos, repo)
	}
	m.reposMu.Unlock()

	for _, repo := range repos {
		m.checkRepo(repo)
	}
}

// checkRepo checks a single repository for new commits
func (m *GitMonitor) checkRepo(repo *gitRepoState) {
	currentCommit, err := m.getCurrentCommit(repo.path)
	if err != nil {
		return // Skip if we can't get commit
	}

	// If commit changed, log all new commits
	if currentCommit != repo.lastCommit && repo.lastCommit != "" {
		if err := m.logNewCommits(repo.path, repo.lastCommit, currentCommit); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to log commits for %s: %v\n", repo.path, err)
		}
	}

	// Update state
	m.reposMu.Lock()
	repo.lastCommit = currentCommit
	repo.lastChecked = time.Now()
	m.reposMu.Unlock()
}

// logNewCommits logs all commits between oldCommit and newCommit
func (m *GitMonitor) logNewCommits(repoPath, oldCommit, newCommit string) error {
	// Get commit range
	cmd := exec.Command("git", "log", "--pretty=format:%H|%an|%ae|%ad|%s", "--date=iso", fmt.Sprintf("%s..%s", oldCommit, newCommit))
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get commit log: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}

		hash := parts[0]
		author := parts[1]
		// email := parts[2] // Not used currently
		dateStr := parts[3]
		message := parts[4]

		// Parse timestamp - git log --date=iso can produce various formats
		var timestamp time.Time
		var err error
		
		// Try common git date formats
		formats := []string{
			"2006-01-02 15:04:05 -0700",
			"2006-01-02 15:04:05 -07:00",
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
		}
		
		for _, format := range formats {
			timestamp, err = time.Parse(format, dateStr)
			if err == nil {
				break
			}
		}
		
		if err != nil {
			// Fallback to current time if parsing fails
			timestamp = time.Now()
		}

		// Get changed files
		files, err := m.getCommitFiles(repoPath, hash)
		if err != nil {
			files = []string{}
		}

		commit := logger.GitCommit{
			Hash:      hash,
			Author:    author,
			Message:   message,
			Timestamp: timestamp,
			Files:     files,
		}

		if err := m.logger.LogGitCommit(commit); err != nil {
			return fmt.Errorf("failed to log commit: %w", err)
		}
	}

	return nil
}

// getCurrentCommit gets the current HEAD commit hash
func (m *GitMonitor) getCurrentCommit(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// getCommitFiles gets the list of files changed in a commit
func (m *GitMonitor) getCommitFiles(repoPath, commitHash string) ([]string, error) {
	cmd := exec.Command("git", "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	files := strings.Split(strings.TrimSpace(string(output)), "\n")
	// Filter out empty strings
	result := make([]string, 0, len(files))
	for _, file := range files {
		if file != "" {
			result = append(result, file)
		}
	}

	return result, nil
}

// Stop stops the monitor
func (m *GitMonitor) Stop() error {
	m.ticker.Stop()
	close(m.stopChan)
	<-m.doneChan
	return nil
}
