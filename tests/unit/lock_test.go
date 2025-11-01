package unit

import (
	"strings"
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

func TestNewLockToken_Format(t *testing.T) {
	token := bullmq.NewLockToken()
	tokenStr := token.String()

	// UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (36 characters)
	assert.Len(t, tokenStr, 36, "Lock token should be 36 characters (UUID format)")

	// Should contain hyphens
	assert.Equal(t, 4, strings.Count(tokenStr, "-"),
		"UUID should contain 4 hyphens")

	// Version field (3rd group) should start with '4' (UUID v4)
	parts := strings.Split(tokenStr, "-")
	assert.Equal(t, "4", string(parts[2][0]),
		"UUID version should be 4 (random)")
}

func TestNewLockToken_Uniqueness(t *testing.T) {
	// Generate 1000 tokens
	tokens := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		token := bullmq.NewLockToken()
		tokens[token.String()] = true
	}

	// All tokens should be unique
	assert.Len(t, tokens, 1000,
		"All 1000 generated lock tokens should be unique")
}

func TestLockToken_String(t *testing.T) {
	token := bullmq.NewLockToken()
	str1 := token.String()
	str2 := token.String()

	// Same token should return same string
	assert.Equal(t, str1, str2,
		"String() should return consistent value")
}
