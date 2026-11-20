package projection

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEarlyLeakBeforeSignedRelease(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"low":  "s-low",
			"high": "s-high",
		},
		Transitions: []Transition{
			{From: "s-low", To: "joined", Label: "A", Probability: "1"},
			{From: "s-high", To: "joined", Label: "B", Probability: "1"},
			{From: "joined", To: "joined", Label: "R", Probability: "1", Release: &Release{
				Category: "tier",
				Signed:   true,
			}},
		},
	}
	config := Config{
		Labels:        []Label{"A", "B", "R"},
		AllowedMerges: []MergePair{Pair("A", "B")},
	}

	unsafe := Projection{Mapping: map[Label]Label{"A": "A", "B": "B", "R": "R"}}
	safe, failure, err := IsSafe(protocol, unsafe)
	if err != nil {
		t.Fatal(err)
	}
	if safe || failure == nil {
		t.Fatal("singleton projection must reject the pre-release A/B leak")
	}
	if failure.Reason != "ordinary_marginal" || failure.Label != "A" {
		t.Fatalf("unexpected witness: %+v", failure)
	}

	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 2 {
		t.Fatalf("category count = %d, want 2", result.CategoryCount)
	}
	if result.Projection.Public("A") != result.Projection.Public("B") {
		t.Fatal("the pre-release labels must merge")
	}
	if result.Projection.Public("R") == result.Projection.Public("A") {
		t.Fatal("the compiler needlessly merged the release label")
	}
	if result.Certificate.Optimality.AboveBest != 1 {
		t.Fatalf("refuted above-best count = %d, want 1", result.Certificate.Optimality.AboveBest)
	}
	if err := VerifyCertificate(protocol, config, result.Certificate); err != nil {
		t.Fatal(err)
	}
	if err := VerifySafetyCertificate(protocol, result.Projection, result.Certificate.Safety); err != nil {
		t.Fatalf("standalone safety proof must include configured labels: %v", err)
	}
}

func TestTwoRoundCorrelationNotSingleRoundMarginal(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"anti-correlated": "s1",
			"correlated":      "s2",
		},
		Transitions: []Transition{
			{From: "s1", To: "s1-a", Label: "A", Probability: "1/3"},
			{From: "s1", To: "s1-b", Label: "B", Probability: "2/3"},
			{From: "s2", To: "s2-a", Label: "A", Probability: "1/3"},
			{From: "s2", To: "s2-b", Label: "B", Probability: "2/3"},

			{From: "s1-a", To: "end", Label: "B", Probability: "1"},
			{From: "s1-b", To: "end", Label: "A", Probability: "1"},
			{From: "s2-a", To: "end", Label: "A", Probability: "1"},
			{From: "s2-b", To: "end", Label: "B", Probability: "1"},
		},
	}
	config := Config{
		Labels:        []Label{"A", "B"},
		AllowedMerges: []MergePair{Pair("A", "B")},
	}

	singleton := Projection{Mapping: map[Label]Label{"A": "A", "B": "B"}}
	safe, failure, err := IsSafe(protocol, singleton)
	if err != nil {
		t.Fatal(err)
	}
	if safe || failure == nil {
		t.Fatal("equal one-round marginals must not be accepted when two-round correlations differ")
	}
	if len(failure.Trace) == 0 {
		t.Fatalf("expected a multi-round witness, got %+v", failure)
	}

	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 1 {
		t.Fatalf("category count = %d, want 1", result.CategoryCount)
	}
	if err := VerifyCertificate(protocol, config, result.Certificate); err != nil {
		t.Fatal(err)
	}
}

func TestTerminationIsObservable(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"stops":     "s-stop",
			"continues": "s-continue",
		},
		Transitions: []Transition{
			{From: "s-continue", To: "s-stop", Label: "A", Probability: "1"},
		},
	}
	config := Config{Labels: []Label{"A"}}
	result, err := Synthesize(protocol, config)
	if !errors.Is(err, ErrNoSafeProjection) {
		t.Fatalf("Synthesize error = %v, want ErrNoSafeProjection", err)
	}
	if result.CategoryCount != 0 {
		t.Fatal("an unsafe result must not carry a projection")
	}

	_, failure, checkErr := CheckSafety(protocol, Projection{Mapping: map[Label]Label{"A": "A"}})
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	if failure == nil || failure.Reason != "termination" {
		t.Fatalf("unexpected termination witness: %+v", failure)
	}
}

func TestMergeHintsAreTransitivelyClosed(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"left":  "s-left",
			"right": "s-right",
		},
		Transitions: []Transition{
			{From: "s-left", To: "end", Label: "A", Probability: "1"},
			{From: "s-right", To: "end", Label: "C", Probability: "1"},
		},
	}
	config := Config{
		Labels:        []Label{"A", "B", "C"},
		AllowedMerges: []MergePair{Pair("A", "B"), Pair("B", "C")},
	}
	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 2 {
		t.Fatalf("category count = %d, want 2 after closing A-B-B-C", result.CategoryCount)
	}
	if result.Projection.Public("A") != result.Projection.Public("C") {
		t.Fatal("transitive closure must permit the unlisted A/C merge")
	}
	if err := VerifyCertificate(protocol, config, result.Certificate); err != nil {
		t.Fatal(err)
	}
}

func TestCombinedMergesCannotCrossBusinessForbiddenPair(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"a": "s-a",
			"b": "s-b",
			"c": "s-c",
		},
		Transitions: []Transition{
			{From: "s-a", To: "end", Label: "A", Probability: "1"},
			{From: "s-b", To: "end", Label: "B", Probability: "1"},
			{From: "s-c", To: "end", Label: "C", Probability: "1"},
		},
	}
	config := Config{
		Labels:          []Label{"A", "B", "C"},
		AllowedMerges:   []MergePair{Pair("A", "B"), Pair("B", "C")},
		ForbiddenMerges: []MergePair{Pair("A", "C")},
	}
	if _, err := Synthesize(protocol, config); !errors.Is(err, ErrNoSafeProjection) {
		t.Fatalf("Synthesize error = %v, want ErrNoSafeProjection", err)
	}
}

func TestBestProjectionAvoidsOptionalIllegalCombination(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"a": "s-a",
			"b": "s-b",
		},
		Transitions: []Transition{
			{From: "s-a", To: "end", Label: "A", Probability: "1"},
			{From: "s-b", To: "end", Label: "B", Probability: "1"},
		},
	}
	config := Config{
		Labels:          []Label{"A", "B", "C"},
		AllowedMerges:   []MergePair{Pair("A", "B"), Pair("B", "C")},
		ForbiddenMerges: []MergePair{Pair("A", "C")},
	}
	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 2 {
		t.Fatalf("category count = %d, want 2", result.CategoryCount)
	}
	if result.Projection.Public("A") != result.Projection.Public("B") {
		t.Fatal("A and B must merge to hide the observed distinction")
	}
	if result.Projection.Public("A") == result.Projection.Public("C") {
		t.Fatal("the forbidden A/C pair was merged as part of a combined block")
	}
	if err := VerifyCertificate(protocol, config, result.Certificate); err != nil {
		t.Fatal(err)
	}
	if err := VerifySafetyCertificate(protocol, result.Projection, result.Certificate.Safety); err != nil {
		t.Fatalf("standalone safety proof must include unused configured labels: %v", err)
	}
}

func TestUnsignedReleaseDoesNotPublishCategory(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{
			"one": "s1",
			"two": "s2",
		},
		Transitions: []Transition{
			{From: "s1", To: "s1", Label: "A", Probability: "1", Release: &Release{Category: "x"}},
			{From: "s2", To: "s2", Label: "B", Probability: "1", Release: &Release{Category: "y"}},
		},
	}
	config := Config{
		Labels:        []Label{"A", "B"},
		AllowedMerges: []MergePair{Pair("A", "B")},
	}
	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 1 {
		t.Fatalf("unsigned categories must not make A/B distinguishable; count = %d", result.CategoryCount)
	}
}

func TestHostSignatureVerifierGatesRelease(t *testing.T) {
	base := Protocol{
		Initial: map[SecretID]StateID{"one": "s1", "two": "s2"},
		Transitions: []Transition{
			{From: "s1", To: "s1", Label: "A", Probability: "1", Release: &Release{
				Category: "x", Signed: true, Signature: "bad",
			}},
			{From: "s2", To: "s2", Label: "B", Probability: "1", Release: &Release{
				Category: "y", Signed: true, Signature: "bad",
			}},
		},
	}
	config := Config{Labels: []Label{"A", "B"}, AllowedMerges: []MergePair{Pair("A", "B")}}

	rejected := base
	rejected.VerifyRelease = func(release Release) bool { return release.Signature == "host" }
	result, err := Synthesize(rejected, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 1 {
		t.Fatalf("rejected releases must remain ordinary; count = %d", result.CategoryCount)
	}

	accepted := base
	accepted.VerifyRelease = func(release Release) bool { return release.Signature == "bad" }
	result, err = Synthesize(accepted, config)
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 2 {
		t.Fatalf("accepted categories may diverge; count = %d", result.CategoryCount)
	}
}

func TestCertificateRoundTripAndTamperDetection(t *testing.T) {
	protocol := Protocol{
		Initial: map[SecretID]StateID{"a": "s-a", "b": "s-b"},
		Transitions: []Transition{
			{From: "s-a", To: "end", Label: "A", Probability: "1"},
			{From: "s-b", To: "end", Label: "B", Probability: "1"},
		},
	}
	config := Config{Labels: []Label{"A", "B"}, AllowedMerges: []MergePair{Pair("A", "B")}}
	result, err := Synthesize(protocol, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySafetyCertificate(protocol, result.Projection, result.Certificate.Safety); err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(result.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Certificate
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(protocol, config, decoded); err != nil {
		t.Fatalf("decoded certificate must verify: %v", err)
	}

	tampered := decoded
	tampered.Projection = tampered.Projection.Clone()
	tampered.Projection.Mapping["A"] = "A"
	tampered.Projection.Mapping["B"] = "B"
	if err := VerifyCertificate(protocol, config, tampered); err == nil {
		t.Fatal("tampered projection must be rejected")
	}

	brokenProof := decoded
	brokenProof.Safety.Relation = brokenProof.Safety.Relation[:len(brokenProof.Safety.Relation)-1]
	if err := VerifySafetyCertificate(protocol, decoded.Projection, brokenProof.Safety); err == nil {
		t.Fatal("incomplete bisimulation relation must be rejected")
	}
}

func TestDecimalProbabilitiesAreExact(t *testing.T) {
	parsed, err := ParseProbability("0.125")
	if err != nil {
		t.Fatal(err)
	}
	if RatString(parsed) != "1/8" {
		t.Fatalf("0.125 parsed as %s, want 1/8", RatString(parsed))
	}
}

func TestVacuousAndSingleSecretProtocols(t *testing.T) {
	empty := Protocol{Initial: map[SecretID]StateID{}}
	result, err := Synthesize(empty, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 0 {
		t.Fatalf("empty protocol count = %d, want 0", result.CategoryCount)
	}
	if err := VerifyCertificate(empty, Config{}, result.Certificate); err != nil {
		t.Fatal(err)
	}

	single := Protocol{
		Initial: map[SecretID]StateID{"only": "s"},
		Transitions: []Transition{
			{From: "s", To: "s", Label: "A", Probability: "1/2"},
			{From: "s", To: "s", Label: "B", Probability: "1/2"},
		},
	}
	result, err = Synthesize(single, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if result.CategoryCount != 2 {
		t.Fatalf("single-secret protocol count = %d, want 2", result.CategoryCount)
	}
	if err := VerifyCertificate(single, Config{}, result.Certificate); err != nil {
		t.Fatal(err)
	}
}
