package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

func main() {
	jsonOut := flag.Bool("json", false, "emit the report as JSON")
	check := flag.Bool("check", false, "exit with code 1 if any rule is fully shadowed")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: shadow [-json] [-check] <rules.json>")
		os.Exit(2)
	}
	shadowed, err := run(flag.Arg(0), *jsonOut)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if *check && shadowed {
		os.Exit(1)
	}
}

func run(path string, jsonOut bool) (bool, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return false, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false, fmt.Errorf("parsing %s: %v", path, err)
	}
	reports, err := Analyze(&cfg)
	if err != nil {
		return false, err
	}
	if jsonOut {
		out, err := json.MarshalIndent(reports, "", "  ")
		if err != nil {
			return false, err
		}
		fmt.Println(string(out))
	} else {
		printText(&cfg, reports)
	}
	shadowed := false
	for _, r := range reports {
		if r.Status == StatusFullyShadowed {
			shadowed = true
		}
	}
	return shadowed, nil
}

func printText(cfg *Config, reports []RuleReport) {
	for _, rep := range reports {
		fmt.Printf("rule %d: %s\n", rep.Index, describeRule(cfg.Rules[rep.Index]))
		switch rep.Status {
		case StatusEffective:
			fmt.Printf("  status: effective\n  witness: %s\n", rep.Witness)
		case StatusPartiallyShadowed:
			fmt.Printf("  status: partially-shadowed\n  witness: %s\n", rep.Witness)
		case StatusFullyShadowed:
			fmt.Printf("  status: fully-shadowed (never takes effect)\n  covered by: rules %v\n", rep.CoveredBy)
		case StatusEmpty:
			fmt.Printf("  status: empty (matches no request)\n")
		}
	}
}

func describeRule(r RuleSpec) string {
	return fmt.Sprintf("%s subjects={%s} actions={%s} path=%q",
		r.Effect, strings.Join(r.Subjects, ","), strings.Join(r.Actions, ","), r.Path)
}
