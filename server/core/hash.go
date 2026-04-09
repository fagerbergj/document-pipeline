package core

import (
	"crypto/sha256"
	"fmt"
)

// ContentHash returns the hex-encoded SHA-256 hash of b.
func ContentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum)
}
