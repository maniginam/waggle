package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New generates a short random ID with the "wg-" prefix.
func New() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "wg-" + hex.EncodeToString(b)
}
