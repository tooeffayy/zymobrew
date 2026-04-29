package selftest_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/selftest"
	"zymobrew/internal/testutil"
)

func TestSelftestPassesAgainstFreshDB(t *testing.T) {
	ctx := context.Background()
	_ = testutil.Pool(t, ctx) // ensures schema is migrated; skips if no DB

	cfg := config.Config{
		DatabaseURL:      os.Getenv("TEST_DATABASE_URL"),
		ListenAddr:       ":0",
		StorageBackend:   "local",
		StorageLocalPath: t.TempDir(),
	}

	var buf bytes.Buffer
	if err := selftest.Run(ctx, cfg, &buf); err != nil {
		t.Fatalf("selftest failed: %v\nreport:\n%s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"connect", "ping", "migrations", "schema", "roundtrip", "storage"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing check %q in report:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL") {
		t.Fatalf("report contains FAIL:\n%s", out)
	}
}

func TestSelftestReportsBadDB(t *testing.T) {
	cfg := config.Config{DatabaseURL: "postgres://nobody:nope@127.0.0.1:1/nope?sslmode=disable&connect_timeout=1"}
	var buf bytes.Buffer
	err := selftest.Run(context.Background(), cfg, &buf)
	if err == nil {
		t.Fatalf("expected failure, got nil; report:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "FAIL") {
		t.Fatalf("report should contain FAIL:\n%s", buf.String())
	}
}
