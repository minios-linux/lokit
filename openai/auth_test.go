package openai

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAuthorizeURL(t *testing.T) {
	url := buildAuthorizeURL("http://127.0.0.1:1455/auth/callback", "state-1", "challenge-1")

	for _, needle := range []string{
		"client_id=app_EMoamEEZ73f0CkXaXp7hrann",
		"state=state-1",
		"code_challenge=challenge-1",
		"codex_cli_simplified_flow=true",
		"id_token_add_organizations=true",
		"originator=opencode",
	} {
		if !strings.Contains(url, needle) {
			t.Fatalf("authorize URL missing %q: %s", needle, url)
		}
	}
}

func TestExtractAccountID(t *testing.T) {
	idToken := testJWT(map[string]any{
		"chatgpt_account_id": "acct-123",
	})

	got := extractAccountID(&tokenResponse{IDToken: idToken})
	if got != "acct-123" {
		t.Fatalf("extractAccountID(id_token) = %q, want acct-123", got)
	}
}

func TestExtractAccountIDFallsBackToOrganization(t *testing.T) {
	accessToken := testJWT(map[string]any{
		"organizations": []map[string]any{
			{"id": "org-456"},
		},
	})

	got := extractAccountID(&tokenResponse{AccessToken: accessToken})
	if got != "org-456" {
		t.Fatalf("extractAccountID(access_token) = %q, want org-456", got)
	}
}

func testJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
