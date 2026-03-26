package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New generates a random ID with the "wg-" prefix.
// Uses 6 bytes (12 hex chars) for ~281 trillion possible values.
func New() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "wg-" + hex.EncodeToString(b)
}
