package queries_test

import (
	"os"
	"testing"

	"zymobrew/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.RunWithCleanup(m)) }
