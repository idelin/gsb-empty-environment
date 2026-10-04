package main

import (
	"io/ioutil"
	"reflect"
	"strings"
	"testing"
)

func assembleFile(t *testing.T, path string) *Program {
	t.Helper()
	data, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := Assemble(string(data))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return prog
}

func TestLoop(t *testing.T) {
	prog := assembleFile(t, "examples/loop.vm")
	r := runOnce(prog, 0)
	if !reflect.DeepEqual(r.Output, []string{"55"}) || r.Err != "" {
		t.Fatalf("got %+v", r)
	}
}

func TestFib(t *testing.T) {
	prog := assembleFile(t, "examples/fib.vm")
	r := runOnce(prog, 0)
	if !reflect.DeepEqual(r.Output, []string{"55"}) || r.Err != "" {
		t.Fatalf("got %+v", r)
	}
}

func TestThrowAndCatch(t *testing.T) {
	prog := assembleFile(t, "examples/throw.vm")
	r := runOnce(prog, 0)
	want := []string{"42", "7", "-1"}
	if !reflect.DeepEqual(r.Output, want) || r.Err != "" {
		t.Fatalf("got %+v, want output %v", r, want)
	}
}

func TestUncaughtException(t *testing.T) {
	prog := assembleFile(t, "examples/uncaught.vm")
	r := runOnce(prog, 0)
	if !strings.Contains(r.Err, "uncaught exception: 7") {
		t.Fatalf("got %+v", r)
	}
}

func TestOutOfBudgetNotCatchable(t *testing.T) {
	prog := assembleFile(t, "examples/budget.vm")
	r := runOnce(prog, 100)
	if r.Err != ErrOutOfBudget.Error() {
		t.Fatalf("expected out-of-budget error, got %+v", r)
	}
	if len(r.Output) != 0 {
		t.Fatalf("script swallowed out-of-budget via catch: %+v", r)
	}
	if r.Gas != 100 {
		t.Fatalf("gas = %d, want exactly 100", r.Gas)
	}
}

func TestBudgetExactBoundary(t *testing.T) {
	prog := assembleFile(t, "examples/loop.vm")
	full := runOnce(prog, 0)
	// Budget exactly at the needed amount: same result.
	if r := runOnce(prog, full.Gas); !reflect.DeepEqual(r, full) {
		t.Fatalf("exact budget: %+v != %+v", r, full)
	}
	// One less: uncatchable out-of-budget, partial output preserved.
	r := runOnce(prog, full.Gas-1)
	if r.Err != ErrOutOfBudget.Error() || r.Gas != full.Gas-1 {
		t.Fatalf("got %+v", r)
	}
}

// TestVerifyAll compares a single uninterrupted run against resume at every
// possible breakpoint (plus chained checkpoints), checking output, first
// error, and gas.
func TestVerifyAll(t *testing.T) {
	cases := []struct {
		prog   string
		budget int64
	}{
		{"examples/loop.vm", 0},
		{"examples/loop.vm", 37}, // runs out of budget mid-loop
		{"examples/fib.vm", 0},
		{"examples/fib.vm", 500}, // out of budget deep in recursion
		{"examples/throw.vm", 0},
		{"examples/throw.vm", 12}, // out of budget inside handler code
		{"examples/uncaught.vm", 0},
		{"examples/budget.vm", 200},
	}
	for _, c := range cases {
		prog := assembleFile(t, c.prog)
		if problems := Verify(prog, c.budget); len(problems) > 0 {
			t.Errorf("%s budget=%d:\n  %s", c.prog, c.budget,
				strings.Join(problems, "\n  "))
		}
	}
}

func TestSnapshotIsCompleteState(t *testing.T) {
	prog := assembleFile(t, "examples/fib.vm")
	vm := NewVM(prog, 0)
	for i := 0; i < 100; i++ {
		vm.Step()
	}
	data, err := vm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, field := range []string{
		`"pc"`, `"frames"`, `"Func"`, `"RetPC"`, `"Locals"`,
		`"stack"`, `"handlers"`, `"gasUsed"`, `"budget"`, `"output"`,
	} {
		if !strings.Contains(s, field) {
			t.Errorf("snapshot missing %s:\n%s", field, s)
		}
	}
}
