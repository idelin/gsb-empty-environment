package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `whitelist - deserialization whitelist danger checker

The danger condition: one identical runtime object is reachable from two
designated root fields (shared reference, possibly through cycles).

usage:
  whitelist solve  <spec.json>               find a minimum-node dangerous object graph
  whitelist verify <spec.json> <graph.json>  independently verify a serialized graph
  whitelist example                          print an example spec to stdout
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "solve":
		err = cmdSolve(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "example":
		err = cmdExample()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdSolve(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: whitelist solve <spec.json>")
	}
	spec, err := loadSpec(args[0])
	if err != nil {
		return err
	}
	m, err := buildModel(spec)
	if err != nil {
		return err
	}
	sol, err := solve(m)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(buildResult(m, sol), "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

func cmdVerify(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: whitelist verify <spec.json> <graph.json>")
	}
	spec, err := loadSpec(args[0])
	if err != nil {
		return err
	}
	m, err := buildModel(spec)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("parse %s: %w", args[1], err)
	}
	failed := 0
	for _, c := range verifyGraph(m, &res) {
		if c.err == nil {
			fmt.Printf("PASS  %s\n", c.name)
		} else {
			fmt.Printf("FAIL  %s: %v\n", c.name, c.err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("verification failed (%d check(s))", failed)
	}
	fmt.Println("VERIFY OK")
	return nil
}

func cmdExample() error {
	spec := Spec{
		RootTypes:     []string{"Session"},
		DangerFields:  []string{"primary", "backup"},
		MaxObjects:    8,
		TriggeredOnly: true,
		Types: []TypeSpec{
			{
				Name: "Session",
				Fields: []FieldSpec{
					{Name: "primary", Type: "Handler"},
					{Name: "backup", Type: "Handler"},
				},
			},
			{
				Name:   "Handler",
				Fields: []FieldSpec{{Name: "next", Type: "Handler"}},
			},
			{
				Name:     "EvilHandler",
				Triggers: []string{"readObject"},
				Fields:   []FieldSpec{{Name: "next", Type: "Handler"}},
			},
		},
		Subtypes: map[string][]string{
			"Handler": {"Handler", "EvilHandler"},
		},
	}
	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
