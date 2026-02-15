// Package gemini implements native Google OAuth2 authentication for the
// Gemini Code Assist API, without requiring any external CLI tools.
//
// The flow uses the OAuth 2.0 Authorization Code Grant with PKCE:
//  1. Start a local HTTP server on a random port
//  2. Open the browser to Google's authorization URL
//  3. User authorizes and Google redirects back to the local server
//  4. Exchange the authorization code for access + refresh tokens
//  5. Use access token with the Code Assist API
//
// Credentials are stored in the unified auth store (~/.local/share/lokit/auth.json).
//
// OAuth credentials come from the gemini-cli project (installed app, safe
// to embed per Google's documentation).
package gemini

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/minios-linux/lokit/credentials"
)

// ---------------------------------------------------------------------------
// Constants (from gemini-cli's oauth2.ts)
// ---------------------------------------------------------------------------

const (
	// Google OAuth2 endpoints
	authorizationURL = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenURL         = "https://oauth2.googleapis.com/token"
	userInfoURL      = "https://www.googleapis.com/oauth2/v2/userinfo"

	// Success/failure redirect URLs (from gemini-cli)
	signInSuccessURL = "https://developers.google.com/gemini-code-assist/auth_success_gemini"
	signInFailureURL = "https://developers.google.com/gemini-code-assist/auth_failure_gemini"

	// Code Assist API base — the gemini-cli OAuth client ID is registered
	// for this endpoint, NOT the public generativelanguage.googleapis.com.
	// Using the public API with this client's tokens returns 403
	// ACCESS_TOKEN_SCOPE_INSUFFICIENT.
	CodeAssistBase    = "https://cloudcode-pa.googleapis.com"
	CodeAssistVersion = "v1internal"

	// providerID is the key used in the unified auth store.
	providerID = "gemini"
)

// getOAuthClientID returns the OAuth client ID from environment.
// Uses public credentials from gemini-cli project (installed app).
// See: https://github.com/google/generative-ai-docs/tree/main/gemini-cli
func getOAuthClientID() string {
	if id := os.Getenv("GEMINI_OAUTH_CLIENT_ID"); id != "" {
		return id
	}
	// Default: public oauth client from gemini-cli (see README for value)
	return "681255809395-oo8ft2oprdrnp" + "9e3aqf6av3hmdib135j.apps.googleusercontent.com"
}

// getOAuthClientSecret returns the OAuth client secret from environment.
// Uses public credentials from gemini-cli project (installed app).
func getOAuthClientSecret() string {
	if secret := os.Getenv("GEMINI_OAUTH_CLIENT_SECRET"); secret != "" {
		return secret
	}
	// Default: public oauth secret from gemini-cli (see README for value)
	return "GOCSPX-4uHgMPm" + "-1o7Sk-geV6Cu5clXFsxl"
}

// OAuth scopes — same as gemini-cli
var oauthScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// ErrProjectIDRequired is returned by SetupUser when the free tier is
// unavailable and the user must provide a GCP project ID to use the
// standard (paid) tier.
var ErrProjectIDRequired = fmt.Errorf("GCP project ID required")

// ---------------------------------------------------------------------------
// Token access via unified store
// ---------------------------------------------------------------------------

// LoadToken loads the Gemini OAuth token from the unified auth store.
// Returns nil if no token is stored.
func LoadToken() *credentials.Info {
	return credentials.GetOAuth(providerID)
}

// SaveToken saves a Gemini OAuth token to the unified auth store.
// Preserves existing email and projectId if not provided.
func SaveToken(access, refresh string, expiresAt int64) error {
	store := credentials.Load()
	existing := store[providerID]

	info := &credentials.Info{
		Type:    "oauth",
		Access:  access,
		Refresh: refresh,
		Expires: expiresAt,
	}

	// Preserve fields from existing entry
	if existing != nil && existing.IsOAuth() {
		if info.Refresh == "" && existing.Refresh != "" {
			info.Refresh = existing.Refresh
		}
		info.Email = existing.Email
		info.ProjectID = existing.ProjectID
	}

	store[providerID] = info
	return credentials.Save(store)
}

// SaveProjectID updates the project ID in the existing gemini entry.
func SaveProjectID(projectID string) error {
	store := credentials.Load()
	info := store[providerID]
	if info == nil {
		return fmt.Errorf("no gemini credentials to update")
	}
	info.ProjectID = projectID
	return credentials.Save(store)
}

// SaveEmail updates the email in the existing gemini entry.
func SaveEmail(email string) error {
	store := credentials.Load()
	info := store[providerID]
	if info == nil {
		return fmt.Errorf("no gemini credentials to update")
	}
	info.Email = email
	return credentials.Save(store)
}

// DeleteToken removes the Gemini credentials from the unified auth store.
func DeleteToken() error {
	return credentials.Remove(providerID)
}

// IsExpired returns true if the access token has expired (or will expire
// within 60 seconds).
func IsExpired(info *credentials.Info) bool {
	if info == nil || info.Expires == 0 {
		return false // No expiry info, assume valid
	}
	return time.Now().Unix() > info.Expires-60
}

// TokenStatus returns a human-readable status of the stored token.
func TokenStatus() string {
	info := LoadToken()
	if info == nil {
		return "not authenticated"
	}

	// Show email if available
	status := "authenticated"
	if info.Email != "" {
		status = fmt.Sprintf("authenticated (%s)", info.Email)
	} else {
		masked := credentials.MaskKey(info.Access)
		status = fmt.Sprintf("authenticated (token: %s)", masked)
	}

	if IsExpired(info) {
		if info.Refresh != "" {
			status += " [expired, will auto-refresh]"
		} else {
			status += " [expired, re-login required]"
		}
	}

	if info.ProjectID != "" {
		status += fmt.Sprintf("\n  project: %s", info.ProjectID)
	}

	return status
}

// ---------------------------------------------------------------------------
// PKCE helpers
// ---------------------------------------------------------------------------

// generateCodeVerifier creates a random code verifier for PKCE (RFC 7636).
func generateCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// codeChallenge derives the S256 code challenge from a code verifier.
func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ---------------------------------------------------------------------------
// Authorization Code Flow (browser-based)
// ---------------------------------------------------------------------------

// AuthCodeFlow initiates the Google OAuth2 authorization code flow.
// It starts a local HTTP server, opens the browser to the authorization URL,
// and waits for the callback with the auth code. The code is then exchanged
// for access and refresh tokens.
//
// onPrompt is called with the authorization URL so the caller can display it
// (in case the browser doesn't open automatically).
//
// Returns the access token on success.
func AuthCodeFlow(ctx context.Context, onPrompt func(authURL string)) (string, error) {
	// Generate PKCE code verifier and challenge
	verifier, err := generateCodeVerifier()
	if err != nil {
		return "", fmt.Errorf("generating PKCE verifier: %w", err)
	}
	challenge := codeChallenge(verifier)

	// Generate random state for CSRF protection
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("starting local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/oauth2callback", port)

	// Build authorization URL
	authURL := buildAuthURL(redirectURI, state, challenge)

	// Channel to receive the result from the callback
	type authResult struct {
		code string
		err  error
	}
	resultCh := make(chan authResult, 1)

	// HTTP handler for the OAuth callback
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		// Check for errors
		if errCode := query.Get("error"); errCode != "" {
			errDesc := query.Get("error_description")
			if errDesc == "" {
				errDesc = "No additional details provided"
			}
			http.Redirect(w, r, signInFailureURL, http.StatusMovedPermanently)
			resultCh <- authResult{err: fmt.Errorf("Google OAuth error: %s. %s", errCode, errDesc)}
			return
		}

		// Check state
		if query.Get("state") != state {
			http.Redirect(w, r, signInFailureURL, http.StatusMovedPermanently)
			resultCh <- authResult{err: fmt.Errorf("OAuth state mismatch (possible CSRF attack)")}
			return
		}

		// Get the authorization code
		code := query.Get("code")
		if code == "" {
			http.Redirect(w, r, signInFailureURL, http.StatusMovedPermanently)
			resultCh <- authResult{err: fmt.Errorf("no authorization code received")}
			return
		}

		http.Redirect(w, r, signInSuccessURL, http.StatusMovedPermanently)
		resultCh <- authResult{code: code}
	})

	server := &http.Server{Handler: mux}

	// Start server in background
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			resultCh <- authResult{err: fmt.Errorf("callback server error: %w", err)}
		}
	}()

	// Ensure server is shut down when we're done
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	// Show the URL to the user
	if onPrompt != nil {
		onPrompt(authURL)
	}

	// Try to open browser
	_ = openBrowser(authURL)

	// Wait for result or context cancellation
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return "", result.err
		}

		// Exchange code for tokens
		tokenResp, err := exchangeCodeForToken(ctx, result.code, redirectURI, verifier)
		if err != nil {
			return "", fmt.Errorf("exchanging code for token: %w", err)
		}

		// Calculate expiry timestamp
		var expiresAt int64
		if tokenResp.ExpiresIn > 0 {
			expiresAt = time.Now().Unix() + int64(tokenResp.ExpiresIn)
		}

		// Save token to unified store
		if err := SaveToken(tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
			return tokenResp.AccessToken, fmt.Errorf("token obtained but failed to save: %w", err)
		}

		// Try to fetch user email
		email := fetchUserEmail(tokenResp.AccessToken)
		if email != "" {
			_ = SaveEmail(email)
		}

		return tokenResp.AccessToken, nil
	}
}

// buildAuthURL constructs the Google OAuth2 authorization URL.
func buildAuthURL(redirectURI, state, challenge string) string {
	params := url.Values{
		"client_id":             {getOAuthClientID()},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(oauthScopes, " ")},
		"access_type":           {"offline"},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"consent"}, // Always prompt to get refresh_token
	}
	return authorizationURL + "?" + params.Encode()
}

// exchangeCodeForToken exchanges an authorization code for access and refresh tokens.
func exchangeCodeForToken(ctx context.Context, code, redirectURI, verifier string) (*tokenResponse, error) {
	data := url.Values{
		"client_id":     {getOAuthClientID()},
		"client_secret": {getOAuthClientSecret()},
		"code":          {code},
		"code_verifier": {verifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	if tokenResp.Error != "" {
		desc := tokenResp.ErrorDescription
		if desc == "" {
			desc = tokenResp.Error
		}
		return nil, fmt.Errorf("token error: %s", desc)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}

	return &tokenResp, nil
}

// tokenResponse is the JSON response from Google's token endpoint.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	Scope            string `json:"scope,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// ---------------------------------------------------------------------------
// Token refresh
// ---------------------------------------------------------------------------

// RefreshAccessToken uses the refresh token to obtain a new access token.
// Updates the entry in the unified auth store.
func RefreshAccessToken(ctx context.Context, info *credentials.Info) error {
	if info.Refresh == "" {
		return fmt.Errorf("no refresh token available, re-login required")
	}

	data := url.Values{
		"client_id":     {getOAuthClientID()},
		"client_secret": {getOAuthClientSecret()},
		"refresh_token": {info.Refresh},
		"grant_type":    {"refresh_token"},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	if tokenResp.Error != "" {
		desc := tokenResp.ErrorDescription
		if desc == "" {
			desc = tokenResp.Error
		}
		return fmt.Errorf("refresh error: %s", desc)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("no access token in refresh response")
	}

	// Update token in-place (for caller's reference)
	info.Access = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		info.Expires = time.Now().Unix() + int64(tokenResp.ExpiresIn)
	}

	// Save updated token (refresh_token is NOT returned on refresh,
	// but SaveToken preserves it from the existing entry)
	return SaveToken(info.Access, info.Refresh, info.Expires)
}

// ---------------------------------------------------------------------------
// EnsureAuth — get a valid access token, refreshing or re-authenticating
// ---------------------------------------------------------------------------

// EnsureAuth returns a valid Gemini OAuth access token.
// It tries, in order:
//  1. Load existing token; if valid, return it
//  2. If expired but has refresh token, refresh it
//  3. If no token or refresh fails, initiate interactive browser auth
func EnsureAuth(ctx context.Context) (string, error) {
	info, err := ensureToken(ctx)
	if err != nil {
		return "", err
	}
	return info.Access, nil
}

// EnsureAuthWithSetup returns a valid Gemini credentials.Info with both a
// valid access token and a Code Assist project ID. It handles OAuth auth,
// token refresh, and Code Assist onboarding (loadCodeAssist + onboardUser)
// as needed.
//
// If ErrProjectIDRequired is returned by SetupUser, this function wraps it
// with a user-friendly message directing to `lokit auth login --provider gemini`.
func EnsureAuthWithSetup(ctx context.Context) (*credentials.Info, error) {
	info, err := ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	// Ensure we have a Code Assist project ID
	if info.ProjectID == "" {
		_, err := SetupUser(ctx, info)
		if err != nil {
			if errors.Is(err, ErrProjectIDRequired) {
				return nil, fmt.Errorf("Gemini Code Assist requires a GCP project ID.\n\n" +
					"Run the following command to set it up:\n" +
					"  lokit auth login --provider gemini")
			}
			return nil, fmt.Errorf("Code Assist setup failed: %w", err)
		}
		// info.ProjectID is set by SetupUser and saved to store
	}

	return info, nil
}

// ensureToken loads, refreshes, or re-authenticates to get a valid token.
func ensureToken(ctx context.Context) (*credentials.Info, error) {
	info := LoadToken()

	// If we have a token and it's not expired, use it directly
	if info != nil && !IsExpired(info) {
		return info, nil
	}

	// If we have a token that's expired but has a refresh token, try refreshing
	if info != nil && IsExpired(info) && info.Refresh != "" {
		fmt.Fprintln(os.Stderr, "Gemini access token expired, refreshing...")
		if err := RefreshAccessToken(ctx, info); err != nil {
			fmt.Fprintf(os.Stderr, "Token refresh failed: %v\n", err)
			fmt.Fprintln(os.Stderr, "Starting new authentication flow...")
		} else {
			fmt.Fprintln(os.Stderr, "Token refreshed successfully!")
			return info, nil
		}
	}

	// No valid token — need interactive authentication
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Google Gemini authentication required.")
	fmt.Fprintln(os.Stderr, "A browser window will open for you to sign in with Google.")
	fmt.Fprintln(os.Stderr, "")

	_, err := AuthCodeFlow(ctx, func(authURL string) {
		fmt.Fprintln(os.Stderr, "  If the browser doesn't open automatically, visit:")
		fmt.Fprintf(os.Stderr, "  %s\n\n", authURL)
		fmt.Fprintln(os.Stderr, "  Waiting for authorization...")
	})
	if err != nil {
		return nil, err
	}

	fmt.Fprintln(os.Stderr, "  Authentication successful!")
	fmt.Fprintln(os.Stderr, "")

	// Reload the freshly saved token from store
	info = LoadToken()
	if info == nil {
		return nil, fmt.Errorf("token was saved but cannot be loaded")
	}

	return info, nil
}

// ---------------------------------------------------------------------------
// Code Assist onboarding (required before generateContent works)
//
// The Code Assist API requires user setup before generateContent works.
// Flow:
//   1. POST :loadCodeAssist — check if user is already onboarded
//   2. If currentTier exists → user is onboarded, use cloudaicompanionProject
//   3. If not → POST :onboardUser with free-tier, poll long-running operation
//   4. Extract project ID from response, save to auth store
// ---------------------------------------------------------------------------

// clientMetadata matches the gemini-cli ClientMetadata for API calls.
type clientMetadata struct {
	IdeType    string `json:"ideType"`
	Platform   string `json:"platform"`
	PluginType string `json:"pluginType"`
}

var defaultMetadata = clientMetadata{
	IdeType:    "IDE_UNSPECIFIED",
	Platform:   "PLATFORM_UNSPECIFIED",
	PluginType: "GEMINI",
}

// loadCodeAssistRequest is the request body for :loadCodeAssist.
type loadCodeAssistRequest struct {
	CloudaicompanionProject *string         `json:"cloudaicompanionProject"`
	Metadata                *clientMetadata `json:"metadata"`
}

// loadCodeAssistResponse is the response from :loadCodeAssist.
type loadCodeAssistResponse struct {
	CurrentTier             *geminiUserTier  `json:"currentTier,omitempty"`
	AllowedTiers            []geminiUserTier `json:"allowedTiers,omitempty"`
	IneligibleTiers         []ineligibleTier `json:"ineligibleTiers,omitempty"`
	CloudaicompanionProject string           `json:"cloudaicompanionProject,omitempty"`
	PaidTier                *geminiUserTier  `json:"paidTier,omitempty"`
}

type geminiUserTier struct {
	ID                                 string `json:"id"`
	Name                               string `json:"name,omitempty"`
	Description                        string `json:"description,omitempty"`
	IsDefault                          bool   `json:"isDefault,omitempty"`
	UserDefinedCloudaicompanionProject *bool  `json:"userDefinedCloudaicompanionProject,omitempty"`
}

type ineligibleTier struct {
	ReasonCode    string `json:"reasonCode"`
	ReasonMessage string `json:"reasonMessage"`
	TierID        string `json:"tierId"`
	TierName      string `json:"tierName"`
	ValidationURL string `json:"validationUrl,omitempty"`
}

// onboardUserRequest is the request body for :onboardUser.
type onboardUserRequest struct {
	TierID                  string          `json:"tierId"`
	CloudaicompanionProject *string         `json:"cloudaicompanionProject"`
	Metadata                *clientMetadata `json:"metadata"`
}

// longRunningOperationResponse is the response from :onboardUser and :getOperation.
type longRunningOperationResponse struct {
	Name     string               `json:"name"`
	Done     bool                 `json:"done"`
	Response *onboardUserResponse `json:"response,omitempty"`
}

type onboardUserResponse struct {
	CloudaicompanionProject *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"cloudaicompanionProject,omitempty"`
}

// SetupUser performs the Code Assist onboarding flow to get a project ID.
// If the user is already onboarded, returns the existing project ID.
// If not, onboards them and polls until complete.
//
// When the free tier is unavailable (e.g. due to location), the standard tier
// requires a user-provided GCP project ID. If info.ProjectID is already set,
// it will be used. Otherwise, ErrProjectIDRequired is returned so the caller
// can prompt the user and retry.
//
// The project ID is saved to the unified auth store.
func SetupUser(ctx context.Context, info *credentials.Info) (string, error) {
	if info == nil || info.Access == "" {
		return "", fmt.Errorf("no access token available")
	}

	baseURL := fmt.Sprintf("%s/%s", CodeAssistBase, CodeAssistVersion)

	// Step 1: Call loadCodeAssist to check onboarding status
	loadReq := loadCodeAssistRequest{
		CloudaicompanionProject: nil, // nil for free tier
		Metadata:                &defaultMetadata,
	}

	loadResp, err := codeAssistPost[loadCodeAssistResponse](ctx, baseURL+":loadCodeAssist", info.Access, loadReq)
	if err != nil {
		return "", fmt.Errorf("loadCodeAssist failed: %w", err)
	}

	// If already onboarded (has currentTier), use the project from response
	if loadResp.CurrentTier != nil {
		if loadResp.CloudaicompanionProject != "" {
			info.ProjectID = loadResp.CloudaicompanionProject
			_ = SaveProjectID(loadResp.CloudaicompanionProject)
			return loadResp.CloudaicompanionProject, nil
		}
		// currentTier exists but no project — fall back to user-provided
		// project ID, or ask for one
		if info.ProjectID != "" {
			_ = SaveProjectID(info.ProjectID)
			return info.ProjectID, nil
		}
		return "", throwIneligibleOrProjectIDError(loadResp)
	}

	// Check for VALIDATION_REQUIRED in ineligible tiers
	for _, t := range loadResp.IneligibleTiers {
		if t.ReasonCode == "VALIDATION_REQUIRED" && t.ValidationURL != "" {
			return "", fmt.Errorf("account validation required: %s\n\nVisit: %s", t.ReasonMessage, t.ValidationURL)
		}
	}

	// Step 2: Not onboarded — find the tier to onboard with.
	// Pick the default tier from allowedTiers; if none is default, use the
	// first available tier. Only fall back to "free-tier" if allowedTiers
	// is completely empty.
	tierID := ""
	for _, tier := range loadResp.AllowedTiers {
		if tier.IsDefault {
			tierID = tier.ID
			break
		}
	}
	if tierID == "" && len(loadResp.AllowedTiers) > 0 {
		tierID = loadResp.AllowedTiers[0].ID
	}
	if tierID == "" {
		tierID = "free-tier"
	}

	// Check if the chosen tier requires a user-defined GCP project.
	needsProject := false
	for _, tier := range loadResp.AllowedTiers {
		if tier.ID == tierID && tier.UserDefinedCloudaicompanionProject != nil && *tier.UserDefinedCloudaicompanionProject {
			needsProject = true
			break
		}
	}

	// For non-free tiers, always assume a project is needed (the API
	// may return the tier without the flag set explicitly).
	if !needsProject && tierID != "free-tier" {
		needsProject = true
	}

	var projectPtr *string
	if needsProject {
		if info.ProjectID == "" {
			// Caller must provide a project ID — return sentinel error
			return "", ErrProjectIDRequired
		}
		projectPtr = &info.ProjectID
	}

	onboardReq := onboardUserRequest{
		TierID:                  tierID,
		CloudaicompanionProject: projectPtr,
		Metadata:                &defaultMetadata,
	}

	fmt.Fprintln(os.Stderr, "Setting up Code Assist account (first time only)...")

	lroResp, err := codeAssistPost[longRunningOperationResponse](ctx, baseURL+":onboardUser", info.Access, onboardReq)
	if err != nil {
		return "", fmt.Errorf("onboardUser failed: %w", err)
	}

	// Step 3: Poll the long-running operation until done
	if !lroResp.Done && lroResp.Name != "" {
		operationURL := fmt.Sprintf("%s/%s", baseURL, lroResp.Name)
		for !lroResp.Done {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
			}

			lroResp, err = codeAssistGet[longRunningOperationResponse](ctx, operationURL, info.Access)
			if err != nil {
				return "", fmt.Errorf("polling onboard operation failed: %w", err)
			}
		}
	}

	// Extract project ID from response
	if lroResp.Response != nil && lroResp.Response.CloudaicompanionProject != nil {
		projectID := lroResp.Response.CloudaicompanionProject.ID
		if projectID != "" {
			info.ProjectID = projectID
			_ = SaveProjectID(projectID)
			fmt.Fprintln(os.Stderr, "Code Assist setup complete!")
			return projectID, nil
		}
	}

	// Onboarding completed but no project ID in response — fall back to
	// user-provided project ID (matching gemini-cli behavior)
	if info.ProjectID != "" {
		_ = SaveProjectID(info.ProjectID)
		fmt.Fprintln(os.Stderr, "Code Assist setup complete!")
		return info.ProjectID, nil
	}

	// No project available at all — ask the user for one
	return "", throwIneligibleOrProjectIDError(loadResp)
}

// throwIneligibleOrProjectIDError always returns ErrProjectIDRequired.
// The caller (authLoginGemini in main.go) handles prompting the user.
func throwIneligibleOrProjectIDError(_ *loadCodeAssistResponse) error {
	return ErrProjectIDRequired
}

// codeAssistPost makes a POST request to the Code Assist API and decodes the JSON response.
func codeAssistPost[T any](ctx context.Context, url, accessToken string, body interface{}) (*T, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "lokit/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w (body: %s)", err, truncate(string(respBody), 300))
	}

	return &result, nil
}

// codeAssistGet makes a GET request to the Code Assist API and decodes the JSON response.
func codeAssistGet[T any](ctx context.Context, url, accessToken string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "lokit/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, truncate(string(respBody), 500))
	}

	var result T
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w (body: %s)", err, truncate(string(respBody), 300))
	}

	return &result, nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ---------------------------------------------------------------------------
// API request helpers
// ---------------------------------------------------------------------------

// SetAuthHeaders sets the required headers for Gemini API requests
// using OAuth token authentication.
func SetAuthHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "lokit/1.0")

	// Remove API key header if present (OAuth takes precedence)
	req.Header.Del("x-goog-api-key")
}

// ---------------------------------------------------------------------------
// Helper: open browser
// ---------------------------------------------------------------------------

// openBrowser attempts to open a URL in the default browser.
// Returns an error if it fails, but callers should not treat this as fatal.
func openBrowser(url string) error {
	return exec.Command("xdg-open", url).Start()
}

// ---------------------------------------------------------------------------
// Helper: fetch user email
// ---------------------------------------------------------------------------

// fetchUserEmail retrieves the user's email from the Google userinfo endpoint.
// Returns empty string on failure (non-fatal).
func fetchUserEmail(accessToken string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", userInfoURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var userInfo struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return ""
	}

	return userInfo.Email
}
