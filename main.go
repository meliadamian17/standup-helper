package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"standup-helper/config"
	"standup-helper/install"
	"standup-helper/logger"
	"standup-helper/monitor"
	"standup-helper/summarizer"
)

func main() {
	installFlag := flag.Bool("install", false, "Install the service")
	configPath := flag.String("config", "", "Path to configuration file (default: ~/.standup-helper/config.yaml)")
	flag.Parse()

	// Handle installation
	if *installFlag {
		if err := install.Install(); err != nil {
			fmt.Fprintf(os.Stderr, "Installation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Service installed successfully!")
		fmt.Println("The service will start automatically on login.")
		return
	}

	// Load configuration
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = config.GetConfigPath()
	}

	// Create default config if it doesn't exist
	if err := config.CreateDefaultConfig(cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create default config: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please edit %s and add directories to monitor.\n", cfgPath)
		os.Exit(1)
	}

	// Validate that directories are configured
	if len(cfg.Directories) == 0 {
		fmt.Fprintf(os.Stderr, "No directories configured. Please edit %s and add directories to monitor.\n", cfgPath)
		os.Exit(1)
	}

	// Set up logger
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	logDir := filepath.Join(homeDir, ".standup-helper", "logs")

	log, err := logger.NewLogger(logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Close()

	// Create summarizer (shared by monitor and optional end-of-day job)
	var summ *summarizer.Summarizer
	if cfg.Summarizer.Enabled {
		summ = summarizer.NewSummarizer(
			cfg.Summarizer.BaseURL,
			cfg.Summarizer.Model,
			true,
			cfg.Summarizer.KeepAlive,
		)
		fmt.Println("Initializing summarization with Ollama...")
		fmt.Printf("Detected Ollama at: %s\n", summ.GetBaseURL())
		fmt.Printf("Ensuring Ollama is running and model '%s' is loaded...\n", cfg.Summarizer.Model)
		if err := summ.EnsureModelLoaded(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to initialize summarization: %v\n", err)
			fmt.Fprintf(os.Stderr, "Make sure Ollama is installed and the model is available:\n")
			fmt.Fprintf(os.Stderr, "  1. Install Ollama: https://ollama.ai\n")
			fmt.Fprintf(os.Stderr, "  2. Pull the model: ollama pull %s\n", cfg.Summarizer.Model)
			fmt.Fprintf(os.Stderr, "  3. Start Ollama: ollama serve\n")
			fmt.Fprintf(os.Stderr, "Continuing without summarization...\n")
			cfg.Summarizer.Enabled = false
			summ = nil
		} else {
			fmt.Printf("✓ Model '%s' is ready for summarization.\n", cfg.Summarizer.Model)
		}
	}

	// Create monitors (pass shared summarizer so keep_alive and model are consistent)
	fsMonitor, err := monitor.NewFileSystemMonitor(cfg, log, summ)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create file system monitor: %v\n", err)
		os.Exit(1)
	}
	defer fsMonitor.Stop()

	gitMonitor := monitor.NewGitMonitor(cfg, log)
	defer gitMonitor.Stop()

	// Start monitors
	if err := fsMonitor.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start file system monitor: %v\n", err)
		os.Exit(1)
	}

	if cfg.Git.TrackCommits {
		if err := gitMonitor.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start git monitor: %v\n", err)
			os.Exit(1)
		}
	}

	// Set up periodic flush
	flushTicker := time.NewTicker(30 * time.Second)
	defer flushTicker.Stop()

	go func() {
		for range flushTicker.C {
			if err := log.Flush(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to flush logs: %v\n", err)
			}
		}
	}()

	// End-of-day AI summary: run once per day at configured time when summarizer and feature are enabled
	if cfg.Summarizer.Enabled && cfg.Summarizer.EndOfDaySummary && summ != nil {
		endOfDayTime, err := time.Parse("15:04", cfg.Summarizer.EndOfDayTime)
		if err != nil {
			endOfDayTime, _ = time.Parse("15:04", "18:00")
		}
		go runEndOfDaySummary(log, summ, endOfDayTime)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	fmt.Println("Standup Helper service started. Monitoring directories:")
	for _, dir := range cfg.Directories {
		fmt.Printf("  - %s\n", dir)
	}
	fmt.Printf("Logs will be written to: %s\n", logDir)
	fmt.Println("Press Ctrl+C to stop...")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down...")

	// Final flush
	if err := log.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to flush logs on shutdown: %v\n", err)
	}
}

// runEndOfDaySummary checks once per minute whether it is past endOfDayTime today; if so and the AI day summary
// has not been written yet, it flushes the log, reads the day's content, calls the summarizer, and writes the section.
func runEndOfDaySummary(log *logger.Logger, summ *summarizer.Summarizer, endOfDayTime time.Time) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		todayEOD := time.Date(now.Year(), now.Month(), now.Day(), endOfDayTime.Hour(), endOfDayTime.Minute(), 0, 0, now.Location())
		if now.Before(todayEOD) {
			continue
		}
		if log.HasWrittenAIDaySummary() {
			continue
		}
		if err := log.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to flush logs before day summary: %v\n", err)
			continue
		}
		content, err := log.GetCurrentDayLogContent()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read day log for AI summary: %v\n", err)
			continue
		}
		summary, err := summ.SummarizeDay(content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate AI day summary: %v\n", err)
			continue
		}
		if summary == "" {
			continue
		}
		if err := log.WriteDaySummary(summary); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write AI day summary: %v\n", err)
			continue
		}
		fmt.Fprintf(os.Stderr, "AI day summary written.\n")
	}
}
