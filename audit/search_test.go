package audit

import (
	"strings"
	"testing"
)

func TestShortestViolationUsesEndedIdentity(t *testing.T) {
	trace, ok := ShortestViolation(16)
	if !ok {
		t.Fatal("no violation found")
	}
	if len(trace.Steps) != 6 {
		t.Fatalf("shortest trace has %d steps:\n%s", len(trace.Steps), trace)
	}
	if !strings.Contains(trace.Violation, "ended identity") {
		t.Fatalf("violation = %q", trace.Violation)
	}
	if trace.Steps[len(trace.Steps)-1].Action != "access-local(R1)" {
		t.Fatalf("unexpected last step: %s", trace.Steps[len(trace.Steps)-1].Action)
	}
}

func TestCrossRequestViolationUsesAnotherLiveRequest(t *testing.T) {
	trace, ok := CrossRequestViolation(16)
	if !ok {
		t.Fatal("no cross-request violation found")
	}
	if len(trace.Steps) != 8 {
		t.Fatalf("cross-request trace has %d steps:\n%s", len(trace.Steps), trace)
	}
	if !strings.Contains(trace.Violation, "another request") {
		t.Fatalf("violation = %q", trace.Violation)
	}
	if trace.Steps[len(trace.Steps)-1].Action != "access-local(R1)" {
		t.Fatalf("unexpected last step: %s", trace.Steps[len(trace.Steps)-1].Action)
	}
}

func TestCoverageViolationExercisesRequiredFeatures(t *testing.T) {
	trace, ok := CoverageViolation(16)
	if !ok {
		t.Fatal("no coverage violation found")
	}
	if !trace.Coverage {
		t.Fatal("trace is not marked as coverage")
	}
	if len(trace.Steps) != 10 {
		t.Fatalf("coverage trace has %d steps:\n%s", len(trace.Steps), trace)
	}
	actions := make([]string, len(trace.Steps))
	for i, step := range trace.Steps {
		actions[i] = step.Action
	}
	joined := "\n" + strings.Join(actions, "\n") + "\n"
	for _, required := range []string{
		"\nnest(R1)\n",
		"\nexit(R1)\n",
		"\nauth(R2)\n",
		"\nenter(R2)\n",
		"\naccess-remote(R1)\n",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("coverage trace missing %q:\n%s", strings.TrimSpace(required), trace)
		}
	}
	if !strings.Contains(trace.Violation, "ended identity") {
		t.Fatalf("violation = %q", trace.Violation)
	}
}

func TestSearchHonorsDepthBound(t *testing.T) {
	if _, ok := ShortestViolation(5); ok {
		t.Fatal("found a violation below the six-step minimum")
	}
}
