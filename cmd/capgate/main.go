package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"formal.local/h26-25236a386a/capgate"
)

func main() {
	oldPath := flag.String("old", "", "path to old dependency graph JSON (optional)")
	newPath := flag.String("new", "", "path to new dependency graph JSON")
	policyPath := flag.String("policy", "", "optional policy JSON file")
	features := flag.String("features", "", "comma-separated small feature universe")
	legal := flag.String("legal", "", "Boolean legal-platform condition")
	maxFeatures := flag.Int("max-features", 12, "exhaustive feature limit (hard maximum 12)")
	flag.Parse()

	if *newPath == "" {
		fmt.Fprintln(os.Stderr, "capgate: -new is required")
		os.Exit(64)
	}

	policy := capgate.DefaultPolicy()
	if *policyPath != "" {
		buf, err := os.ReadFile(*policyPath)
		if err != nil {
			fail(err)
		}
		if err := json.Unmarshal(buf, &policy); err != nil {
			fail(fmt.Errorf("parse policy: %w", err))
		}
	}
	if *features != "" {
		policy.Features = splitCSV(*features)
	}
	if *legal != "" {
		policy.LegalCondition = *legal
	}
	if *maxFeatures > 0 {
		policy.MaxFeatures = *maxFeatures
	}

	newGraph, err := readGraph(*newPath)
	if err != nil {
		fail(err)
	}
	var oldGraph *capgate.Graph
	if *oldPath != "" {
		oldGraph, err = readGraph(*oldPath)
		if err != nil {
			fail(err)
		}
	}

	report, err := capgate.Compare(oldGraph, newGraph, policy)
	if err != nil {
		fail(err)
	}
	if err := report.WriteJSON(os.Stdout); err != nil {
		fail(err)
	}
	if err := report.GateError(); err != nil {
		var failure *capgate.GateFailure
		if errors.As(err, &failure) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fail(err)
	}
}

func readGraph(path string) (*capgate.Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return capgate.DecodeGraph(f)
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "capgate:", err)
	os.Exit(1)
}
