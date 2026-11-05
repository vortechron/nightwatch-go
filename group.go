package nightwatch

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	inClauseRegex = regexp.MustCompile(`in \([\d?\s,]+\)`)
	valuesRegex   = regexp.MustCompile(`values [(?,\s)]+`)
	sqlDrivers    = []string{"mariadb", "mysql", "pgsql", "sqlite", "sqlsrv", "singlestore"}
)

// RequestGroup builds the _group of a request record. The methods are sorted
// on a copy, the same way the PHP sensor does it.
func RequestGroup(methods []string, domain, path string) string {
	sorted := slices.Clone(methods)
	slices.Sort(sorted)

	return GroupHash(strings.Join(sorted, "|") + "," + domain + "," + path)
}

// QueryGroup builds the _group of a query record. driver is the Laravel
// driver name (pgsql, mysql, sqlite ...). For known SQL drivers the statement
// is normalised first so lists of values group together.
func QueryGroup(driver, connection, sql string) string {
	if !slices.Contains(sqlDrivers, driver) {
		return GroupHash(connection + "," + sql)
	}

	normalised := inClauseRegex.ReplaceAllString(sql, "in (...?)")
	if strings.Contains(normalised, "insert") {
		normalised = valuesRegex.ReplaceAllString(normalised, "values ...")
	}

	return GroupHash(connection + "," + normalised)
}

// ExceptionGroup builds the _group of an exception record.
func ExceptionGroup(class, code, file string, line int) string {
	return GroupHash(class + "," + code + "," + file + "," + strconv.Itoa(line))
}

// ScheduledTaskGroup builds the _group of a scheduled-task record.
func ScheduledTaskGroup(name, cron, timezone string, repeatSeconds int) string {
	value := name + "," + cron + "," + timezone
	if repeatSeconds > 0 {
		value += "," + strconv.Itoa(repeatSeconds)
	}

	return GroupHash(value)
}

// CacheGroup builds the _group of a cache-event record.
func CacheGroup(store, key string) string {
	return GroupHash(store + "," + key)
}

// NameGroup is the _group used by commands, jobs, mail, notifications and
// outgoing requests: the hash of one name (class, command name or host).
func NameGroup(name string) string {
	return GroupHash(name)
}
