package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestListModelsUsesAccountCatalogAndProtocolVersion(t *testing.T) {
	resetVersionCache(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if err := SaveToken("test-token", "refresh", time.Now().Add(time.Hour).Unix(), "test-account"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == codexLatestReleaseURL {
			return releaseResponse(`{"tag_name":"rust-v0.162.1","draft":false,"prerelease":false}`), nil
		}
		if req.URL.String() != CodexModelsEndpoint+"?client_version=0.162.1" {
			t.Fatalf("unexpected catalog URL: %s", req.URL)
		}
		if req.Header.Get("Authorization") != "Bearer test-token" || req.Header.Get("ChatGPT-Account-Id") != "test-account" {
			t.Fatal("catalog lost OAuth account identity")
		}
		if req.Header.Get("version") != "0.162.1" || req.Header.Get("originator") != "lokit" {
			t.Fatal("catalog lost client contract headers")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"models":[
			{"slug":"gpt-6-text","display_name":"Future text model","description":"Text"},
			{"slug":"gpt-reserve"},{"slug":"codex-auto-review"},{"slug":"o-future"}]}`))}, nil
	})}
	models, err := ListModels(context.Background(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 4 || models[1].ID != "gpt-6-text" || models[1].Name != "Future text model" || models[3].ID != "o-future" {
		t.Fatalf("model names were filtered or substituted: %#v", models)
	}
}

func TestListModelsAPIKeyUsesPublicEndpoint(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != apiModelsEndpoint || req.Header.Get("Authorization") != "Bearer api-key" {
			t.Fatal("wrong API-key request")
		}
		if req.Header.Get("ChatGPT-Account-Id") != "" || req.URL.RawQuery != "" {
			t.Fatal("OAuth fields leaked into API-key request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"o3"},{"id":"gpt-future"}]}`))}, nil
	})}
	models, err := ListModels(context.Background(), client, "api-key")
	if err != nil || len(models) != 2 || models[1].ID != "o3" {
		t.Fatalf("API-key catalog: %#v, %v", models, err)
	}
}

func TestModelCatalogRejectsAmbiguousOrMalformedData(t *testing.T) {
	for _, test := range []struct {
		body  string
		oauth bool
	}{
		{`{"models":[{"slug":""}]}`, true},
		{`{"models":[{"slug":"same"},{"slug":"same"}]}`, true},
		{`{"models":[{"slug":"line\nbreak"}]}`, true},
		{`{"data":[]}`, true},
		{`{"models":[]}`, false},
		{`{`, true},
	} {
		if _, err := parseModels([]byte(test.body), test.oauth); err == nil {
			t.Fatalf("accepted malformed catalog: %s", test.body)
		}
	}
}

func TestCatalogHTTPErrorDoesNotBecomeFallbackModel(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`private diagnostic`))}, nil
	})}
	models, err := ListModels(context.Background(), client, "api-key")
	if err == nil || models != nil || strings.Contains(err.Error(), "private diagnostic") {
		t.Fatalf("catalog failure returned models or raw response: %#v, %v", models, err)
	}
}
