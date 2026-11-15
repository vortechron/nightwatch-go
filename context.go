package nightwatch

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

const (
	// AttrContext is the one attribute AddContext sets on an execution root:
	// the members of a JSON object ("key":value pairs joined by commas, without
	// the braces). A key added twice is in it twice; the last one wins. One
	// attribute, however many keys, so the context never pushes other
	// attributes (status code, sample rate) past the span's attribute limit.
	AttrContext = "nightwatch.context"
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

	exec.mu.Lock()
	defer exec.mu.Unlock()

	exec.root.SetAttributes(attribute.String(AttrContext, exec.context.add(key, string(encoded))))
}

// contextData is the context of one execution. An add appends one member to
// the attribute text, so it costs the size of its value, not of the whole
// context. When replaced members make up more than half of the text, it is
// written again from the latest members.
type contextData struct {
	members map[string]string // key to its latest "key":value member
	text    strings.Builder
	stale   int // bytes of text that later adds replaced
}

// add records the encoded value of key and returns the attribute text.
func (c *contextData) add(key, encoded string) string {
	if c.members == nil {
		c.members = map[string]string{}
	}

	member := jsonString(key) + ":" + encoded
	if old, found := c.members[key]; found {
		c.stale += len(old) + 1
	}

	c.members[key] = member

	if c.stale > c.text.Len()/2 {
		c.text = strings.Builder{}
		c.stale = 0

		for _, kept := range c.members {
			c.append(kept)
		}
	} else {
		c.append(member)
	}

	return c.text.String()
}

func (c *contextData) append(member string) {
	if c.text.Len() > 0 {
		c.text.WriteByte(',')
	}

	c.text.WriteString(member)
}

// executionContext builds the "context" column of an execution record.
func (t *Translator) executionContext(attrs attrSet) string {
	if attrs.bool(AttrContextError) {
		return contextError
	}

	var members map[string]json.RawMessage
	if text := attrs.str(AttrContext); text != "" {
		_ = json.Unmarshal([]byte("{"+text+"}"), &members) // a later duplicate key wins
	}

	values := make(map[string]any, len(members))

	for name, raw := range members {
		var decoded any
		if json.Unmarshal(raw, &decoded) == nil {
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
// column always holds valid JSON (Laravel cuts the text instead). Each value
// is encoded once to learn its size, so the work grows with the number of
// keys, not its square.
func fitContext(values map[string]any) string {
	encoded := jsonString(values)
	if len(encoded) <= maxText {
		return encoded
	}

	type member struct {
		key         string
		value, size int // bytes of the value and of the whole "key":value member
	}

	members := make([]member, 0, len(values))

	for key, value := range values {
		if key != contextTruncated {
			valueSize := len(jsonString(value))
			members = append(members, member{key: key, value: valueSize, size: len(jsonString(key)) + 1 + valueSize})
		}
	}

	slices.SortFunc(members, func(a, b member) int { return cmp.Or(a.value-b.value, strings.Compare(a.key, b.key)) })

	kept := map[string]any{contextTruncated: true}
	size := len(jsonString(kept))

	for _, next := range members {
		if size+1+next.size <= maxText { // a comma and the member
			kept[next.key] = values[next.key]
			size += 1 + next.size
		}
	}

	return jsonString(kept)
}
