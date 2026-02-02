package summarizer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Summarizer handles diff summarization using Ollama
type Summarizer struct {
	baseURL    string
	model      string
	enabled    bool
	httpClient *http.Client
}

// GetBaseURL returns the detected/base URL for Ollama
func (s *Summarizer) GetBaseURL() string {
	return s.baseURL
}

// OllamaRequest represents the request to Ollama API
type OllamaRequest struct {
	Model    string    `json:"model"`
	Prompt   string    `json:"prompt"`
	Stream   bool      `json:"stream"`
	Options  Options   `json:"options"`
}

// Options for Ollama generation
type Options struct {
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"num_predict,omitempty"`
}

// OllamaResponse represents the response from Ollama API
type OllamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// NewSummarizer creates a new summarizer instance
func NewSummarizer(baseURL, model string, enabled bool) *Summarizer {
	// Auto-detect Ollama URL if not provided or empty
	if baseURL == "" {
		baseURL = detectOllamaURL()
	}

	return &Summarizer{
		baseURL: baseURL,
		model:   model,
		enabled: enabled,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// detectOllamaURL automatically detects the Ollama service URL
func detectOllamaURL() string {
	// Check OLLAMA_HOST environment variable first
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		// OLLAMA_HOST can be in format "host:port" or just "host"
		if !strings.Contains(host, ":") {
			host = host + ":11434"
		}
		if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
			host = "http://" + host
		}
		return host
	}

	// Try common ports on localhost
	commonPorts := []int{11434, 11435, 11436}
	for _, port := range commonPorts {
		url := fmt.Sprintf("http://localhost:%d", port)
		if checkOllamaURL(url) {
			return url
		}
	}

	// Fall back to default
	return "http://localhost:11434"
}

// checkOllamaURL checks if Ollama is available at the given URL
func checkOllamaURL(url string) bool {
	client := &http.Client{
		Timeout: 2 * time.Second,
	}
	
	// Try to connect to the API
	resp, err := client.Get(url + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	
	return resp.StatusCode == http.StatusOK
}

// SummarizeDiff summarizes a git diff using the local LLM
func (s *Summarizer) SummarizeDiff(diff string, filePath string) (string, error) {
	if !s.enabled {
		return diff, nil // Return original diff if summarization is disabled
	}

	if diff == "" {
		return "", nil
	}

	// Truncate very long diffs to avoid token limits
	maxDiffLength := 8000
	if len(diff) > maxDiffLength {
		diff = diff[:maxDiffLength] + "\n... (truncated)"
	}

	// Create prompt for summarization
	prompt := s.createPrompt(diff, filePath)

	// Call Ollama API
	summary, err := s.callOllama(prompt)
	if err != nil {
		// If summarization fails, return original diff
		return diff, fmt.Errorf("summarization failed: %w", err)
	}

	return summary, nil
}

// createPrompt creates a prompt for the LLM to summarize the diff
func (s *Summarizer) createPrompt(diff, filePath string) string {
	return fmt.Sprintf(`Summarize the following code changes in a concise, standup-friendly format. Focus on what was changed and why it matters, not the exact code.

File: %s

Diff:
%s

Summary (2-3 sentences max):`, filePath, diff)
}

// callOllama calls the Ollama API to generate a summary
func (s *Summarizer) callOllama(prompt string) (string, error) {
	url := fmt.Sprintf("%s/api/generate", s.baseURL)

	req := OllamaRequest{
		Model:  s.model,
		Prompt: prompt,
		Stream: false,
		Options: Options{
			Temperature: 0.3, // Lower temperature for more focused summaries
			MaxTokens:   200, // Limit response length
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to call Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var ollamaResp OllamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	summary := strings.TrimSpace(ollamaResp.Response)
	if summary == "" {
		return "", fmt.Errorf("empty response from Ollama")
	}

	return summary, nil
}

// IsAvailable checks if Ollama is available
func (s *Summarizer) IsAvailable() bool {
	if !s.enabled {
		return false
	}

	// Try a simple health check
	url := fmt.Sprintf("%s/api/tags", s.baseURL)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// ModelExists checks if the specified model is available in Ollama
func (s *Summarizer) ModelExists() (bool, error) {
	if !s.enabled {
		return false, nil
	}

	url := fmt.Sprintf("%s/api/tags", s.baseURL)
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return false, fmt.Errorf("failed to check models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Ollama API returned status %d", resp.StatusCode)
	}

	var tagsResponse struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tagsResponse); err != nil {
		return false, fmt.Errorf("failed to decode models list: %w", err)
	}

	for _, model := range tagsResponse.Models {
		if model.Name == s.model || strings.HasPrefix(model.Name, s.model+":") {
			return true, nil
		}
	}

	return false, nil
}

// PullModel attempts to pull the model from Ollama
func (s *Summarizer) PullModel() error {
	if !s.enabled {
		return nil
	}

	// Find ollama executable
	ollamaPath, err := s.findOllamaPath()
	if err != nil {
		return fmt.Errorf("failed to find Ollama: %w", err)
	}

	fmt.Printf("Pulling model '%s' (this may take a few minutes)...\n", s.model)
	
	// Run ollama pull
	cmd := exec.Command(ollamaPath, "pull", s.model)
	cmd.Env = os.Environ()
	
	// Stream output so user can see progress
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}

	fmt.Printf("Model '%s' pulled successfully.\n", s.model)
	return nil
}

// findOllamaPath finds the ollama executable in common installation locations
func (s *Summarizer) findOllamaPath() (string, error) {
	// Try common installation paths
	commonPaths := []string{
		"/opt/homebrew/bin/ollama",      // Homebrew on Apple Silicon
		"/usr/local/bin/ollama",         // Homebrew on Intel
		"/usr/bin/ollama",               // System installation
		filepath.Join(os.Getenv("HOME"), ".local/bin/ollama"), // User installation
	}

	// Also try to find it in PATH
	if path, err := exec.LookPath("ollama"); err == nil {
		return path, nil
	}

	// Check common paths
	for _, path := range commonPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("ollama not found in PATH or common locations")
}

// StartOllama attempts to start Ollama if it's not running
func (s *Summarizer) StartOllama() error {
	if !s.enabled {
		return nil
	}

	// Check if already running
	if s.IsAvailable() {
		return nil
	}

	// Find ollama executable
	ollamaPath, err := s.findOllamaPath()
	if err != nil {
		return fmt.Errorf("failed to find Ollama: %w", err)
	}

	// Try to start Ollama in the background
	cmd := exec.Command(ollamaPath, "serve")
	// Set environment to include PATH for any dependencies
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Ollama: %w (make sure Ollama is installed)", err)
	}

	// Wait a bit for Ollama to start
	time.Sleep(2 * time.Second)

	// Check if it's now available
	if !s.IsAvailable() {
		return fmt.Errorf("Ollama started but not responding at %s", s.baseURL)
	}

	return nil
}

// EnsureModelLoaded ensures the model is loaded by making a request to pull/load it
func (s *Summarizer) EnsureModelLoaded() error {
	if !s.enabled {
		return nil // Not an error if summarization is disabled
	}

	// First check if Ollama is running, try to start it if not
	if !s.IsAvailable() {
		fmt.Printf("Ollama not running, attempting to start...\n")
		if err := s.StartOllama(); err != nil {
			return fmt.Errorf("Ollama is not available at %s: %w", s.baseURL, err)
		}
	}

	// Check if model exists
	exists, err := s.ModelExists()
	if err != nil {
		return fmt.Errorf("failed to check if model exists: %w", err)
	}

	if !exists {
		fmt.Printf("Model '%s' not found. Attempting to pull...\n", s.model)
		if err := s.PullModel(); err != nil {
			return fmt.Errorf("model '%s' not available and failed to pull: %w\nPlease run: ollama pull %s", s.model, err, s.model)
		}
	}

	// Try to load the model by making a simple generate request
	// This will trigger Ollama to load the model if it's not already loaded
	url := fmt.Sprintf("%s/api/generate", s.baseURL)

	req := OllamaRequest{
		Model:  s.model,
		Prompt: "test", // Minimal prompt just to trigger model load
		Stream: false,
		Options: Options{
			Temperature: 0.1,
			MaxTokens:   5, // Just a few tokens to minimize overhead
		},
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Use a longer timeout for initial model load
	client := &http.Client{
		Timeout: 120 * time.Second, // Model loading can take time
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to load model: %w (make sure Ollama is running and model '%s' is available)", err, s.model)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to load model: status %d - %s", resp.StatusCode, string(body))
	}

	return nil
}
