package id

import (
	"crypto/rand"
	"encoding/hex"
)

func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback shape only; rand.Read should not fail in normal operation
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b)
}
