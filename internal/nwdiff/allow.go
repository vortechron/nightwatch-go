package nwdiff

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Allow lists known differences: "type:log" or "field:request.route_name".
type Allow struct {
	types  map[string]bool
	fields map[string]bool
}

// ParseAllow reads an allow-list file. Blank lines and # comments are ignored.
func ParseAllow(path string) (Allow, error) {
	allow := Allow{types: map[string]bool{}, fields: map[string]bool{}}
	if path == "" {
		return allow, nil
	}

	file, err := os.Open(path) //nolint:gosec // the path is a command line argument
	if err != nil {
		return allow, fmt.Errorf("opening allow list: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		kind, value, found := strings.Cut(line, ":")
		switch {
		case found && kind == "type":
			allow.types[strings.TrimSpace(value)] = true
		case found && kind == "field":
			allow.fields[strings.TrimSpace(value)] = true
		default:
			return allow, fmt.Errorf("allow list: bad line %q (use type:<t> or field:<t>.<f>)", line)
		}
	}

	return allow, scanner.Err() //nolint:wrapcheck // scanner error is self explanatory
}

// Type reports whether a whole record type may be missing.
func (a Allow) Type(kind string) bool { return a.types[kind] }

// Field reports whether a field may be missing or differ.
func (a Allow) Field(kind, field string) bool { return a.fields[kind+"."+field] }
