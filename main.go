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

	// Create monitors
	fsMonitor, err := monitor.NewFileSystemMonitor(cfg, log)
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
