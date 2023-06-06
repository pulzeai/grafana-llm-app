package plugin

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

func newOpenAIProxy() http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			config := httpadapter.PluginConfigFromContext(r.In.Context())
			settings := loadSettings(*config.AppInstanceSettings)
			apiKey := config.AppInstanceSettings.DecryptedSecureJSONData["apiKey"]
			u, _ := url.Parse(settings.OpenAIURL)
			r.SetURL(u)
			r.Out.Header.Set("Authorization", "Bearer "+apiKey)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/openai")
		},
	}
}

// registerRoutes takes a *http.ServeMux and registers some HTTP handlers.
func (a *App) registerRoutes(mux *http.ServeMux) {
	mux.Handle("/openai/", newOpenAIProxy())
}
