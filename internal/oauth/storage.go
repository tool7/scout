package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Tokens is what we persist between runs. ExpiresAt is the absolute
// expiry of AccessToken; oauth2's TokenSource uses that to decide
// when to refresh.
type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	CloudID      string    `json:"cloud_id"`
	Scope        string    `json:"scope"`
}

var ErrNotLoggedIn = errors.New("not logged in to Jira (run 'scout jira-login')")

func TokenPath(dataDir string) string {
	return filepath.Join(dataDir, TokenFileName)
}

func Load(dataDir string) (Tokens, error) {
	path := TokenPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Tokens{}, ErrNotLoggedIn
		}
		return Tokens{}, fmt.Errorf("read %s: %w", path, err)
	}
	var t Tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return Tokens{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return t, nil
}

func Save(dataDir string, t Tokens) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create dataDir %s: %w", dataDir, err)
	}
	path := TokenPath(dataDir)
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func Delete(dataDir string) error {
	path := TokenPath(dataDir)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
