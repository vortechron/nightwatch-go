package nwdiff

import (
	"fmt"
	"sort"
)

// Finding is one difference. Fatal findings make nwdiff exit 1.
type Finding struct {
	Type   string
	Field  string
	Detail string
	Fatal  bool
}

// TypeStats counts records of one type on each side.
type TypeStats struct {
	Type   string
	Golden int
	Actual int
}

// Report is the result of a comparison.
type Report struct {
	Stats    []TypeStats
	Findings []Finding
}

// Failed reports whether any fatal finding remains.
func (r Report) Failed() bool {
	for _, finding := range r.Findings {
		if finding.Fatal {
			return true
		}
	}

	return false
}

// profile is what one side shows for one field.
type profile struct {
	kinds  map[string]bool
	values map[string]bool
	groups map[int]bool
}

func profileFields(records []map[string]any) map[string]*profile {
	fields := map[string]*profile{}

	for _, record := range records {
		for key, value := range record {
			p, ok := fields[key]
			if !ok {
				p = &profile{kinds: map[string]bool{}, values: map[string]bool{}, groups: map[int]bool{}}
				fields[key] = p
			}

			p.kinds[kindOf(value)] = true

			if text, isString := value.(string); isString {
				recordValue(p, key, text)
			}
		}
	}

	return fields
}

var enumFields = map[string]bool{
	"status": true, "type": true, "execution_source": true, "execution_stage": true,
	"connection_type": true, "level": true,
}

func recordValue(p *profile, key, text string) {
	if key == "_group" {
		p.groups[len(text)] = true
	}

	if enumFields[key] {
		p.values[text] = true
	}
}

// Compare checks actual (the Go side) against golden (the Laravel side).
// allow holds entries like "type:log" or "field:request.route_name" that are
// accepted as known differences.
func Compare(golden, actual Set, allow Allow) Report {
	var report Report

	for _, kind := range union(golden.Types(), actual.Types()) {
		report.Stats = append(report.Stats, TypeStats{Type: kind, Golden: len(golden.ByType[kind]), Actual: len(actual.ByType[kind])})
		report.Findings = append(report.Findings, compareType(kind, golden.ByType[kind], actual.ByType[kind], allow)...)
	}

	report.Findings = append(report.Findings, checkLinking(actual)...)

	return report
}

func compareType(kind string, golden, actual []map[string]any, allow Allow) []Finding {
	switch {
	case len(golden) == 0:
		return []Finding{{Type: kind, Detail: "only on the Go side (not in golden)"}}
	case len(actual) == 0:
		return []Finding{{Type: kind, Detail: "missing on the Go side", Fatal: !allow.Type(kind)}}
	}

	want, got := profileFields(golden), profileFields(actual)

	var findings []Finding

	for _, field := range sortedKeys(want) {
		findings = append(findings, compareField(kind, field, want[field], got[field], allow)...)
	}

	for _, field := range sortedKeys(got) {
		if _, known := want[field]; !known {
			findings = append(findings, Finding{Type: kind, Field: field, Detail: "extra field on the Go side"})
		}
	}

	return findings
}

func compareField(kind, field string, want, got *profile, allow Allow) []Finding {
	if got == nil {
		return []Finding{{Type: kind, Field: field, Detail: "field missing on the Go side", Fatal: !allow.Field(kind, field)}}
	}

	var findings []Finding

	for goKind := range got.kinds {
		if !want.kinds[goKind] && !numberCompatible(want.kinds, goKind) {
			findings = append(findings, Finding{
				Type: kind, Field: field, Fatal: !allow.Field(kind, field),
				Detail: fmt.Sprintf("JSON type %s on the Go side, golden has %s", goKind, joined(want.kinds)),
			})
		}
	}

	for length := range got.groups {
		if length != 0 && length != 32 {
			findings = append(findings, Finding{Type: kind, Field: field, Detail: fmt.Sprintf("_group length %d", length), Fatal: true})
		}
	}

	for value := range got.values {
		if len(want.values) > 0 && !want.values[value] {
			findings = append(findings, Finding{Type: kind, Field: field, Detail: fmt.Sprintf("enum value %q not seen in golden", value)})
		}
	}

	return findings
}

// numberCompatible accepts an int where golden has a float and the reverse:
// a whole-number float such as 1.0 is encoded as an int by Go.
func numberCompatible(want map[string]bool, goKind string) bool {
	return (goKind == "int" && want["float"]) || (goKind == "float" && want["int"])
}

func joined(set map[string]bool) string {
	return fmt.Sprint(sortedKeys(set))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	for _, list := range [][]string{a, b} {
		for _, item := range list {
			seen[item] = true
		}
	}

	return sortedKeys(seen)
}
