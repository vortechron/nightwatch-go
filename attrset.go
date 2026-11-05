package nightwatch

import (
	"strconv"

	"go.opentelemetry.io/otel/attribute"
)

// attrSet gives typed, forgiving reads over span attributes.
type attrSet map[attribute.Key]attribute.Value

func newAttrSet(kvs []attribute.KeyValue) attrSet {
	set := make(attrSet, len(kvs))
	for _, kv := range kvs {
		set[kv.Key] = kv.Value
	}

	return set
}

func (a attrSet) has(key string) bool {
	_, ok := a[attribute.Key(key)]

	return ok
}

// str returns the first non-empty string among the keys.
func (a attrSet) str(keys ...string) string {
	for _, key := range keys {
		value, ok := a[attribute.Key(key)]
		if !ok {
			continue
		}

		if text := value.Emit(); text != "" {
			return text
		}
	}

	return ""
}

func (a attrSet) int(key string) int64 {
	value, ok := a[attribute.Key(key)]
	if !ok {
		return 0
	}

	switch value.Type() {
	case attribute.INT64:
		return value.AsInt64()
	case attribute.FLOAT64:
		return int64(value.AsFloat64())
	case attribute.STRING:
		parsed, _ := strconv.ParseInt(value.AsString(), 10, 64)

		return parsed
	default:
		return 0
	}
}

func (a attrSet) bool(key string) bool {
	value, ok := a[attribute.Key(key)]
	if !ok {
		return false
	}

	if value.Type() == attribute.BOOL {
		return value.AsBool()
	}

	return truthy(value.Emit())
}
