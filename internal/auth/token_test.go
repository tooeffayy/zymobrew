package auth_test

import (
	"testing"

	"zymobrew/internal/auth"
)

func TestNewSessionToken(t *testing.T) {
	raw, hash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || hash == "" {
		t.Fatalf("empty raw or hash: %q / %q", raw, hash)
	}
	if raw == hash {
		t.Fatal("raw token and hash must differ")
	}
	if got := auth.HashSessionToken(raw); got != hash {
		t.Fatalf("HashSessionToken(raw) = %q, want %q", got, hash)
	}
}

func TestTokensAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		raw, _, err := auth.NewSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[raw] {
			t.Fatalf("collision after %d iterations", i)
		}
		seen[raw] = true
	}
}
