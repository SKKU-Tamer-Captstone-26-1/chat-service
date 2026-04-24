package id

import (
	"crypto/rand"
	"fmt"
)

func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback shape only; rand.Read should not fail in normal operation
		return "00000000-0000-0000-0000-000000000000"
	}

	// UUIDv4 variant bits for interoperable canonical formatting.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}
