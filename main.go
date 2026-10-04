// Command precheck audits an archive's entry list for naming conflicts
// before import, using an explicitly provided folding table.
//
// Usage:
//
//	precheck -fold fold.json [-list entries.txt | -scan dir]
//
// Exit code is 1 when conflicts or violations are found, 0 when clean.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"archive-precheck/precheck"
)

func main() {
	foldPath := flag.String("fold", "", "JSON folding table (required)")
	listPath := flag.String("list", "", "entry list file ('F <path>' / 'D <path>' lines, '-' for stdin)")
	scanDir := flag.String("scan", "", "directory to scan instead of reading an entry list")
	flag.Parse()

	if *foldPath == "" || (*listPath == "") == (*scanDir == "") {
		fmt.Fprintln(os.Stderr, "usage: precheck -fold fold.json (-list entries.txt | -scan dir)")
		os.Exit(2)
	}

	table, err := precheck.LoadFoldTable(*foldPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	var entries []precheck.Entry
	if *scanDir != "" {
		entries, err = precheck.ScanDir(*scanDir)
	} else {
		var in *os.File
		if *listPath == "-" {
			in = os.Stdin
		} else {
			in, err = os.Open(*listPath)
			if err == nil {
				defer in.Close()
			}
		}
		if err == nil {
			entries, err = precheck.ParseList(in)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	checker := precheck.NewChecker(table)
	for _, e := range entries {
		checker.Add(e)
	}
	report := checker.Report()

	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if len(report.Groups) > 0 {
		os.Exit(1)
	}
}
