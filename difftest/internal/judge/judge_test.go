package judge

import (
	"context"
	"testing"
	"time"

	"difftest/internal/faultinj"
	"difftest/internal/normalize"
	"difftest/internal/runner"
)

var numericRules = normalize.Rules{
	TrimLineTrailingSpace:    true,
	IgnoreTrailingBlankLines: true,
	NumericTokens:            true,
	FloatTolerance:           1e-9,
}

func run(e runner.Executor) runner.Outcome {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return e.Run(ctx, []byte("in"))
}

func valueOut(s string) runner.Outcome {
	return runner.Outcome{Kind: runner.KindValue, Stdout: s}
}

// Fault-injection matrix: parse failure, run failure, timeout and semantic
// difference must never be confused with one another.
func TestJudgeFaultInjectionMatrix(t *testing.T) {
	mk := func(b faultinj.Behavior, out string) *faultinj.Executor {
		return &faultinj.Executor{Label: string(b), Behavior: b, Output: out}
	}
	good := run(mk(faultinj.Value, "42"))
	other := run(mk(faultinj.Value, "43"))
	garbage := run(mk(faultinj.ParseGarbage, "oops"))
	crash := run(mk(faultinj.RunFail, ""))
	hang := run(mk(faultinj.Hang, ""))

	if good.Kind != runner.KindValue || other.Kind != runner.KindValue {
		t.Fatal("sanity: value executors must produce values")
	}
	if garbage.Kind != runner.KindValue {
		t.Fatal("sanity: parse garbage still exits 0 (it is a parse failure, not a run failure)")
	}
	if crash.Kind != runner.KindError {
		t.Fatal("sanity: runfail must be a run error")
	}
	if hang.Kind != runner.KindTimeout {
		t.Fatal("sanity: hang must be a timeout")
	}

	cases := []struct {
		name string
		a, b runner.Outcome
		want Verdict
	}{
		{"equal", good, good, Equal},
		{"semantic diff", good, other, SemanticDiff},
		{"parse error B", good, garbage, ParseError},
		{"parse error both", garbage, garbage, ParseError},
		{"run error B", good, crash, RunError},
		{"timeout B", good, hang, Timeout},
		// The core invariants:
		{"timeout is not value inequality", hang, other, Timeout},
		{"timeout beats parse error", hang, garbage, Timeout},
		{"run error is not semantic diff", crash, other, RunError},
		{"run error is not parse error", crash, garbage, RunError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.a, tc.b, numericRules)
			if got.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s (detail: %s)", got.Verdict, tc.want, got.Detail)
			}
			if got.Verdict == SemanticDiff && (tc.a.Kind != runner.KindValue || tc.b.Kind != runner.KindValue) {
				t.Fatal("semantic diff requires two well-formed values")
			}
		})
	}
}

func TestTimeoutNeverComparedAsValue(t *testing.T) {
	// Even if one side prints a different value and the other times out,
	// the verdict must remain timeout, never semantic_diff.
	a := runner.Outcome{Kind: runner.KindTimeout}
	b := valueOut("completely different")
	if got := Judge(a, b, numericRules); got.Verdict != Timeout {
		t.Fatalf("got %s, want timeout", got.Verdict)
	}
	if got := Judge(b, a, numericRules); got.Verdict != Timeout {
		t.Fatalf("got %s, want timeout (side order must not matter)", got.Verdict)
	}
}

func TestNormalizationAppliedBeforeCompare(t *testing.T) {
	a := valueOut("42   \n\n")
	b := valueOut("42")
	if got := Judge(a, b, numericRules); got.Verdict != Equal {
		t.Fatalf("got %s, want equal after normalization", got.Verdict)
	}
}
