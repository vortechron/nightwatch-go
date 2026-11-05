package nwdiff

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// Print writes the report as plain tables.
func Print(w io.Writer, report Report) {
	table := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(table, "TYPE\tGOLDEN\tGO\tSTATUS")

	for _, stat := range report.Stats {
		fmt.Fprintf(table, "%s\t%d\t%d\t%s\n", stat.Type, stat.Golden, stat.Actual, statusOf(report, stat.Type))
	}

	_ = table.Flush()

	if len(report.Findings) == 0 {
		fmt.Fprintln(w, "\nno differences")

		return
	}

	fmt.Fprintln(w, "\nFINDINGS")

	details := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)

	for _, finding := range report.Findings {
		level := "warn"
		if finding.Fatal {
			level = "FAIL"
		}

		fmt.Fprintf(details, "%s\t%s\t%s\t%s\n", level, finding.Type, finding.Field, finding.Detail)
	}

	_ = details.Flush()
}

func statusOf(report Report, kind string) string {
	status := "ok"

	for _, finding := range report.Findings {
		if finding.Type != kind {
			continue
		}

		if finding.Fatal {
			return "FAIL"
		}

		status = "warn"
	}

	return status
}
