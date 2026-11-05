package nightwatch

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const maxKnownUsers = 10000

// userRecords emits a "user" record the first time an id is seen together
// with a name or username. The user field on other records is only the id.
func (t *Translator) userRecords(x execInfo, members []sdktrace.ReadOnlySpan) []Record {
	if x.user == "" {
		return nil
	}

	if _, seen := t.users[x.user]; seen {
		return nil
	}

	for _, span := range members {
		attrs := newAttrSet(span.Attributes())
		name, username := attrs.str(AttrUserName), attrs.str(AttrUserUsername)

		if name == "" && username == "" {
			continue
		}

		if len(t.users) >= maxKnownUsers {
			t.users = map[string]struct{}{}
		}

		t.users[x.user] = struct{}{}

		return []Record{{
			"v":         1,
			"t":         RecordUser,
			"timestamp": unixSeconds(t.now()),
			"id":        TinyText(x.user),
			"name":      TinyText(name),
			"username":  TinyText(username),
		}}
	}

	return nil
}
