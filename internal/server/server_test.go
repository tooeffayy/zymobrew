package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

func newServerNoDB(t *testing.T) *server.Server {
	t.Helper()
	return server.New(nil, config.Config{InstanceMode: config.ModeOpen})
}

func TestHealthz(t *testing.T) {
	srv := newServerNoDB(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestReadyzWithoutDB(t *testing.T) {
	srv := newServerNoDB(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestReadyzWithDB(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	srv := server.New(pool, config.Config{InstanceMode: config.ModeOpen})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
