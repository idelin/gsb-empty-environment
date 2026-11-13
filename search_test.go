package netsec

import "testing"

func secretLineTable() []int {
	table := make([]int, NumSecrets)
	for secret := range table {
		table[secret] = secret * 256
	}
	return table
}

func TestVictimMultipleLoadsUpdateLRU(t *testing.T) {
	victim := mustProgram(t,
		ConstExpr(0),
		ConstExpr(256),
		ConstExpr(512),
	)
	experiment, err := Simulate(0, 4, []int{0, 256}, nil, victim)
	if err != nil {
		t.Fatal(err)
	}
	if len(experiment.VictimAddresses) != 3 || len(experiment.VictimAccesses) != 3 {
		t.Fatalf("victim trace lengths = %d/%d, want 3/3",
			len(experiment.VictimAddresses), len(experiment.VictimAccesses))
	}
	if !experiment.VictimAccesses[0].Hit || !experiment.VictimAccesses[1].Hit {
		t.Fatalf("first two victim loads should hit primed lines")
	}
	third := experiment.VictimAccesses[2]
	if third.Hit || !third.Evicted || third.EvictedTag != TagOf(0) {
		t.Fatalf("third victim load = %+v, want miss evicting tag %d", third, TagOf(0))
	}
	if got := third.After.LRU(); !equalInts(got, []int{TagOf(256), TagOf(512)}) {
		t.Fatalf("victim LRU after third load = %v", got)
	}
	if len(experiment.Accesses) != 5 {
		t.Fatalf("complete access count = %d, want 5", len(experiment.Accesses))
	}
	for i, access := range experiment.Accesses {
		if access.Before.Set != access.Set || access.After.Set != access.Set {
			t.Fatalf("access %d has inconsistent set snapshots", i)
		}
	}
}

func TestSearchDistinguishesSecrets(t *testing.T) {
	table := secretLineTable()
	victim := mustProgram(t, AddExpr(PublicExpr(), LookupExpr(SecretExpr(), table)))

	result, err := Search(SearchConfig{
		Publics: []int{0, 1},
		Prime:   []int{0, 256},
		Probe:   []int{0, 256},
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || len(result.MissingSecrets) != 0 {
		t.Fatalf("full secret sweep reported incomplete: missing %v", result.MissingSecrets)
	}
	if !result.Distinguishable || result.Verdict != VerdictDistinguishable {
		t.Fatalf("search verdict = %v, want distinguishable", result.Verdict)
	}
	if result.Indistinguishable {
		t.Fatalf("result cannot be both distinguishable and indistinguishable")
	}
	if result.FirstWitness == nil {
		t.Fatal("expected a first witness")
	}
	if result.FirstWitness.SecretA == result.FirstWitness.SecretB ||
		result.FirstWitness.ObservationA == result.FirstWitness.ObservationB {
		t.Fatalf("invalid witness: %+v", *result.FirstWitness)
	}

	firstOutcome := result.Publics[0]
	if len(firstOutcome.Experiments) != NumSecrets {
		t.Fatalf("experiments = %d, want %d", len(firstOutcome.Experiments), NumSecrets)
	}
	experiment := firstOutcome.Experiments[2]
	if len(experiment.Accesses) != 5 {
		t.Fatalf("access count = %d, want 2 prime + 1 victim + 2 probe", len(experiment.Accesses))
	}
	if experiment.ProbeObservation != ObservationString(experiment.ProbeBits) {
		t.Fatalf("observation string and bits disagree")
	}
	if len(experiment.FinalCache) != NumSets {
		t.Fatalf("final cache snapshot has %d sets", len(experiment.FinalCache))
	}
}

func TestIndistinguishableRequiresCompleteSweep(t *testing.T) {
	victim := mustProgram(t, ConstExpr(0))
	prime := []int{0}
	probe := []int{0}

	full, err := Search(SearchConfig{
		Publics: []int{3},
		Prime:   prime,
		Probe:   probe,
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !full.Complete || !full.Indistinguishable || full.Verdict != VerdictIndistinguishable {
		t.Fatalf("complete constant trace = complete %v indistinguishable %v verdict %v",
			full.Complete, full.Indistinguishable, full.Verdict)
	}

	partial, err := Search(SearchConfig{
		Publics: []int{3},
		Secrets: []int{0, 1},
		Prime:   prime,
		Probe:   probe,
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if partial.Complete || partial.Indistinguishable {
		t.Fatalf("partial sweep must not claim indistinguishability")
	}
	if partial.Verdict != VerdictInconclusive {
		t.Fatalf("partial sweep verdict = %v, want inconclusive", partial.Verdict)
	}
	if len(partial.MissingSecrets) != NumSecrets-2 {
		t.Fatalf("missing secrets = %v", partial.MissingSecrets)
	}

	noPublics, err := Search(SearchConfig{
		Prime:  prime,
		Probe:  probe,
		Victim: victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if noPublics.Complete || noPublics.Indistinguishable || noPublics.Verdict != VerdictInconclusive {
		t.Fatalf("empty public set must be inconclusive, got %+v", noPublics)
	}
}

func TestDistinguishableWitnessIsValidForPartialSweep(t *testing.T) {
	victim := mustProgram(t, LookupExpr(SecretExpr(), secretLineTable()))
	result, err := Search(SearchConfig{
		Publics: []int{0},
		Secrets: []int{0, 2},
		Prime:   []int{0, 256},
		Probe:   []int{0, 256},
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.Indistinguishable {
		t.Fatalf("partial sweep made a completeness claim")
	}
	if !result.Distinguishable || result.Verdict != VerdictDistinguishable {
		t.Fatalf("witness in partial sweep was lost: %+v", result)
	}
}

func TestSearchUsesOnlyProbeObservations(t *testing.T) {
	table := make([]int, NumSecrets)
	for secret := range table {
		table[secret] = secret * 16
	}
	victim := mustProgram(t, LookupExpr(SecretExpr(), table))
	result, err := Search(SearchConfig{
		Publics: []int{0},
		Secrets: []int{0, 1},
		Prime:   []int{0, 256},
		Probe:   []int{0, 256},
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Distinguishable || len(result.Witnesses) != 0 {
		t.Fatalf("victim hit differences leaked into the distinguisher: %+v", result.Witnesses)
	}
	for _, experiment := range result.Publics[0].Experiments {
		if experiment.ProbeObservation != "HH" {
			t.Fatalf("secret %d probe observation = %q, want HH", experiment.Secret, experiment.ProbeObservation)
		}
	}
	if result.Publics[0].Experiments[0].VictimAccesses[0].Hit ==
		result.Publics[0].Experiments[1].VictimAccesses[0].Hit {
		t.Fatalf("test needs differing victim hit outcomes")
	}
	partition := result.Publics[0].Partition
	if len(partition.Classes) != 1 || !equalInts(partition.Candidates("HH"), []int{0, 1}) {
		t.Fatalf("equal probe observations were not kept in one class: %+v", partition.Classes)
	}
}

func TestPartitionKeepsEqualObservationsTogether(t *testing.T) {
	table := []int{
		0, 0, 512, 512,
		1024, 1024, 1536, 1536,
		2048, 2048, 2304, 2304,
		2560, 2560, 2816, 2816,
	}
	victim := mustProgram(t, AddExpr(PublicExpr(), LookupExpr(SecretExpr(), table)))
	result, err := Search(SearchConfig{
		Publics: []int{0, 1},
		Prime:   []int{0, 256},
		Probe:   []int{0, 256},
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}

	partition, err := PartitionForPublic(result, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !partition.Complete || len(partition.Classes) != 2 {
		t.Fatalf("partition complete=%v classes=%d, want 2 classes",
			partition.Complete, len(partition.Classes))
	}
	if !equalInts(partition.Candidates("HH"), []int{0, 1}) {
		t.Fatalf("HH candidates = %v, want [0 1]", partition.Candidates("HH"))
	}
	if len(partition.Candidates("MM")) != NumSecrets-2 {
		t.Fatalf("MM candidates = %v", partition.Candidates("MM"))
	}
	if _, ok := partition.Recover("HH"); ok {
		t.Fatalf("must not recover a unique secret from an ambiguous class")
	}
	recovery, err := result.RecoverObservation(0, "HH")
	if err != nil {
		t.Fatal(err)
	}
	if recovery.Unique || !equalInts(recovery.Candidates, []int{0, 1}) {
		t.Fatalf("RecoverObservation = %+v", recovery)
	}

	bySecret, err := PartitionSecrets(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bySecret.Complete || len(bySecret.Classes) != 2 {
		t.Fatalf("secret partition = complete %v classes %d", bySecret.Complete, len(bySecret.Classes))
	}
	if !equalInts(bySecret.Classes[0].Secrets, []int{0, 1}) {
		t.Fatalf("first secret class = %v", bySecret.Classes[0].Secrets)
	}
	if _, ok := bySecret.Recover(); ok {
		t.Fatalf("ambiguous secret partition must not claim unique recovery")
	}
}

func TestPartitionRecoverRequiresSingletonAndCompleteness(t *testing.T) {
	ambiguous := ObservationPartition{
		Public:   0,
		Complete: true,
		Classes: []ObservationClass{
			{Observation: "HH", Secrets: []int{3, 4}},
		},
	}
	if _, ok := ambiguous.Recover("HH"); ok {
		t.Fatalf("two-secret class must not recover")
	}

	singletonIncomplete := ObservationPartition{
		Public:   0,
		Complete: false,
		Classes: []ObservationClass{
			{Observation: "HH", Secrets: []int{3}},
		},
	}
	if _, ok := singletonIncomplete.Recover("HH"); ok {
		t.Fatalf("incomplete partition must not claim recovery")
	}

	singletonComplete := ObservationPartition{
		Public:   0,
		Complete: true,
		Classes: []ObservationClass{
			{Observation: "HH", Secrets: []int{3}},
		},
	}
	if secret, ok := singletonComplete.Recover("HH"); !ok || secret != 3 {
		t.Fatalf("complete singleton recovery = (%d,%v), want (3,true)", secret, ok)
	}
}

func TestSearchTracesMatchNaiveCacheStepByStep(t *testing.T) {
	table := secretLineTable()
	victim := mustProgram(t,
		AddExpr(PublicExpr(), LookupExpr(SecretExpr(), table)),
		XorExpr(PublicExpr(), ConstExpr(0x123)),
	)
	prime := []int{0, 256, 512, 0, 0x10, 0x110}
	probe := []int{0, 256, 512, 0, 256}
	result, err := Search(SearchConfig{
		Publics: []int{0, 17, 4095},
		Prime:   prime,
		Probe:   probe,
		Victim:  victim,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, outcome := range result.Publics {
		for _, experiment := range outcome.Experiments {
			naive := &naiveCache{}
			addresses := make([]int, 0, len(prime)+len(experiment.VictimAddresses)+len(probe))
			addresses = append(addresses, prime...)
			addresses = append(addresses, experiment.VictimAddresses...)
			addresses = append(addresses, probe...)
			if len(addresses) != len(experiment.Accesses) {
				t.Fatalf("public %d secret %d access count = %d, want %d",
					outcome.Public, experiment.Secret, len(experiment.Accesses), len(addresses))
			}
			for i, addr := range addresses {
				optimizedResult := experiment.Accesses[i]
				before := naive.snapshotLRU()
				want := naive.access(addr)
				if optimizedResult.Address != addr ||
					optimizedResult.Set != want.set ||
					optimizedResult.Tag != want.tag ||
					optimizedResult.Hit != want.hit ||
					optimizedResult.Evicted != want.evicted ||
					optimizedResult.EvictedTag != want.evictedTag {
					t.Fatalf("public %d secret %d access %d mismatch: got %+v want %+v",
						outcome.Public, experiment.Secret, i, optimizedResult, want)
				}
				if !equalInts(optimizedResult.Before.LRU(), before[want.set]) ||
					!equalInts(optimizedResult.After.LRU(), want.afterLRU) {
					t.Fatalf("public %d secret %d access %d LRU mismatch",
						outcome.Public, experiment.Secret, i)
				}
			}
		}
	}
}

func TestExpressionEvaluationAndValidation(t *testing.T) {
	sum, err := AddExpr(ConstExpr(4095), ConstExpr(2)).Eval(0, 0)
	if err != nil || sum != 1 {
		t.Fatalf("modular add = (%d,%v), want (1,nil)", sum, err)
	}
	xor, err := XorExpr(ConstExpr(4095), ConstExpr(1)).Eval(0, 0)
	if err != nil || xor != 4094 {
		t.Fatalf("xor = (%d,%v), want (4094,nil)", xor, err)
	}
	lookup, err := LookupExpr(SecretExpr(), []int{10, 20, 30}).Eval(0, 2)
	if err != nil || lookup != 30 {
		t.Fatalf("lookup = (%d,%v), want (30,nil)", lookup, err)
	}
	if _, err := LookupExpr(SecretExpr(), []int{10, 20, 30}).Eval(0, 5); err == nil {
		t.Fatalf("out-of-range lookup index must fail")
	}
	if err := LookupExpr(ConstExpr(0), []int{4096}).Validate(); err == nil {
		t.Fatalf("out-of-range table value must fail validation")
	}
	if err := ConstExpr(-1).Validate(); err == nil {
		t.Fatalf("negative constant must fail validation")
	}
	if _, err := ConstExpr(0).Eval(4096, 0); err == nil {
		t.Fatalf("out-of-range public value must fail")
	}
	if _, err := ConstExpr(0).Eval(0, -1); err == nil {
		t.Fatalf("negative secret must fail")
	}

	loads := make([]*Expr, MaxVictimLoads+1)
	for i := range loads {
		loads[i] = ConstExpr(0)
	}
	if _, err := NewProgram(loads...); err == nil {
		t.Fatalf("victim with %d loads must fail", len(loads))
	}

	maximum := loads[:MaxVictimLoads]
	if _, err := NewProgram(maximum...); err != nil {
		t.Fatalf("victim with %d loads should be accepted: %v", len(maximum), err)
	}
}
