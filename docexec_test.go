package docexec

import (
	"strings"
	"testing"
)

func mustExecute(t *testing.T, src string) *Report {
	t.Helper()
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	rep, err := Execute(doc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return rep
}

func TestDependencyOrderAndSharedNamespace(t *testing.T) {
	// b appears before a in the document but depends on it.
	rep := mustExecute(t, `
`+"```run id=b needs=a defines=B\n"+`
set B got-$A
emit B is $B
`+"```\n"+`
`+"```run id=a defines=A\n"+`
set A 1
emit A is $A
`+"```\n")

	if rep.Result("a").Status != OK || rep.Result("b").Status != OK {
		t.Fatalf("statuses: a=%v b=%v", rep.Result("a").Status, rep.Result("b").Status)
	}
	if got := rep.Namespace["B"]; got != "got-1" {
		t.Fatalf("B = %q, want %q", got, "got-1")
	}
	// Outputs map back to source lines: a's emit is on line 11.
	outs := rep.Result("a").Outputs
	if len(outs) != 1 || outs[0].Line != 11 || outs[0].Text != "A is 1" {
		t.Fatalf("a outputs = %+v", outs)
	}
	outs = rep.Result("b").Outputs
	if len(outs) != 1 || outs[0].Line != 5 || outs[0].Text != "B is got-1" {
		t.Fatalf("b outputs = %+v", outs)
	}
}

func TestFailureSkipsOnlyAffectedBlocks(t *testing.T) {
	rep := mustExecute(t, `
`+"```run id=root defines=R\n"+`
set R ok
`+"```\n"+`
`+"```run id=bad needs=root\n"+`
fail boom
`+"```\n"+`
`+"```run id=child needs=bad\n"+`
emit never
`+"```\n"+`
`+"```run id=grandchild needs=child\n"+`
emit never either
`+"```\n"+`
`+"```run id=sibling needs=root defines=S\n"+`
set S fine
emit sibling ran
`+"```\n")

	want := map[string]Status{
		"root": OK, "bad": Failed, "child": Skipped,
		"grandchild": Skipped, "sibling": OK,
	}
	for id, st := range want {
		if got := rep.Result(id).Status; got != st {
			t.Errorf("block %q status = %v, want %v", id, got, st)
		}
	}
	if r := rep.Result("child"); !strings.Contains(r.SkipReason, "bad (failed)") {
		t.Errorf("child skip reason = %q", r.SkipReason)
	}
	if r := rep.Result("grandchild"); !strings.Contains(r.SkipReason, "child (skipped)") {
		t.Errorf("grandchild skip reason = %q", r.SkipReason)
	}
}

func TestFailedBlockLeavesNoPartialState(t *testing.T) {
	rep := mustExecute(t, `
`+"```run id=bad\n"+`
set LEAKED oops
emit partial output
fail after writes
`+"```\n"+`
`+"```run id=probe defines=P\n"+`
set P clean
`+"```\n")

	if _, leaked := rep.Namespace["LEAKED"]; leaked {
		t.Fatalf("LEAKED leaked into namespace: %v", rep.Namespace)
	}
	if rep.Namespace["P"] != "clean" {
		t.Fatalf("P = %q", rep.Namespace["P"])
	}
	r := rep.Result("bad")
	if r.Status != Failed || r.ErrLine != 6 {
		t.Fatalf("bad result = %+v", r)
	}
	if len(r.Outputs) != 0 {
		t.Fatalf("failed block should record no outputs, got %+v", r.Outputs)
	}
}

func TestValidateDuplicateDefines(t *testing.T) {
	doc, err := Parse(`
` + "```run id=a defines=X\n" + `
set X 1
` + "```\n" + `
` + "```run id=b defines=X\n" + `
set X 2
` + "```\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = Validate(doc)
	if err == nil || !strings.Contains(err.Error(), `"X" defined by both`) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateDuplicateIDs(t *testing.T) {
	doc, _ := Parse(`
` + "```run id=a\nfail x\n```\n```run id=a\nfail y\n```\n")
	if err := Validate(doc); err == nil || !strings.Contains(err.Error(), `duplicate block id "a"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateUnknownDependency(t *testing.T) {
	doc, _ := Parse("```run id=a needs=ghost\nfail x\n```\n")
	if err := Validate(doc); err == nil || !strings.Contains(err.Error(), `unknown block "ghost"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateCycle(t *testing.T) {
	doc, _ := Parse(`
` + "```run id=a needs=c\nfail x\n```\n" + `
` + "```run id=b needs=a\nfail x\n```\n" + `
` + "```run id=c needs=b\nfail x\n```\n")
	err := Validate(doc)
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "a -> ") {
		t.Fatalf("cycle path missing: %v", err)
	}
}

func TestRuntimeDuplicateSetFails(t *testing.T) {
	rep := mustExecute(t, `
`+"```run id=a defines=X\n"+`
set X 1
set X 2
`+"```\n")
	r := rep.Result("a")
	if r.Status != Failed || !strings.Contains(r.Err, "already defined") {
		t.Fatalf("result = %+v", r)
	}
	if _, ok := rep.Namespace["X"]; ok {
		t.Fatalf("X leaked despite failure")
	}
}

func TestUndefinedVariableFails(t *testing.T) {
	rep := mustExecute(t, "```run id=a\nemit $NOPE\n```\n")
	if r := rep.Result("a"); r.Status != Failed || !strings.Contains(r.Err, "undefined variable") {
		t.Fatalf("result = %+v", r)
	}
}

func TestExpectMismatchFails(t *testing.T) {
	rep := mustExecute(t, "```run id=a defines=X\nset X 1\nexpect X 2\n```\n")
	if r := rep.Result("a"); r.Status != Failed || !strings.Contains(r.Err, "expect") {
		t.Fatalf("result = %+v", r)
	}
}

func TestNonRunFencesIgnored(t *testing.T) {
	rep := mustExecute(t, `
`+"```sh\n"+`
set X hacked
`+"```\n"+`
`+"```run id=a defines=Y\n"+`
set Y real
`+"```\n")
	if _, ok := rep.Namespace["X"]; ok {
		t.Fatal("non-run fence leaked into namespace")
	}
	if rep.Namespace["Y"] != "real" {
		t.Fatalf("Y = %q", rep.Namespace["Y"])
	}
}

func TestExpansionInSetAndBraces(t *testing.T) {
	rep := mustExecute(t, "```run id=a defines=A,B\nset A wo\nset B ${A}rld\nemit hello $B\n```\n")
	if rep.Namespace["B"] != "world" {
		t.Fatalf("B = %q", rep.Namespace["B"])
	}
	outs := rep.Result("a").Outputs
	if len(outs) != 1 || outs[0].Text != "hello world" {
		t.Fatalf("outputs = %+v", outs)
	}
}
