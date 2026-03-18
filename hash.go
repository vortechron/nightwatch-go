package nightwatch

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/zeebo/xxh3"
)

// TokenHash returns the first 7 characters of the hex-encoded xxh128 hash of the token.
// This matches PHP's substr(hash('xxh128', $token), 0, 7).
func TokenHash(token string) string {
	return xxh128Hex(token)[:7]
}

// GroupHash returns the full 32-character hex-encoded xxh128 hash for grouping records.
// This matches PHP's hash('xxh128', $value).
func GroupHash(value string) string {
	return xxh128Hex(value)
}

// GroupHashf is a convenience wrapper around GroupHash with fmt.Sprintf formatting.
func GroupHashf(format string, args ...any) string {
	return GroupHash(fmt.Sprintf(format, args...))
}

// xxh128Hex computes xxh128 and returns it in PHP-compatible hex format.
// PHP's hash('xxh128') outputs: BigEndian(Hi) + BigEndian(Lo).
func xxh128Hex(s string) string {
	h := xxh3.Hash128([]byte(s))
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], h.Hi)
	binary.BigEndian.PutUint64(buf[8:16], h.Lo)
	return hex.EncodeToString(buf[:])
}
