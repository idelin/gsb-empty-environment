package netsec

import (
	"fmt"
	"sort"
)

// SoundnessResult compares static findings with concrete executions over all
// combinations of external string values.
type SoundnessResult struct {
	Executions             int
	ConcreteDangerousNodes []int
	AnalyzedNodes          []int
	MissedNodes            []int
	AnalyzerOnlyNodes      []int
	ConcreteWrites         int
}

// CheckSoundness concretely enumerates every external-string assignment in
// StringDomain and checks the fundamental soundness property: every syntax
// node that reaches a shared-prototype write in some concrete execution must
// be present in the static report.
//
// maxRuns is a safety limit on 5^externals. If maxRuns <= 0, a default limit
// of 1,000,000 runs is used.
func CheckSoundness(p *Program, maxRuns int) (*SoundnessResult, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	report, err := Analyze(p)
	if err != nil {
		return nil, err
	}
	sites := ExternalSites(p)
	if maxRuns <= 0 {
		maxRuns = 1_000_000
	}
	runs := 1
	for range sites {
		if runs > maxRuns/len(stringDomain) {
			return nil, fmt.Errorf("%w: %d external sites exceed limit %d",
				ErrExecutionLimit, len(sites), maxRuns)
		}
		runs *= len(stringDomain)
	}

	concrete := make(map[int]int)
	writes := 0
	choices := make(map[*ExternalStringExpr]string, len(sites))

	var walk func(pos int) error
	walk = func(pos int) error {
		if pos == len(sites) {
			exec, err := Execute(p, choices)
			if err != nil {
				return err
			}
			for _, w := range exec.Dangerous {
				concrete[w.NodeID]++
				writes++
			}
			return nil
		}
		for _, value := range stringDomain {
			choices[sites[pos]] = value
			if err := walk(pos + 1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
	}

	analyzed := make(map[int]bool, len(report.Findings))
	for _, f := range report.Findings {
		analyzed[f.NodeID] = true
	}
	missed := make([]int, 0)
	for id := range concrete {
		if !analyzed[id] {
			missed = append(missed, id)
		}
	}
	only := make([]int, 0)
	for id := range analyzed {
		if _, ok := concrete[id]; !ok {
			only = append(only, id)
		}
	}
	sort.Ints(missed)
	sort.Ints(only)
	return &SoundnessResult{
		Executions:             runs,
		ConcreteDangerousNodes: sortedNodeSet(concrete),
		AnalyzedNodes:          sortedNodeSetFromBool(analyzed),
		MissedNodes:            missed,
		AnalyzerOnlyNodes:      only,
		ConcreteWrites:         writes,
	}, nil
}

func sortedNodeSet(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func sortedNodeSetFromBool(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}
