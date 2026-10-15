package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sync"
)

const codexLatestReleaseURL = "https://api.github.com/repos/openai/codex/releases/latest"

var codexReleaseTag = regexp.MustCompile(`^rust-v([0-9]+\.[0-9]+\.[0-9]+)$`)
var codexVersionCache struct {
	sync.Mutex
	version string
}

// LatestCodexVersion obtains the stable client version from the official Codex
// GitHub release. The catalog gates models by this version; lokit's own version
// or a frozen Codex version would hide new model families. Share one successful
// lookup within the process, including across parallel translation workers.
// No OAuth credentials are sent to GitHub and no guessed fallback is used.
func LatestCodexVersion(ctx context.Context, client *http.Client) (string, error) {
	codexVersionCache.Lock()
	defer codexVersionCache.Unlock()
	if codexVersionCache.version != "" {
		return codexVersionCache.version, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexLatestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "lokit")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("getting latest Codex release from GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("getting latest Codex release from GitHub: HTTP %d", resp.StatusCode)
	}
	const limit = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("reading Codex release: %w", err)
	}
	if len(body) > limit {
		return "", fmt.Errorf("Codex release response is too large")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("invalid Codex release response: %w", err)
	}
	match := codexReleaseTag.FindStringSubmatch(release.Tag)
	if release.Draft || release.Prerelease || match == nil {
		return "", fmt.Errorf("GitHub did not return a stable Codex rust-vMAJOR.MINOR.PATCH release")
	}
	codexVersionCache.version = match[1]
	return match[1], nil
}
