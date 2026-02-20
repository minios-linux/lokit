package gemini

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minios-linux/lokit/settings"
)

func useTempAuthStore(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
}

func TestGetOAuthClientCredentialsFromEnv(t *testing.T) {
	t.Setenv("GEMINI_OAUTH_CLIENT_ID", "env-client-id")
	t.Setenv("GEMINI_OAUTH_CLIENT_SECRET", "env-client-secret")

	if got := getOAuthClientID(); got != "env-client-id" {
		t.Fatalf("getOAuthClientID() = %q, want %q", got, "env-client-id")
	}
	if got := getOAuthClientSecret(); got != "env-client-secret" {
		t.Fatalf("getOAuthClientSecret() = %q, want %q", got, "env-client-secret")
	}
}

func TestGetOAuthClientCredentialsDefault(t *testing.T) {
	t.Setenv("GEMINI_OAUTH_CLIENT_ID", "")
	t.Setenv("GEMINI_OAUTH_CLIENT_SECRET", "")

	id := getOAuthClientID()
	if !strings.Contains(id, ".apps.googleusercontent.com") {
		t.Fatalf("default client id %q does not look like a Google OAuth ID", id)
	}

	secret := getOAuthClientSecret()
	if !strings.HasPrefix(secret, "GOCSPX-") {
		t.Fatalf("default client secret %q does not have expected prefix", secret)
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name string
		info *settings.Info
		want bool
	}{
		{name: "nil info", info: nil, want: false},
		{name: "no expiry", info: &settings.Info{Expires: 0}, want: false},
		{name: "far future", info: &settings.Info{Expires: now + 3600}, want: false},
		{name: "within safety window", info: &settings.Info{Expires: now + 30}, want: true},
		{name: "already expired", info: &settings.Info{Expires: now - 10}, want: true},
	}

	for _, tc := range tests {
		if got := IsExpired(tc.info); got != tc.want {
			t.Fatalf("%s: IsExpired() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTokenStatus(t *testing.T) {
	useTempAuthStore(t)

	if got := TokenStatus(); got != "not authenticated" {
		t.Fatalf("TokenStatus() without token = %q, want %q", got, "not authenticated")
	}

	store := settings.Load()
	store[providerID] = &settings.Info{
		Type:      "oauth",
		Access:    "abcdefghijklmnopqrstuvwxyz",
		Refresh:   "refresh-token",
		Expires:   time.Now().Unix() - 3600,
		Email:     "user@example.com",
		ProjectID: "proj-123",
	}
	if err := settings.Save(store); err != nil {
		t.Fatalf("settings.Save() error: %v", err)
	}

	want := "authenticated (user@example.com) [expired, will auto-refresh]\n  project: proj-123"
	if got := TokenStatus(); got != want {
		t.Fatalf("TokenStatus() = %q, want %q", got, want)
	}
}

func TestSaveTokenPreservesExistingFields(t *testing.T) {
	useTempAuthStore(t)

	store := settings.Load()
	store[providerID] = &settings.Info{
		Type:      "oauth",
		Access:    "old-access",
		Refresh:   "old-refresh",
		Email:     "user@example.com",
		ProjectID: "proj-1",
	}
	if err := settings.Save(store); err != nil {
		t.Fatalf("settings.Save() error: %v", err)
	}

	if err := SaveToken("new-access", "", 1234); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}

	info := LoadToken()
	if info == nil {
		t.Fatalf("LoadToken() returned nil")
	}
	if info.Access != "new-access" {
		t.Fatalf("Access = %q, want %q", info.Access, "new-access")
	}
	if info.Refresh != "old-refresh" {
		t.Fatalf("Refresh = %q, want %q", info.Refresh, "old-refresh")
	}
	if info.Email != "user@example.com" {
		t.Fatalf("Email = %q, want %q", info.Email, "user@example.com")
	}
	if info.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q, want %q", info.ProjectID, "proj-1")
	}
}

func TestCodeChallengeRFCVector(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	if got := codeChallenge(verifier); got != want {
		t.Fatalf("codeChallenge() = %q, want %q", got, want)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("truncate short = %q, want %q", got, "hello")
	}
	if got := truncate("hello", 5); got != "hello" {
		t.Fatalf("truncate equal = %q, want %q", got, "hello")
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Fatalf("truncate long = %q, want %q", got, "hello...")
	}
}

func TestBuildAuthURL(t *testing.T) {
	t.Setenv("GEMINI_OAUTH_CLIENT_ID", "client-id-123")

	raw := buildAuthURL("http://127.0.0.1:8080/oauth2callback", "state-1", "challenge-1")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse() error: %v", err)
	}

	if parsed.Scheme+"://"+parsed.Host+parsed.Path != authorizationURL {
		t.Fatalf("authorization endpoint = %q, want %q", parsed.Scheme+"://"+parsed.Host+parsed.Path, authorizationURL)
	}

	q := parsed.Query()
	if got := q.Get("client_id"); got != "client-id-123" {
		t.Fatalf("client_id = %q, want %q", got, "client-id-123")
	}
	if got := q.Get("redirect_uri"); got != "http://127.0.0.1:8080/oauth2callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	if got := q.Get("response_type"); got != "code" {
		t.Fatalf("response_type = %q, want %q", got, "code")
	}
	if got := q.Get("state"); got != "state-1" {
		t.Fatalf("state = %q, want %q", got, "state-1")
	}
	if got := q.Get("code_challenge"); got != "challenge-1" {
		t.Fatalf("code_challenge = %q, want %q", got, "challenge-1")
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want %q", got, "S256")
	}
	if got := q.Get("access_type"); got != "offline" {
		t.Fatalf("access_type = %q, want %q", got, "offline")
	}
	if got := q.Get("prompt"); got != "consent" {
		t.Fatalf("prompt = %q, want %q", got, "consent")
	}
	if got := q.Get("scope"); got != strings.Join(oauthScopes, " ") {
		t.Fatalf("scope = %q, want %q", got, strings.Join(oauthScopes, " "))
	}
}

func TestSetAuthHeaders(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.com", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error: %v", err)
	}
	req.Header.Set("x-goog-api-key", "api-key")

	SetAuthHeaders(req, "oauth-token")

	if got := req.Header.Get("Authorization"); got != "Bearer oauth-token" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer oauth-token")
	}
	if got := req.Header.Get("User-Agent"); got != "lokit/1.0" {
		t.Fatalf("User-Agent header = %q, want %q", got, "lokit/1.0")
	}
	if got := req.Header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key header = %q, want empty", got)
	}
}
