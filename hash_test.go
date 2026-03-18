package nightwatch

import "testing"

// TestTokenHash_PHPCompat verifies Go xxh128 output matches PHP's hash('xxh128', ...).
// PHP reference values generated with PHP 8.1+:
//
//	php -r "echo substr(hash('xxh128', 'test-token-123'), 0, 7);"
func TestTokenHash_PHPCompat(t *testing.T) {
	tests := []struct {
		input string
		want  string // first 7 chars of PHP hash('xxh128', input)
	}{
		{"", "99aa06d"},
		{"test-token-123", "e9c611a"},
		{"hello", "b5e9c1a"},
		{"nightwatch", "59b55e9"},
	}

	for _, tt := range tests {
		got := TokenHash(tt.input)
		if got != tt.want {
			t.Errorf("TokenHash(%q) = %q, want %q (PHP reference)", tt.input, got, tt.want)
		}
	}
}

// TestGroupHash_PHPCompat verifies full 32-char hash matches PHP.
func TestGroupHash_PHPCompat(t *testing.T) {
	tests := []struct {
		input string
		want  string // full PHP hash('xxh128', input)
	}{
		{"", "99aa06d3014798d86001c324468d497f"},
		{"test-token-123", "e9c611a1238fa9e8c5b238cdc14589d0"},
		{"hello", "b5e9c1ad071b3e7fc779cfaa5e523818"},
		{"nightwatch", "59b55e96790c67c2beb47fcc0fa8f7bb"},
		{"GET|POST,example.com,/users/{id}", "09249a603052d3adeb80c90dc7596369"},
		{"mysql,SELECT * FROM users WHERE id = ?", "f1f7b0d7e589b6a5d6e98bdccac2a58f"},
	}

	for _, tt := range tests {
		got := GroupHash(tt.input)
		if got != tt.want {
			t.Errorf("GroupHash(%q) = %q, want %q (PHP reference)", tt.input, got, tt.want)
		}
	}
}

func TestGroupHash_Deterministic(t *testing.T) {
	a := GroupHash("test-input")
	b := GroupHash("test-input")
	if a != b {
		t.Errorf("GroupHash is not deterministic: %q != %q", a, b)
	}
}
