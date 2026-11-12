package netsec

import (
	"errors"
	"testing"
	"time"
)

func testKey(version string) Key {
	return Key{ContentDigest: "sha256:abc", RulesVersion: version}
}

func TestMergeRejectsDifferentKeys(t *testing.T) {
	t1 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	a := &Conclusion{
		Key:     Key{ContentDigest: "sha256:abc", RulesVersion: "v1"},
		Results: []CheckResult{NewNegativeResult("quick", t1)},
	}
	b := &Conclusion{
		Key:     Key{ContentDigest: "sha256:abc", RulesVersion: "v2"},
		Results: []CheckResult{NewNegativeResult("quick", t1)},
	}

	if _, err := Merge(a, b); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("expected ErrKeyMismatch for rule version upgrade, got %v", err)
	}

	b.Key = Key{ContentDigest: "sha256:xyz", RulesVersion: "v1"}
	if _, err := Merge(a, b); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("expected ErrKeyMismatch for different digest, got %v", err)
	}
}

func TestMergePositiveNotOverwrittenByNegative(t *testing.T) {
	t1 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	positive := &Conclusion{
		Key: testKey("v1"),
		Results: []CheckResult{
			NewPositiveResult("signature", []string{"EICAR hit", "packer:upx"}, t1),
		},
	}
	// The later, negative result for the same check must not erase the finding.
	negative := &Conclusion{
		Key: testKey("v1"),
		Results: []CheckResult{
			NewNegativeResult("signature", t2),
			NewNegativeResult("heuristic", t2),
		},
	}

	merged, err := Merge(positive, negative)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	result, ok := findResult(merged, "signature")
	if !ok {
		t.Fatal("signature check missing from merged conclusion")
	}
	if result.Verdict != VerdictPositive {
		t.Fatalf("positive verdict was overwritten: got %s", result.Verdict)
	}
	if len(result.Evidence) != 2 {
		t.Fatalf("positive evidence lost: %v", result.Evidence)
	}
	if merged.Status() != StatusThreat {
		t.Fatalf("merged status = %s, want threat", merged.Status())
	}

	// Reverse order yields the same outcome: merge order must not matter.
	reversed, err := Merge(negative, positive)
	if err != nil {
		t.Fatalf("reverse merge failed: %v", err)
	}
	result, _ = findResult(reversed, "signature")
	if result.Verdict != VerdictPositive || len(result.Evidence) != 2 {
		t.Fatalf("reverse merge lost positive finding: %+v", result)
	}
	if reversed.Status() != StatusThreat {
		t.Fatalf("reversed status = %s, want threat", reversed.Status())
	}
}

func TestMergeIdempotentAndPendingSettlement(t *testing.T) {
	t1 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	quick := &Conclusion{
		Key:     testKey("v1"),
		Results: []CheckResult{NewNegativeResult("quick", t1)},
		Pending: []CheckID{"full", "sandbox"},
	}

	once, err := Merge(quick, quick)
	if err != nil {
		t.Fatalf("self merge failed: %v", err)
	}
	if len(once.Results) != 1 || len(once.Pending) != 2 {
		t.Fatalf("self merge changed content: %+v", once)
	}

	full := &Conclusion{
		Key: testKey("v1"),
		Results: []CheckResult{
			NewNegativeResult("full", t1.Add(time.Minute)),
			NewPositiveResult("sandbox", []string{"runtime: ransomware behavior"}, t1.Add(2*time.Minute)),
		},
	}

	merged, err := Merge(quick, full)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	// Executed checks leave the pending set.
	if len(merged.Pending) != 0 {
		t.Fatalf("expected no pending checks, got %v", merged.Pending)
	}
	if !merged.Covers([]CheckID{"quick", "full", "sandbox"}) {
		t.Fatal("merged conclusion does not cover the full check set")
	}
	if merged.Status() != StatusThreat {
		t.Fatalf("status = %s, want threat", merged.Status())
	}

	// Re-applying either input must not change the merged conclusion.
	for _, other := range []*Conclusion{quick, full, merged} {
		again, err := Merge(merged, other)
		if err != nil {
			t.Fatalf("idempotent merge failed: %v", err)
		}
		if !conclusionsEqual(again, merged) {
			t.Fatalf("merge not idempotent:\n%+v\n%+v", again, merged)
		}
	}
}

func TestPartialConclusionNeverPasses(t *testing.T) {
	t1 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	quick := &Conclusion{
		Key:     testKey("v1"),
		Results: []CheckResult{NewNegativeResult("quick", t1)},
		Pending: []CheckID{"full"},
	}

	fullSet := []CheckID{"quick", "full"}
	if quick.Covers(fullSet) {
		t.Fatal("partial quick screen must not cover the full analysis set")
	}
	if got := quick.Decision(fullSet); got != StatusIncomplete {
		t.Fatalf("decision = %s, want incomplete", got)
	}
	if got := quick.Missing(fullSet); len(got) != 1 || got[0] != "full" {
		t.Fatalf("missing = %v, want [full]", got)
	}
	if quick.Status() != StatusIncomplete {
		t.Fatalf("status = %s, want incomplete while pending", quick.Status())
	}

	// Once the full set completes negative, it is genuinely clean.
	completed, err := Merge(quick, &Conclusion{
		Key:     testKey("v1"),
		Results: []CheckResult{NewNegativeResult("full", t1.Add(time.Minute))},
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if completed.Decision(fullSet) != StatusClean {
		t.Fatalf("decision = %s, want clean", completed.Decision(fullSet))
	}
}

func TestMergeValidation(t *testing.T) {
	t1 := time.Now()
	valid := &Conclusion{
		Key:     testKey("v1"),
		Results: []CheckResult{NewNegativeResult("quick", t1)},
	}

	if _, err := Merge(nil, valid); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil merge: got %v, want ErrInvalidKey", err)
	}

	badVerdict := &Conclusion{
		Key:     testKey("v1"),
		Results: []CheckResult{{Check: "quick", Verdict: Verdict(99), RanAt: t1}},
	}
	if _, err := Merge(valid, badVerdict); !errors.Is(err, ErrInvalidVerdict) {
		t.Fatalf("bad verdict: got %v, want ErrInvalidVerdict", err)
	}

	badKey := &Conclusion{
		Key:     Key{ContentDigest: "", RulesVersion: "v1"},
		Results: []CheckResult{NewNegativeResult("quick", t1)},
	}
	if _, err := Merge(valid, badKey); !errors.Is(err, ErrKeyMismatch) {
		t.Fatalf("bad key: got %v, want ErrKeyMismatch", err)
	}

	// An invalid key on its own is still rejected when both inputs agree on it.
	sameBadKey := &Conclusion{
		Key:     Key{ContentDigest: "", RulesVersion: "v1"},
		Results: []CheckResult{NewNegativeResult("other", t1)},
	}
	if _, err := Merge(badKey, sameBadKey); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid shared key: got %v, want ErrInvalidKey", err)
	}
}

func findResult(c *Conclusion, check CheckID) (CheckResult, bool) {
	for _, result := range c.Results {
		if result.Check == check {
			return result, true
		}
	}
	return CheckResult{}, false
}

func conclusionsEqual(a, b *Conclusion) bool {
	if a.Key != b.Key || len(a.Results) != len(b.Results) || len(a.Pending) != len(b.Pending) {
		return false
	}
	for i := range a.Results {
		ra, rb := a.Results[i], b.Results[i]
		if ra.Check != rb.Check || ra.Verdict != rb.Verdict || len(ra.Evidence) != len(rb.Evidence) {
			return false
		}
		for j := range ra.Evidence {
			if ra.Evidence[j] != rb.Evidence[j] {
				return false
			}
		}
	}
	for i := range a.Pending {
		if a.Pending[i] != b.Pending[i] {
			return false
		}
	}
	return true
}
