package interp

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
)

func mustAssemble(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := Assemble(src)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return prog
}

// runToEnd drives a VM through pauses until it halts or fails.
func runToEnd(vm *VM) Status {
	for vm.Status() == StatusReady || vm.Status() == StatusPaused {
		vm.Run()
	}
	return vm.Status()
}

type outcome struct {
	status Status
	output []int64
	cost   int64
	err    *ScriptError
}

func runOneShot(t *testing.T, prog *Program, budget int64) outcome {
	t.Helper()
	vm := NewVM(prog, budget)
	st := runToEnd(vm)
	return outcome{st, vm.Output(), vm.Cost(), vm.FirstError()}
}

// runChunked executes the program in random instruction quanta, taking a
// JSON snapshot and restoring from it at every single breakpoint.
func runChunked(t *testing.T, prog *Program, budget int64, seed int64) outcome {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	vm := NewVM(prog, budget)
	for vm.Status() == StatusReady || vm.Status() == StatusPaused {
		vm.RunQuantum(1 + rng.Int63n(7))
		if vm.Status() != StatusPaused {
			break
		}
		data, err := vm.Snapshot()
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		vm, err = Restore(prog, data)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
	}
	return outcome{vm.Status(), vm.Output(), vm.Cost(), vm.FirstError()}
}

func sameOutcome(t *testing.T, want, got outcome) {
	t.Helper()
	if want.status != got.status {
		t.Errorf("status: one-shot %v, chunked %v", want.status, got.status)
	}
	if !reflect.DeepEqual(want.output, got.output) {
		t.Errorf("output: one-shot %v, chunked %v", want.output, got.output)
	}
	if want.cost != got.cost {
		t.Errorf("cost: one-shot %d, chunked %d", want.cost, got.cost)
	}
	if (want.err == nil) != (got.err == nil) {
		t.Fatalf("first error: one-shot %v, chunked %v", want.err, got.err)
	}
	if want.err != nil && *want.err != *got.err {
		t.Errorf("first error: one-shot %v, chunked %v", want.err, got.err)
	}
}

func TestLoopAndLocals(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 2
	push 0
	store 1          ; sum = 0
	push 1
	store 0          ; i = 1
loop:
	load 0
	push 11
	lt
	jz done
	load 1
	load 0
	add
	store 1          ; sum += i
	load 0
	push 1
	add
	store 0          ; i++
	jmp loop
done:
	load 1
	print
	halt
`)
	got := runOneShot(t, prog, -1)
	if got.status != StatusHalted {
		t.Fatalf("status = %v, err = %v", got.status, got.err)
	}
	if !reflect.DeepEqual(got.output, []int64{55}) {
		t.Fatalf("output = %v, want [55]", got.output)
	}
	if got.err != nil {
		t.Fatalf("unexpected error %v", got.err)
	}
}

func TestRecursiveFunction(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 1
	push 10
	call fib
	print
	halt
func fib 1 2
	load 0
	push 2
	lt
	jz rec
	load 0
	ret
rec:
	load 0
	push 1
	sub
	call fib
	store 1
	load 0
	push 2
	sub
	call fib
	load 1
	add
	ret
`)
	got := runOneShot(t, prog, -1)
	if got.status != StatusHalted {
		t.Fatalf("status = %v, err = %v", got.status, got.err)
	}
	if !reflect.DeepEqual(got.output, []int64{55}) {
		t.Fatalf("output = %v, want [55]", got.output)
	}
}

func TestTryCatchAndCrossFrameThrow(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 1
	try caught
	push 10
	push 0
	div
	untry
	push 999
	print
	halt
caught:
	print            ; prints error code 1 (div by zero)
	try caught2
	push 42
	call thrower
	untry
	push 0
	print
	halt
caught2:
	print            ; prints 77 thrown inside thrower
	halt
func thrower 1 1
	push 77
	throw
	ret
`)
	got := runOneShot(t, prog, -1)
	if got.status != StatusHalted {
		t.Fatalf("status = %v, err = %v", got.status, got.err)
	}
	if !reflect.DeepEqual(got.output, []int64{1, 77}) {
		t.Fatalf("output = %v, want [1 77]", got.output)
	}
	if got.err == nil || got.err.Code != CodeDivZero {
		t.Fatalf("first error = %v, want code %d", got.err, CodeDivZero)
	}
}

func TestUncaughtError(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 1
	push 5
	push 6
	mul
	print
	push 1
	push 0
	div
	push 2
	print
	halt
`)
	got := runOneShot(t, prog, -1)
	if got.status != StatusFailed {
		t.Fatalf("status = %v", got.status)
	}
	if !reflect.DeepEqual(got.output, []int64{30}) {
		t.Fatalf("output = %v, want [30]", got.output)
	}
	if got.err == nil || got.err.Code != CodeDivZero {
		t.Fatalf("first error = %v", got.err)
	}
}

func TestBudgetExhaustionUncatchable(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 1
	try caught
loop:
	push 1
	store 0
	jmp loop
caught:
	push 0
	print
	halt
`)
	got := runOneShot(t, prog, 1000)
	if got.status != StatusFailed {
		t.Fatalf("status = %v, want failed", got.status)
	}
	if got.err == nil || got.err.Code != CodeBudget {
		t.Fatalf("first error = %v, want budget error", got.err)
	}
	if len(got.output) != 0 {
		t.Fatalf("catch swallowed budget error, output = %v", got.output)
	}
	if got.cost <= 1000 {
		t.Fatalf("cost = %d, want > budget", got.cost)
	}
}

// complexProg exercises locals, loops, recursion, try/catch, throw and
// explicit pause instructions.
const complexProg = `
func main 0 3
	push 0
	store 2          ; sum = 0
	push 1
	store 0          ; i = 1
loop:
	load 0
	push 8
	lt
	jz done
	pause
	load 0
	call fib
	load 2
	add
	store 2          ; sum += fib(i)
	try caught
	load 0
	push 3
	mod
	push 0
	eq
	jz nocatch
	push 100
	push 0
	div               ; fault every 3rd iteration
nocatch:
	untry
	jmp next
caught:
	load 2
	add
	store 2          ; sum += error code
next:
	load 0
	push 1
	add
	store 0
	jmp loop
done:
	load 2
	print
	halt
func fib 1 2
	load 0
	push 2
	lt
	jz rec
	load 0
	ret
rec:
	load 0
	push 1
	sub
	call fib
	store 1
	load 0
	push 2
	sub
	call fib
	load 1
	add
	ret
`

func TestOneShotVsChunkedEquivalence(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		budget int64
	}{
		{"complex", complexProg, -1},
		{"complex-budgeted", complexProg, 500},
		{"complex-tight-budget", complexProg, 37},
		{"uncaught", `
func main 0 1
	push 1
	store 0
loop:
	pause
	load 0
	push 5
	mul
	store 0
	load 0
	push 10000
	lt
	jz boom
	jmp loop
boom:
	push 1
	push 0
	div
	halt
`, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := mustAssemble(t, tc.src)
			want := runOneShot(t, prog, tc.budget)
			for seed := int64(0); seed < 20; seed++ {
				got := runChunked(t, prog, tc.budget, seed)
				sameOutcome(t, want, got)
			}
		})
	}
}

// TestSnapshotEveryInstruction restores from JSON after every single
// executed instruction: the strongest possible breakpoint granularity.
func TestSnapshotEveryInstruction(t *testing.T) {
	prog := mustAssemble(t, complexProg)
	want := runOneShot(t, prog, -1)

	vm := NewVM(prog, -1)
	for vm.Status() == StatusReady || vm.Status() == StatusPaused {
		vm.RunQuantum(1)
		if vm.Status() != StatusPaused {
			break
		}
		data, err := vm.Snapshot()
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		vm, err = Restore(prog, data)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
	}
	got := outcome{vm.Status(), vm.Output(), vm.Cost(), vm.FirstError()}
	sameOutcome(t, want, got)
}

func TestSnapshotContainsFullState(t *testing.T) {
	prog := mustAssemble(t, `
func main 0 2
	push 7
	store 0
	push 3
	call callee
	print
	halt
func callee 1 2
	push 9
	store 1
	pause
	load 0
	load 1
	add
	ret
`)
	vm := NewVM(prog, -1)
	vm.Run()
	if vm.Status() != StatusPaused {
		t.Fatalf("status = %v, want paused", vm.Status())
	}
	data, err := vm.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var decoded struct {
		Frames []Frame `json:"frames"`
		Cost   int64   `json:"cost"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Frames) != 2 {
		t.Fatalf("frames = %d, want 2 (call stack must be serialized)", len(decoded.Frames))
	}
	callee := decoded.Frames[1]
	if callee.Func != "callee" || len(callee.Locals) != 2 || callee.Locals[0] != 3 || callee.Locals[1] != 9 {
		t.Fatalf("callee frame locals not serialized: %+v", callee)
	}

	vm2, err := Restore(prog, data)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if st := runToEnd(vm2); st != StatusHalted {
		t.Fatalf("status = %v", st)
	}
	if !reflect.DeepEqual(vm2.Output(), []int64{12}) {
		t.Fatalf("output = %v, want [12]", vm2.Output())
	}
	if vm2.Cost() <= decoded.Cost {
		t.Fatalf("cost did not accumulate across restore: %d <= %d", vm2.Cost(), decoded.Cost)
	}
}
