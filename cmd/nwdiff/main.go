// Command nwdiff compares two folders of captured Nightwatch batches.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/vortechron/nightwatch-go/internal/nwdiff"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr *os.File) int {
	flags := flag.NewFlagSet("nwdiff", flag.ContinueOnError)
	flags.SetOutput(stderr)
	golden := flags.String("golden", "", "folder with the Laravel batch-*.json files")
	actual := flags.String("go", "", "folder with the Go batch-*.json files")
	allowPath := flags.String("allow", "", "allow-list file (type:<t> / field:<t>.<f> per line)")

	if err := flags.Parse(args); err != nil || *golden == "" || *actual == "" {
		fmt.Fprintln(stderr, "usage: nwdiff -golden <dir> -go <dir> [-allow file]")

		return 2
	}

	report, err := compare(*golden, *actual, *allowPath)
	if err != nil {
		fmt.Fprintln(stderr, "nwdiff:", err)

		return 2
	}

	nwdiff.Print(stdout, report)

	if report.Failed() {
		return 1
	}

	return 0
}

func compare(goldenDir, actualDir, allowPath string) (nwdiff.Report, error) {
	golden, err := nwdiff.Load(goldenDir)
	if err != nil {
		return nwdiff.Report{}, fmt.Errorf("golden: %w", err)
	}

	actual, err := nwdiff.Load(actualDir)
	if err != nil {
		return nwdiff.Report{}, fmt.Errorf("go side: %w", err)
	}

	allow, err := nwdiff.ParseAllow(allowPath)
	if err != nil {
		return nwdiff.Report{}, err //nolint:wrapcheck // already wrapped
	}

	return nwdiff.Compare(golden, actual, allow), nil
}
