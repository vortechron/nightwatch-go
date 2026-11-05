package nightwatch

import (
	"encoding/json"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
)

const (
	redactedValue = "[redacted]"
	keyPassword   = "password"
)

// mandatoryRedactHeaders are always redacted, whatever NIGHTWATCH_REDACT_HEADERS
// says. The PHP package defaults to Authorization, Cookie, Proxy-Authorization,
// X-XSRF-TOKEN (config) and X-CSRF-TOKEN (code fallback); Set-Cookie is added
// because responses and proxies can carry it.
var mandatoryRedactHeaders = []string{
	"Authorization", "Cookie", "Proxy-Authorization", "Set-Cookie", "X-CSRF-TOKEN", "X-XSRF-TOKEN",
}

// defaultRedactQuery are query parameters whose values are redacted in URLs.
// PHP sends the full URL unredacted; this client does not, because tokens in
// links (password resets, signed URLs, OAuth callbacks) are common.
var defaultRedactQuery = []string{
	keyPassword, "password_confirmation", "token", "access_token", "refresh_token", "id_token", "api_key", "apikey",
	"key", "secret", "client_secret", "signature", "code", "auth",
}

var defaultRedactPayloadFields = []string{"_token", "password", "password_confirmation"}

func equalFold(a, b string) bool { return strings.EqualFold(a, b) }

// mergeRedactNames returns the mandatory header names followed by the extra
// ones, without case-insensitive duplicates.
func mergeRedactNames(extra []string) []string {
	return mergeNames(mandatoryRedactHeaders, extra)
}

// mergeNames returns base followed by the extra names that are not in it,
// ignoring case.
func mergeNames(base, extra []string) []string {
	merged := slices.Clone(base)

	for _, name := range extra {
		if !slices.ContainsFunc(merged, func(have string) bool { return equalFold(have, name) }) {
			merged = append(merged, name)
		}
	}

	return merged
}

// RedactHeadersJSON parses a JSON object of headers (name to value or list of
// values), replaces every value whose header name matches names (ignoring
// case) with "[redacted]", and encodes it again. Anything that is not a JSON
// object becomes "{}": the raw text is never passed through.
func RedactHeadersJSON(raw string, names []string) string {
	var headers map[string]any
	if err := json.Unmarshal([]byte(raw), &headers); err != nil || headers == nil {
		return "{}"
	}

	out := make(map[string]any, len(headers))
	for name, value := range headers {
		if matches(name, names) {
			out[name] = redactValue(value)
		} else {
			out[name] = value
		}
	}

	return jsonString(out)
}

func matches(name string, names []string) bool {
	return slices.ContainsFunc(names, func(candidate string) bool { return equalFold(candidate, name) })
}

func redactValue(value any) any {
	if list, ok := value.([]any); ok {
		out := make([]any, len(list))
		for i := range list {
			out[i] = redactedValue
		}

		return out
	}

	return redactedValue
}

// RedactQuery parses a URL query, replaces the values of the named keys
// (ignoring case) with "[redacted]", keeps the other parameters and encodes
// again with the keys sorted, so the result is deterministic. A query that
// cannot be parsed gives "": it is dropped, never passed through.
func RedactQuery(raw string, names []string) string {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	}

	for key, list := range values {
		if matches(key, names) {
			for i := range list {
				list[i] = redactedValue
			}
		}
	}

	return values.Encode()
}

// RedactURL redacts the query of a URL with RedactQuery. The fragment is
// dropped when there is a query; a query that cannot be parsed is dropped.
func RedactURL(raw string, names []string) string {
	base, query, found := strings.Cut(raw, "?")
	if !found {
		return raw
	}

	query, _, _ = strings.Cut(query, "#")

	if redacted := RedactQuery(query, names); redacted != "" {
		return base + "?" + redacted
	}

	return base
}

// HeadersToJSON builds the value of AttrHeaders from real headers, redacting
// at capture time so a secret never sits in a span attribute. Names are
// lowercase, like the PHP package.
func HeadersToJSON(headers map[string][]string, names []string) string {
	out := make(map[string]any, len(headers))

	for name, values := range headers {
		key := strings.ToLower(textproto.TrimString(name))
		if matches(key, names) {
			out[key] = redactValue(toAnyList(values))

			continue
		}

		out[key] = toAnyList(values)
	}

	return jsonString(out)
}

func toAnyList(values []string) []any {
	list := make([]any, len(values))
	for i, value := range values {
		list[i] = value
	}

	return list
}

// RedactPayloadJSON redacts the named fields (ignoring case, at any depth) of
// a JSON request body. Anything that is not JSON becomes "": the raw body is
// never passed through.
func RedactPayloadJSON(raw string, fields []string) string {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}

	return jsonString(redactTree(payload, fields))
}

func redactTree(value any, fields []string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			if matches(key, fields) {
				out[key] = redactedValue
			} else {
				out[key] = redactTree(inner, fields)
			}
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = redactTree(inner, fields)
		}

		return out
	default:
		return value
	}
}
