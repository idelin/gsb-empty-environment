// Command stabber solves the minimum-cost stabbing problem: given closed
// integer intervals and candidate points with non-negative costs, choose a
// subset of candidates stabbing every interval. Objectives, in order:
// minimize total cost, then number of points, then lexicographic order of
// the sorted chosen coordinates.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"time"
)

type rawCandidate struct {
	X    int64  `json:"x"`
	Cost *int64 `json:"cost"`
}

type rawInput struct {
	Intervals  [][2]int64     `json:"intervals"`
	Candidates []rawCandidate `json:"candidates"`
	MaxNodes   int64          `json:"maxNodes"`
	TimeoutMs  int64          `json:"timeoutMs"`
	Verify     *bool          `json:"verify"`
	VerifyMax  int            `json:"verifyMax"`
}

type proofEntry struct {
	Index    int      `json:"index"`
	Interval [2]int64 `json:"interval"`
	Point    int64    `json:"point"`
}

type infeasibleEntry struct {
	Index                 int      `json:"index"`
	Interval              [2]int64 `json:"interval"`
	Message               string   `json:"message"`
	NearestCandidateBelow *int64   `json:"nearestCandidateBelow,omitempty"`
	NearestCandidateAbove *int64   `json:"nearestCandidateAbove,omitempty"`
}

type boundsOut struct {
	LowerCost int64  `json:"lowerCost"`
	UpperCost *int64 `json:"upperCost"`
}

type verificationOut struct {
	Performed         bool    `json:"performed"`
	Reason            string  `json:"reason,omitempty"`
	SubsetsEnumerated uint64  `json:"subsetsEnumerated,omitempty"`
	FeasibleSubsets   uint64  `json:"feasibleSubsets,omitempty"`
	OptimalCost       *int64  `json:"optimalCost,omitempty"`
	OptimalCount      *int    `json:"optimalCount,omitempty"`
	OptimalPoints     []int64 `json:"optimalPoints,omitempty"`
	MatchesSolver     *bool   `json:"matchesSolver,omitempty"`
}

type statsOut struct {
	Intervals         int   `json:"intervals"`
	Candidates        int   `json:"candidates"`
	DuplicatesRemoved int   `json:"duplicatesRemoved"`
	Nodes             int64 `json:"nodes"`
	ElapsedMs         int64 `json:"elapsedMs"`
}

type output struct {
	Status       string            `json:"status"` // optimal | interrupted | infeasible | error
	Error        string            `json:"error,omitempty"`
	Cost         *int64            `json:"cost,omitempty"`
	Count        *int              `json:"count,omitempty"`
	Points       []int64           `json:"points,omitempty"`
	Proof        []proofEntry      `json:"proof,omitempty"`
	Infeasible   []infeasibleEntry `json:"infeasibleEvidence,omitempty"`
	Bounds       *boundsOut        `json:"bounds,omitempty"`
	Verification *verificationOut  `json:"verification,omitempty"`
	Stats        statsOut          `json:"stats"`
}

func solve(raw rawInput) output {
	start := time.Now()
	out := output{Status: "error"}

	ivs := make([]Interval, len(raw.Intervals))
	for i, p := range raw.Intervals {
		if p[0] > p[1] {
			out.Error = fmt.Sprintf("interval %d [%d, %d] has l > r", i, p[0], p[1])
			return out
		}
		ivs[i] = Interval{L: p[0], R: p[1]}
	}

	minCostAt := make(map[int64]int64)
	for i, c := range raw.Candidates {
		if c.Cost == nil {
			out.Error = fmt.Sprintf("candidate %d (x=%d) is missing required field \"cost\"", i, c.X)
			return out
		}
		if *c.Cost < 0 {
			out.Error = fmt.Sprintf("candidate %d (x=%d) has negative cost %d", i, c.X, *c.Cost)
			return out
		}
		if old, ok := minCostAt[c.X]; !ok || *c.Cost < old {
			minCostAt[c.X] = *c.Cost
		}
	}
	dups := len(raw.Candidates) - len(minCostAt)
	xs := make([]int64, 0, len(minCostAt))
	for x := range minCostAt {
		xs = append(xs, x)
	}
	sort.Slice(xs, func(a, b int) bool { return xs[a] < xs[b] })
	cost := make([]int64, len(xs))
	for j, x := range xs {
		cost[j] = minCostAt[x]
	}
	out.Stats = statsOut{Intervals: len(ivs), Candidates: len(xs), DuplicatesRemoved: dups}

	if len(ivs) == 0 {
		zero := int64(0)
		zc := 0
		out.Status = "optimal"
		out.Cost = &zero
		out.Count = &zc
		out.Points = []int64{}
		out.Proof = []proofEntry{}
		out.Bounds = &boundsOut{LowerCost: 0, UpperCost: &zero}
		out.Stats.ElapsedMs = int64(time.Since(start) / time.Millisecond)
		return out
	}

	var bad []infeasibleEntry
	for i, iv := range ivs {
		j := sort.Search(len(xs), func(j int) bool { return xs[j] >= iv.L })
		if j >= len(xs) || xs[j] > iv.R {
			e := infeasibleEntry{
				Index:    i,
				Interval: [2]int64{iv.L, iv.R},
				Message:  fmt.Sprintf("no candidate point in [%d, %d]", iv.L, iv.R),
			}
			if j > 0 {
				v := xs[j-1]
				e.NearestCandidateBelow = &v
			}
			if j < len(xs) {
				v := xs[j]
				e.NearestCandidateAbove = &v
			}
			bad = append(bad, e)
		}
	}
	if len(bad) > 0 {
		out.Status = "infeasible"
		out.Infeasible = bad
		out.Stats.ElapsedMs = int64(time.Since(start) / time.Millisecond)
		return out
	}

	s := newSolver(ivs, xs, cost, raw.MaxNodes, raw.TimeoutMs)
	s.solve()
	out.Stats.Nodes = s.nodes

	c := s.bestCost
	cnt := len(s.bestPts)
	out.Cost = &c
	out.Count = &cnt
	out.Points = s.bestPts
	out.Proof = buildProof(ivs, s.bestPts)

	if s.interrupted {
		out.Status = "interrupted"
		lower := s.liveLB
		if s.bestCost < lower {
			lower = s.bestCost
		}
		upper := s.bestCost
		out.Bounds = &boundsOut{LowerCost: lower, UpperCost: &upper}
	} else {
		out.Status = "optimal"
		out.Bounds = &boundsOut{LowerCost: s.bestCost, UpperCost: &s.bestCost}
	}

	verify := true
	if raw.Verify != nil {
		verify = *raw.Verify
	}
	vmax := raw.VerifyMax
	if vmax <= 0 {
		vmax = 20
	}
	if verify {
		out.Verification = runVerification(xs, cost, ivs, vmax, s)
	}
	out.Stats.ElapsedMs = int64(time.Since(start) / time.Millisecond)
	return out
}

// buildProof emits, for every interval (input order), one chosen point that
// stabs it.
func buildProof(ivs []Interval, pts []int64) []proofEntry {
	proof := make([]proofEntry, len(ivs))
	for i, iv := range ivs {
		j := sort.Search(len(pts), func(j int) bool { return pts[j] >= iv.L })
		proof[i] = proofEntry{Index: i, Interval: [2]int64{iv.L, iv.R}, Point: pts[j]}
	}
	return proof
}

func runVerification(xs, cost []int64, ivs []Interval, vmax int, s *solver) *verificationOut {
	v := &verificationOut{}
	if len(xs) > vmax {
		v.Reason = fmt.Sprintf("skipped: %d candidates exceed verifyMax=%d", len(xs), vmax)
		return v
	}
	if len(xs) > 26 {
		v.Reason = fmt.Sprintf("skipped: %d candidates exceed the hard cap of 26", len(xs))
		return v
	}
	w := uint64((len(ivs) + 63) / 64)
	if w == 0 {
		w = 1
	}
	if (uint64(1)<<uint(len(xs)))*w*8 > 512<<20 {
		v.Reason = "skipped: enumeration would exceed the 512MB memory budget"
		return v
	}
	res := enumerate(xs, cost, ivs)
	v.Performed = true
	v.SubsetsEnumerated = res.total
	v.FeasibleSubsets = res.feasible
	if res.hasBest {
		oc := res.bestCost
		ok := res.bestCnt
		v.OptimalCost = &oc
		v.OptimalCount = &ok
		v.OptimalPoints = res.bestPts
		match := s.hasBest && s.bestCost == res.bestCost &&
			len(s.bestPts) == res.bestCnt && equalInts(s.bestPts, res.bestPts)
		v.MatchesSolver = &match
	}
	return v
}

func equalInts(a, b []int64) bool {
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

func fatal(format string, args ...interface{}) {
	e := output{Status: "error", Error: fmt.Sprintf(format, args...)}
	enc, _ := json.MarshalIndent(e, "", "  ")
	fmt.Println(string(enc))
	os.Exit(1)
}

func main() {
	inputPath := flag.String("input", "", "path to input JSON (default: stdin)")
	maxNodes := flag.Int64("max-nodes", -1, "branch-and-bound node limit, 0 = unlimited (overrides JSON maxNodes)")
	timeoutMs := flag.Int64("timeout-ms", -1, "wall-clock limit in milliseconds, 0 = unlimited (overrides JSON timeoutMs)")
	verifyMax := flag.Int("verify-max", -1, "max candidate count for the exhaustive enumeration check (default 20)")
	noVerify := flag.Bool("no-verify", false, "disable the exhaustive enumeration check")
	flag.Parse()

	var data []byte
	var err error
	if *inputPath == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*inputPath)
	}
	if err != nil {
		fatal("read input: %v", err)
	}
	var raw rawInput
	if uerr := json.Unmarshal(data, &raw); uerr != nil {
		fatal("parse input: %v", uerr)
	}
	if *maxNodes >= 0 {
		raw.MaxNodes = *maxNodes
	}
	if *timeoutMs >= 0 {
		raw.TimeoutMs = *timeoutMs
	}
	if *verifyMax >= 0 {
		raw.VerifyMax = *verifyMax
	}
	if *noVerify {
		f := false
		raw.Verify = &f
	}

	out := solve(raw)
	enc, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(enc))
	if out.Status == "error" {
		os.Exit(1)
	}
}
