package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	OllamaDefaultBaseURL = "http://localhost:11434"
	ollamaTimeout        = 5 * time.Second
)

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// OllamaModels queries <baseURL>/api/tags and returns the names of the
// locally installed models. An empty baseURL falls back to the default.
func OllamaModels(ctx context.Context, baseURL string) ([]string, error) {
	if baseURL == "" {
		baseURL = OllamaDefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	url := baseURL + "/api/tags"

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, ollamaTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("setup: build ollama request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("setup: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("setup: %s returned status %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("setup: decode ollama tags: %w", err)
	}

	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		if m.Name != "" {
			names = append(names, m.Name)
		}
	}
	return names, nil
}
