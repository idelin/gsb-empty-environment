// Command bpmatch is a JSON CLI for exact weighted bipartite matching.
//
//	bpmatch solve  [-in input.json] [-out result.json]
//	bpmatch update [-in input.json] [-out result.json] (-add TASK=EMP:PROFIT | -del TASK=EMP)
//	bpmatch verify -in input.json -sol result.json
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"bpmatch/internal/matcher"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: bpmatch <solve|update|verify> [flags]")
	}
	switch args[0] {
	case "solve":
		fs := flag.NewFlagSet("solve", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		in := fs.String("in", "", "input JSON file (default: stdin)")
		out := fs.String("out", "", "output JSON file (default: stdout)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		inst, err := readInstance(*in, stdin)
		if err != nil {
			return err
		}
		res, err := matcher.SolveInstance(inst)
		if err != nil {
			return err
		}
		return writeJSON(*out, stdout, matcher.ToJSON(res))

	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		in := fs.String("in", "", "input JSON file (default: stdin)")
		out := fs.String("out", "", "output JSON file (default: stdout)")
		add := fs.String("add", "", "add or re-price one edge: TASK=EMP:PROFIT")
		del := fs.String("del", "", "remove one edge: TASK=EMP")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if (*add == "") == (*del == "") {
			return errors.New("update needs exactly one of -add / -del")
		}
		inst, err := readInstance(*in, stdin)
		if err != nil {
			return err
		}
		if *add != "" {
			e, err := parseAdd(*add)
			if err != nil {
				return err
			}
			if err := matcher.AddEdge(inst, e); err != nil {
				return err
			}
		} else {
			task, emp, err := parseDel(*del)
			if err != nil {
				return err
			}
			if err := matcher.RemoveEdge(inst, task, emp); err != nil {
				return err
			}
		}
		// The updated instance is re-solved exactly, so the result is
		// consistent with a from-scratch solve by construction.
		res, err := matcher.SolveInstance(inst)
		if err != nil {
			return err
		}
		payload := struct {
			Input  *matcher.Instance   `json:"input"`
			Result *matcher.ResultJSON `json:"result"`
		}{inst, matcher.ToJSON(res)}
		return writeJSON(*out, stdout, payload)

	case "verify":
		fs := flag.NewFlagSet("verify", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		in := fs.String("in", "", "input JSON file")
		sol := fs.String("sol", "", "solution JSON file to check")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *in == "" || *sol == "" {
			return errors.New("verify requires -in and -sol")
		}
		inst, err := readInstance(*in, stdin)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(*sol)
		if err != nil {
			return err
		}
		var rj matcher.ResultJSON
		if err := json.Unmarshal(data, &rj); err != nil {
			return fmt.Errorf("decoding solution: %w", err)
		}
		res, err := rj.ToResult()
		if err != nil {
			return err
		}
		checks, err := matcher.VerifyInstance(inst, res)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "OK: %s\n", strings.Join(checks, ", "))
		return nil
	}
	return fmt.Errorf("unknown subcommand %q", args[0])
}

func readInstance(path string, stdin io.Reader) (*matcher.Instance, error) {
	var data []byte
	var err error
	if path == "" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var inst matcher.Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		return nil, fmt.Errorf("decoding input: %w", err)
	}
	return &inst, nil
}

func writeJSON(path string, stdout io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "" {
		_, err = stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// parseAdd parses "TASK=EMP:PROFIT" (IDs may not contain '=' or ':').
func parseAdd(s string) (matcher.Edge, error) {
	var e matcher.Edge
	pair, profit, ok := strings.Cut(s, ":")
	if !ok {
		return e, fmt.Errorf("bad -add %q, want TASK=EMP:PROFIT", s)
	}
	task, emp, ok := strings.Cut(pair, "=")
	if !ok {
		return e, fmt.Errorf("bad -add %q, want TASK=EMP:PROFIT", s)
	}
	p, err := strconv.ParseInt(profit, 10, 64)
	if err != nil {
		return e, fmt.Errorf("bad profit in %q: %w", s, err)
	}
	e.Task, e.Employee, e.Profit = task, emp, p
	return e, nil
}

// parseDel parses "TASK=EMP".
func parseDel(s string) (string, string, error) {
	task, emp, ok := strings.Cut(s, "=")
	if !ok {
		return "", "", fmt.Errorf("bad -del %q, want TASK=EMP", s)
	}
	return task, emp, nil
}
