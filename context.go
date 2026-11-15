package nightwatch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

const (
	// AttrContextPrefix starts the attributes AddContext sets on an execution
	// root: "nightwatch.context.<key>" holds the JSON of the value.
	AttrContextPrefix = "nightwatch.context."
	// AttrContextError says a value could not be encoded.
	AttrContextError = "nightwatch.context_error"

	contextError     = `{"_nightwatch_error":"Failed to serialize context"}`
	contextTruncated = "_nightwatch_truncated"
)

// AddContext adds a key and a value to the context of the execution ctx
// belongs to (Laravel's Context::add): Omni shows it on the request, command,
// job attempt or scheduled task. A later add of the same key replaces the
// value. The value is stored as JSON; one that cannot be encoded turns the
// whole context into Laravel's {"_nightwatch_error": ...} object. Keys that
// look secret (password, token, session ...) are redacted at any depth, and
// the context is kept under 64 KB. Outside an execution it does nothing.
//
// The context does not travel into queued jobs (Laravel's does): add what a
// job needs in the job.
func AddContext(ctx context.Context, key string, value any) {
	exec := executionOf(ctx)
	if exec == nil || key == "" {
		return
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		exec.root.SetAttributes(attribute.Bool(AttrContextError, true))

		return
	}

	exec.root.SetAttributes(attribute.String(AttrContextPrefix+key, string(encoded)))
}

// executionContext builds the "context" column of an execution record.
func (t *Translator) executionContext(attrs attrSet) string {
	if attrs.bool(AttrContextError) {
		return contextError
	}

	values := map[string]any{}

	for key, value := range attrs {
		name, found := strings.CutPrefix(string(key), AttrContextPrefix)
		if !found {
			continue
		}

		var decoded any
		if json.Unmarshal([]byte(value.AsString()), &decoded) == nil {
			values[name] = t.redactContext(name, decoded)
		}
	}

	return fitContext(values)
}

// redactContext replaces the values of secret looking keys at any depth.
func (t *Translator) redactContext(key string, value any) any {
	if key != "" && t.secretKey(key) {
		return redactedValue
	}

	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for inner, item := range typed {
			out[inner] = t.redactContext(inner, item)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = t.redactContext("", item)
		}

		return out
	default:
		return value
	}
}

// fitContext encodes the context. When it is larger than a TEXT column it
// keeps the smallest values that fit and adds "_nightwatch_truncated", so the
// column always holds valid JSON (Laravel cuts the text instead).
func fitContext(values map[string]any) string {
	encoded := jsonString(values)
	if len(encoded) <= maxText {
		return encoded
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}

	slices.SortFunc(keys, func(a, b string) int { return len(jsonString(values[a])) - len(jsonString(values[b])) })

	kept := map[string]any{contextTruncated: true}

	for _, key := range keys {
		kept[key] = values[key]
		if len(jsonString(kept)) > maxText {
			delete(kept, key)
		}
	}

	return jsonString(kept)
}
