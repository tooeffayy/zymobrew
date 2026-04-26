package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// SessionTokenBytes is the raw entropy length of a session token (32 bytes
// → 256 bits, encoded as 43-char URL-safe base64).
const SessionTokenBytes = 32

// NewSessionToken returns (raw, hash) where raw is the token sent to the
// client and hash is what gets stored in sessions.token_hash. Callers should
// never store raw alongside the hash.
func NewSessionToken() (raw, hash string, err error) {
	b := make([]byte, SessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	hash = HashSessionToken(raw)
	return raw, hash, nil
}

// HashSessionToken returns the SHA-256 of the raw token, base64-encoded.
// Used by the auth middleware to look up sessions.
func HashSessionToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
