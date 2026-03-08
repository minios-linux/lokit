// Package openai implements OpenAI authentication for lokit.
//
// It supports:
//   - Browser OAuth with PKCE
//   - Device-code style authorization
//   - Refreshing stored OAuth tokens
//
// Credentials are stored in the unified auth store (~/.local/share/lokit/auth.json).
package openai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/minios-linux/lokit/settings"
)

const (
	clientID                 = "app_EMoamEEZ73f0CkXaXp7hrann"
	issuer                   = "https://auth.openai.com"
	CodexResponsesEndpoint   = "https://chatgpt.com/backend-api/codex/responses"
	oauthRedirectPort        = 1455
	oauthRedirectPath        = "/auth/callback"
	deviceRedirectURI        = issuer + "/deviceauth/callback"
	oauthScope               = "openid profile email offline_access"
	oauthPollingSafetyMargin = 3 * time.Second
	oauthTimeout             = 5 * time.Minute
	deviceCodeTimeout        = 10 * time.Minute
	providerID               = "openai"
)

// LoadToken loads the OpenAI OAuth token from the unified auth store.
func LoadToken() *settings.Info {
	return settings.GetOAuth(providerID)
}

// SaveToken saves an OpenAI OAuth token to the unified auth store.
func SaveToken(access, refresh string, expiresAt int64, accountID string) error {
	return settings.SetOAuth(providerID, access, refresh, expiresAt, accountID)
}

// DeleteToken removes the OpenAI credentials from the unified auth store.
func DeleteToken() error {
	return settings.Remove(providerID)
}

// IsExpired returns true if the access token has expired or will expire soon.
// A zero Expires value (missing expires_in from server) is treated as expired
// to trigger a proactive refresh rather than waiting for a 401.
func IsExpired(info *settings.Info) bool {
	if info == nil {
		return false
	}
	if info.Expires == 0 {
		return true
	}
	return time.Now().Unix() > info.Expires-60
}

// TokenStatus returns a human-readable status of the stored OpenAI auth.
func TokenStatus() string {
	info := settings.Get(providerID)
	if info == nil {
		return "not configured"
	}

	if info.IsAPI() {
		return fmt.Sprintf("API key configured (key: %s)", settings.MaskKey(info.Key))
	}

	status := fmt.Sprintf("authenticated (token: %s)", settings.MaskKey(info.Access))
	if info.AccountID != "" {
		status += fmt.Sprintf("\n  account: %s", info.AccountID)
	}
	if IsExpired(info) {
		if info.Refresh != "" {
			status += " [expired, will auto-refresh]"
		} else {
			status += " [expired, re-login required]"
		}
	}
	return status
}

// IsOAuthModel returns true if the model is supported by OpenAI OAuth/device
// code authentication (ChatGPT Codex endpoint). Currently GPT-5 and Codex
// model families are supported.
func IsOAuthModel(model string) bool {
	return strings.HasPrefix(model, "gpt-5") || strings.Contains(model, "codex")
}

type tokenResponse struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	Error        string `json:"error,omitempty"`
}

type deviceCodeResponse struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
	Interval     string `json:"interval"`
}

type deviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type jwtClaims struct {
	AccountID string `json:"chatgpt_account_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Auth      struct {
		AccountID string `json:"chatgpt_account_id,omitempty"`
	} `json:"https://api.openai.com/auth,omitempty"`
	Organizations []struct {
		ID string `json:"id"`
	} `json:"organizations,omitempty"`
}

// BrowserOAuthFlow authenticates with OpenAI using the browser OAuth flow.
func BrowserOAuthFlow(ctx context.Context, onPrompt func(authURL string)) (string, error) {
	verifier, err := generateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("generating PKCE verifier: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", oauthRedirectPort))
	if err != nil {
		return "", fmt.Errorf("starting local callback server: %w", err)
	}
	defer listener.Close()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", oauthRedirectPort, oauthRedirectPath)
	authURL := buildAuthorizeURL(redirectURI, state, codeChallenge(verifier))

	type authResult struct {
		code string
		err  error
	}
	resultCh := make(chan authResult, 1)

	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}
	mux.HandleFunc(oauthRedirectPath, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		if errCode := query.Get("error"); errCode != "" {
			errMsg := query.Get("error_description")
			if errMsg == "" {
				errMsg = errCode
			}
			http.Error(w, "Authorization failed. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- authResult{err: fmt.Errorf("OAuth error: %s", errMsg)}:
			default:
			}
			return
		}

		if query.Get("state") != state {
			http.Error(w, "Invalid OAuth state. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- authResult{err: fmt.Errorf("OAuth state mismatch")}:
			default:
			}
			return
		}

		code := query.Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code. You can close this window.", http.StatusBadRequest)
			select {
			case resultCh <- authResult{err: fmt.Errorf("no authorization code received")}:
			default:
			}
			return
		}

		_, _ = io.WriteString(w, "Authorization successful. You can close this window.")
		select {
		case resultCh <- authResult{code: code}:
		default:
		}
	})

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			select {
			case resultCh <- authResult{err: fmt.Errorf("callback server error: %w", err)}:
			default:
			}
		}
	}()

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if onPrompt != nil {
		onPrompt(authURL)
	}
	_ = openBrowser(authURL)

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(oauthTimeout):
		return "", fmt.Errorf("OAuth callback timeout")
	case result := <-resultCh:
		if result.err != nil {
			return "", result.err
		}

		tokenResp, err := exchangeCodeForTokens(ctx, result.code, redirectURI, verifier)
		if err != nil {
			return "", fmt.Errorf("exchanging code for token: %w", err)
		}

		expiresAt := expiryUnix(tokenResp.ExpiresIn)
		accountID := extractAccountID(tokenResp)
		if err := SaveToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt, accountID); err != nil {
			return tokenResp.AccessToken, fmt.Errorf("token obtained but failed to save: %w", err)
		}

		return tokenResp.AccessToken, nil
	}
}

// DeviceCodeFlow authenticates with OpenAI using the device authorization flow.
func DeviceCodeFlow(ctx context.Context, onPrompt func(verificationURI, userCode string)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, deviceCodeTimeout)
	defer cancel()

	deviceResp, err := requestDeviceCode(ctx)
	if err != nil {
		return "", fmt.Errorf("requesting device code: %w", err)
	}

	if onPrompt != nil {
		onPrompt(issuer+"/codex/device", deviceResp.UserCode)
	}

	interval := parseDeviceInterval(deviceResp.Interval)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		pollResp, statusCode, err := pollDeviceToken(ctx, deviceResp)
		if err != nil {
			return "", fmt.Errorf("polling device token: %w", err)
		}

		if statusCode == http.StatusOK {
			tokenResp, err := exchangeDeviceCode(ctx, pollResp.AuthorizationCode, pollResp.CodeVerifier)
			if err != nil {
				return "", fmt.Errorf("exchanging device authorization code: %w", err)
			}

			expiresAt := expiryUnix(tokenResp.ExpiresIn)
			accountID := extractAccountID(tokenResp)
			if err := SaveToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt, accountID); err != nil {
				return tokenResp.AccessToken, fmt.Errorf("token obtained but failed to save: %w", err)
			}

			return tokenResp.AccessToken, nil
		}

		if statusCode != http.StatusForbidden && statusCode != http.StatusNotFound {
			return "", fmt.Errorf("device authorization failed with status %d", statusCode)
		}
	}
}

// RefreshAccessToken refreshes an expired OpenAI OAuth token in-place.
func RefreshAccessToken(ctx context.Context, info *settings.Info) error {
	if info == nil || info.Refresh == "" {
		return fmt.Errorf("no refresh token available, re-login required")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {info.Refresh},
		"client_id":     {clientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("parsing refresh response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("no access token in refresh response")
	}

	info.Access = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		info.Refresh = tokenResp.RefreshToken
	}
	info.Expires = expiryUnix(tokenResp.ExpiresIn)
	if accountID := extractAccountID(&tokenResp); accountID != "" {
		info.AccountID = accountID
	}

	return SaveToken(info.Access, info.Refresh, info.Expires, info.AccountID)
}

// EnsureAuth returns a valid OpenAI OAuth token, refreshing or re-authing if needed.
func EnsureAuth(ctx context.Context) (*settings.Info, error) {
	info := LoadToken()
	if info != nil && !IsExpired(info) {
		return info, nil
	}

	if info != nil && IsExpired(info) && info.Refresh != "" {
		fmt.Fprintln(os.Stderr, "OpenAI access token expired, refreshing...")
		if err := RefreshAccessToken(ctx, info); err == nil {
			fmt.Fprintln(os.Stderr, "Token refreshed successfully!")
			return info, nil
		} else {
			fmt.Fprintf(os.Stderr, "Token refresh failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "Starting new authentication flow...")
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "OpenAI authentication required.")
	fmt.Fprintln(os.Stderr, "A browser window will open for you to sign in to ChatGPT.")
	fmt.Fprintln(os.Stderr)

	_, err := BrowserOAuthFlow(ctx, func(authURL string) {
		fmt.Fprintln(os.Stderr, "  If the browser doesn't open automatically, visit:")
		fmt.Fprintf(os.Stderr, "  %s\n\n", authURL)
		fmt.Fprintln(os.Stderr, "  Waiting for authorization...")
	})
	if err != nil {
		return nil, err
	}

	info = LoadToken()
	if info == nil {
		return nil, fmt.Errorf("token was saved but cannot be loaded")
	}
	return info, nil
}

func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func codeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func buildAuthorizeURL(redirectURI, state, challenge string) string {
	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {clientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {oauthScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"state":                      {state},
		"originator":                 {"opencode"},
	}
	return issuer + "/oauth/authorize?" + params.Encode()
}

func exchangeCodeForTokens(ctx context.Context, code, redirectURI, verifier string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}
	return &tokenResp, nil
}

func requestDeviceCode(ctx context.Context) (*deviceCodeResponse, error) {
	body, err := json.Marshal(map[string]string{
		"client_id": clientID,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/usercode", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lokit")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed deviceCodeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	if parsed.DeviceAuthID == "" || parsed.UserCode == "" {
		return nil, fmt.Errorf("invalid device code response")
	}
	return &parsed, nil
}

func pollDeviceToken(ctx context.Context, info *deviceCodeResponse) (*deviceTokenResponse, int, error) {
	body, err := json.Marshal(map[string]string{
		"device_auth_id": info.DeviceAuthID,
		"user_code":      info.UserCode,
	})
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/api/accounts/deviceauth/token", strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lokit")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	var parsed deviceTokenResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, 0, fmt.Errorf("parsing device token response: %w", err)
	}
	if parsed.AuthorizationCode == "" || parsed.CodeVerifier == "" {
		return nil, 0, fmt.Errorf("invalid device token response")
	}
	return &parsed, resp.StatusCode, nil
}

func exchangeDeviceCode(ctx context.Context, authorizationCode, verifier string) (*tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {authorizationCode},
		"redirect_uri":  {deviceRedirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}
	return &tokenResp, nil
}

func parseDeviceInterval(raw string) time.Duration {
	seconds, err := time.ParseDuration(raw + "s")
	if err == nil && seconds > 0 {
		return seconds + oauthPollingSafetyMargin
	}
	return 5*time.Second + oauthPollingSafetyMargin
}

func expiryUnix(expiresIn int) int64 {
	if expiresIn <= 0 {
		return 0
	}
	return time.Now().Unix() + int64(expiresIn)
}

func extractAccountID(tokens *tokenResponse) string {
	if tokens == nil {
		return ""
	}
	if tokens.IDToken != "" {
		if claims, ok := parseJWTClaims(tokens.IDToken); ok {
			if accountID := accountIDFromClaims(claims); accountID != "" {
				return accountID
			}
		}
	}
	if tokens.AccessToken != "" {
		if claims, ok := parseJWTClaims(tokens.AccessToken); ok {
			return accountIDFromClaims(claims)
		}
	}
	return ""
}

func parseJWTClaims(token string) (*jwtClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}

	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return &claims, true
}

func accountIDFromClaims(claims *jwtClaims) string {
	if claims == nil {
		return ""
	}
	if claims.AccountID != "" {
		return claims.AccountID
	}
	if claims.Auth.AccountID != "" {
		return claims.Auth.AccountID
	}
	if len(claims.Organizations) > 0 && claims.Organizations[0].ID != "" {
		return claims.Organizations[0].ID
	}
	return ""
}

func openBrowser(authURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", authURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", authURL)
	default:
		cmd = exec.Command("xdg-open", authURL)
	}
	return cmd.Start()
}
