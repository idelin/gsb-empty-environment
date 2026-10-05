// Command matchtool solves weighted bipartite assignment problems.
//
// Usage:
//
//	matchtool solve  [-in file]   read an Instance JSON, print results
//	matchtool brute  [-in file]   independent exhaustive re-computation
//	matchtool verify [-in file]   check {"instance":..., "result":...}
//
// Input is read from stdin when -in is omitted. "solve" applies the
// optional updates array one edge at a time and prints the initial
// result plus one result per update; "brute" prints the exhaustive
// answer after all updates (independent exact recomputation path);
// "verify" exits non-zero unless every certificate check passes.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"matchtool/match"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	in := fs.String("in", "", "input JSON file (default stdin)")
	fs.Parse(os.Args[2:])

	data, err := readAll(*in)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "solve":
		var inst match.Instance
		if err := json.Unmarshal(data, &inst); err != nil {
			fatal(err)
		}
		s := match.NewSolver(inst.Tasks, inst.Employees, inst.Edges)
		out := struct {
			Initial match.Result   `json:"initial"`
			Steps   []match.Result `json:"steps"`
		}{Initial: s.Solve()}
		for _, u := range inst.Updates {
			switch u.Op {
			case "add":
				s.AddEdge(u.Task, u.Employee, u.Profit)
			case "remove":
				s.RemoveEdge(u.Task, u.Employee)
			default:
				fatal(fmt.Errorf("unknown update op %q", u.Op))
			}
			out.Steps = append(out.Steps, s.Solve())
		}
		writeJSON(out)

	case "brute":
		var inst match.Instance
		if err := json.Unmarshal(data, &inst); err != nil {
			fatal(err)
		}
		edges := inst.Edges
		for _, u := range inst.Updates {
			switch u.Op {
			case "add":
				edges = append(edges, match.Edge{Task: u.Task, Employee: u.Employee, Profit: u.Profit})
			case "remove":
				kept := edges[:0]
				for _, e := range edges {
					if e.Task != u.Task || e.Employee != u.Employee {
						kept = append(kept, e)
					}
				}
				edges = kept
			default:
				fatal(fmt.Errorf("unknown update op %q", u.Op))
			}
		}
		writeJSON(match.BruteSolve(inst.Tasks, inst.Employees, edges))

	case "verify":
		var payload struct {
			Instance match.Instance `json:"instance"`
			Result   match.Result   `json:"result"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			fatal(err)
		}
		if err := match.Verify(payload.Instance.Tasks, payload.Instance.Employees,
			payload.Instance.Edges, &payload.Result); err != nil {
			fmt.Fprintln(os.Stderr, "INVALID:", err)
			os.Exit(1)
		}
		fmt.Println("OK")

	default:
		usage()
		os.Exit(2)
	}
}

func readAll(path string) ([]byte, error) {
	if path == "" {
		return ioutil.ReadAll(os.Stdin)
	}
	return ioutil.ReadFile(path)
}

func writeJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: matchtool {solve|brute|verify} [-in file] < input.json")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
