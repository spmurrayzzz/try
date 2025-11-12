package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalculateScore(t *testing.T) {
	ts := &TrySelector{}

	// Test with date-prefixed directory
	score := ts.calculateScore("2025-01-15-test", "test", time.Now(), time.Now())
	if score <= 2.0 {
		t.Errorf("Expected score > 2.0 for date-prefixed directory, got %f", score)
	}

	// Test fuzzy matching
	score = ts.calculateScore("connection-pool", "conpool", time.Now(), time.Now())
	if score <= 0 {
		t.Errorf("Expected positive score for fuzzy match, got %f", score)
	}

	// Test non-match
	score = ts.calculateScore("redis-test", "postgres", time.Now(), time.Now())
	if score != 0 {
		t.Errorf("Expected score 0 for non-match, got %f", score)
	}
}

func TestParseGitURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected map[string]string
	}{
		{
			"https://github.com/user/repo.git",
			map[string]string{"user": "user", "repo": "repo", "host": "github.com"},
		},
		{
			"git@github.com:user/repo",
			map[string]string{"user": "user", "repo": "repo", "host": "github.com"},
		},
		{
			"https://gitlab.com/user/repo",
			map[string]string{"user": "user", "repo": "repo", "host": "gitlab.com"},
		},
	}

	for _, test := range tests {
		result := parseGitURI(test.uri)
		if result == nil {
			t.Errorf("Failed to parse URI: %s", test.uri)
			continue
		}
		if result["user"] != test.expected["user"] ||
			result["repo"] != test.expected["repo"] ||
			result["host"] != test.expected["host"] {
			t.Errorf("For URI %s, expected %+v, got %+v", test.uri, test.expected, result)
		}
	}
}

func TestGenerateCloneDirectoryName(t *testing.T) {
	// Test with custom name
	name := generateCloneDirectoryName("https://github.com/user/repo.git", "custom")
	if name != "custom" {
		t.Errorf("Expected 'custom', got '%s'", name)
	}

	// Test auto-generated name
	name = generateCloneDirectoryName("https://github.com/user/repo.git", "")
	expectedPrefix := time.Now().Format("2006-01-02") + "-user-repo"
	if name != expectedPrefix {
		t.Errorf("Expected '%s', got '%s'", expectedPrefix, name)
	}
}

func TestExpandTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"{h1}Hello{reset}", "\x1b[1;33mHello\x1b[0m\x1b[39m\x1b[49m"},
		{"{highlight}World{reset_fg}", "\x1b[1;33mWorld\x1b[39m"},
		{"Plain text", "Plain text"},
	}

	for _, test := range tests {
		result := expandTokens(test.input)
		if result != test.expected {
			t.Errorf("For input '%s', expected '%s', got '%s'", test.input, test.expected, result)
		}
	}
}

func TestUniqueDirName(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "try-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Test when directory doesn't exist
	name := uniqueDirName(tmpDir, "test-dir")
	if name != "test-dir" {
		t.Errorf("Expected 'test-dir', got '%s'", name)
	}

	// Create the directory
	os.MkdirAll(filepath.Join(tmpDir, "test-dir"), 0755)

	// Test when directory exists
	name = uniqueDirName(tmpDir, "test-dir")
	if name != "test-dir-2" {
		t.Errorf("Expected 'test-dir-2', got '%s'", name)
	}
}

// func main() {
// 	fmt.Println("Running tests...")

// 	// Run a simple test
// 	ts := &TrySelector{}
// 	score := ts.calculateScore("2025-01-15-redis-test", "redis", time.Now(), time.Now())
// 	fmt.Printf("Score for '2025-01-15-redis-test' with query 'redis': %.2f\n", score)

// 	// Test git URI parsing
// 	parsed := parseGitURI("https://github.com/user/repo.git")
// 	fmt.Printf("Parsed git URI: %+v\n", parsed)

// 	// Test directory name generation
// 	name := generateCloneDirectoryName("https://github.com/user/repo.git", "")
// 	fmt.Printf("Generated directory name: %s\n", name)

// 	fmt.Println("\nAll basic tests passed!")
// }
