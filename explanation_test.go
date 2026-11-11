package netsec

import (
	"errors"
	"math/bits"
	"reflect"
	"sort"
	"testing"
)

// configMask interprets the Enabled flags as a bit mask over rule indices so
// test callbacks can encode arbitrary, deliberately non-monotonic behavior.
func configMask(config []RuleConfig) uint32 {
	var mask uint32
	for i, rule := range config {
		if rule.Enabled {
			mask |= 1 << uint(i)
		}
	}
	return mask
}

func rules(ids ...string) []RuleConfig {
	rules := make([]RuleConfig, len(ids))
	for i, id := range ids {
		rules[i] = RuleConfig{RuleID: id}
	}
	return rules
}

func allEnabled(rules []RuleConfig) []RuleConfig {
	target := make([]RuleConfig, len(rules))
	copy(target, rules)
	for i := range target {
		target[i].Enabled = true
	}
	return target
}

// independentMinimal brute-forces every subset without using any package
// helper, returning the global masks of inclusion-minimal reproducing sets.
func independentMinimal(t *testing.T, n int, identified func(mask uint32) bool) []uint32 {
	t.Helper()
	total := 1 << n
	hit := make([]bool, total)
	for mask := 0; mask < total; mask++ {
		hit[mask] = identified(uint32(mask))
	}
	var minimal []uint32
	for mask := 0; mask < total; mask++ {
		if !hit[mask] {
			continue
		}
		isMinimal := true
		for sub := (mask - 1) & mask; sub != mask; sub = (sub - 1) & mask {
			if hit[sub] {
				isMinimal = false
				break
			}
			if sub == 0 {
				break
			}
		}
		if isMinimal {
			minimal = append(minimal, uint32(mask))
		}
	}
	sort.Slice(minimal, func(i, j int) bool {
		if bits.OnesCount32(minimal[i]) != bits.OnesCount32(minimal[j]) {
			return bits.OnesCount32(minimal[i]) < bits.OnesCount32(minimal[j])
		}
		return minimal[i] < minimal[j]
	})
	return minimal
}

func TestExplainNonMonotonic(t *testing.T) {
	baseline := rules("r0", "r1", "r2", "r3", "r4")
	target := allEnabled(baseline)

	// Deliberately non-monotonic oracle:
	//   - {r1,r2} identifies on its own,
	//   - {r2,r4} identifies only when r1 is absent (r1 blocks r4),
	//   - every superset of {r1,r2} keeps identifying.
	// The full target (all five enabled) therefore does NOT identify, which a
	// monotonic assumption could never explain.
	oracle := func(mask uint32) bool {
		switch mask {
		case 0b00110, 0b01110, 0b10110, 0b11110:
			return true
		case 0b10100:
			return true
		default:
			return false
		}
	}
	var calls int
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		calls++
		mask := configMask(config)
		if oracle(mask) {
			return AnonymityReport{CandidateSetSize: 1}, nil
		}
		return AnonymityReport{CandidateSetSize: 7}, nil
	}

	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: evaluate,
	})
	if err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}

	if got, want := len(report.DifferingRuleIndices), 5; got != want {
		t.Fatalf("differing rules = %d, want %d", got, want)
	}
	if got, want := report.TotalSubsets, 1<<5; got != want {
		t.Fatalf("total subsets = %d, want %d", got, want)
	}
	if got, want := calls, (1<<5)+1; got != want {
		t.Fatalf("callback calls = %d, want %d", got, want)
	}
	if got, want := len(report.Invocations), calls; got != want {
		t.Fatalf("invocation summaries = %d, want %d", got, want)
	}
	if got, want := report.ReproducingSubsets, 5; got != want {
		t.Fatalf("reproducing subsets = %d, want %d", got, want)
	}
	if report.BaselineCandidateSetSize != 7 || report.TargetCandidateSetSize != 7 {
		t.Fatalf("baseline=%d target=%d, both want 7 (target not identified)",
			report.BaselineCandidateSetSize, report.TargetCandidateSetSize)
	}
	if report.MinimumCardinality != 2 {
		t.Fatalf("minimum cardinality = %d, want 2", report.MinimumCardinality)
	}

	wantMinimal := independentMinimal(t, 5, oracle)
	var gotMinimal []uint32
	for _, set := range report.MinimalSets {
		gotMinimal = append(gotMinimal, set.Mask)
	}
	if !reflect.DeepEqual(gotMinimal, wantMinimal) {
		t.Fatalf("minimal masks = %b, want %b", gotMinimal, wantMinimal)
	}

	// Every reported set must reproduce the collapse and survive deletion
	// checks using the already-recorded full-subset evidence only.
	seenMasks := map[uint32]bool{}
	for _, set := range report.MinimalSets {
		if seenMasks[set.Mask] {
			t.Fatalf("duplicate minimal set %b", set.Mask)
		}
		seenMasks[set.Mask] = true
		if set.CandidateSetSize != 1 {
			t.Fatalf("set %b candidate size = %d, want 1", set.Mask, set.CandidateSetSize)
		}
		if len(set.DeletionProof) != set.Cardinality {
			t.Fatalf("set %b has %d deletion proofs for cardinality %d",
				set.Mask, len(set.DeletionProof), set.Cardinality)
		}
		for _, evidence := range set.DeletionProof {
			if evidence.Identified {
				t.Fatalf("set %b remains identified after deleting rule %d",
					set.Mask, evidence.RuleIndex)
			}
			if evidence.CandidateSetSize != 7 {
				t.Fatalf("deletion evidence size = %d, want 7", evidence.CandidateSetSize)
			}
			rebuilt := report.Config(evidence.ReducedMask)
			if configMask(rebuilt) != evidence.ReducedMask {
				t.Fatalf("Config reconstruction mismatch for mask %b", evidence.ReducedMask)
			}
			if got := report.RuleIDs(evidence.ReducedMask); len(got) == 0 && evidence.ReducedMask != 0 {
				t.Fatalf("RuleIDs empty for non-zero mask %b", evidence.ReducedMask)
			}
		}
	}

	// Every invocation summary must agree with an independent re-evaluation,
	// proving the full subset enumeration really happened.
	for _, call := range report.Invocations {
		config := report.Config(call.Mask)
		wantIdentified := oracle(configMask(config))
		if call.Identified != wantIdentified {
			t.Fatalf("call %d mask %b: identified=%t want %t",
				call.Seq, call.Mask, call.Identified, wantIdentified)
		}
		wantSize := 7
		if wantIdentified {
			wantSize = 1
		}
		if call.CandidateSetSize != wantSize {
			t.Fatalf("call %d mask %b: size=%d want %d",
				call.Seq, call.Mask, call.CandidateSetSize, wantSize)
		}
	}
}

func TestExplainMonotonicTargetIdentified(t *testing.T) {
	baseline := rules("a", "b", "c", "d")
	target := allEnabled(baseline)

	// Monotonic oracle: each enabled rule independently removes candidates;
	// identification starts once all four rules are enabled.
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		enabled := bits.OnesCount32(configMask(config))
		size := 9 - 2*enabled
		if size < 1 {
			size = 1
		}
		return AnonymityReport{CandidateSetSize: size}, nil
	}

	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: evaluate,
	})
	if err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}
	if report.TargetCandidateSetSize != 1 {
		t.Fatalf("target candidate size = %d, want 1", report.TargetCandidateSetSize)
	}
	if report.MinimumCardinality != 4 {
		t.Fatalf("minimum cardinality = %d, want 4", report.MinimumCardinality)
	}

	oracle := func(mask uint32) bool { return bits.OnesCount32(mask) == 4 }
	wantMinimal := independentMinimal(t, 4, oracle)
	var gotMinimal []uint32
	for _, set := range report.MinimalSets {
		gotMinimal = append(gotMinimal, set.Mask)
		for _, evidence := range set.DeletionProof {
			if evidence.Identified {
				t.Fatalf("mask %b still identified after deleting rule %d",
					set.Mask, evidence.RuleIndex)
			}
		}
	}
	if !reflect.DeepEqual(gotMinimal, wantMinimal) {
		t.Fatalf("minimal masks = %b, want %b", gotMinimal, wantMinimal)
	}
	if len(gotMinimal) != 1 {
		t.Fatalf("number of minimal sets = %d, want 1", len(gotMinimal))
	}
}

func TestExplainThresholdThreeMultipleMinimalSets(t *testing.T) {
	baseline := rules("a", "b", "c", "d")
	target := allEnabled(baseline)

	// Monotonic threshold oracle: exactly three or more enabled rules identify,
	// giving the four 3-rule subsets as inclusion-minimal explanations.
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		enabled := bits.OnesCount32(configMask(config))
		if enabled >= 3 {
			return AnonymityReport{CandidateSetSize: 1}, nil
		}
		return AnonymityReport{CandidateSetSize: 6 - 2*enabled}, nil
	}

	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: evaluate,
	})
	if err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}
	if report.MinimumCardinality != 3 || len(report.MinimalSets) != 4 {
		t.Fatalf("want four 3-rule minimal sets, got cardinality=%d count=%d",
			report.MinimumCardinality, len(report.MinimalSets))
	}
	oracle := func(mask uint32) bool { return bits.OnesCount32(mask) >= 3 }
	wantMinimal := independentMinimal(t, 4, oracle)
	var gotMinimal []uint32
	for _, set := range report.MinimalSets {
		gotMinimal = append(gotMinimal, set.Mask)
		for _, evidence := range set.DeletionProof {
			if evidence.Identified || evidence.CandidateSetSize != 2 {
				t.Fatalf("deleting rule %d should yield size 2 not identified, got %+v",
					evidence.RuleIndex, evidence)
			}
		}
	}
	if !reflect.DeepEqual(gotMinimal, wantMinimal) {
		t.Fatalf("minimal masks = %b, want %b", gotMinimal, wantMinimal)
	}
}

func TestExplainEighteenRulesFullEnumeration(t *testing.T) {
	baseline := make([]RuleConfig, MaxRules)
	for i := range baseline {
		baseline[i] = RuleConfig{RuleID: "rule-" + string(rune('a'+i))}
	}
	target := allEnabled(baseline)

	var calls int
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		calls++
		// Only one exact, large pattern identifies: proves non-monotonic
		// handling at the maximum rule count (it has no identifying submask).
		if configMask(config) == (1<<MaxRules)-1 {
			return AnonymityReport{CandidateSetSize: 1}, nil
		}
		return AnonymityReport{CandidateSetSize: 12}, nil
	}

	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: evaluate,
	})
	if err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}
	if want := 1 << MaxRules; report.TotalSubsets != want {
		t.Fatalf("total subsets = %d, want %d", report.TotalSubsets, want)
	}
	if want := (1 << MaxRules) + 1; calls != want {
		t.Fatalf("callback calls = %d, want %d", calls, want)
	}
	if len(report.Invocations) != calls {
		t.Fatalf("invocation summaries = %d, want %d", len(report.Invocations), calls)
	}
	if report.ReproducingSubsets != 1 || len(report.MinimalSets) != 1 {
		t.Fatalf("reproducing=%d minimal=%d, want 1/1",
			report.ReproducingSubsets, len(report.MinimalSets))
	}
	only := report.MinimalSets[0]
	if only.Cardinality != MaxRules || only.Mask != (1<<MaxRules)-1 {
		t.Fatalf("unexpected minimal set: mask=%b cardinality=%d", only.Mask, only.Cardinality)
	}
	if len(only.DeletionProof) != MaxRules {
		t.Fatalf("deletion proofs = %d, want %d", len(only.DeletionProof), MaxRules)
	}
	for _, evidence := range only.DeletionProof {
		if evidence.Identified || evidence.CandidateSetSize != 12 {
			t.Fatalf("deletion of rule %d unexpectedly identified: %+v",
				evidence.RuleIndex, evidence)
		}
	}
}

func TestExplainZeroDifferingRules(t *testing.T) {
	noError := func(config []RuleConfig) (AnonymityReport, error) {
		return AnonymityReport{CandidateSetSize: 5}, nil
	}

	t.Run("baseline already identified", func(t *testing.T) {
		baseline := rules("x", "y")
		target := cloneRules(baseline)
		evaluate := func(config []RuleConfig) (AnonymityReport, error) {
			return AnonymityReport{CandidateSetSize: 1}, nil
		}
		report, err := Explain(ExplainRequest{
			Baseline: baseline,
			Target:   target,
			Evaluate: evaluate,
		})
		if err != nil {
			t.Fatalf("Explain returned error: %v", err)
		}
		if len(report.DifferingRuleIndices) != 0 || report.TotalSubsets != 1 {
			t.Fatalf("unexpected diff state: %+v", report)
		}
		if len(report.MinimalSets) != 1 || report.MinimalSets[0].Cardinality != 0 {
			t.Fatalf("want single empty minimal set, got %+v", report.MinimalSets)
		}
		if report.MinimumCardinality != 0 {
			t.Fatalf("minimum cardinality = %d, want 0", report.MinimumCardinality)
		}
		if len(report.MinimalSets[0].DeletionProof) != 0 {
			t.Fatal("empty minimal set must carry no deletion proof")
		}
	})

	t.Run("baseline not identified", func(t *testing.T) {
		baseline := rules("x", "y")
		report, err := Explain(ExplainRequest{
			Baseline: baseline,
			Target:   cloneRules(baseline),
			Evaluate: noError,
		})
		if err != nil {
			t.Fatalf("Explain returned error: %v", err)
		}
		if len(report.MinimalSets) != 0 {
			t.Fatalf("want no minimal set, got %+v", report.MinimalSets)
		}
		if report.MinimumCardinality != -1 || report.ReproducingSubsets != 0 {
			t.Fatalf("unexpected report stats: %+v", report)
		}
	})
}

func TestExplainErrors(t *testing.T) {
	baseline := rules("a", "b")
	target := allEnabled(baseline)
	evaluate := func(config []RuleConfig) (AnonymityReport, error) {
		return AnonymityReport{CandidateSetSize: 4}, nil
	}

	if _, err := Explain(ExplainRequest{Baseline: baseline, Target: target}); err == nil {
		t.Fatal("expected error for nil callback")
	}
	if _, err := Explain(ExplainRequest{
		Baseline: nil,
		Target:   target,
		Evaluate: evaluate,
	}); err == nil {
		t.Fatal("expected error for empty baseline")
	}
	if _, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   allEnabled(rules("a", "b", "c")),
		Evaluate: evaluate,
	}); err == nil {
		t.Fatal("expected error for mismatched rule counts")
	}

	mismatched := cloneRules(target)
	mismatched[0].RuleID = "other"
	if _, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   mismatched,
		Evaluate: evaluate,
	}); err == nil {
		t.Fatal("expected error for mismatched rule ids")
	}

	tooMany := make([]RuleConfig, MaxRules+1)
	for i := range tooMany {
		tooMany[i] = RuleConfig{RuleID: "r"}
	}
	if _, err := Explain(ExplainRequest{
		Baseline: tooMany,
		Target:   cloneRules(tooMany),
		Evaluate: evaluate,
	}); err == nil {
		t.Fatalf("expected error for more than %d rules", MaxRules)
	}

	boom := errors.New("callback boom")
	report, err := Explain(ExplainRequest{
		Baseline: baseline,
		Target:   target,
		Evaluate: func(config []RuleConfig) (AnonymityReport, error) {
			return AnonymityReport{}, boom
		},
	})
	if err != nil {
		t.Fatalf("callback errors must be recorded, not returned: %v", err)
	}
	for _, call := range report.Invocations {
		if !errors.Is(call.Err, boom) || call.CandidateSetSize != -1 || call.Identified {
			t.Fatalf("error not recorded correctly in call %+v", call)
		}
	}
	if len(report.MinimalSets) != 0 || report.ReproducingSubsets != 0 {
		t.Fatalf("failed callbacks must yield no minimal sets, got %+v", report.MinimalSets)
	}
}

func TestExplainRandomizedDifferential(t *testing.T) {
	// Fixed-seed pseudo-random oracles give reproducible, deliberately
	// non-monotonic predicates. Each run is cross-checked against a fully
	// independent brute-force implementation.
	seed := uint32(0x9e3779b9)
	nextRand := func() uint32 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return seed
	}

	for trial := 0; trial < 40; trial++ {
		n := 1 + int(nextRand()%MaxRules)
		baseline := make([]RuleConfig, n)
		for i := range baseline {
			baseline[i] = RuleConfig{RuleID: "r" + string(rune('a'+i))}
		}
		target := allEnabled(baseline)

		oracle := make([]bool, 1<<n)
		for mask := range oracle {
			oracle[mask] = nextRand()%3 == 0
		}
		oracle[0] = false // keep the baseline non-identified for this trial

		evaluate := func(config []RuleConfig) (AnonymityReport, error) {
			mask := configMask(config)
			if oracle[mask] {
				return AnonymityReport{CandidateSetSize: 1}, nil
			}
			return AnonymityReport{CandidateSetSize: 3 + int(mask%5)}, nil
		}

		report, err := Explain(ExplainRequest{
			Baseline: baseline,
			Target:   target,
			Evaluate: evaluate,
		})
		if err != nil {
			t.Fatalf("trial %d: Explain error: %v", trial, err)
		}

		hits := 0
		for _, hit := range oracle {
			if hit {
				hits++
			}
		}
		if report.ReproducingSubsets != hits {
			t.Fatalf("trial %d n=%d: reproducing=%d want %d",
				trial, n, report.ReproducingSubsets, hits)
		}

		wantMinimal := independentMinimal(t, n, func(mask uint32) bool { return oracle[mask] })
		var gotMinimal []uint32
		for _, set := range report.MinimalSets {
			gotMinimal = append(gotMinimal, set.Mask)
			if !oracle[set.Mask] {
				t.Fatalf("trial %d: reported minimal mask %b does not identify", trial, set.Mask)
			}
			for _, evidence := range set.DeletionProof {
				if evidence.Identified {
					t.Fatalf("trial %d: mask %b stays identified after deleting rule %d",
						trial, set.Mask, evidence.RuleIndex)
				}
				if oracle[evidence.ReducedMask] {
					t.Fatalf("trial %d: evidence contradicts oracle for mask %b",
						trial, evidence.ReducedMask)
				}
			}
		}
		if !reflect.DeepEqual(gotMinimal, wantMinimal) {
			t.Fatalf("trial %d n=%d: minimal=%b want=%b", trial, n, gotMinimal, wantMinimal)
		}

		if hits == 0 {
			if report.MinimumCardinality != -1 || len(report.MinimalSets) != 0 {
				t.Fatalf("trial %d: expected no explanation, got %+v", trial, report.MinimalSets)
			}
			continue
		}
		minCardinality := bits.OnesCount32(wantMinimal[0])
		if report.MinimumCardinality != minCardinality {
			t.Fatalf("trial %d: minimum cardinality=%d want %d",
				trial, report.MinimumCardinality, minCardinality)
		}
		if len(report.Invocations) != (1<<n)+1 {
			t.Fatalf("trial %d: invocations=%d want %d",
				trial, len(report.Invocations), (1<<n)+1)
		}
	}
}
