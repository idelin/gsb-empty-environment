package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

type proofEntry struct {
	Index    int      `json:"index"`
	Interval Interval `json:"interval"`
	Point    int64    `json:"point"`
}

type infeasibleEntry struct {
	Index    int      `json:"index"`
	Interval Interval `json:"interval"`
}

type bounds struct {
	Lower int64 `json:"lower"`
	Upper int64 `json:"upper"`
}

type stats struct {
	Nodes             int64 `json:"nodes"`
	ElapsedMs         int64 `json:"elapsedMs"`
	Candidates        int   `json:"candidates"`
	CandidatesDropped int   `json:"candidatesDropped"`
}

type verifyResult struct {
	Method     string  `json:"method"`
	Subsets    uint64  `json:"subsets,omitempty"`
	Match      *bool   `json:"match,omitempty"`
	Note       string  `json:"note,omitempty"`
	EnumCost   *int64  `json:"enumCost,omitempty"`
	EnumPoints []int64 `json:"enumPoints,omitempty"`
}

type report struct {
	Status     string            `json:"status"` // optimal | interrupted | infeasible
	Cost       *int64            `json:"cost,omitempty"`
	Points     []int64           `json:"points,omitempty"`
	Proof      []proofEntry      `json:"proof,omitempty"`
	Infeasible []infeasibleEntry `json:"infeasibleIntervals,omitempty"`
	Bounds     *bounds           `json:"bounds,omitempty"`
	Stats      stats             `json:"stats"`
	Verify     *verifyResult     `json:"verification,omitempty"`
}

func equalPoints(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func run(in *Input, nodeLimit, timeLimitMs int64, doVerify bool, maxEnum int) (*report, error) {
	p, err := prepare(in)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	rep := &report{}
	rep.Stats.Candidates = len(p.points)
	rep.Stats.CandidatesDropped = p.dropped

	var sol *Solver
	if len(p.infeasible) > 0 {
		rep.Status = "infeasible"
		for _, oi := range p.infeasible {
			rep.Infeasible = append(rep.Infeasible, infeasibleEntry{Index: oi, Interval: in.Intervals[oi]})
		}
	} else {
		sol = &Solver{p: p, nodeLimit: nodeLimit}
		if timeLimitMs > 0 {
			sol.deadline = start.Add(time.Duration(timeLimitMs) * time.Millisecond)
		}
		gpts, gcost, _ := greedy(p)
		sol.best, sol.bestCost, sol.hasBest = sortedCopy(gpts), gcost, true
		sol.search(make([]uint64, p.nWords), nil, 0)
		rep.Stats.Nodes = sol.nodes

		if sol.interrupted {
			rep.Status = "interrupted"
			lb, _ := sol.lowerBound()
			rep.Bounds = &bounds{Lower: lb, Upper: sol.bestCost}
		} else {
			rep.Status = "optimal"
			rep.Bounds = &bounds{Lower: sol.bestCost, Upper: sol.bestCost}
		}
		rep.Points = sol.best
		rep.Cost = &sol.bestCost

		// Per-interval stabbing proof: for every input interval, name one
		// chosen point inside it (cheapest, then smallest).
		costOf := map[int64]int64{}
		for j, pt := range p.points {
			costOf[pt] = p.costs[j]
		}
		for oi, iv := range in.Intervals {
			var bestPt, bestC int64
			first := true
			for _, pt := range sol.best {
				if iv.Lo <= pt && pt <= iv.Hi {
					c := costOf[pt]
					if first || c < bestC || (c == bestC && pt < bestPt) {
						bestPt, bestC, first = pt, c, false
					}
				}
			}
			rep.Proof = append(rep.Proof, proofEntry{Index: oi, Interval: iv, Point: bestPt})
		}
	}
	rep.Stats.ElapsedMs = time.Since(start).Milliseconds()

	if doVerify {
		er := enumerate(p, maxEnum)
		v := &verifyResult{Method: "enumeration"}
		switch {
		case er.Skipped:
			v.Note = fmt.Sprintf("skipped: %d candidates exceed -max-enum %d", len(p.points), maxEnum)
		default:
			v.Subsets = er.Subsets
			if er.Feasible {
				v.EnumCost = &er.Cost
				v.EnumPoints = er.Points
			}
			switch {
			case rep.Status == "infeasible":
				ok := !er.Feasible
				v.Match = &ok
				if !ok {
					v.Note = "MISMATCH: enumeration found a feasible solution"
				}
			case !er.Feasible:
				ok := false
				v.Match = &ok
				v.Note = "MISMATCH: enumeration reports infeasible but solver produced a solution"
			case rep.Status == "interrupted":
				ok := rep.Bounds.Lower <= er.Cost && er.Cost <= rep.Bounds.Upper
				v.Match = &ok
				v.Note = "interrupted: checked lower <= enumeration optimum <= upper"
			default: // optimal
				ok := er.Cost == sol.bestCost && equalPoints(er.Points, sol.best)
				v.Match = &ok
				if !ok {
					v.Note = "MISMATCH: solver and enumeration disagree"
				}
			}
		}
		rep.Verify = v
	}
	return rep, nil
}

func main() {
	var inPath string
	var doVerify bool
	var maxEnum int
	var nodeLimit, timeLimitMs int64
	flag.StringVar(&inPath, "in", "-", "input JSON file, or - for stdin")
	flag.BoolVar(&doVerify, "verify", true, "cross-check with exhaustive candidate-subset enumeration")
	flag.IntVar(&maxEnum, "max-enum", 22, "maximum candidate count for enumeration cross-check")
	flag.Int64Var(&nodeLimit, "node-limit", 0, "branch-and-bound node limit, 0 = unlimited")
	flag.Int64Var(&timeLimitMs, "time-limit", 0, "wall-clock limit in milliseconds, 0 = unlimited")
	flag.Parse()

	var data []byte
	var err error
	if inPath == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(inPath)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(2)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(2)
	}
	rep, err := run(&in, nodeLimit, timeLimitMs, doVerify, maxEnum)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid input:", err)
		os.Exit(2)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	if rep.Verify != nil && rep.Verify.Match != nil && !*rep.Verify.Match {
		os.Exit(1)
	}
}
