package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// KimiCliCredentials represents the ~/.kimi/credentials/kimi-code.json file structure.
type KimiCliCredentials struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    float64 `json:"expires_at"`
	Scope        string  `json:"scope"`
	TokenType    string  `json:"token_type"`
}

// ReadKimiCliCredentials reads OAuth tokens from the Kimi CLI's credentials file.
func ReadKimiCliCredentials() (accessToken, refreshToken string, expiresAt time.Time, err error) {
	credPath, err := resolveKimiCredentialsPath()
	if err != nil {
		return "", "", time.Time{}, err
	}

	data, err := os.ReadFile(credPath)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("reading %s: %w", credPath, err)
	}

	var creds KimiCliCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", "", time.Time{}, fmt.Errorf("parsing %s: %w", credPath, err)
	}

	if creds.AccessToken == "" {
		return "", "", time.Time{}, fmt.Errorf("no access_token in %s", credPath)
	}

	expiresAt = time.Unix(int64(creds.ExpiresAt), 0)

	return creds.AccessToken, creds.RefreshToken, expiresAt, nil
}

// CreateKimiCliTokenSource creates a token source that reads from ~/.kimi/credentials/kimi-code.json.
// This allows the existing KimiProvider to reuse Kimi CLI credentials.
func CreateKimiCliTokenSource() func() (string, string, error) {
	return func() (string, string, error) {
		token, _, expiresAt, err := ReadKimiCliCredentials()
		if err != nil {
			return "", "", fmt.Errorf("reading kimi cli credentials: %w", err)
		}

		if time.Now().After(expiresAt) {
			return "", "", fmt.Errorf("kimi cli credentials expired. Run: kimi login")
		}

		return token, "", nil
	}
}

// IsKimiCliInstalled checks if the Kimi CLI is installed.
func IsKimiCliInstalled() bool {
	_, err := exec.LookPath("kimi")
	return err == nil
}

// IsKimiCliAuthenticated checks if the Kimi CLI has valid credentials.
func IsKimiCliAuthenticated() bool {
	_, _, expiresAt, err := ReadKimiCliCredentials()
	if err != nil {
		return false
	}
	return time.Now().Before(expiresAt)
}

func resolveKimiCredentialsPath() (string, error) {
	kimiHome := os.Getenv("KIMI_HOME")
	if kimiHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("getting home dir: %w", err)
		}
		kimiHome = filepath.Join(home, ".kimi")
	}
	return filepath.Join(kimiHome, "credentials", "kimi-code.json"), nil
}
