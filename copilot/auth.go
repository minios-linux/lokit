// Package copilot implements native GitHub Copilot OAuth authentication
// using the device code flow, without requiring any external CLI tools.
//
// The flow follows RFC 8628 (OAuth 2.0 Device Authorization Grant):
//  1. Request device code from GitHub
//  2. User visits verification URL and enters the user code
//  3. Poll for access token until authorized
//  4. Use access token with the Copilot API (OpenAI-compatible)
//
// Credentials are stored in the unified auth store (~/.local/share/lokit/auth.json).
package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minios-linux/lokit/credentials"
)

// ---------------------------------------------------------------------------
// Constants (from opencode's copilot.ts)
// ---------------------------------------------------------------------------

const (
	// clientID is the GitHub OAuth app client ID used by opencode/Copilot.
	clientID = "Ov23li8tweQw6odWQebz"

	// OAuth endpoints
	deviceCodeURL  = "https://github.com/login/device/code"
	accessTokenURL = "https://github.com/login/oauth/access_token"

	// Copilot API
	CopilotAPIBase = "https://api.githubcopilot.com"

	// oauthScope is the required scope for Copilot access.
	oauthScope = "read:user"

	// providerID is the key used in the unified auth store.
	providerID = "copilot"
)

// ---------------------------------------------------------------------------
// Token access via unified store
// ---------------------------------------------------------------------------

// LoadToken loads the Copilot OAuth token from the unified auth store.
// Returns nil if no token is stored.
func LoadToken() *credentials.Info {
	return credentials.GetOAuth(providerID)
}

// SaveToken saves a Copilot OAuth token to the unified auth store.
func SaveToken(access string) error {
	return credentials.SetOAuth(providerID, access, "", 0)
}

// DeleteToken removes the Copilot credentials from the unified auth store.
func DeleteToken() error {
	return credentials.Remove(providerID)
}

// TokenStatus returns a human-readable status of the stored token.
func TokenStatus() string {
	info := LoadToken()
	if info == nil {
		return "not authenticated"
	}

	masked := credentials.MaskKey(info.Access)
	return fmt.Sprintf("authenticated (token: %s)", masked)
}

// ---------------------------------------------------------------------------
// Device code flow
// ---------------------------------------------------------------------------

// deviceCodeResponse is the response from the device/code endpoint.
type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// accessTokenResponse is the response from the access_token endpoint.
type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
	Interval    int    `json:"interval"`
}

// DeviceCodeFlow initiates the GitHub OAuth device code flow.
// It prints instructions for the user and blocks until authentication
// is complete or the context is cancelled.
//
// onPrompt is called with the verification URL and user code so the
// caller can display them to the user.
func DeviceCodeFlow(ctx context.Context, onPrompt func(verificationURI, userCode string)) (string, error) {
	// Step 1: Request device code
	dcResp, err := requestDeviceCode(ctx)
	if err != nil {
		return "", fmt.Errorf("requesting device code: %w", err)
	}

	// Step 2: Show the user code to the user
	if onPrompt != nil {
		onPrompt(dcResp.VerificationURI, dcResp.UserCode)
	}

	// Step 3: Poll for access token
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	// Add 3-second safety margin per opencode implementation
	interval += 3 * time.Second

	expiry := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(expiry) {
			return "", fmt.Errorf("device code expired, please try again")
		}

		atResp, err := pollAccessToken(ctx, dcResp.DeviceCode)
		if err != nil {
			return "", fmt.Errorf("polling access token: %w", err)
		}

		switch atResp.Error {
		case "":
			// Success! Save to unified store
			if err := SaveToken(atResp.AccessToken); err != nil {
				return atResp.AccessToken, fmt.Errorf("token obtained but failed to save: %w", err)
			}
			return atResp.AccessToken, nil

		case "authorization_pending":
			// Keep polling
			continue

		case "slow_down":
			// Increase interval by 5 seconds per RFC 8628
			interval += 5 * time.Second
			continue

		case "expired_token":
			return "", fmt.Errorf("device code expired, please try again")

		case "access_denied":
			return "", fmt.Errorf("authorization denied by user")

		default:
			desc := atResp.ErrorDesc
			if desc == "" {
				desc = atResp.Error
			}
			return "", fmt.Errorf("authorization failed: %s", desc)
		}
	}
}

// requestDeviceCode makes the initial device code request.
func requestDeviceCode(ctx context.Context) (*deviceCodeResponse, error) {
	body := fmt.Sprintf(`{"client_id":"%s","scope":"%s"}`, clientID, oauthScope)

	req, err := http.NewRequestWithContext(ctx, "POST", deviceCodeURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var dcResp deviceCodeResponse
	if err := json.Unmarshal(respBody, &dcResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if dcResp.DeviceCode == "" || dcResp.UserCode == "" {
		return nil, fmt.Errorf("invalid device code response: %s", string(respBody))
	}

	return &dcResp, nil
}

// pollAccessToken polls for the access token.
func pollAccessToken(ctx context.Context, deviceCode string) (*accessTokenResponse, error) {
	body := fmt.Sprintf(`{"client_id":"%s","device_code":"%s","grant_type":"urn:ietf:params:oauth:grant-type:device_code"}`,
		clientID, deviceCode)

	req, err := http.NewRequestWithContext(ctx, "POST", accessTokenURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var atResp accessTokenResponse
	if err := json.Unmarshal(respBody, &atResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &atResp, nil
}

// ---------------------------------------------------------------------------
// EnsureAuth checks for an existing token and initiates device flow if needed.
// ---------------------------------------------------------------------------

// EnsureAuth returns a valid Copilot access token.
// If a token is already stored, it returns that.
// Otherwise, it initiates the device code flow interactively.
func EnsureAuth(ctx context.Context) (string, error) {
	// Check for existing token
	info := LoadToken()
	if info != nil {
		return info.Access, nil
	}

	// No token — need interactive authentication
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "GitHub Copilot authentication required.")
	fmt.Fprintln(os.Stderr, "Starting device code flow...")
	fmt.Fprintln(os.Stderr, "")

	accessToken, err := DeviceCodeFlow(ctx, func(verificationURI, userCode string) {
		fmt.Fprintln(os.Stderr, "  1. Open this URL in your browser:")
		fmt.Fprintf(os.Stderr, "     %s\n\n", verificationURI)
		fmt.Fprintln(os.Stderr, "  2. Enter this code:")
		fmt.Fprintf(os.Stderr, "     %s\n\n", userCode)
		fmt.Fprintln(os.Stderr, "  Waiting for authorization...")
	})
	if err != nil {
		return "", err
	}

	fmt.Fprintln(os.Stderr, "  Authentication successful!")
	fmt.Fprintln(os.Stderr, "")

	return accessToken, nil
}

// ---------------------------------------------------------------------------
// API request helpers
// ---------------------------------------------------------------------------

// SetAuthHeaders sets the required headers for Copilot API requests.
// This must be called on each request to the Copilot API.
func SetAuthHeaders(req *http.Request, accessToken string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "lokit/1.0")
	req.Header.Set("Openai-Intent", "conversation-edits")
	req.Header.Set("X-Initiator", "user")

	// Remove headers that might interfere
	req.Header.Del("x-api-key")
}
