package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

const usage = `backup-planner - pick a minimum-byte file set restoring every shard

usage:
  planner solve  [-budget N] [-input file]   read planner input JSON, write plan JSON
  planner verify [-input file]               read {"input":...,"solution":...} JSON, check it

budget: maximum number of search nodes expanded; negative means unlimited.
stdin is used when -input is omitted.`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "solve":
		fs := flag.NewFlagSet("solve", flag.ContinueOnError)
		budget := fs.Int64("budget", -1, "node expansion budget (negative = unlimited)")
		input := fs.String("input", "", "input JSON file (default: stdin)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		data, err := readAll(*input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var in Input
		if err := json.Unmarshal(data, &in); err != nil {
			return writeJSON(&SolveOutput{Status: StatusError, Errors: []DiagError{{Kind: "invalid_json", Detail: err.Error()}}, Budget: *budget}, 1)
		}
		b := *budget
		if b < 0 && in.Budget != nil {
			b = *in.Budget
		}
		out := solve(&in, b)
		code := 0
		if out.Status == StatusError {
			code = 1
		}
		return writeJSON(out, code)
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ContinueOnError)
		input := fs.String("input", "", "witness JSON file (default: stdin)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		data, err := readAll(*input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var req VerifyRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return writeJSON(&VerifyOutput{Valid: false, Reason: "invalid json: " + err.Error()}, 1)
		}
		out := verify(&req)
		code := 0
		if !out.Valid {
			code = 1
		}
		return writeJSON(out, code)
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
}

func readAll(path string) ([]byte, error) {
	if path == "" {
		return ioutil.ReadAll(os.Stdin)
	}
	return ioutil.ReadFile(path)
}

func writeJSON(v interface{}, code int) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return code
}
