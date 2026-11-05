// Package nwdiff compares two folders of captured Nightwatch batches: a
// Laravel golden set and a Go set.
package nwdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Set is every record of a folder, grouped by type.
type Set struct {
	ByType map[string][]map[string]any
}

// Load reads all batch-*.json files in dir.
func Load(dir string) (Set, error) {
	files, err := filepath.Glob(filepath.Join(dir, "batch-*.json"))
	if err != nil {
		return Set{}, fmt.Errorf("listing %s: %w", dir, err)
	}

	set := Set{ByType: map[string][]map[string]any{}}

	for _, file := range files {
		raw, err := os.ReadFile(file) //nolint:gosec // the files come from the folder given on the command line
		if err != nil {
			return Set{}, fmt.Errorf("reading %s: %w", file, err)
		}

		var batch struct {
			Records []map[string]any `json:"records"`
		}

		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()

		if err := decoder.Decode(&batch); err != nil {
			return Set{}, fmt.Errorf("parsing %s: %w", file, err)
		}

		for _, record := range batch.Records {
			kind, _ := record["t"].(string)
			set.ByType[kind] = append(set.ByType[kind], record)
		}
	}

	return set, nil
}

// Types lists the record types present, sorted.
func (s Set) Types() []string {
	types := make([]string, 0, len(s.ByType))
	for kind := range s.ByType {
		types = append(types, kind)
	}

	sort.Strings(types)

	return types
}

// kindOf names the JSON type of a value. Numbers are split into int and
// float because Omni stores them in different column types.
func kindOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			return "float"
		}

		return "int"
	default:
		return "other"
	}
}
