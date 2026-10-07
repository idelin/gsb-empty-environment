// Command psdcomp solves a PSD matrix completion problem given as JSON
// on stdin (or via -input) and prints the optimal completion, its
// all-principal-minors certificate, and search statistics as JSON.
//
// Input schema:
//
//	{
//	  "n": 3,
//	  "fixed":     [{"i":0,"j":0,"value":"1"}, ...],
//	  "variables": [{"i":0,"j":1,"candidates":["-1/2","0","1/2"]}, ...]
//	}
//
// Rationals are strings like "3", "-2", or "22/7".
//
// Flags:
//
//	-input path     read problem from file instead of stdin
//	-evidence path  write every pruning event as JSON lines to this file
//	-verify=false   skip independent verification (elimination PSD test,
//	                dual determinant algorithms, brute-force agreement)
//	-describe       print a human-readable report instead of JSON
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"

	"psdcomp"
)

type problemJSON struct {
	N     int `json:"n"`
	Fixed []struct {
		I     int    `json:"i"`
		J     int    `json:"j"`
		Value string `json:"value"`
	} `json:"fixed"`
	Variables []struct {
		I          int      `json:"i"`
		J          int      `json:"j"`
		Candidates []string `json:"candidates"`
	} `json:"variables"`
}

type minorJSON struct {
	Indices []int  `json:"indices"`
	Det     string `json:"det"`
}

type outputJSON struct {
	Feasible     bool        `json:"feasible"`
	Trace        string      `json:"trace,omitempty"`
	Matrix       [][]string  `json:"matrix,omitempty"`
	Certificate  []minorJSON `json:"certificate,omitempty"`
	Stats        statsJSON   `json:"stats"`
	Infeasible   *infeasJSON `json:"infeasible,omitempty"`
	Verification string      `json:"verification"`
}

type statsJSON struct {
	Combos      uint64 `json:"combos"`
	Nodes       uint64 `json:"nodes"`
	Leaves      uint64 `json:"leaves"`
	PrunedMinor uint64 `json:"pruned_minor"`
	PrunedTrace uint64 `json:"pruned_trace"`
}

type infeasJSON struct {
	Reason string      `json:"reason"`
	Minors []minorJSON `json:"minors,omitempty"`
}

type pruneJSON struct {
	Depth  int                 `json:"depth"`
	Prefix []map[string]string `json:"prefix"`
	Reason string              `json:"reason"`
	Minor  *minorJSON          `json:"minor,omitempty"`
	Bound  string              `json:"bound,omitempty"`
	Best   string              `json:"best,omitempty"`
}

func parseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid rational %q", s)
	}
	return r, nil
}

func main() {
	input := flag.String("input", "", "problem JSON file (default: stdin)")
	evidence := flag.String("evidence", "", "write per-branch pruning events (JSON lines) to this file")
	verify := flag.Bool("verify", true, "independently verify the result")
	describe := flag.Bool("describe", false, "human-readable report instead of JSON")
	flag.Parse()

	var data []byte
	var err error
	if *input != "" {
		data, err = os.ReadFile(*input)
	} else {
		data, err = readAllStdin()
	}
	must(err)

	var pj problemJSON
	must(json.Unmarshal(data, &pj))

	p := &psdcomp.Problem{N: pj.N}
	for _, f := range pj.Fixed {
		v, err := parseRat(f.Value)
		must(err)
		p.Fixed = append(p.Fixed, psdcomp.FixedEntry{I: f.I, J: f.J, Value: v})
	}
	for _, v := range pj.Variables {
		variable := psdcomp.Variable{I: v.I, J: v.J}
		for _, cs := range v.Candidates {
			c, err := parseRat(cs)
			must(err)
			variable.Candidates = append(variable.Candidates, c)
		}
		p.Vars = append(p.Vars, variable)
	}

	solver := &psdcomp.Solver{}
	var evFile *os.File
	var evWriter *bufio.Writer
	if *evidence != "" {
		evFile, err = os.Create(*evidence)
		must(err)
		defer evFile.Close()
		evWriter = bufio.NewWriter(evFile)
		defer evWriter.Flush()
		enc := json.NewEncoder(evWriter)
		solver.OnPrune = func(ev psdcomp.PruneEvent) {
			pj := pruneJSON{Depth: ev.Depth, Reason: ev.Reason.String()}
			for _, a := range ev.Prefix {
				pj.Prefix = append(pj.Prefix, map[string]string{
					"pos":   fmt.Sprintf("(%d,%d)", a.I, a.J),
					"value": a.Value.RatString(),
				})
			}
			if ev.Minor != nil {
				pj.Minor = &minorJSON{Indices: ev.Minor.Indices, Det: ev.Minor.Det.RatString()}
			}
			if ev.Bound != nil {
				pj.Bound = ev.Bound.RatString()
				pj.Best = ev.Best.RatString()
			}
			if err := enc.Encode(pj); err != nil {
				fmt.Fprintln(os.Stderr, "evidence encode error:", err)
			}
		}
	}

	res, err := solver.Solve(p)
	must(err)

	if *describe {
		fmt.Print(res.Describe())
		fmt.Printf("stats: combos=%d nodes=%d leaves=%d pruned_minor=%d pruned_trace=%d\n",
			res.Stats.Combos, res.Stats.Nodes, res.Stats.Leaves,
			res.Stats.PrunedMinor, res.Stats.PrunedTrace)
	} else {
		out := outputJSON{
			Feasible: res.Feasible,
			Stats: statsJSON{
				Combos:      res.Stats.Combos,
				Nodes:       res.Stats.Nodes,
				Leaves:      res.Stats.Leaves,
				PrunedMinor: res.Stats.PrunedMinor,
				PrunedTrace: res.Stats.PrunedTrace,
			},
		}
		if res.Feasible {
			out.Trace = res.Trace.RatString()
			for i := 0; i < res.Matrix.N; i++ {
				var row []string
				for j := 0; j < res.Matrix.N; j++ {
					row = append(row, res.Matrix.Get(i, j).RatString())
				}
				out.Matrix = append(out.Matrix, row)
			}
			for _, c := range res.Certificate {
				out.Certificate = append(out.Certificate, minorJSON{Indices: c.Indices, Det: c.Det.RatString()})
			}
		} else {
			out.Infeasible = &infeasJSON{Reason: res.Infeasible.Reason}
			for _, m := range res.Infeasible.Minors {
				out.Infeasible.Minors = append(out.Infeasible.Minors, minorJSON{Indices: m.Indices, Det: m.Det.RatString()})
			}
		}
		if *verify {
			if err := psdcomp.VerifyResult(p, res); err != nil {
				out.Verification = "FAILED: " + err.Error()
			} else {
				out.Verification = "ok (certificate >= 0, dual determinants agree, elimination PSD test passed, brute-force agreement)"
			}
		} else {
			out.Verification = "skipped"
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		must(enc.Encode(out))
		if *verify && out.Verification != "" && out.Verification[:2] == "FA" {
			os.Exit(1)
		}
	}
}

func readAllStdin() ([]byte, error) {
	return io.ReadAll(os.Stdin)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "psdcomp:", err)
		os.Exit(2)
	}
}
