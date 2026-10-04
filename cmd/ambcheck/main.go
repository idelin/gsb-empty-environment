// Command ambcheck reads a grammar and search options as JSON and reports
// the shortest ambiguous string (if any) with two witness parse trees.
//
// Usage: ambcheck [grammar.json]   (defaults to stdin)
//
// Input format:
//
//	{
//	  "terminals": ["a", "+"],
//	  "nonterminals": ["E"],
//	  "start": "E",
//	  "rules": [{"lhs": "E", "rhs": ["E", "+", "E"]}, {"lhs": "E", "rhs": ["a"]}],
//	  "maxLength": 8,
//	  "budget": 1000000
//	}
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"ambcheck"
)

type input struct {
	Terminals    []string        `json:"terminals"`
	NonTerminals []string        `json:"nonterminals"`
	Start        string          `json:"start"`
	Rules        []ambcheck.Rule `json:"rules"`
	MaxLength    int             `json:"maxLength"`
	Budget       int             `json:"budget"`
}

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var in input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(fmt.Errorf("decoding input: %w", err))
	}
	g := &ambcheck.Grammar{
		Terminals:    in.Terminals,
		NonTerminals: in.NonTerminals,
		Start:        in.Start,
		Rules:        in.Rules,
	}
	res, err := ambcheck.Check(g, ambcheck.Options{MaxLength: in.MaxLength, Budget: in.Budget})
	if err != nil {
		fatal(err)
	}
	if res.Ambiguous {
		if err := res.Verify(g); err != nil {
			fatal(fmt.Errorf("self-verification failed: %w", err))
		}
	}
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
	switch {
	case res.Ambiguous:
		fmt.Printf("shortest ambiguous string: %s\n\n", strings.Join(res.String, " "))
		fmt.Printf("tree 1:\n%s\ntree 2:\n%s", res.Tree1, res.Tree2)
	case res.BudgetExceeded:
		fmt.Println("inconclusive: state budget exhausted")
	default:
		fmt.Println("no ambiguous string within the length bound")
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ambcheck:", err)
	os.Exit(1)
}
