package netsec

import (
	"testing"
)

func globalConfig() ExperimentConfig {
	return ExperimentConfig{
		Policy:            GlobalDedupPolicy{},
		AttackerDomain:    "attacker",
		VictimDomain:      "victim",
		VictimShareable:   true,
		AttackerShareable: true,
	}
}

func TestGlobalPolicyReproducesCrossDomainProbe(t *testing.T) {
	report, err := RunExperiment([]string{"alpha", "beta"}, globalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Leaked() || report.Verdict != Leak {
		t.Fatalf("verdict = %v, want definite leak; report=%+v", report.Verdict, report)
	}
	if !report.CrossDomain {
		t.Fatal("report should be marked cross-domain")
	}
	if len(report.ShortestSequence) != 2 {
		t.Fatalf("shortest sequence length = %d, want 2", len(report.ShortestSequence))
	}
	if report.ShortestSequence[0].Kind != OpPrime || report.ShortestSequence[1].Kind != OpWrite {
		t.Fatalf("shortest sequence = %v, want prime/write", report.ShortestSequence)
	}
	if len(report.Groups) < 2 {
		t.Fatalf("expected at least two trace groups, got %d", len(report.Groups))
	}
	first, second, ok := report.DistinguishingPair()
	if !ok || first == second {
		t.Fatalf("bad distinguishing pair %q/%q ok=%v", first, second, ok)
	}
}

func TestHardenedPolicyCrossDomainTracesAreIndependent(t *testing.T) {
	secrets := []Secret{
		{ID: "s1", Content: []byte("alpha")},
		{ID: "s2", Content: []byte("beta")},
	}
	cfg := ExperimentConfig{
		Policy:            HardenedPolicy{},
		AttackerDomain:    "attacker",
		VictimDomain:      "victim",
		VictimShareable:   true,
		AttackerShareable: true,
	}
	report, err := RunExperiment(secrets, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != NoLeak || report.Certainty != CertaintyNone {
		t.Fatalf("verdict = %v/%v, want no leak", report.Verdict, report.Certainty)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(report.Groups))
	}
	for _, group := range report.Groups {
		if len(group.Secrets) != len(secrets) {
			t.Fatalf("group did not contain every secret: %v", group.Secrets)
		}
	}
	if report.ShortestSequence != nil {
		t.Fatalf("unexpected distinguishing sequence: %v", report.ShortestSequence)
	}
}

func TestHardenedPolicyStillAllowsSameDomainLeak(t *testing.T) {
	cfg := ExperimentConfig{
		Policy:            HardenedPolicy{},
		AttackerDomain:    "same",
		VictimDomain:      "same",
		VictimShareable:   true,
		AttackerShareable: true,
	}
	report, err := RunExperiment([]string{"alpha", "beta"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Leaked() {
		t.Fatalf("same-domain dedup should leak, got %+v", report)
	}
}

func TestSensitiveVictimBlocksSameDomainLeak(t *testing.T) {
	cfg := ExperimentConfig{
		Policy:            HardenedPolicy{},
		AttackerDomain:    "same",
		VictimDomain:      "same",
		VictimTags:        []string{"secret"},
		VictimShareable:   true,
		AttackerShareable: true,
	}
	report, err := RunExperiment([]string{"alpha", "beta"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != NoLeak {
		t.Fatalf("sensitive victim leaked: %+v", report)
	}
}

func TestNoisyCostsProducePossibleLeakOnly(t *testing.T) {
	cfg := globalConfig()
	cfg.Costs = CostModel{
		COW:          Interval(8, 12),
		PrivateWrite: Interval(10, 14),
		Access:       FixedCost(0),
	}
	report, err := RunExperiment([]string{"alpha", "beta"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != PossibleLeak || report.Certainty != CertaintyPossible {
		t.Fatalf("verdict = %v/%v, want possible leak", report.Verdict, report.Certainty)
	}
	if report.Leaked() {
		t.Fatal("overlapping noise intervals must not be reported as a definite leak")
	}
	if len(report.ShortestSequence) == 0 {
		t.Fatal("expected a possibly distinguishing sequence")
	}
}

func TestDisjointNoisyCostsProduceDefiniteLeak(t *testing.T) {
	cfg := globalConfig()
	cfg.Costs = CostModel{
		COW:          Interval(20, 30),
		PrivateWrite: Interval(1, 5),
		Access:       FixedCost(0),
	}
	report, err := RunExperiment([]string{"alpha", "beta"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != Leak || report.Certainty != CertaintyDefinite {
		t.Fatalf("verdict = %v/%v, want definite leak", report.Verdict, report.Certainty)
	}
}

func TestDigestCollisionAliasIsNotTimingDistinguishable(t *testing.T) {
	const alias Digest = "same-digest"
	secrets := []Secret{
		{ID: "alice-secret", Content: []byte("alice"), Digest: alias},
		{ID: "bob-secret", Content: []byte("bob"), Digest: alias},
	}
	report, err := RunExperiment(secrets, globalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != NoLeak {
		t.Fatalf("colliding digests were distinguished: %+v", report)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("collision aliases formed %d groups, want 1", len(report.Groups))
	}
}

func TestRunnerDoesNotReadVictimPage(t *testing.T) {
	// The runner only keeps the victim reference for setup; this test asserts
	// the observable trace contains accesses to attacker-allocated pages only.
	cfg := ExperimentConfig{
		Policy:            GlobalDedupPolicy{},
		AttackerDomain:    "a",
		VictimDomain:      "v",
		VictimShareable:   true,
		AttackerShareable: true,
	}
	runner := NewRunner(cfg)
	seq := Sequence{PrimeString("alpha"), WriteString("alpha!"), Read(), Release()}
	trace, err := runner.Run(Secret{ID: "alpha", Content: []byte("alpha")}, seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace) != 3 {
		t.Fatalf("trace length = %d, want 3", len(trace))
	}
	// The victim is page 0 in a fresh host; every observation must reference
	// the attacker page created by the first operation.
	for _, obs := range trace {
		if obs.PageID == 0 {
			t.Fatalf("runner observed victim page: %+v", obs)
		}
	}
}

func TestCustomProbeSequence(t *testing.T) {
	cfg := globalConfig()
	cfg.Probes = []Sequence{{PrimeString("alpha"), WriteString("x"), WriteString("y")}}
	report, err := RunExperiment([]string{"alpha", "beta"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProbeCount != 1 {
		t.Fatalf("probe count = %d, want 1", report.ProbeCount)
	}
	if !report.Leaked() {
		t.Fatalf("custom probe did not find leak: %+v", report)
	}
	if len(report.ShortestSequence) != 3 {
		t.Fatalf("shortest sequence length = %d, want 3", len(report.ShortestSequence))
	}
}

func TestShortestSequenceHelper(t *testing.T) {
	seq, ok, err := ShortestStringDistinguishingSequence([]string{"alpha", "beta"}, globalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(seq) == 0 {
		t.Fatalf("no sequence returned: ok=%v seq=%v", ok, seq)
	}
}

func TestRunExperimentAcceptsDirectPolicy(t *testing.T) {
	report, err := RunExperiment([]string{"alpha", "beta"}, GlobalDedupPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Leaked() {
		t.Fatalf("direct policy argument did not reproduce probe: %+v", report)
	}
}

func TestRunExperimentDefaultsAreConservative(t *testing.T) {
	report, err := RunExperiment([]string{"alpha", "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != NoLeak {
		t.Fatalf("default policy leaked: %+v", report)
	}
	if !report.CrossDomainIndependent() {
		t.Fatal("conservative cross-domain traces were not independent")
	}
}
