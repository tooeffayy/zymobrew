package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
)

// staticServer builds a Server with a nil DB pool — the static handler
// touches neither queries nor storage.
func staticServer() *server.Server {
	return server.New(nil, config.Config{InstanceMode: config.ModeOpen}, nil, nil)
}

func httpGet(srv *server.Server, path string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Result()
}

// In the test environment dist/ contains only .gitkeep, so the
// placeholder handler is what we exercise. The real-build path is
// exercised by the production binary; testing it would require copying
// a synthetic index.html into the embed FS, which embed doesn't
// support at runtime.
func TestStatic_PlaceholderServedAtRoot(t *testing.T) {
	srv := staticServer()
	resp := httpGet(srv, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type %q, want text/html", ct)
	}
}

func TestStatic_PlaceholderServedForSPAPath(t *testing.T) {
	// Deep-link to a client-side route — must still return the
	// placeholder (or, in production, index.html), not a 404.
	srv := staticServer()
	resp := httpGet(srv, "/login")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
}

// API 404s must stay JSON even though they fall through to the static
// handler — clients rely on the {"error": "..."} shape.
func TestStatic_APINotFoundIsJSON(t *testing.T) {
	srv := staticServer()
	resp := httpGet(srv, "/api/this/does/not/exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type %q, want application/json", ct)
	}
}

// Existing API routes still take precedence over the SPA fallback.
func TestStatic_HealthzStillWorks(t *testing.T) {
	srv := staticServer()
	resp := httpGet(srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type %q, want application/json", ct)
	}
}
