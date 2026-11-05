package nightwatch

import "strings"

// PathIgnored reports whether a request path matches one of the ignore
// patterns.
//
// path must be the path the router routes on: the request's decoded URL.Path.
// The query string is not part of it, and nothing is cut off here. A "?" or "#"
// in path is an ordinary character, so /favicon.ico%3Fanything (which decodes
// to "/favicon.ico?anything") does not match "/favicon.ico". Nothing is
// cleaned either: a path with a ".." or "." segment, or an empty segment
// ("//"), is never ignored, whatever the patterns say, because the application
// may treat it differently from what the pattern author had in mind.
//
// Pattern syntax, matched against the whole path, case sensitive:
//
//   - any characters except "/" (stays inside one segment)
//     **  any characters including "/" (any depth, possibly none)
//     everything else matches itself
//
// So "/build/*" covers /build/app.js but not /build/assets/app.js, and
// "/build/**" covers both. Neither covers "/build" itself, and "/favicon.ico"
// matches only that path. Blank patterns are skipped.
func PathIgnored(patterns []string, path string) bool {
	if path == "" || len(patterns) == 0 || !plainPath(path) {
		return false
	}

	for _, pattern := range patterns {
		if pattern = strings.TrimSpace(pattern); pattern != "" && globMatch(pattern, path) {
			return true
		}
	}

	return false
}

// plainPath rejects paths with a "." or ".." segment or an empty segment
// inside the path. The leading and trailing slash of a path are fine.
func plainPath(path string) bool {
	segments := strings.Split(path, "/")

	for i, segment := range segments {
		switch {
		case segment == "." || segment == "..":
			return false
		case segment == "" && i != 0 && i != len(segments)-1:
			return false
		}
	}

	return true
}

type globToken struct {
	kind byte // 'c' literal, '1' for *, '2' for **
	char byte
}

func tokenizeGlob(pattern string) []globToken {
	tokens := make([]globToken, 0, len(pattern))

	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '*' {
			tokens = append(tokens, globToken{kind: 'c', char: pattern[i]})

			continue
		}

		run := 1
		for i+run < len(pattern) && pattern[i+run] == '*' {
			run++
		}

		kind := byte('1')
		if run > 1 {
			kind = '2'
		}

		tokens = append(tokens, globToken{kind: kind})
		i += run - 1
	}

	return tokens
}

// globMatch runs the pattern as a small automaton: the set of pattern
// positions that can be reached after each character of s. It never
// backtracks, so its time is O(len(s) * len(pattern)) for any input.
func globMatch(pattern, s string) bool {
	tokens := tokenizeGlob(pattern)
	current := make([]bool, len(tokens)+1)
	next := make([]bool, len(tokens)+1)

	current[0] = true
	closeStars(tokens, current)

	for i := 0; i < len(s); i++ {
		clear(next)

		for pos, active := range current[:len(tokens)] {
			if !active {
				continue
			}

			switch token := tokens[pos]; {
			case token.kind == 'c' && token.char == s[i]:
				next[pos+1] = true
			case token.kind == '2', token.kind == '1' && s[i] != '/':
				next[pos] = true
			}
		}

		closeStars(tokens, next)
		current, next = next, current
	}

	return current[len(tokens)]
}

// closeStars lets a star match the empty string: whenever a star position is
// active, the position after it is active too.
func closeStars(tokens []globToken, active []bool) {
	for pos, token := range tokens {
		if active[pos] && token.kind != 'c' {
			active[pos+1] = true
		}
	}
}
