package bullmq

import (
	"crypto/rand"
	"encoding/hex"
)

// generateRandomHex generates a random hex string of specified length
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to timestamp-based if crypto/rand fails (should never happen)
		return "000000"
	}
	return hex.EncodeToString(bytes)
}
