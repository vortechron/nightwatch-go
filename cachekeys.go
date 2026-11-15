package nightwatch

import (
	"regexp"
	"slices"
	"strings"
)

// keyMatcher matches a cache key: a compiled pattern or an exact key.
type keyMatcher func(key string) bool

// vendorCacheKeys is Laravel's Nightwatch::defaultVendorCacheKeys(), with a
// Go matcher for each pattern (Go's regexp has no look-ahead).
var vendorCacheKeys = []struct {
	pattern string
	match   keyMatcher
}{
	{`/(^laravel_vapor_job_attemp(t?)s:)/`, prefixRegexp(`^laravel_vapor_job_attempt?s:`)}, //nolint:misspell // Laravel's pattern
	{`/^illuminate:(?!cache:flexible:created:)/`, func(key string) bool {
		return strings.HasPrefix(key, "illuminate:") && !strings.HasPrefix(key, "illuminate:cache:flexible:created:")
	}},
	{`/^framework\/schedule/`, prefix("framework/schedule")},
	{`/^laravel:pulse:/`, prefix("laravel:pulse:")},
	{`/^laravel:reverb:/`, prefix("laravel:reverb:")},
	{`/^nova/`, prefix("nova")},
	{`/^telescope:/`, prefix("telescope:")},
	{`/^livewire-checksum-failures:/`, prefix("livewire-checksum-failures:")},
}

func prefix(value string) keyMatcher {
	return func(key string) bool { return strings.HasPrefix(key, value) }
}

func prefixRegexp(pattern string) keyMatcher {
	return regexp.MustCompile(pattern).MatchString
}

// DefaultVendorCacheKeys lists the cache key patterns of framework and package
// internals that Laravel leaves out by default, in Laravel's own notation.
// nightwatch-go records them unless CaptureDefaultVendorCacheKeys(false) is
// called, so upgrading changes nothing for an app.
func DefaultVendorCacheKeys() []string {
	out := make([]string, len(vendorCacheKeys))
	for i, entry := range vendorCacheKeys {
		out[i] = entry.pattern
	}

	return out
}

// RejectCacheKeys drops cache events whose key matches one of keys. Like
// Laravel, a key written as a PHP-style regular expression with delimiters
// ("/^session:/", "#^lock-\d+$#i") is a pattern, and anything else is compared
// as an exact key. A pattern Go's regexp cannot compile (look-arounds,
// back-references, the x flag) is compared as an exact key, the way PHP
// treats an invalid pattern.
func (h *Hooks) RejectCacheKeys(keys ...string) {
	matchers := make([]keyMatcher, len(keys))
	for i, key := range keys {
		matchers[i] = compileKey(key)
	}

	h.update(func(s *hookSet) { s.rejectCacheKeys = append(slices.Clip(s.rejectCacheKeys), matchers...) })
}

// CaptureDefaultVendorCacheKeys turns the DefaultVendorCacheKeys filter off
// (true, the default here) or on (false, Laravel's default).
func (h *Hooks) CaptureDefaultVendorCacheKeys(capture bool) {
	h.update(func(s *hookSet) { s.rejectVendorKeys = !capture })
}

// cacheKeyRejected applies the vendor list and RejectCacheKeys.
func (s *hookSet) cacheKeyRejected(key string) bool {
	if s.rejectVendorKeys {
		for _, entry := range vendorCacheKeys {
			if entry.match(key) {
				return true
			}
		}
	}

	for _, match := range s.rejectCacheKeys {
		if match(key) {
			return true
		}
	}

	return false
}

var closingDelimiter = map[byte]byte{'(': ')', '[': ']', '{': '}', '<': '>'}

// compileKey turns a PHP-style pattern into a matcher, or an exact match.
func compileKey(key string) keyMatcher {
	exact := func(candidate string) bool { return candidate == key }

	if len(key) < 2 || !isDelimiter(key[0]) {
		return exact
	}

	closing := key[0]
	if pair, ok := closingDelimiter[closing]; ok {
		closing = pair
	}

	end := strings.LastIndexByte(key, closing)
	if end <= 0 {
		return exact
	}

	flags, ok := goFlags(key[end+1:])
	if !ok {
		return exact
	}

	compiled, err := regexp.Compile(flags + key[1:end])
	if err != nil {
		return exact
	}

	return compiled.MatchString
}

// isDelimiter accepts what PCRE accepts as a delimiter, limited to printable
// ASCII: anything but a letter, a digit, a backslash or white space.
func isDelimiter(c byte) bool {
	return c > ' ' && c < 0x7f && c != '\\' && !isAlnum(c)
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// goFlags maps PHP modifiers to a Go flag group. "u" is Go's default; any
// modifier Go does not have makes the pattern unusable.
func goFlags(modifiers string) (string, bool) {
	var flags strings.Builder

	for i := range len(modifiers) {
		switch modifiers[i] {
		case 'i', 'm', 's', 'U':
			flags.WriteByte(modifiers[i])
		case 'u':
		default:
			return "", false
		}
	}

	if flags.Len() == 0 {
		return "", true
	}

	return "(?" + flags.String() + ")", true
}
