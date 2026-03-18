package nightwatch

import "unicode/utf8"

const (
	maxTinyText   = 255
	maxText       = 65_535
	maxMediumText = 16_777_215
)

// TinyText truncates a string to 255 bytes (MySQL TINYTEXT limit).
func TinyText(s string) string {
	return restrict(s, maxTinyText)
}

// Text truncates a string to 65,535 bytes (MySQL TEXT limit).
func Text(s string) string {
	return restrict(s, maxText)
}

// MediumText truncates a string to 16,777,215 bytes (MySQL MEDIUMTEXT limit).
func MediumText(s string) string {
	return restrict(s, maxMediumText)
}

func restrict(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// SanitizeUTF8 replaces invalid UTF-8 sequences with the Unicode replacement character.
// This matches PHP's JSON_INVALID_UTF8_SUBSTITUTE behavior.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	// Build a new string, replacing invalid sequences
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			result = append(result, []byte(string(utf8.RuneError))...)
		} else {
			result = append(result, s[i:i+size]...)
		}
		i += size
	}
	return string(result)
}
