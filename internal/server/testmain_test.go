package server_test

import (
	"os"
	"testing"

	"zymobrew/internal/testutil"
)

// Truncates app tables once the package's tests finish so a dev server
// pointed at the same DATABASE_URL doesn't see leftover users/recipes.
// See testutil.RunWithCleanup for the rationale.
func TestMain(m *testing.M) { os.Exit(testutil.RunWithCleanup(m)) }
