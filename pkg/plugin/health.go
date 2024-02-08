package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/build"
)

// Define models for each provider to be included in the health check.
var providerModels = map[string][]string{
	"openai": {"gpt-3.5-turbo", "gpt-4"},
	"pulze":  {"pulze", "openai/gpt-4"},
}

type healthCheckClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type openAIModelHealth struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type openAIHealthDetails struct {
	Configured bool                         `json:"configured"`
	OK         bool                         `json:"ok"`
	Error      string                       `json:"error,omitempty"`
	Models     map[string]openAIModelHealth `json:"models"`
}

type vectorHealthDetails struct {
	Enabled bool   `json:"enabled"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type healthCheckDetails struct {
	OpenAI  openAIHealthDetails `json:"openAI"`
	Vector  vectorHealthDetails `json:"vector"`
	Version string              `json:"version"`
}

func getVersion() string {
	buildInfo, err := build.GetBuildInfo()
	if err != nil {
		return "unknown"
	}
	return buildInfo.Version
}

func (a *App) testOpenAIModel(ctx context.Context, model string, tenant string) error {
	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": "Hello",
			},
		},
	}
	req, err := a.newOpenAIChatCompletionsRequest(ctx, body, tenant)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := a.healthCheckClient.Do(req)
	if err != nil {
		return fmt.Errorf("make request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		return fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// openAIHealth performs a health check for the selected provider and caches the
// result if successful. The caller must lock a.healthCheckMutex.
func (a *App) openAIHealth(ctx context.Context, req *backend.CheckHealthRequest) openAIHealthDetails {
	if a.healthOpenAI != nil {
		return *a.healthOpenAI
	}

	d := openAIHealthDetails{
		OK:         true,
		Configured: a.settings.OpenAI.apiKey != "",
		Models:     map[string]openAIModelHealth{},
	}
	models := providerModels["openai"]
	if a.settings.OpenAI.Provider == openAIProviderPulze {
		models = providerModels["pulze"]
	}

	for _, model := range models {
		health := openAIModelHealth{OK: false, Error: "OpenAI not configured"}
		if d.Configured {
			health.OK = true
			health.Error = ""
			err := a.testOpenAIModel(ctx, model, a.settings.Tenant)
			if err != nil {
				health.OK = false
				health.Error = err.Error()
			}
		}
		d.Models[model] = health
	}
	anyOK := false
	for _, v := range d.Models {
		if v.OK {
			anyOK = true
			break
		}
	}
	if !anyOK {
		d.OK = false
		d.Error = "No models are working"
	}

	// Only cache result if openAI is ok to use.
	if d.OK {
		a.healthOpenAI = &d
	}
	return d
}

// testVectorService checks the health of VectorAPI and caches the result if
// successful. The caller must lock a.healthCheckMutex.
func (a *App) testVectorService(ctx context.Context) error {
	if a.vectorService == nil {
		return fmt.Errorf("vector service not configured")
	}
	err := a.vectorService.Health(ctx)
	if err != nil {
		return fmt.Errorf("vector service health check failed: %w", err)
	}
	return nil
}

// vectorHealth performs a health check for the Vector service and caches the
// result if successful. The caller must lock a.healthCheckMutex.
func (a *App) vectorHealth(ctx context.Context) vectorHealthDetails {
	if a.healthVector != nil {
		return *a.healthVector
	}

	d := vectorHealthDetails{
		Enabled: a.settings.Vector.Enabled,
		OK:      true,
	}
	if !d.Enabled {
		d.OK = false
		return d
	}
	err := a.testVectorService(ctx)
	if err != nil {
		d.OK = false
		d.Error = err.Error()
	}

	// Only cache if the health check succeeded.
	if d.OK {
		a.healthVector = &d
	}
	return d
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// It returns whether each feature is working based on the plugin settings.
func (a *App) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	a.healthCheckMutex.Lock()
	defer a.healthCheckMutex.Unlock()

	openAI := a.openAIHealth(ctx, req)
	if openAI.Error == "" {
		a.healthOpenAI = &openAI
	}

	vector := a.vectorHealth(ctx)
	if vector.Error == "" {
		a.healthVector = &vector
	}

	details := healthCheckDetails{
		OpenAI:  openAI,
		Vector:  vector,
		Version: getVersion(),
	}
	body, err := json.Marshal(details)
	if err != nil {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "failed to marshal details",
		}, nil
	}
	return &backend.CheckHealthResult{
		Status:      backend.HealthStatusOk,
		JSONDetails: body,
	}, nil
}
