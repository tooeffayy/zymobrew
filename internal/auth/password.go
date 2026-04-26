// Package auth implements the cryptographic primitives behind authentication:
// argon2id password hashing/verification and opaque session token generation.
// HTTP wiring (middleware, handlers, cookie management) lives in the server
// package — this package is intentionally framework-free.
//
// Passwords are stored as argon2id PHC strings on users.password_hash.
// Sessions are random 32-byte tokens; the SHA-256 of the token is what
// lives in sessions.token_hash, so a DB leak does not yield usable tokens.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// ErrPasswordMismatch is returned by VerifyPassword when the password does
// not match the stored hash.
var ErrPasswordMismatch = errors.New("password mismatch")

// DummyHash returns a stable, valid argon2id PHC hash used to equalise login
// timing when a user lookup fails. Login handlers should run VerifyPassword
// against either the real hash or DummyHash so a network observer cannot
// distinguish "user does not exist" from "user exists, password wrong".
//
// The hashed value is a random throwaway; nobody should ever match it.
var DummyHash = sync.OnceValue(func() string {
	h, err := HashPassword("zymo-timing-equaliser-not-a-real-password")
	if err != nil {
		// HashPassword can only fail if crypto/rand fails, which on supported
		// platforms is fatal-grade; panicking here is the right call.
		panic("auth: failed to compute dummy hash: " + err.Error())
	}
	return h
})

type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLen     uint32
	keyLen      uint32
}

// OWASP-recommended argon2id baseline (2024): m=64MB, t=1, p=4.
var defaultArgonParams = argon2Params{
	memory:      64 * 1024,
	iterations:  1,
	parallelism: 4,
	saltLen:     16,
	keyLen:      32,
}

// HashPassword returns a PHC-formatted argon2id hash for the given password.
func HashPassword(password string) (string, error) {
	p := defaultArgonParams
	salt := make([]byte, p.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.memory, p.iterations, p.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword compares a candidate password against a PHC-formatted hash.
// Returns ErrPasswordMismatch on a clean mismatch; other errors indicate a
// malformed hash.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	// Expected: ["", "argon2id", "v=19", "m=...,t=...,p=...", "<salt>", "<hash>"]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("invalid hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}
	if version != argon2.Version {
		return fmt.Errorf("unsupported argon2 version %d", version)
	}
	var p argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.iterations, &p.parallelism); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("invalid salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("invalid hash: %w", err)
	}
	actual := argon2.IDKey([]byte(password), salt, p.iterations, p.memory, p.parallelism, uint32(len(expected)))
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}
