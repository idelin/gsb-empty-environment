package docexec

import (
	"strings"
	"testing"
)

func resultByName(t *testing.T, res *RunResult, name string) *BlockResult {
	t.Helper()
	for _, r := range res.Results {
		if r.Block.Name == name {
			return r
		}
	}
	t.Fatalf("no result for block %q", name)
	return nil
}

func TestDependencyOrderAndSharedNamespace(t *testing.T) {
	doc := Parse(`# Demo

` + "```run name=use needs=setup\nprint got $greeting\n```\n\n" +
		"```run name=setup\nset greeting hello\nprint defined\n```\n")
	res := Run(doc)

	if len(res.Errors) != 0 {
		t.Fatalf("unexpected validation errors: %v", res.Errors)
	}
	setup := resultByName(t, res, "setup")
	use := resultByName(t, res, "use")
	if setup.Status != StatusSucceeded || use.Status != StatusSucceeded {
		t.Fatalf("statuses: setup=%s use=%s", setup.Status, use.Status)
	}
	if got := strings.Join(use.Output, "\n"); got != "got hello" {
		t.Fatalf("use output = %q, want %q", got, "got hello")
	}
	if v, ok := res.NS.Get("greeting"); !ok || v != "hello" {
		t.Fatalf("namespace missing greeting: %v", res.NS.Snapshot())
	}
}

func TestFailureSkipsOnlyDependents(t *testing.T) {
	doc := Parse("" +
		"```run name=bad\nset partial 1\nfail boom\n```\n" +
		"```run name=child needs=bad\nprint never\n```\n" +
		"```run name=grandchild needs=child\nprint never\n```\n" +
		"```run name=independent\nprint still runs\n```\n")
	res := Run(doc)

	if r := resultByName(t, res, "bad"); r.Status != StatusFailed {
		t.Fatalf("bad status = %s", r.Status)
	}
	for _, name := range []string{"child", "grandchild"} {
		if r := resultByName(t, res, name); r.Status != StatusSkipped {
			t.Fatalf("%s status = %s, want skipped", name, r.Status)
		}
	}
	indep := resultByName(t, res, "independent")
	if indep.Status != StatusSucceeded {
		t.Fatalf("independent status = %s", indep.Status)
	}
	if got := strings.Join(indep.Output, "\n"); got != "still runs" {
		t.Fatalf("independent output = %q", got)
	}
}

func TestFailedBlockLeavesNoPartialState(t *testing.T) {
	doc := Parse("" +
		"```run name=bad\nset leaked 1\nset other 2\nfail boom\n```\n" +
		"```run name=probe\nprint leaked=$leaked\n```\n")
	res := Run(doc)

	if r := resultByName(t, res, "bad"); r.Status != StatusFailed {
		t.Fatalf("bad status = %s", r.Status)
	}
	if _, ok := res.NS.Get("leaked"); ok {
		t.Fatalf("failed block leaked state: %v", res.NS.Snapshot())
	}
	if _, ok := res.NS.Get("other"); ok {
		t.Fatalf("failed block leaked state: %v", res.NS.Snapshot())
	}
	// The probe block must fail on the undefined variable, proving the
	// partial writes were rolled back rather than merely hidden.
	probe := resultByName(t, res, "probe")
	if probe.Status != StatusFailed {
		t.Fatalf("probe status = %s, want failed", probe.Status)
	}
	if !strings.Contains(probe.Err.Error(), `undefined variable "leaked"`) {
		t.Fatalf("probe err = %v", probe.Err)
	}
}

func TestCycleDetection(t *testing.T) {
	doc := Parse("" +
		"```run name=a needs=b\nprint a\n```\n" +
		"```run name=b needs=a\nprint b\n```\n" +
		"```run name=free\nprint ok\n```\n")
	res := Run(doc)

	if len(res.Errors) == 0 {
		t.Fatal("expected a cycle validation error")
	}
	foundCycle := false
	for _, err := range res.Errors {
		if strings.Contains(err.Error(), "cycle") {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("errors do not mention cycle: %v", res.Errors)
	}
	for _, name := range []string{"a", "b"} {
		if r := resultByName(t, res, name); r.Status != StatusSkipped {
			t.Fatalf("%s status = %s, want skipped", name, r.Status)
		}
	}
	if r := resultByName(t, res, "free"); r.Status != StatusSucceeded {
		t.Fatalf("unrelated block should still run, got %s", r.Status)
	}
}

func TestDuplicateBlockNames(t *testing.T) {
	doc := Parse("" +
		"```run name=dup\nprint first\n```\n" +
		"```run name=dup\nprint second\n```\n")
	res := Run(doc)

	if len(res.Errors) == 0 {
		t.Fatal("expected a duplicate-name validation error")
	}
	first := res.Results[0]
	second := res.Results[1]
	if first.Status != StatusSucceeded {
		t.Fatalf("first definition should run, got %s", first.Status)
	}
	if second.Status != StatusFailed {
		t.Fatalf("second definition status = %s, want failed", second.Status)
	}
}

func TestDuplicateVariableDefinition(t *testing.T) {
	doc := Parse("" +
		"```run name=one\nset x 1\n```\n" +
		"```run name=two\nset x 2\n```\n")
	res := Run(doc)

	if r := resultByName(t, res, "one"); r.Status != StatusSucceeded {
		t.Fatalf("one status = %s", r.Status)
	}
	two := resultByName(t, res, "two")
	if two.Status != StatusFailed {
		t.Fatalf("two status = %s, want failed", two.Status)
	}
	if !strings.Contains(two.Err.Error(), "duplicate definition") {
		t.Fatalf("two err = %v", two.Err)
	}
	if v, _ := res.NS.Get("x"); v != "1" {
		t.Fatalf("x = %q, want %q (failed redefinition must not overwrite)", v, "1")
	}
}

func TestUnknownDependency(t *testing.T) {
	doc := Parse("```run name=a needs=ghost\nprint a\n```\n")
	res := Run(doc)

	if len(res.Errors) == 0 {
		t.Fatal("expected an unknown-dependency validation error")
	}
	r := resultByName(t, res, "a")
	if r.Status != StatusSkipped || !strings.Contains(r.Reason, "ghost") {
		t.Fatalf("a = %s (%s)", r.Status, r.Reason)
	}
}

func TestOutputMappedToSourceLines(t *testing.T) {
	src := "# Title\n\n```run name=a\nprint A\n```\ntext\n```run name=b\nprint B\n```\n"
	doc := Parse(src)
	res := Run(doc)

	a := resultByName(t, res, "a")
	b := resultByName(t, res, "b")
	if a.Block.StartLine != 3 || a.Block.EndLine != 5 {
		t.Fatalf("a lines = %d-%d, want 3-5", a.Block.StartLine, a.Block.EndLine)
	}
	if b.Block.StartLine != 7 || b.Block.EndLine != 9 {
		t.Fatalf("b lines = %d-%d, want 7-9", b.Block.StartLine, b.Block.EndLine)
	}

	annotated := Annotate(doc, res)
	lines := strings.Split(annotated, "\n")
	// The output fence for block a must appear immediately after its
	// closing fence (original line 5), i.e. at annotated line 6.
	if lines[5] != "```output" {
		t.Fatalf("line 6 of annotated doc = %q, want ```output", lines[5])
	}
	if !strings.Contains(strings.Join(lines[5:9], "\n"), "A") {
		t.Fatalf("annotated output for a missing: %q", strings.Join(lines[5:9], "\n"))
	}
	// Original source lines must be preserved verbatim and in order.
	pos := 0
	for _, srcLine := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		for pos < len(lines) && lines[pos] != srcLine {
			pos++
		}
		if pos == len(lines) {
			t.Fatalf("source line %q not found in annotated output", srcLine)
		}
	}
}

func TestReportIncludesLineNumbers(t *testing.T) {
	doc := Parse("# T\n\n```run name=a\nprint hi\n```\n")
	res := Run(doc)
	report := Report(res)
	if !strings.Contains(report, "lines 3-5") {
		t.Fatalf("report missing line range:\n%s", report)
	}
	if !strings.Contains(report, "| hi") {
		t.Fatalf("report missing output:\n%s", report)
	}
}
