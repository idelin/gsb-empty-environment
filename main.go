package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const usage = `cogbin - discrete-position bin packing with an exact center-of-gravity constraint

usage:
  cogbin solve  [input.json]     solve instance (stdin when no file), print solution JSON
  cogbin verify [solution.json]  re-verify a solution document (embeds its input)

all quantities are exact rationals; weights/positions may be given as
strings ("3/2", "1.5", "7") or JSON numbers.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "solve":
		err = cmdSolve(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func readArg(args []string) ([]byte, error) {
	if len(args) == 0 {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(args[0])
}

func writeJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdSolve(args []string) error {
	data, err := readArg(args)
	if err != nil {
		return err
	}
	in, err := ParseInput(data)
	if err != nil {
		return err
	}
	sol, err := Solve(in)
	if err != nil {
		if errors.Is(err, ErrInfeasible) {
			return writeJSON(&Solution{Status: "infeasible", Bins: []BinReport{}, Input: data})
		}
		return err
	}
	sol.Input = data
	return writeJSON(sol)
}

func cmdVerify(args []string) error {
	data, err := readArg(args)
	if err != nil {
		return err
	}
	var sol Solution
	if err := json.Unmarshal(data, &sol); err != nil {
		return fmt.Errorf("invalid solution JSON: %v", err)
	}
	if len(sol.Input) == 0 {
		return fmt.Errorf("solution document does not embed its input")
	}
	in, err := ParseInput(sol.Input)
	if err != nil {
		return err
	}
	probs := Verify(in, &sol)
	report := struct {
		Valid    bool     `json:"valid"`
		Problems []string `json:"problems"`
	}{Valid: len(probs) == 0, Problems: probs}
	if report.Problems == nil {
		report.Problems = []string{}
	}
	if err := writeJSON(report); err != nil {
		return err
	}
	if !report.Valid {
		return errors.New("verification failed")
	}
	return nil
}
