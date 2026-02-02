# Standup Helper

A background macOS service that monitors file changes and git commits in configured directories, logging everything to daily markdown files for standup preparation.

## Features

- **File System Monitoring**: Tracks file creates, modifications, and deletes using efficient file watching
- **Git Commit Tracking**: Monitors git repositories and logs commits with messages, authors, and changed files
- **Diff Capture**: Captures file diffs for changed files (when available)
- **AI-Powered Summarization**: Optionally summarizes code diffs using local LLM (Ollama) for standup-friendly summaries
- **Daily Logs**: Automatically rotates logs daily in markdown format for easy reading
- **Zero Configuration**: Runs entirely in the background with minimal setup
- **Low Overhead**: Debounced events and efficient polling minimize performance impact

## Installation

### Prerequisites

1. **Go 1.19 or later** (for building)
2. **Git** (for commit tracking and diff generation)
3. **Ollama** (optional, for AI-powered diff summarization)

### Install Ollama (Optional, for Summarization)

If you want to use AI-powered diff summarization:

1. **Install Ollama:**
   ```bash
   # macOS
   brew install ollama
   
   # Or download from https://ollama.ai
   ```

2. **Start Ollama:**
   ```bash
   ollama serve
   ```
   
   Note: The service will attempt to start Ollama automatically if it's not running, but you may need to start it manually the first time.

3. **Pull a model:**
   ```bash
   # Recommended lightweight models for summarization:
   ollama pull llama3.2        # ~2GB, fast and efficient
   ollama pull phi3            # ~2.3GB, very fast
   ollama pull gemma2:2b       # ~1.6GB, extremely fast
   ```

### Install Standup Helper

1. Build the binary:
```bash
go build -o standup-helper
```

2. Install the service:
```bash
./standup-helper -install
```

This will:
- Create `~/.standup-helper/` directory
- Generate a default configuration file at `~/.standup-helper/config.yaml`
- Install the service as a LaunchAgent
- Start the service automatically

The service will run in the background and start automatically on login.

## Configuration

Edit `~/.standup-helper/config.yaml` to configure directories to monitor:

```yaml
directories:
  - /Users/meliadamian17/repos/project1
  - /Users/meliadamian17/repos/project2

exclusions:
  - node_modules
  - .git
  - dist
  - build
  - .DS_Store
  - "*.log"

git:
  poll_interval: 30s
  track_commits: true

filesystem:
  debounce: 2s
  track_diffs: true

summarizer:
  enabled: false
  # base_url is optional - will be auto-detected from OLLAMA_HOST or common ports
  model: llama3.2
```

### Configuration Options

- **directories**: List of absolute paths (or `~/path`) to directories to monitor
- **exclusions**: Patterns for files/directories to exclude from monitoring
- **git.poll_interval**: How often to check for new git commits (default: 30s)
- **git.track_commits**: Whether to track git commits (default: true)
- **filesystem.debounce**: Delay before processing file changes to batch rapid edits (default: 2s)
- **filesystem.track_diffs**: Whether to capture file diffs (default: true)
- **summarizer.enabled**: Enable AI-powered diff summarization (default: false)
- **summarizer.base_url**: Ollama API URL (optional, auto-detected if not set)
  - Auto-detection checks `OLLAMA_HOST` environment variable first
  - Then tries common ports (11434, 11435, 11436)
  - Falls back to `http://localhost:11434` if not found
- **summarizer.model**: Ollama model to use for summarization (default: llama3.2)

## Log Files

Daily log files are written to `~/.standup-helper/logs/standup-YYYY-MM-DD.md`.

Example log format:

```markdown
# Standup Log - 2026-01-15

## File Changes
- `src/main.go` (modified at 14:23:15)
  ```diff
  Summary: Added new feature function to handle user authentication. Implemented error handling and validation logic.
  
  Full diff:
  + func newFeature() {
  +     // implementation
  + }
  ```

## Git Commits
- Commit: `abc123` by John Doe at 14:30:00
  Message: "Add new feature"
  Files: src/main.go, src/utils.go

## Summary
- 5 file(s) modified
- 2 commit(s)
- 3 file(s) created
```

### Summarization

When summarization is enabled, diffs are automatically summarized using a local LLM. The summary appears above the full diff, providing a concise, standup-friendly explanation of what changed. If summarization fails or is disabled, only the full diff is shown.

## Manual Operation

To run the service manually (for testing):

```bash
./standup-helper
```

The service will monitor configured directories and write logs. Press Ctrl+C to stop.

## Service Management

### Check if service is running:
```bash
launchctl list | grep standuphelper
```

### Stop the service:
```bash
launchctl unload ~/Library/LaunchAgents/com.standuphelper.plist
```

### Start the service:
```bash
launchctl load ~/Library/LaunchAgents/com.standuphelper.plist
```

### View service logs:
```bash
tail -f ~/.standup-helper/logs/service.log
```

## Uninstallation

To remove the service:

1. Stop and unload the service:
```bash
launchctl unload ~/Library/LaunchAgents/com.standuphelper.plist
rm ~/Library/LaunchAgents/com.standuphelper.plist
```

2. Optionally remove configuration and logs:
```bash
rm -rf ~/.standup-helper
```

## Requirements

- macOS (uses LaunchAgent for background service)
- Go 1.19 or later (for building)
- Git (for commit tracking and diff generation)
- Ollama (optional, for AI-powered diff summarization)

## Performance

The service is designed for minimal performance overhead:

- File system events are debounced (default 2 seconds) to batch rapid changes
- Git polling is configurable (default 30 seconds)
- Excluded directories are not monitored
- Binary files and common build artifacts are skipped

## Troubleshooting

### Service not starting

Check the service log:
```bash
cat ~/.standup-helper/logs/service.log
```

Common issues:
- No directories configured in `config.yaml`
- Invalid directory paths (must be absolute)
- Permission issues accessing directories

### No logs being generated

- Verify directories are configured correctly
- Check that directories exist and are accessible
- Ensure the service is running: `launchctl list | grep standuphelper`

### Missing git commits

- Ensure `track_commits: true` in config
- Verify git repositories exist in monitored directories
- Check git is installed and accessible

### Summarization not working

- Ensure Ollama is installed: `brew install ollama` or download from https://ollama.ai
- Start Ollama: `ollama serve` (or let the service start it automatically)
- Pull the model: `ollama pull <model-name>` (e.g., `ollama pull llama3.2`)
- Verify the model name in config matches the pulled model
- Check service logs for summarization errors: `tail -f ~/.standup-helper/logs/service.log`
- If summarization fails, the service will continue with full diffs (no summarization)

## License

MIT
