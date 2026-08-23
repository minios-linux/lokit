package copilot

import (
	"net/http"
	"testing"

	"github.com/minios-linux/lokit/settings"
)

func useTempAuthStore(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

func TestTokenStatus(t *testing.T) {
	useTempAuthStore(t)

	if got := TokenStatus(); got != "not authenticated" {
		t.Fatalf("TokenStatus() without token = %q, want %q", got, "not authenticated")
	}

	access := "abcdefghijklmnopqrstuvwxyz"
	if err := SaveToken(access); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}
	if got := settings.GetOAuth(providerID); got == nil || got.Access != access {
		t.Fatalf("canonical token = %#v, want saved token", got)
	}

	want := "authenticated (token: " + settings.MaskKey(access) + ")"
	if got := TokenStatus(); got != want {
		t.Fatalf("TokenStatus() = %q, want %q", got, want)
	}

	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken() error: %v", err)
	}
	if got := TokenStatus(); got != "not authenticated" {
		t.Fatalf("TokenStatus() after delete = %q, want %q", got, "not authenticated")
	}
}

func TestLoadAndDeleteLegacyToken(t *testing.T) {
	useTempAuthStore(t)

	if err := settings.SetOAuth(legacyProviderID, "legacy-access", "", 0, ""); err != nil {
		t.Fatalf("SetOAuth() error: %v", err)
	}
	if got := LoadToken(); got == nil || got.Access != "legacy-access" {
		t.Fatalf("LoadToken() = %#v, want legacy token", got)
	}
	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken() error: %v", err)
	}
	if got := settings.GetOAuth(legacyProviderID); got != nil {
		t.Fatalf("legacy token remains after DeleteToken(): %#v", got)
	}
}

func TestSetAuthHeaders(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.com", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error: %v", err)
	}
	req.Header.Set("x-api-key", "legacy")

	SetAuthHeaders(req, "token123")

	if got := req.Header.Get("Authorization"); got != "Bearer token123" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer token123")
	}
	if got := req.Header.Get("User-Agent"); got != "lokit/1.0" {
		t.Fatalf("User-Agent header = %q, want %q", got, "lokit/1.0")
	}
	if got := req.Header.Get("Openai-Intent"); got != "conversation-edits" {
		t.Fatalf("Openai-Intent header = %q, want %q", got, "conversation-edits")
	}
	if got := req.Header.Get("X-Initiator"); got != "user" {
		t.Fatalf("X-Initiator header = %q, want %q", got, "user")
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key header = %q, want empty", got)
	}
}
