package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataDirAndFilePathUseXDGDataHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error: %v", err)
	}
	wantDir := filepath.Join(tmp, "lokit")
	if dir != wantDir {
		t.Fatalf("DataDir() = %q, want %q", dir, wantDir)
	}

	wantPath := filepath.Join(tmp, "lokit", "auth.json")
	if got := FilePath(); got != wantPath {
		t.Fatalf("FilePath() = %q, want %q", got, wantPath)
	}
}

func TestSaveLoadRemoveLifecycle(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	store := Store{
		"google":  {Type: "api", Key: "apikey123456"},
		"copilot": {Type: "oauth", Access: "acc", Refresh: "ref", Expires: 123},
	}

	if err := Save(store); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	path := filepath.Join(tmp, "lokit", "auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("auth.json mode = %o, want 600", info.Mode().Perm())
	}

	loaded := Load()
	if loaded["google"] == nil || loaded["google"].Key != "apikey123456" {
		t.Fatalf("Load() missing google key: %#v", loaded["google"])
	}
	if loaded["copilot"] == nil || loaded["copilot"].Access != "acc" {
		t.Fatalf("Load() missing copilot oauth: %#v", loaded["copilot"])
	}

	if err := Remove("google"); err != nil {
		t.Fatalf("Remove(google) error: %v", err)
	}
	if got := GetAPIKey("google"); got != "" {
		t.Fatalf("GetAPIKey after remove = %q, want empty", got)
	}
	if GetOAuth("copilot") == nil {
		t.Fatalf("copilot oauth should remain after removing google")
	}

	if err := Remove("missing-provider"); err != nil {
		t.Fatalf("Remove(missing) should be no-op, got: %v", err)
	}

	if err := RemoveAll(); err != nil {
		t.Fatalf("RemoveAll() error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("auth.json should be removed, stat err=%v", err)
	}
	if got := Load(); len(got) != 0 {
		t.Fatalf("Load() after RemoveAll should be empty, got=%#v", got)
	}
}

func TestResolveAPIKeyPriority(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	if err := SetAPIKey("google", "stored-key"); err != nil {
		t.Fatalf("SetAPIKey() error: %v", err)
	}

	t.Setenv("GOOGLE_API_KEY", "env-key")

	if got := ResolveAPIKey("google", "flag-key"); got != "flag-key" {
		t.Fatalf("flag should win, got %q", got)
	}
	if got := ResolveAPIKey("google", ""); got != "env-key" {
		t.Fatalf("env should win over store, got %q", got)
	}

	t.Setenv("GOOGLE_API_KEY", "")
	if got := ResolveAPIKey("google", ""); got != "stored-key" {
		t.Fatalf("stored key expected, got %q", got)
	}
}

func TestEnvVarForProviderAndMaskKey(t *testing.T) {
	cases := map[string]string{
		"google":        "GOOGLE_API_KEY",
		"groq":          "GROQ_API_KEY",
		"opencode":      "OPENCODE_API_KEY",
		"custom-openai": "OPENAI_API_KEY",
		"copilot":       "",
		"gemini":        "",
		"ollama":        "",
		"unknown":       "",
	}
	for provider, want := range cases {
		if got := EnvVarForProvider(provider); got != want {
			t.Fatalf("EnvVarForProvider(%q) = %q, want %q", provider, got, want)
		}
	}

	if got := MaskKey("short"); got != "****" {
		t.Fatalf("MaskKey(short) = %q, want ****", got)
	}
	if got := MaskKey("12345678"); got != "****" {
		t.Fatalf("MaskKey(8 chars) = %q, want ****", got)
	}
	if got := MaskKey("123456789"); got != "1234...6789" {
		t.Fatalf("MaskKey(9 chars) = %q, want 1234...6789", got)
	}
}

func TestSetOAuthPreservesExistingFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	if err := Set("gemini", &Info{
		Type:      "oauth",
		Access:    "old-access",
		Refresh:   "old-refresh",
		Email:     "user@example.com",
		ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	if err := SetOAuth("gemini", "new-access", "", 999); err != nil {
		t.Fatalf("SetOAuth() error: %v", err)
	}

	got := GetOAuth("gemini")
	if got == nil {
		t.Fatal("GetOAuth(gemini) returned nil")
	}
	if got.Access != "new-access" {
		t.Fatalf("access = %q, want new-access", got.Access)
	}
	if got.Refresh != "old-refresh" {
		t.Fatalf("refresh = %q, want old-refresh", got.Refresh)
	}
	if got.Email != "user@example.com" || got.ProjectID != "project-1" {
		t.Fatalf("email/project not preserved: %#v", got)
	}
}
