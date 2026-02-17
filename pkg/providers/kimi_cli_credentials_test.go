package providers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadKimiCliCredentials(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Create credentials directory and file
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	
	// Test with valid credentials
	validCreds := `{
		"access_token": "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_at": 9999999999.0,
		"scope": "kimi-code",
		"token_type": "Bearer"
	}`
	
	if err := os.WriteFile(credFile, []byte(validCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	accessToken, refreshToken, expiresAt, err := ReadKimiCliCredentials()
	if err != nil {
		t.Errorf("ReadKimiCliCredentials() error: %v", err)
	}

	if accessToken != "test-access-token" {
		t.Errorf("accessToken = %q, want %q", accessToken, "test-access-token")
	}

	if refreshToken != "test-refresh-token" {
		t.Errorf("refreshToken = %q, want %q", refreshToken, "test-refresh-token")
	}

	expectedExpiry := time.Unix(9999999999, 0)
	if !expiresAt.Equal(expectedExpiry) {
		t.Errorf("expiresAt = %v, want %v", expiresAt, expectedExpiry)
	}
}

func TestReadKimiCliCredentials_MissingFile(t *testing.T) {
	// Create a temporary directory without credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	_, _, _, err = ReadKimiCliCredentials()
	if err == nil {
		t.Error("Expected error for missing credentials file")
	}
}

func TestReadKimiCliCredentials_InvalidJSON(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Create credentials directory and file with invalid JSON
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	if err := os.WriteFile(credFile, []byte("invalid json"), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	_, _, _, err = ReadKimiCliCredentials()
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestReadKimiCliCredentials_EmptyAccessToken(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Create credentials directory and file with empty access token
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	emptyCreds := `{
		"access_token": "",
		"refresh_token": "test-refresh",
		"expires_at": 9999999999.0
	}`
	
	if err := os.WriteFile(credFile, []byte(emptyCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	_, _, _, err = ReadKimiCliCredentials()
	if err == nil {
		t.Error("Expected error for empty access token")
	}
}

func TestCreateKimiCliTokenSource(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Create credentials directory and file
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	
	// Test with valid credentials (far future expiry)
	validCreds := `{
		"access_token": "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_at": 9999999999.0,
		"scope": "kimi-code",
		"token_type": "Bearer"
	}`
	
	if err := os.WriteFile(credFile, []byte(validCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	tokenSource := CreateKimiCliTokenSource()
	token, accountID, err := tokenSource()
	
	if err != nil {
		t.Errorf("TokenSource() error: %v", err)
	}
	
	if token != "test-access-token" {
		t.Errorf("token = %q, want %q", token, "test-access-token")
	}
	
	if accountID != "" {
		t.Errorf("accountID = %q, want empty string", accountID)
	}
}

func TestCreateKimiCliTokenSource_Expired(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Create credentials directory and file
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	
	// Test with expired credentials (past expiry)
	expiredCreds := `{
		"access_token": "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_at": 1000000000.0,
		"scope": "kimi-code",
		"token_type": "Bearer"
	}`
	
	if err := os.WriteFile(credFile, []byte(expiredCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	tokenSource := CreateKimiCliTokenSource()
	_, _, err = tokenSource()
	
	if err == nil {
		t.Error("Expected error for expired credentials")
	}
}

func TestIsKimiCliAuthenticated(t *testing.T) {
	// Create a temporary directory for test credentials
	tmpDir, err := os.MkdirTemp("", "kimi-credentials-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Set KIMI_HOME to our temp directory
	origKimiHome := os.Getenv("KIMI_HOME")
	os.Setenv("KIMI_HOME", tmpDir)
	defer os.Setenv("KIMI_HOME", origKimiHome)

	// Initially should not be authenticated
	if IsKimiCliAuthenticated() {
		t.Error("Expected not authenticated without credentials file")
	}

	// Create credentials directory and file
	credDir := filepath.Join(tmpDir, "credentials")
	if err := os.MkdirAll(credDir, 0755); err != nil {
		t.Fatalf("Failed to create credentials dir: %v", err)
	}

	credFile := filepath.Join(credDir, "kimi-code.json")
	
	// Test with valid credentials (far future expiry)
	validCreds := `{
		"access_token": "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_at": 9999999999.0,
		"scope": "kimi-code",
		"token_type": "Bearer"
	}`
	
	if err := os.WriteFile(credFile, []byte(validCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	// Now should be authenticated
	if !IsKimiCliAuthenticated() {
		t.Error("Expected authenticated with valid credentials")
	}

	// Test with expired credentials
	expiredCreds := `{
		"access_token": "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_at": 1000000000.0,
		"scope": "kimi-code",
		"token_type": "Bearer"
	}`
	
	if err := os.WriteFile(credFile, []byte(expiredCreds), 0600); err != nil {
		t.Fatalf("Failed to write credentials file: %v", err)
	}

	// Should not be authenticated with expired credentials
	if IsKimiCliAuthenticated() {
		t.Error("Expected not authenticated with expired credentials")
	}
}
