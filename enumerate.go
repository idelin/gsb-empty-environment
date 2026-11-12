package netsec

import (
	"fmt"
	"strconv"
)

// EnumerateConfig bounds the short-history enumeration used to cross-check
// challenge replacement against stale-answer races.
type EnumerateConfig struct {
	// MaxOps limits the number of operations per generated history.
	MaxOps int
	// WrongLimit is the fixed wrong-answer budget exercised by the histories.
	WrongLimit int
	// IncludeBugs adds one-result-mutation histories that must be rejected.
	IncludeBugs bool
}

// EnumerateStats summarizes an enumeration run.
type EnumerateStats struct {
	Histories    int
	Linearizable int
	Rejected     int
	BugsFound    int
}

// EnumeratedHistory pairs a generated history with the expectation derived
// from the reference specification.
type EnumeratedHistory struct {
	History      History
	ExpectLinear bool
	// SeedOrder is the sequential schedule from which the history was derived.
	SeedOrder []int
	// Template names the scenario that generated this history.
	Template string
	// Buggy reports whether one observed result was deliberately mutated.
	Buggy bool
}

// EnumerateShortHistories generates bounded concurrent histories centered on
// challenge replacement and stale-answer competition, runs Check on each, and
// returns every generated history plus aggregate statistics. The enumeration
// is exhaustive over the fixed scenario templates in this package: every
// valid template must linearize, and every injected bug must be rejected.
func EnumerateShortHistories(cfg EnumerateConfig) ([]EnumeratedHistory, EnumerateStats, error) {
	if cfg.MaxOps <= 0 {
		cfg.MaxOps = 6
	}
	if cfg.WrongLimit <= 0 {
		cfg.WrongLimit = 2
	}

	templates := buildTemplates(cfg.MaxOps)
	var generated []EnumeratedHistory
	var stats EnumerateStats

	for _, tpl := range templates {
		for _, item := range expandTemplate(tpl, cfg) {
			item.Template = tpl.name
			generated = append(generated, item)
		}
	}

	for _, item := range generated {
		stats.Histories++
		report, err := Check(item.History)
		if err != nil {
			return nil, stats, err
		}
		if report.Linearizable {
			stats.Linearizable++
			if item.Buggy {
				return nil, stats, fmt.Errorf("injected bug at history %d was accepted", stats.Histories)
			}
			if err := VerifyOrder(item.History, report.Order); err != nil {
				return nil, stats, fmt.Errorf("returned order fails verification: %w", err)
			}
		} else {
			stats.Rejected++
			if item.Buggy {
				stats.BugsFound++
			}
		}
	}

	return generated, stats, nil
}

// template is an operation skeleton without observed results; results are
// derived by replaying the skeleton through the reference spec.
type template struct {
	name       string
	operations []Operation
}

func buildTemplates(maxOps int) []template {
	var out []template

	// Template 1: replacement races a stale answer on the retired challenge.
	for n := 2; n <= maxOps && n <= 5; n++ {
		out = append(out, replaceRaceTemplate(n))
	}
	// Template 2: a stale grant races the replacement that retires it.
	for n := 2; n <= maxOps && n <= 5; n++ {
		out = append(out, staleGrantTemplate(n))
	}
	// Template 3: wrong-answer budget exercised concurrently with replacement.
	for n := 3; n <= maxOps && n <= 6; n++ {
		out = append(out, wrongBudgetTemplate(n))
	}
	// Template 4: double-grant competition on the single current challenge.
	for n := 2; n <= maxOps && n <= 4; n++ {
		out = append(out, doubleGrantTemplate(n))
	}
	return out
}

func baseIntervals(count int) (invoke, ret []int64) {
	invoke = make([]int64, count)
	ret = make([]int64, count)
	for i := range invoke {
		invoke[i] = int64(2 * i)
		ret[i] = int64(2*i + 1)
	}
	return invoke, ret
}

// overlap widens the intervals of the given indices so they are all concurrent
// while preserving the relative order of the remaining operations.
func overlap(invoke, ret []int64, indices []int) {
	if len(indices) == 0 {
		return
	}
	start := invoke[indices[0]]
	end := ret[indices[len(indices)-1]] + int64(len(indices))
	for _, i := range indices {
		invoke[i] = start
		ret[i] = end
	}
}

func replaceRaceTemplate(n int) template {
	ops := []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "1111"},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "2222"},
	}
	for i := 3; i <= n; i++ {
		wrong := "bad" + strconv.Itoa(i)
		ops = append(ops, Operation{ID: i, Kind: Answer, ChallengeID: "c1", Answer: wrong})
	}
	invoke, ret := baseIntervals(len(ops))
	race := make([]int, 0, len(ops))
	for i := 1; i < len(ops); i++ {
		race = append(race, i)
	}
	overlap(invoke, ret, race)
	for i := range ops {
		ops[i].Invoke, ops[i].Return = invoke[i], ret[i]
	}
	return template{name: "replace_race", operations: ops}
}

func staleGrantTemplate(n int) template {
	ops := []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "1111"},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "2222"},
		{ID: 3, Kind: Grant, ChallengeID: "c1"},
	}
	for i := 4; i <= n; i++ {
		ops = append(ops, Operation{ID: i, Kind: Grant, ChallengeID: "c2"})
	}
	invoke, ret := baseIntervals(len(ops))
	race := make([]int, 0, len(ops)-1)
	for i := 1; i < len(ops); i++ {
		race = append(race, i)
	}
	overlap(invoke, ret, race)
	for i := range ops {
		ops[i].Invoke, ops[i].Return = invoke[i], ret[i]
	}
	return template{name: "stale_grant", operations: ops}
}

func wrongBudgetTemplate(n int) template {
	ops := []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "1111"},
		{ID: 2, Kind: Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "2222"},
	}
	for i := 3; i <= n; i++ {
		ops = append(ops, Operation{ID: i, Kind: Answer, ChallengeID: "c1", Answer: "bad"})
	}
	invoke, ret := baseIntervals(len(ops))
	race := make([]int, 0, len(ops))
	for i := 1; i < len(ops); i++ {
		race = append(race, i)
	}
	overlap(invoke, ret, race)
	for i := range ops {
		ops[i].Invoke, ops[i].Return = invoke[i], ret[i]
	}
	return template{name: "wrong_budget", operations: ops}
}

func doubleGrantTemplate(n int) template {
	ops := []Operation{
		{ID: 1, Kind: Create, ChallengeID: "c1", Answer: "1111"},
	}
	for i := 2; i <= n; i++ {
		ops = append(ops, Operation{ID: i, Kind: Grant, ChallengeID: "c1"})
	}
	invoke, ret := baseIntervals(len(ops))
	race := make([]int, 0, len(ops)-1)
	for i := 1; i < len(ops); i++ {
		race = append(race, i)
	}
	overlap(invoke, ret, race)
	for i := range ops {
		ops[i].Invoke, ops[i].Return = invoke[i], ret[i]
	}
	return template{name: "double_grant", operations: ops}
}

// expandTemplate replays the skeleton in its seed order to derive results,
// then optionally emits one-result mutations. A mutation is kept only when it
// is genuinely non-linearizable, since concurrent operations can admit
// multiple legal orderings with different result sets.
func expandTemplate(tpl template, cfg EnumerateConfig) []EnumeratedHistory {
	base := make([]Operation, len(tpl.operations))
	copy(base, tpl.operations)
	spec := NewSpec(cfg.WrongLimit)
	seedOrder := make([]int, len(base))
	for i, op := range base {
		op.Result = spec.expected(op)
		spec.commit(op)
		base[i] = op
		seedOrder[i] = op.ID
	}

	good := History{Operations: base, WrongLimit: cfg.WrongLimit}
	out := []EnumeratedHistory{{History: good, ExpectLinear: true, SeedOrder: seedOrder}}

	if !cfg.IncludeBugs {
		return out
	}
	for i := range base {
		mutated := make([]Operation, len(base))
		copy(mutated, base)
		alt, ok := alternativeResult(mutated[i].Result)
		if !ok {
			continue
		}
		mutated[i].Result = alt
		trial := History{Operations: mutated, WrongLimit: cfg.WrongLimit}
		if ok, _ := IsLinearizable(trial); ok {
			continue
		}
		out = append(out, EnumeratedHistory{
			History:      trial,
			ExpectLinear: false,
			SeedOrder:    seedOrder,
			Buggy:        true,
		})
	}
	return out
}

// alternativeResult picks a different reachable result for bug injection.
func alternativeResult(r Result) (Result, bool) {
	all := []Result{OK, ErrNoChallenge, ErrExpired, ErrWrongAnswer, ErrConsumed, ErrLocked, ErrReplay}
	for _, candidate := range all {
		if candidate != r {
			return candidate, true
		}
	}
	return 0, false
}
