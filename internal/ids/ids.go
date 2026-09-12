// Package ids generates cryptographically random paste IDs and delete
// tokens, and hashes delete tokens for storage.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
)

const (
	// IDLen is the length of a paste ID in base62 characters.
	IDLen = 10
	// TokenBytes is the entropy (in bytes) of a delete token; the encoded
	// token is longer (43 chars base64url).
	TokenBytes = 32
)

// alphabet is the base62 character set.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// reserved lists route words that must never collide with a paste ID.
// The router matches exact routes before the /{id} pattern, but the
// generator also refuses these defensively.
var reserved = map[string]struct{}{
	"healthz":     {},
	"robots.txt":  {},
	"favicon.ico": {},
	"static":      {},
	"raw":         {},
	"delete":      {},
}

// ErrReserved is returned when a generated ID collides with a route word.
var ErrReserved = errors.New("generated id collides with a reserved route word")

// idPattern is the shape of every paste ID.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{10}$`)

// ValidID reports whether id is shaped like a paste ID. Callers treat any
// non-matching path value as unknown (404).
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// IsReserved reports whether id collides with a route word.
func IsReserved(id string) bool {
	_, ok := reserved[id]
	return ok
}

// NewID returns a random 10-character base62 paste ID.
func NewID() (string, error) {
	// Rejection sampling: 62 * 4 = 248, so bytes >= 248 are discarded to
	// avoid modulo bias.
	const max = 248
	out := make([]byte, IDLen)
	buf := make([]byte, 1)
	for i := 0; i < IDLen; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if buf[0] >= max {
			continue
		}
		id := alphabet[int(buf[0])%len(alphabet)]
		out[i] = id
		i++
	}
	id := string(out)
	if IsReserved(id) {
		return "", ErrReserved
	}
	return id, nil
}

// NewDeleteToken returns a random delete token with >=32 bytes of entropy.
// The plaintext token is shown to the creator exactly once; only its hash is
// ever stored.
func NewDeleteToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the hex-encoded SHA-256 hash of a delete token.
// Verification compares hashes in constant time.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
