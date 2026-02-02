package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"standup-helper/config"
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.standuphelper</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinaryPath}}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

// Install installs the service as a LaunchAgent
func Install() error {
	// Get the binary path
	binaryPath, err := getBinaryPath()
	if err != nil {
		return fmt.Errorf("failed to get binary path: %w", err)
	}

	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create .standup-helper directory
	appDir := filepath.Join(homeDir, ".standup-helper")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}

	// Create logs directory
	logDir := filepath.Join(appDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Create default config if it doesn't exist
	configPath := filepath.Join(appDir, "config.yaml")
	if err := config.CreateDefaultConfig(configPath); err != nil {
		return fmt.Errorf("failed to create default config: %w", err)
	}

	// Generate plist file
	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.standuphelper.plist")
	if err := generatePlist(plistPath, binaryPath, logDir); err != nil {
		return fmt.Errorf("failed to generate plist: %w", err)
	}

	// Load the service
	if err := loadService(plistPath); err != nil {
		return fmt.Errorf("failed to load service: %w", err)
	}

	return nil
}

// getBinaryPath gets the path to the current binary
func getBinaryPath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Resolve symlinks to get absolute path
	absPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return execPath, nil // Return original if symlink resolution fails
	}

	return absPath, nil
}

// generatePlist generates the LaunchAgent plist file
func generatePlist(plistPath, binaryPath, logPath string) error {
	// Create LaunchAgents directory if it doesn't exist
	plistDir := filepath.Dir(plistPath)
	if err := os.MkdirAll(plistDir, 0755); err != nil {
		return fmt.Errorf("failed to create LaunchAgents directory: %w", err)
	}

	// Parse template
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse plist template: %w", err)
	}

	// Create file
	file, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("failed to create plist file: %w", err)
	}
	defer file.Close()

	// Execute template
	data := struct {
		BinaryPath string
		LogPath    string
	}{
		BinaryPath: binaryPath,
		LogPath:    filepath.Join(logPath, "service.log"),
	}

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

// loadService loads the LaunchAgent service
func loadService(plistPath string) error {
	// Unload first if it exists
	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run() // Ignore error if not loaded

	// Load the service
	cmd = exec.Command("launchctl", "load", plistPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to load service: %w", err)
	}

	return nil
}

// Uninstall removes the service
func Uninstall() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", "com.standuphelper.plist")

	// Unload the service
	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run() // Ignore error if not loaded

	// Remove plist file
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist file: %w", err)
	}

	return nil
}
