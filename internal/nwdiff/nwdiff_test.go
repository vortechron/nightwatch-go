package nwdiff

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBatch(t *testing.T, dir string, records ...map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"records": records})
	if err := os.WriteFile(filepath.Join(dir, "batch-1.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, records ...map[string]any) Set {
	t.Helper()
	dir := t.TempDir()
	writeBatch(t, dir, records...)
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	return set
}

const (
	execID = "0b9e3f6a-6d0c-4d6e-9d77-1d2a5c0f8a11"
	group  = "9bb563b3a9618d10cbfb3b0fa929d4ce"
)

func goldenSet(t *testing.T) Set {
	return load(t,
		map[string]any{"t": "request", "trace_id": execID, "duration": 5, "route_methods": []string{"GET"}, "_group": group},
		map[string]any{"t": "query", "trace_id": execID, "execution_id": execID, "line": 3, "execution_stage": "action"},
		map[string]any{"t": "mail", "trace_id": execID, "failed": false},
	)
}

func TestCompare_IdenticalShapesPass(t *testing.T) {
	actual := load(t,
		map[string]any{"t": "request", "trace_id": execID, "duration": 9, "route_methods": []string{"POST"}, "_group": ""},
		map[string]any{"t": "query", "trace_id": execID, "execution_id": execID, "line": 0, "execution_stage": "action"},
		map[string]any{"t": "mail", "trace_id": execID, "failed": true},
	)
	report := Compare(goldenSet(t), actual, Allow{})
	if report.Failed() {
		var b bytes.Buffer
		Print(&b, report)
		t.Fatal(b.String())
	}
}

func TestCompare_FindsRealGaps(t *testing.T) {
	actual := load(t,
		map[string]any{"t": "request", "trace_id": execID, "duration": "5", "route_methods": nil, "_group": "abc"},
		map[string]any{"t": "query", "trace_id": execID, "execution_id": "ffffffff-6d0c-4d6e-9d77-1d2a5c0f8a11", "execution_stage": "action"},
	)
	report := Compare(goldenSet(t), actual, Allow{})
	if !report.Failed() {
		t.Fatal("must fail")
	}
	var b bytes.Buffer
	Print(&b, report)
	out := b.String()
	for _, want := range []string{
		"mail", "missing on the Go side",
		"JSON type string on the Go side",
		"JSON type null",
		"_group length 3",
		"line", "field missing",
		"point to an execution",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}

func TestCompare_AllowListAcceptsKnownGaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	_ = os.WriteFile(path, []byte("# known\ntype:mail\nfield:query.line\n\n"), 0o600)
	allow, err := ParseAllow(path)
	if err != nil {
		t.Fatal(err)
	}
	actual := load(t,
		map[string]any{"t": "request", "trace_id": execID, "duration": 1, "route_methods": []string{}, "_group": group},
		map[string]any{"t": "query", "trace_id": execID, "execution_id": execID, "execution_stage": "action"},
	)
	if report := Compare(goldenSet(t), actual, allow); report.Failed() {
		var b bytes.Buffer
		Print(&b, report)
		t.Fatal(b.String())
	}
}

func TestParseAllow_BadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	_ = os.WriteFile(path, []byte("whatever\n"), 0o600)
	if _, err := ParseAllow(path); err == nil {
		t.Fatal("want error")
	}
	if _, err := ParseAllow(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("want error")
	}
	if allow, err := ParseAllow(""); err != nil || allow.Type("x") {
		t.Fatal("empty path is an empty list")
	}
}

func TestLoad_Errors(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "batch-1.json"), []byte("nope"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Fatal("want parse error")
	}
}

func TestNumberCompat(t *testing.T) {
	golden := load(t, map[string]any{"t": "log", "timestamp": 1.5})
	actual := load(t, map[string]any{"t": "log", "timestamp": 2})
	if Compare(golden, actual, Allow{}).Failed() {
		t.Fatal("int for float must pass")
	}
}
