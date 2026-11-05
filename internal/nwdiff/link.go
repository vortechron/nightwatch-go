package nwdiff

import "fmt"

// executionTypes are the records that are executions themselves.
var executionTypes = []string{"request", "command", "scheduled-task", "job-attempt"}

// checkLinking makes sure every child names an execution that is in the set.
// A request, command or scheduled task is identified by trace_id and a job
// attempt by attempt_id.
func checkLinking(set Set) []Finding {
	ids := map[string]bool{}

	for _, kind := range executionTypes {
		for _, record := range set.ByType[kind] {
			field := "trace_id"
			if kind == "job-attempt" {
				field = "attempt_id"
			}

			if id, _ := record[field].(string); id != "" {
				ids[id] = true
			}
		}
	}

	var findings []Finding

	for _, kind := range set.Types() {
		orphans := 0

		for _, record := range set.ByType[kind] {
			if id, _ := record["execution_id"].(string); id != "" && !ids[id] {
				orphans++
			}
		}

		if orphans > 0 {
			findings = append(findings, Finding{
				Type: kind, Field: "execution_id", Fatal: true,
				Detail: fmt.Sprintf("%d record(s) point to an execution that is not in the set", orphans),
			})
		}
	}

	return findings
}
