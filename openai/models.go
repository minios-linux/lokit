package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/minios-linux/lokit/settings"
)

const (
	CodexModelsEndpoint = "https://chatgpt.com/backend-api/codex/models"
	apiModelsEndpoint   = "https://api.openai.com/v1/models"
)

// Model is an account-visible model from the selected authentication endpoint.
// Presence in the catalog is not a recommendation for translation.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// SetOAuthHeaders shares the account identity and client contract between model
// discovery and Responses requests. Model names are always sent unchanged.
func SetOAuthHeaders(req *http.Request, info *settings.Info, clientVersion string) {
	req.Header.Set("Authorization", "Bearer "+info.Access)
	if info.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", info.AccountID)
	}
	req.Header.Set("User-Agent", "lokit")
	req.Header.Set("originator", "lokit")
	req.Header.Set("version", clientVersion)
}

// ListModels queries the live catalog, using API-key or ChatGPT OAuth access.
// It does not reject explicitly selected models merely because a catalog is
// unavailable or incomplete; inference remains the authority for that choice.
func ListModels(ctx context.Context, client *http.Client, apiKey string) ([]Model, error) {
	endpoint := apiModelsEndpoint
	var info *settings.Info
	var clientVersion string
	if apiKey == "" {
		var err error
		info, err = EnsureAuth(ctx)
		if err != nil {
			return nil, err
		}
		clientVersion, err = LatestCodexVersion(ctx, client)
		if err != nil {
			return nil, err
		}
		endpoint = CodexModelsEndpoint + "?" + url.Values{"client_version": {clientVersion}}.Encode()
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if info != nil {
			SetOAuthHeaders(req, info, clientVersion)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("listing OpenAI models: %w", err)
		}
		const limit = 8 * 1024 * 1024
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading OpenAI model catalog: %w", readErr)
		}
		if resp.StatusCode == http.StatusUnauthorized && info != nil && attempt == 0 {
			if err := RefreshAccessToken(ctx, info); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("listing OpenAI models: HTTP %d", resp.StatusCode)
		}
		if len(body) > limit {
			return nil, fmt.Errorf("OpenAI model catalog exceeds %d bytes", limit)
		}
		return parseModels(body, info != nil)
	}
	return nil, fmt.Errorf("OpenAI model catalog authentication failed")
}

func parseModels(body []byte, oauth bool) ([]Model, error) {
	var envelope struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
		} `json:"models"`
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("invalid OpenAI model catalog: %w", err)
	}
	models := envelope.Data
	if oauth {
		if envelope.Models == nil {
			return nil, fmt.Errorf("OpenAI OAuth catalog is missing models")
		}
		models = make([]Model, 0, len(envelope.Models))
		for _, model := range envelope.Models {
			models = append(models, Model{ID: model.Slug, Name: model.DisplayName, Description: model.Description})
		}
	} else if models == nil {
		return nil, fmt.Errorf("OpenAI API catalog is missing data")
	}
	seen := make(map[string]bool)
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" || strings.ContainsAny(model.ID, "\r\n\t") || seen[model.ID] {
			return nil, fmt.Errorf("OpenAI model catalog contains an empty, invalid or duplicate model ID")
		}
		seen[model.ID] = true
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}
