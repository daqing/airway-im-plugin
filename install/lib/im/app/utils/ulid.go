package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID returns a sortable, opaque 26-character identifier without shared state.
func NewULID() (string, error) {
	var raw [16]byte
	ms := uint64(time.Now().UTC().UnixMilli())
	raw[0] = byte(ms >> 40)
	raw[1] = byte(ms >> 32)
	raw[2] = byte(ms >> 24)
	raw[3] = byte(ms >> 16)
	raw[4] = byte(ms >> 8)
	raw[5] = byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate ULID entropy: %w", err)
	}

	value := new(big.Int).SetBytes(raw[:])
	base := big.NewInt(32)
	var out [26]byte
	for index := len(out) - 1; index >= 0; index-- {
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(value, base, remainder)
		out[index] = crockford[remainder.Int64()]
		value = quotient
	}
	return string(out[:]), nil
}
