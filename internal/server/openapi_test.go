package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"zymobrew/internal/config"
)

// TestOpenAPICoversAllRoutes walks the chi router and asserts that every
// registered (METHOD, path) pair appears in openapi.yaml. Fails when a new
// route is added without updating the spec.
func TestOpenAPICoversAllRoutes(t *testing.T) {
	s := New(nil, config.Config{InstanceMode: config.ModeOpen}, nil)
	router, ok := s.handler.(chi.Routes)
	if !ok {
		t.Fatal("handler does not implement chi.Routes")
	}

	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}

	// Build "METHOD /path" keys from the spec.
	inSpec := make(map[string]bool)
	for path, methods := range spec.Paths {
		for method := range methods {
			inSpec[strings.ToUpper(method)+" "+path] = true
		}
	}

	var missing []string
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi reports sub-router roots with trailing slashes ("/api/batches/");
		// the spec uses clean paths without them.
		normalized := strings.TrimRight(route, "/")
		if normalized == "" {
			normalized = "/"
		}
		if !inSpec[method+" "+normalized] {
			missing = append(missing, method+" "+route)
		}
		return nil
	})

	if len(missing) > 0 {
		t.Errorf("routes not documented in openapi.yaml — add them or update the spec:\n  %s",
			strings.Join(missing, "\n  "))
	}
}
