package utils

import (
	"strings"
	"testing"
	"time"
)

// decodeTimestamp extracts the 48-bit millisecond timestamp, which occupies
// exactly the first ten Crockford characters of a ULID.
func decodeTimestamp(id string) int64 {
	value := int64(0)
	for _, char := range id[:10] {
		value = value*32 + int64(strings.IndexByte(crockford, byte(char)))
	}
	return value
}

func TestNewULIDProducesSortableOpaqueIDs(t *testing.T) {
	previous, err := NewULID()
	if err != nil {
		t.Fatalf("generate ULID: %v", err)
	}
	seen := map[string]bool{previous: true}

	for range 100 {
		id, err := NewULID()
		if err != nil {
			t.Fatalf("generate ULID: %v", err)
		}
		if len(id) != 26 {
			t.Fatalf("length = %d, id = %q", len(id), id)
		}
		if strings.IndexFunc(id, func(r rune) bool {
			return !strings.ContainsRune(crockford, r)
		}) != -1 {
			t.Fatalf("id %q contains characters outside the Crockford alphabet", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		if decodeTimestamp(id) < decodeTimestamp(previous) {
			t.Fatalf("timestamps are not monotonically sortable: %q before %q", previous, id)
		}
		previous = id
	}
}

func TestNewULIDEncodesTimestamp(t *testing.T) {
	before := time.Now().UTC().UnixMilli()
	id, err := NewULID()
	if err != nil {
		t.Fatalf("generate ULID: %v", err)
	}
	after := time.Now().UTC().UnixMilli()

	if encoded := decodeTimestamp(id); encoded < before || encoded > after {
		t.Fatalf("timestamp %d outside [%d, %d]", encoded, before, after)
	}
}
