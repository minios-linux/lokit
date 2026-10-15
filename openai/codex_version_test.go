package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func resetVersionCache(t *testing.T) {
	t.Helper()
	clear := func() {
		codexVersionCache.Lock()
		codexVersionCache.version = ""
		codexVersionCache.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func releaseResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func TestLatestCodexVersionFromGitHubIsSharedByWorkers(t *testing.T) {
	resetVersionCache(t)
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		if req.URL.String() != "https://api.github.com/repos/openai/codex/releases/latest" {
			t.Errorf("unexpected release source: %s", req.URL)
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("ChatGPT-Account-Id") != "" {
			t.Error("OpenAI credentials must not be sent to GitHub")
		}
		return releaseResponse(`{"tag_name":"rust-v9.8.7","draft":false,"prerelease":false}`), nil
	})}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := LatestCodexVersion(context.Background(), client)
			if err != nil || version != "9.8.7" {
				t.Errorf("latest version = %q, %v", version, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("GitHub queried %d times", calls.Load())
	}
}

func TestLatestCodexVersionDoesNotGuessOnInvalidRelease(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"rust-v1.2.3","draft":true}`,
		`{"tag_name":"rust-v1.2.3","prerelease":true}`,
		`{"tag_name":"rust-v1.2.3-beta.1"}`,
		`{"tag_name":"unknown"}`, `{}`, `{`,
	} {
		t.Run(body, func(t *testing.T) {
			resetVersionCache(t)
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return releaseResponse(body), nil
			})}
			version, err := LatestCodexVersion(context.Background(), client)
			if err == nil || version != "" {
				t.Fatalf("guessed version = %q, %v", version, err)
			}
		})
	}
}

func TestGitHubFailureIsNotCachedAsAClientVersion(t *testing.T) {
	resetVersionCache(t)
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("rate limited"))}, nil
		}
		return releaseResponse(`{"tag_name":"rust-v9.9.9"}`), nil
	})}
	if version, err := LatestCodexVersion(context.Background(), client); version != "" || err == nil {
		t.Fatalf("HTTP error produced a version: %q, %v", version, err)
	}
	if version, err := LatestCodexVersion(context.Background(), client); version != "9.9.9" || err != nil {
		t.Fatalf("failed to recover release lookup: %q, %v", version, err)
	}
}
