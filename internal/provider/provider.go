// Package provider describes the services hunch can send System One requests
// to. TypeSafe serves the API itself; gateways such as OpenRouter and OpenCode
// Zen serve the same API under their own base URL, keys and model names.
//
// Adding a gateway that speaks the System One API is one entry in registry.
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yarolegovich/hunch/internal/typesafe"
)

// Provider is one service that serves the System One API.
type Provider struct {
	// ID is the value used in --provider and the config file.
	ID   string
	Name string
	// BaseURL is the API root; requests go to BaseURL + "/v1/systemone".
	BaseURL string
	// KeyEnv is the environment variable holding the provider's API key.
	KeyEnv string
	// KeyURL is where users create a key.
	KeyURL string
	// Models are the model names to suggest, the default first.
	Models []string
	// Headers are sent with every request.
	Headers map[string]string

	// modelsURL lists models; filter keeps the ones that serve System One.
	modelsURL string
	filter    func(id string) bool
}

// DefaultModel is the model used when none is configured.
func (p *Provider) DefaultModel() string { return p.Models[0] }

var (
	TypeSafe = &Provider{
		ID:      "typesafe",
		Name:    "TypeSafe",
		BaseURL: typesafe.DefaultBaseURL,
		KeyEnv:  typesafe.EnvAPIKey,
		KeyURL:  "https://console.typesafe.ai/",
		Models:  []string{"jev-latest", "jev-preview"},
	}

	// OpenRouter: https://docs.typesafe.ai/sdk/python/usage/basic#configuring-the-base-url
	OpenRouter = &Provider{
		ID:        "openrouter",
		Name:      "OpenRouter",
		BaseURL:   "https://openrouter.ai/api",
		KeyEnv:    "OPENROUTER_API_KEY",
		KeyURL:    "https://openrouter.ai/settings/keys",
		Models:    []string{"~typesafe/jev-latest"},
		Headers:   map[string]string{"HTTP-Referer": "https://github.com/yarolegovich/hunch", "X-Title": "hunch"},
		modelsURL: "https://openrouter.ai/api/v1/models",
		filter:    func(id string) bool { return strings.Contains(id, "typesafe/") },
	}

	// OpenCode Zen: https://opencode.ai/docs/zen/
	Zen = &Provider{
		ID:        "zen",
		Name:      "OpenCode Zen",
		BaseURL:   "https://opencode.ai/inference/systemone",
		KeyEnv:    "OPENCODE_API_KEY",
		KeyURL:    "https://opencode.ai/console",
		Models:    []string{"jev-1.13", "jev-1.13-free"},
		modelsURL: "https://opencode.ai/inference/v1/models",
		filter:    func(id string) bool { return strings.HasPrefix(id, "jev") },
	}

	registry = []*Provider{TypeSafe, OpenRouter, Zen}
)

// All returns every provider in display order.
func All() []*Provider { return registry }

// IDs returns every provider ID.
func IDs() []string {
	ids := make([]string, len(registry))
	for i, p := range registry {
		ids[i] = p.ID
	}
	return ids
}

// Lookup finds a provider by ID.
func Lookup(id string) (*Provider, error) {
	for _, p := range registry {
		if p.ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider %q (want %s)", id, strings.Join(IDs(), ", "))
}

// Endpoint is a provider with the settings of one run.
type Endpoint struct {
	*Provider
	Key   string
	Model string
	// URL overrides BaseURL when set.
	URL string
}

func (e *Endpoint) baseURL() string {
	if e.URL != "" {
		return strings.TrimRight(e.URL, "/")
	}
	return e.BaseURL
}

// Client returns a System One client for the endpoint.
func (e *Endpoint) Client(opts ...typesafe.Option) *typesafe.Client {
	all := []typesafe.Option{
		typesafe.WithAPIKey(e.Key),
		typesafe.WithBaseURL(e.baseURL()),
		typesafe.WithModel(e.Model),
	}
	for k, v := range e.Headers {
		all = append(all, typesafe.WithHeader(k, v))
	}
	return typesafe.NewClient(append(all, opts...)...)
}

// ListModels returns the System One models the provider offers.
func (e *Endpoint) ListModels(ctx context.Context) ([]typesafe.Model, error) {
	if e.modelsURL == "" {
		return e.Client().ListModels(ctx)
	}
	url := e.modelsURL
	if e.URL != "" {
		url = e.baseURL() + "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if e.Key != "" {
		req.Header.Set("Authorization", "Bearer "+e.Key)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &typesafe.APIError{StatusCode: resp.StatusCode, Body: body, Header: resp.Header}
	}
	return parseModels(body, e.filter)
}

// parseModels reads either the System One shape ({"models": [...]}) or the
// OpenAI-style shape gateways use ({"data": [{"id": ...}]}).
func parseModels(body []byte, keep func(string) bool) ([]typesafe.Model, error) {
	var v struct {
		Models []typesafe.Model `json:"models"`
		Data   []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Created     int64  `json:"created"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	out := v.Models
	for _, d := range v.Data {
		m := typesafe.Model{Name: d.ID, Description: d.Description}
		if d.Created > 0 {
			m.ReleaseDate = time.Unix(d.Created, 0).UTC().Format(time.DateOnly)
		}
		out = append(out, m)
	}
	if keep == nil {
		return out, nil
	}
	kept := out[:0]
	for _, m := range out {
		if keep(m.Name) {
			kept = append(kept, m)
		}
	}
	return kept, nil
}
