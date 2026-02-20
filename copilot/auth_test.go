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
