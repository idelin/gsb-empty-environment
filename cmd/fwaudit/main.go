// Command fwaudit audits a firewall-firmware bytecode program (JSON) for
// reachable out-of-bounds array reads and prints, for each vulnerable
// load, the lexicographically smallest input that replays the fault.
//
// Usage: fwaudit <program.json>
package main

import (
	"encoding/json"
	"fmt"
	"os"

	fwaudit "example.org/fwaudit"
)

type jsonFinding struct {
	Instr    int    `json:"instr"`
	Array    int    `json:"array"`
	Index    uint8  `json:"index"`
	Input    []int  `json:"input"`
	InputHex string `json:"input_hex"`
}

type jsonReport struct {
	Findings  []jsonFinding `json:"findings"`
	Paths     int           `json:"paths"`
	Truncated bool          `json:"truncated"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fwaudit <program.json>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "fwaudit:", err)
		os.Exit(1)
	}
	prog, err := fwaudit.ParseJSON(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fwaudit:", err)
		os.Exit(1)
	}
	rep, err := fwaudit.Audit(prog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fwaudit:", err)
		os.Exit(1)
	}
	out := jsonReport{Paths: rep.Paths, Truncated: rep.Truncated}
	for _, f := range rep.Findings {
		jf := jsonFinding{Instr: f.Instr, Array: f.Array, Index: f.Index}
		for _, b := range f.Input {
			jf.Input = append(jf.Input, int(b))
			jf.InputHex += fmt.Sprintf("%02x", b)
		}
		out.Findings = append(out.Findings, jf)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "fwaudit:", err)
		os.Exit(1)
	}
}
