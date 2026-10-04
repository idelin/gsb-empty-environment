// Command iqp solves a bounded integer quadratic program given as JSON on
// stdin and prints the result as JSON. Integers are decimal strings so
// arbitrary sizes are supported.
//
// Input:
//
//	{"Q":[["2","-1"],["-1","2"]], "c":["0","0"], "const":"0",
//	 "lo":["-5","-5"], "hi":["5","5"], "budget":1000000}
//
// budget is the maximum number of branch-and-bound nodes; use a negative
// number (or omit) for no limit.
package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"iqp"
)

type input struct {
	Q      [][]string `json:"Q"`
	C      []string   `json:"c"`
	Const  string     `json:"const"`
	Lo     []string   `json:"lo"`
	Hi     []string   `json:"hi"`
	Budget int64      `json:"budget"`
}

type output struct {
	Status string   `json:"status"`
	X      []string `json:"x,omitempty"`
	Upper  string   `json:"upper,omitempty"`
	Lower  string   `json:"lower"`
	Nodes  int64    `json:"nodes"`
}

func parseInt(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		fatal("bad integer: %q", s)
	}
	return v
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "iqp: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	var in input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		fatal("decoding input: %v", err)
	}
	p := &iqp.Problem{
		Q:     make([][]*big.Int, len(in.Q)),
		C:     make([]*big.Int, len(in.C)),
		Const: big.NewInt(0),
		Lo:    make([]*big.Int, len(in.Lo)),
		Hi:    make([]*big.Int, len(in.Hi)),
	}
	for i, row := range in.Q {
		p.Q[i] = make([]*big.Int, len(row))
		for j, s := range row {
			p.Q[i][j] = parseInt(s)
		}
	}
	for i, s := range in.C {
		p.C[i] = parseInt(s)
	}
	if in.Const != "" {
		p.Const = parseInt(in.Const)
	}
	for i, s := range in.Lo {
		p.Lo[i] = parseInt(s)
	}
	for i, s := range in.Hi {
		p.Hi[i] = parseInt(s)
	}
	if err := p.Validate(); err != nil {
		fatal("%v", err)
	}
	budget := in.Budget
	if budget == 0 {
		budget = -1
	}
	res := p.Solve(budget)
	out := output{Status: res.Status.String(), Nodes: res.Nodes}
	if res.X != nil {
		out.X = make([]string, len(res.X))
		for i, v := range res.X {
			out.X[i] = v.String()
		}
		out.Upper = res.Upper.String()
	}
	out.Lower = res.Lower.String()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal("encoding result: %v", err)
	}
}
