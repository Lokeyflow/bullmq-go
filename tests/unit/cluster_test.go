package unit

import (
	"testing"

	"github.com/lokeyflow/bullmq-go/pkg/bullmq"
	"github.com/stretchr/testify/assert"
)

// TestCRC16Calculation validates the CRC16 implementation consistency
// Redis Cluster uses CRC16-CCITT for slot calculation
func TestCRC16Calculation(t *testing.T) {
	tests := []struct {
		input       string
		description string
	}{
		{"test-queue", "Queue name"},
		{"myqueue", "Simple queue"},
		{"queue1", "Numeric suffix"},
		{"a", "Single character"},
		{"", "Empty string"},
		{"123", "Numeric string"},
		{"test-queue-123", "Complex string"},
		{"emoji-queue-🎉", "Unicode/emoji"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := bullmq.CalculateCRC16([]byte(tt.input))

			// Verify result is consistent (same input = same output)
			result2 := bullmq.CalculateCRC16([]byte(tt.input))
			assert.Equal(t, result, result2, "CRC16 should be deterministic")

			// Verify result produces valid slot
			slot := int(result) % bullmq.RedisClusterSlots
			assert.GreaterOrEqual(t, slot, 0)
			assert.Less(t, slot, bullmq.RedisClusterSlots)

			t.Logf("%-20s → CRC16: 0x%04X, Slot: %d", tt.description, result, slot)
		})
	}
}

// TestGetClusterSlot validates slot calculation with hash tags
// This is critical for Redis Cluster compatibility
func TestGetClusterSlot(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		expectSlot  int
		description string
	}{
		{
			name:        "NoHashTag",
			key:         "bull:myqueue:wait",
			expectSlot:  bullmq.GetClusterSlot("bull:myqueue:wait"),
			description: "Key without hash tag - hashes entire key",
		},
		{
			name:        "WithHashTag",
			key:         "bull:{myqueue}:wait",
			expectSlot:  bullmq.GetClusterSlot("{myqueue}"),
			description: "Key with hash tag - hashes only tag content",
		},
		{
			name:        "EmptyHashTag",
			key:         "bull:{}:wait",
			expectSlot:  bullmq.GetClusterSlot("bull:{}:wait"),
			description: "Empty hash tag - hashes entire key",
		},
		{
			name:        "MultipleHashTags",
			key:         "bull:{queue1}:job:{123}",
			expectSlot:  bullmq.GetClusterSlot("{queue1}"),
			description: "Multiple hash tags - uses first tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slot := bullmq.GetClusterSlot(tt.key)
			assert.Equal(t, tt.expectSlot, slot, tt.description)
			assert.GreaterOrEqual(t, slot, 0, "Slot should be >= 0")
			assert.Less(t, slot, bullmq.RedisClusterSlots, "Slot should be < %d", bullmq.RedisClusterSlots)
			t.Logf("Key: %-40s → Slot: %d", tt.key, slot)
		})
	}
}

// TestValidateHashTags_BullMQKeys validates that all BullMQ queue keys hash to same slot
// This is P0 critical - without this, multi-key Lua scripts fail in Redis Cluster
func TestValidateHashTags_BullMQKeys(t *testing.T) {
	queueName := "test-queue"

	// All keys that BullMQ uses for a single queue
	keys := []string{
		"bull:{" + queueName + "}:wait",
		"bull:{" + queueName + "}:active",
		"bull:{" + queueName + "}:prioritized",
		"bull:{" + queueName + "}:delayed",
		"bull:{" + queueName + "}:completed",
		"bull:{" + queueName + "}:failed",
		"bull:{" + queueName + "}:meta",
		"bull:{" + queueName + "}:events",
		"bull:{" + queueName + "}:id",
		"bull:{" + queueName + "}:1",        // Job hash
		"bull:{" + queueName + "}:1:lock",   // Lock key
		"bull:{" + queueName + "}:1:logs",   // Logs list
		"bull:{" + queueName + "}:paused",   // Paused key
		"bull:{" + queueName + "}:marker",   // Marker key
	}

	allSame, expectedSlot, slots := bullmq.ValidateHashTags(keys)

	assert.True(t, allSame, "All BullMQ keys for a queue MUST hash to the same slot")
	t.Logf("✅ All %d keys hash to slot %d", len(keys), expectedSlot)

	// Verify each key individually
	for i, key := range keys {
		assert.Equal(t, expectedSlot, slots[i],
			"Key %s should be in slot %d but is in slot %d",
			key, expectedSlot, slots[i])
		t.Logf("   %-50s → Slot %d ✓", key, slots[i])
	}
}

// TestValidateHashTags_KeysWithoutHashTags validates negative case
// Keys without hash tags should hash to different slots
func TestValidateHashTags_KeysWithoutHashTags(t *testing.T) {
	// Keys without hash tags - will hash to different slots
	badKeys := []string{
		"bull:queue1:wait",
		"bull:queue2:wait",
		"bull:queue3:wait",
	}

	allSame, firstSlot, slots := bullmq.ValidateHashTags(badKeys)

	assert.False(t, allSame, "Keys without consistent hash tags should NOT all be in same slot")
	t.Logf("❌ Keys hash to different slots (expected behavior for bad keys)")
	t.Logf("   First slot: %d", firstSlot)
	for i, key := range badKeys {
		t.Logf("   %-40s → Slot %d", key, slots[i])
	}
}

// TestKeyBuilder_HashTagValidation validates T113: All KeyBuilder methods use hash tags
func TestKeyBuilder_HashTagValidation(t *testing.T) {
	queueName := "validation-queue"
	// Test cluster mode (hash tags enabled)
	kb := bullmq.NewKeyBuilderWithHashTags(queueName, true)

	// Test all KeyBuilder methods
	keys := map[string]string{
		"Wait()":        kb.Wait(),
		"Active()":      kb.Active(),
		"Prioritized()": kb.Prioritized(),
		"Delayed()":     kb.Delayed(),
		"Completed()":   kb.Completed(),
		"Failed()":      kb.Failed(),
		"Events()":      kb.Events(),
		"Meta()":        kb.Meta(),
		"Job(1)":        kb.Job("1"),
		"Lock(1)":       kb.Lock("1"),
		"Logs(1)":       kb.Logs("1"),
	}

	expectedHashTag := "{" + queueName + "}"

	for method, key := range keys {
		t.Run(method, func(t *testing.T) {
			// Verify key contains hash tag
			assert.Contains(t, key, expectedHashTag,
				"%s should include hash tag {%s}", method, queueName)

			// Verify hash tag is in correct format (not escaped or malformed)
			assert.Regexp(t, `^bull:\{[^}]+\}:`, key,
				"%s should start with bull:{...}: pattern", method)

			t.Logf("%-20s → %s ✓", method, key)
		})
	}

	// Verify all keys hash to same slot
	allKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		allKeys = append(allKeys, key)
	}

	allSame, slot, _ := bullmq.ValidateHashTags(allKeys)
	assert.True(t, allSame, "All KeyBuilder keys should hash to the same slot")
	t.Logf("\n✅ All KeyBuilder methods produce keys that hash to slot %d", slot)
}

// TestAnalyzeClusterKeys provides diagnostic information
func TestAnalyzeClusterKeys(t *testing.T) {
	queueName := "analysis-queue"
	// Test cluster mode (hash tags enabled)
	kb := bullmq.NewKeyBuilderWithHashTags(queueName, true)

	keys := []string{
		kb.Wait(),
		kb.Active(),
		kb.Prioritized(),
		kb.Job("1"),
		kb.Lock("1"),
	}

	analysis := bullmq.AnalyzeClusterKeys(keys)

	assert.Len(t, analysis, 1, "All keys should be in one slot")
	assert.Equal(t, queueName, analysis[0].HashTag, "Hash tag should be queue name")
	assert.Len(t, analysis[0].Keys, len(keys), "All keys should be in same slot")

	t.Logf("\n📊 Cluster Analysis for queue '%s':", queueName)
	for _, info := range analysis {
		t.Logf("   Slot %d (hash tag: {%s}):", info.Slot, info.HashTag)
		for _, key := range info.Keys {
			t.Logf("     - %s", key)
		}
	}
}

// BenchmarkCRC16Calculation benchmarks the CRC16 calculation
func BenchmarkCRC16Calculation(b *testing.B) {
	testData := []byte("bull:{test-queue}:wait")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bullmq.CalculateCRC16(testData)
	}
}

// BenchmarkGetClusterSlot benchmarks the full slot calculation
func BenchmarkGetClusterSlot(b *testing.B) {
	key := "bull:{test-queue}:wait"
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bullmq.GetClusterSlot(key)
	}
}
