// recurctl is a JSON command line front-end for the recur library.
//
//	recurctl query  -rule rule.json -from 2026-01-01T00:00:00Z -to 2026-02-01T00:00:00Z
//	recurctl expand -rule rule.json
//	recurctl diff   -old old.json -new new.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"recur"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "query":
		err = cmdQuery(os.Args[2:])
	case "expand":
		err = cmdExpand(os.Args[2:])
	case "diff":
		err = cmdDiff(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "recurctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  recurctl query  -rule rule.json -from <RFC3339> -to <RFC3339>
  recurctl expand -rule rule.json
  recurctl diff   -old old.json -new new.json
Use -rule - to read the rule from stdin.`)
}

func loadRule(path string) (*recur.Rule, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	return recur.ParseRule(data)
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	rulePath := fs.String("rule", "", "rule JSON file, or - for stdin")
	fromStr := fs.String("from", "", "range start, RFC 3339 with offset")
	toStr := fs.String("to", "", "range end, RFC 3339 with offset")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rule, err := loadRule(*rulePath)
	if err != nil {
		return err
	}
	from, err := recur.ParseTime(*fromStr)
	if err != nil {
		return err
	}
	to, err := recur.ParseTime(*toStr)
	if err != nil {
		return err
	}
	if from >= to {
		return fmt.Errorf("empty query range: from must be before to")
	}
	ix := recur.NewIndex(rule)
	return writeJSON(recur.InstancesToJSON(ix.Query(from, to)))
}

func cmdExpand(args []string) error {
	fs := flag.NewFlagSet("expand", flag.ContinueOnError)
	rulePath := fs.String("rule", "", "rule JSON file, or - for stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rule, err := loadRule(*rulePath)
	if err != nil {
		return err
	}
	return writeJSON(recur.InstancesToJSON(rule.AllInstances()))
}

func cmdDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	oldPath := fs.String("old", "", "old rule JSON file")
	newPath := fs.String("new", "", "new rule JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	oldRule, err := loadRule(*oldPath)
	if err != nil {
		return err
	}
	newRule, err := loadRule(*newPath)
	if err != nil {
		return err
	}
	return writeJSON(recur.DiffRules(oldRule, newRule).ToJSON())
}
