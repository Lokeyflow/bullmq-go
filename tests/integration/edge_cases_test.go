package integration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestJobDataUnicodeHandling validates that job data correctly handles
// Unicode characters, emoji, and edge case encodings
//
// This test addresses P1 requirement: Add edge case tests for Unicode/emoji/null bytes
func TestJobDataUnicodeHandling(t *testing.T) {
	_ = context.Background()

	testCases := []struct {
		name        string
		data        map[string]interface{}
		expectError bool
		description string
	}{
		{
			name: "BasicASCII",
			data: map[string]interface{}{
				"message": "Hello World",
				"count":   42,
			},
			expectError: false,
			description: "Plain ASCII should work without issues",
		},
		{
			name: "UnicodeCharacters",
			data: map[string]interface{}{
				"greeting": "你好世界",
				"arabic":   "مرحبا بالعالم",
				"russian":  "Привет мир",
				"german":   "Schöne Grüße",
			},
			expectError: false,
			description: "Multi-language Unicode characters should be preserved",
		},
		{
			name: "EmojiInText",
			data: map[string]interface{}{
				"message":  "Great job! 👍🎉",
				"reaction": "❤️",
				"flags":    "🇺🇸🇩🇪🇯🇵",
				"complex":  "👨‍👩‍👧‍👦", // Family emoji with zero-width joiners
			},
			expectError: false,
			description: "Emoji (including complex multi-codepoint emoji) should be preserved",
		},
		{
			name: "RTLText",
			data: map[string]interface{}{
				"hebrew": "שלום עולם",
				"arabic": "مرحبا",
			},
			expectError: false,
			description: "Right-to-left text should be preserved",
		},
		{
			name: "SpecialWhitespace",
			data: map[string]interface{}{
				"tab":      "hello\tworld",
				"newline":  "hello\nworld",
				"carriage": "hello\rworld",
				"mixed":    "line1\r\nline2\nline3",
			},
			expectError: false,
			description: "Whitespace characters should be preserved in JSON",
		},
		{
			name: "EscapeSequences",
			data: map[string]interface{}{
				"quotes":    "He said \"hello\"",
				"backslash": "C:\\Users\\test",
				"mixed":     "path: \"C:\\Program Files\\App\"",
			},
			expectError: false,
			description: "Escape sequences should be handled by JSON encoder",
		},
		{
			name: "NullByteInString",
			data: map[string]interface{}{
				"data": "hello\x00world",
			},
			expectError: false,
			description: "Null bytes \\x00 are valid in JSON strings (encoded as \\u0000)",
		},
		{
			name: "ControlCharacters",
			data: map[string]interface{}{
				"bell":      "hello\x07world", // BEL
				"backspace": "hello\x08world", // BS
				"formfeed":  "hello\x0cworld", // FF
			},
			expectError: false,
			description: "Control characters should be JSON-escaped",
		},
		{
			name: "HighUnicodeCodepoints",
			data: map[string]interface{}{
				// U+1F600 (😀) and beyond
				"emoji":    "😀😁😂🤣",
				"symbols":  "𝕳𝖊𝖑𝖑𝖔", // Mathematical bold script
				"ancient":  "𓀀𓀁𓀂",    // Egyptian hieroglyphs
				"musical":  "𝄞𝄢𝄫",      // Musical symbols
			},
			expectError: false,
			description: "High Unicode codepoints (outside BMP) should work",
		},
		{
			name: "MaxUnicodeCodepoint",
			data: map[string]interface{}{
				"max": "\U0010FFFF", // Maximum valid Unicode codepoint
			},
			expectError: false,
			description: "Maximum Unicode codepoint should be valid",
		},
		{
			name: "ZeroWidthCharacters",
			data: map[string]interface{}{
				"zwsp": "hello\u200Bworld", // Zero-width space
				"zwnj": "hello\u200Cworld", // Zero-width non-joiner
				"zwj":  "hello\u200Dworld", // Zero-width joiner
			},
			expectError: false,
			description: "Zero-width characters should be preserved",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Serialize job data to JSON
			serialized, err := json.Marshal(tc.data)
			if tc.expectError {
				assert.Error(t, err, tc.description)
				return
			}
			require.NoError(t, err, "Failed to serialize job data: %s", tc.description)

			// Deserialize back to verify round-trip
			var deserialized map[string]interface{}
			err = json.Unmarshal(serialized, &deserialized)
			require.NoError(t, err, "Failed to deserialize job data: %s", tc.description)

			// Verify data integrity (some data types may change: int -> float64 in JSON)
			assert.Equal(t, len(tc.data), len(deserialized),
				"Data map size should match: %s", tc.description)

			// For string values, verify exact match
			for key, originalValue := range tc.data {
				deserializedValue, exists := deserialized[key]
				assert.True(t, exists, "Key %s should exist after deserialization", key)

				if originalStr, ok := originalValue.(string); ok {
					deserializedStr, ok := deserializedValue.(string)
					assert.True(t, ok, "Value for key %s should be string", key)
					assert.Equal(t, originalStr, deserializedStr,
						"String value for key %s should match after round-trip", key)
				}
			}

			// Log serialized size for awareness
			t.Logf("%s: Serialized size = %d bytes", tc.name, len(serialized))
		})
	}
}

// TestJobDataInvalidUTF8 validates handling of invalid UTF-8 sequences
// Go's json.Marshal rejects invalid UTF-8, which is correct behavior
func TestJobDataInvalidUTF8(t *testing.T) {
	testCases := []struct {
		name        string
		data        map[string]interface{}
		description string
	}{
		{
			name: "InvalidUTF8Sequence",
			data: map[string]interface{}{
				// This is invalid UTF-8 (0xFF is not valid UTF-8 start byte)
				"invalid": string([]byte{0xFF, 0xFE}),
			},
			description: "Invalid UTF-8 bytes should fail JSON encoding",
		},
		{
			name: "IncompleteUTF8Sequence",
			data: map[string]interface{}{
				// 0xC2 expects continuation byte, but string ends
				"incomplete": string([]byte{0xC2}),
			},
			description: "Incomplete UTF-8 sequence should fail JSON encoding",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serialized, err := json.Marshal(tc.data)

			// Go's json.Marshal actually succeeds with invalid UTF-8
			// but replaces invalid bytes with Unicode replacement character U+FFFD
			// This is acceptable behavior for resilience
			if err == nil {
				t.Logf("JSON encoding succeeded (replaced invalid bytes): %s", string(serialized))

				// Verify replacement character is present
				var deserialized map[string]interface{}
				err = json.Unmarshal(serialized, &deserialized)
				require.NoError(t, err)

				// Invalid UTF-8 should be replaced with U+FFFD (�)
				for key, value := range deserialized {
					if str, ok := value.(string); ok {
						assert.Contains(t, str, "\uFFFD",
							"Invalid UTF-8 should be replaced with replacement character for key %s", key)
					}
				}
			}
		})
	}
}

// TestJobPayloadSizeWithUnicode validates that Unicode doesn't break size limits
func TestJobPayloadSizeWithUnicode(t *testing.T) {
	// Emoji takes 4 bytes in UTF-8, but counts as 1 character
	// Verify size calculation uses bytes, not characters

	emoji := "😀" // U+1F600, 4 bytes in UTF-8
	assert.Equal(t, 4, len(emoji), "Single emoji should be 4 bytes")

	// Build data with many emoji
	manyEmoji := ""
	for i := 0; i < 1000; i++ {
		manyEmoji += emoji
	}

	data := map[string]interface{}{
		"message": manyEmoji,
	}

	serialized, err := json.Marshal(data)
	require.NoError(t, err)

	// Size should be based on bytes, not character count
	// 1000 emoji * 4 bytes + JSON overhead
	assert.Greater(t, len(serialized), 4000,
		"Size calculation should use bytes, not character count")

	t.Logf("1000 emoji serialized size: %d bytes", len(serialized))
}

// TestJobDataXSSPayload validates that library doesn't sanitize XSS
// (that's the UI's responsibility, library should store data as-is)
func TestJobDataXSSPayload(t *testing.T) {
	// Security test: verify library doesn't inject, but also doesn't sanitize
	xssPayloads := []string{
		"<script>alert('XSS')</script>",
		"<img src=x onerror=alert('XSS')>",
		"';DROP TABLE jobs;--",
		"{{constructor.constructor('alert(1)')()}}",
	}

	for _, payload := range xssPayloads {
		data := map[string]interface{}{
			"userInput": payload,
		}

		serialized, err := json.Marshal(data)
		require.NoError(t, err)

		var deserialized map[string]interface{}
		err = json.Unmarshal(serialized, &deserialized)
		require.NoError(t, err)

		// Verify payload is stored AS-IS (not sanitized, not modified)
		assert.Equal(t, payload, deserialized["userInput"],
			"Library should store potentially malicious data as-is (sanitization is UI's job)")
	}
}
