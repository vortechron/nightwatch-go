package nightwatch

import "strings"

// PathIgnored reports whether a request path matches one of the ignore
// patterns. The query string and fragment are cut off first, so they can
// neither hide a match nor create one. A pattern is matched against the whole
// path; "*" matches any run of characters, "/" included, so "/build/*"
// covers /build/app.js and /build/assets/css/app.css but not "/build" itself,
// and "*.png" covers every png. A pattern without "*" must be equal. Matching
// is case sensitive. Blank patterns are skipped.
func PathIgnored(patterns []string, path string) bool {
	if path == "" || len(patterns) == 0 {
		return false
	}

	if cut := strings.IndexAny(path, "?#"); cut >= 0 {
		path = path[:cut]
	}

	for _, pattern := range patterns {
		if pattern = strings.TrimSpace(pattern); pattern != "" && wildcardMatch(pattern, path) {
			return true
		}
	}

	return false
}

// wildcardMatch matches s against a pattern where "*" is any run of
// characters. It runs in O(len(pattern)*len(s)) at worst and never recurses.
func wildcardMatch(pattern, s string) bool {
	p, i := 0, 0
	star, mark := -1, 0

	for i < len(s) {
		switch {
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, i
			p++
		case p < len(pattern) && pattern[p] == s[i]:
			p++
			i++
		case star >= 0:
			p = star + 1
			mark++
			i = mark
		default:
			return false
		}
	}

	for p < len(pattern) && pattern[p] == '*' {
		p++
	}

	return p == len(pattern)
}
