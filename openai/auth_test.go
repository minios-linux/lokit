package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minios-linux/lokit/settings"
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

func TestEnsureAuthMissingTokenDoesNotOpenBrowser(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	_, err := EnsureAuth(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lokit auth login --provider openai") {
		t.Fatalf("missing auth should immediately request explicit login: %v", err)
	}
}

func TestParallelEnsureAuthRefreshesOnce(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SaveToken("expired", "old-refresh", time.Now().Add(-time.Hour).Unix(), "account"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.String() != issuer+"/oauth/token" {
			t.Errorf("unexpected refresh URL: %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"fresh","refresh_token":"new-refresh","expires_in":3600}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := EnsureAuth(context.Background())
			if err != nil || info.Access != "fresh" {
				t.Errorf("parallel auth: %#v, %v", info, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("rotated refresh token %d times", calls.Load())
	}
}

func TestRefreshReusesTokenAlreadyRotatedByAnotherWorker(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SaveToken("fresh", "new-refresh", time.Now().Add(time.Hour).Unix(), "account"); err != nil {
		t.Fatal(err)
	}
	stale := &settings.Info{Access: "expired", Refresh: "old-refresh"}
	if err := RefreshAccessToken(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if stale.Access != "fresh" || stale.Refresh != "new-refresh" {
		t.Fatalf("stale worker did not adopt refreshed token: %#v", stale)
	}
}
