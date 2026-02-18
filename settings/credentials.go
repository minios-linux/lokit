// Package settings provides unified storage for lokit user settings,
// including authentication credentials and AI translation prompts.
//
// All settings are stored in the XDG data directory:
//
//	$XDG_DATA_HOME/lokit/  (default: ~/.local/share/lokit/)
//
// Files stored:
//   - auth.json     — Authentication credentials (OAuth tokens and API keys)
//   - prompts.json  — AI translation system prompts (customizable by user)
//
// Auth.json format:
// The file is a JSON object keyed by provider ID, where each value is a
// discriminated union on the "type" field:
//
//   - "oauth"  — OAuth tokens (copilot, gemini)
//   - "api"    — API keys (google, groq, opencode, custom-openai)
//
// File permissions are 0600 (owner read/write only).
//
// Lookup order for API keys:
//  1. --api-key flag (highest priority)
//  2. LOKIT_API_KEY environment variable
//  3. This credential store
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	dataDirName = "lokit"
	fileName    = "auth.json"
)

// ---------------------------------------------------------------------------
// Auth entry types (discriminated union on "type")
// ---------------------------------------------------------------------------

// Info is the discriminated union stored per provider in auth.json.
// Exactly matches OpenCode's Auth.Info shape.
type Info struct {
	// Type discriminator: "oauth" or "api"
	Type string `json:"type"`

	// OAuth fields (type == "oauth")
	Access  string `json:"access,omitempty"`
	Refresh string `json:"refresh,omitempty"`
	Expires int64  `json:"expires,omitempty"` // Unix timestamp (0 = no expiry)

	// Gemini-specific OAuth fields
	Email     string `json:"email,omitempty"`     // Google account email
	ProjectID string `json:"projectId,omitempty"` // Code Assist project ID

	// API key fields (type == "api")
	Key string `json:"key,omitempty"`

	// Custom endpoint URL (custom-openai)
	BaseURL string `json:"baseUrl,omitempty"`
}

// IsOAuth returns true if this is an OAuth entry.
func (i *Info) IsOAuth() bool {
	return i.Type == "oauth"
}

// IsAPI returns true if this is an API key entry.
func (i *Info) IsAPI() bool {
	return i.Type == "api"
}

// Store holds all provider credentials, keyed by provider ID.
type Store map[string]*Info

// ---------------------------------------------------------------------------
// File path
// ---------------------------------------------------------------------------

// dataDir returns the XDG data directory for lokit.
// Respects $XDG_DATA_HOME (falls back to ~/.local/share), matching OpenCode.
func dataDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, dataDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", dataDirName), nil
}

// filePath returns the path to the auth file.
// Default: ~/.local/share/lokit/auth.json (or $XDG_DATA_HOME/lokit/auth.json).
func filePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// FilePath returns the auth.json file path for display purposes.
func FilePath() string {
	p, err := filePath()
	if err != nil {
		return ""
	}
	return p
}

// PromptsFilePath returns the path to the prompts.json file.
// Default: ~/.local/share/lokit/prompts.json (or $XDG_DATA_HOME/lokit/prompts.json).
func PromptsFilePath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "prompts.json"), nil
}

// DataDir returns the lokit data directory path.
// Default: ~/.local/share/lokit (or $XDG_DATA_HOME/lokit).
func DataDir() (string, error) {
	return dataDir()
}

// ---------------------------------------------------------------------------
// Load / Save
// ---------------------------------------------------------------------------

// Load reads the credential store from disk.
// Returns an empty store if the file doesn't exist or is invalid.
func Load() Store {
	path, err := filePath()
	if err != nil {
		return make(Store)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return make(Store)
	}

	var store Store
	if err := json.Unmarshal(data, &store); err != nil {
		return make(Store)
	}

	if store == nil {
		return make(Store)
	}

	return store
}

// Save writes the credential store to disk with 0600 permissions.
func Save(store Store) error {
	path, err := filePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing auth file: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Get / Set / Delete — generic
// ---------------------------------------------------------------------------

// Get returns the auth entry for a provider, or nil if not found.
func Get(providerID string) *Info {
	store := Load()
	return store[providerID]
}

// Set stores an auth entry for a provider (upsert).
func Set(providerID string, info *Info) error {
	store := Load()
	store[providerID] = info
	return Save(store)
}

// Remove deletes credentials for a provider.
func Remove(providerID string) error {
	store := Load()
	if _, ok := store[providerID]; !ok {
		return nil // Nothing to delete
	}
	delete(store, providerID)
	return Save(store)
}

// ---------------------------------------------------------------------------
// OAuth helpers
// ---------------------------------------------------------------------------

// SetOAuth stores OAuth credentials for a provider.
func SetOAuth(providerID, access, refresh string, expires int64) error {
	store := Load()
	existing := store[providerID]

	info := &Info{
		Type:    "oauth",
		Access:  access,
		Refresh: refresh,
		Expires: expires,
	}

	// Preserve extra fields from existing entry
	if existing != nil && existing.IsOAuth() {
		if info.Refresh == "" && existing.Refresh != "" {
			info.Refresh = existing.Refresh
		}
		info.Email = existing.Email
		info.ProjectID = existing.ProjectID
	}

	store[providerID] = info
	return Save(store)
}

// GetOAuth returns OAuth credentials for a provider, or nil if not found.
func GetOAuth(providerID string) *Info {
	info := Get(providerID)
	if info == nil || !info.IsOAuth() {
		return nil
	}
	return info
}

// ---------------------------------------------------------------------------
// API key helpers
// ---------------------------------------------------------------------------

// SetAPIKey stores an API key for a provider.
func SetAPIKey(providerID, key string) error {
	return Set(providerID, &Info{
		Type: "api",
		Key:  key,
	})
}

// SetAPIKeyWithBaseURL stores an API key and base URL for custom-openai.
func SetAPIKeyWithBaseURL(providerID, key, baseURL string) error {
	return Set(providerID, &Info{
		Type:    "api",
		Key:     key,
		BaseURL: baseURL,
	})
}

// GetAPIKey retrieves the stored API key for a provider.
// Returns empty string if not found or not an API key entry.
func GetAPIKey(providerID string) string {
	info := Get(providerID)
	if info == nil || !info.IsAPI() {
		return ""
	}
	return info.Key
}

// GetBaseURL retrieves the stored base URL for a provider.
// Returns empty string if not found.
func GetBaseURL(providerID string) string {
	info := Get(providerID)
	if info == nil {
		return ""
	}
	return info.BaseURL
}

// ---------------------------------------------------------------------------
// Display helpers
// ---------------------------------------------------------------------------

// MaskKey returns a masked version of a key/token for display.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// RemoveAll removes all stored credentials.
func RemoveAll() error {
	path, err := filePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing auth file: %w", err)
	}
	return nil
}
