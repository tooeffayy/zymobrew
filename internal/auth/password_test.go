package auth_test

import (
	"errors"
	"strings"
	"testing"

	"zymobrew/internal/auth"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash does not look like argon2id PHC: %q", hash)
	}
	if err := auth.VerifyPassword("correct horse battery staple", hash); err != nil {
		t.Fatalf("VerifyPassword (correct): %v", err)
	}
	if err := auth.VerifyPassword("wrong password", hash); !errors.Is(err, auth.ErrPasswordMismatch) {
		t.Fatalf("VerifyPassword (wrong) = %v, want ErrPasswordMismatch", err)
	}
}

func TestHashesAreUnique(t *testing.T) {
	a, err := auth.HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of the same password must differ (different salts)")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	cases := []string{"", "plain", "$argon2id$v=19$$$", "$bcrypt$xxxxx"}
	for _, c := range cases {
		if err := auth.VerifyPassword("anything", c); err == nil {
			t.Errorf("expected error for malformed hash %q", c)
		}
	}
}
