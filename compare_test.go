package netsec

import (
	"errors"
	"testing"
)

func TestBuiltinCompareTraceShapeAndExhaustiveCases(t *testing.T) {
	if err := VerifyCompareProgram(BuiltinCompareSteps); err != nil {
		t.Fatalf("VerifyCompareProgram: %v", err)
	}

	var reference [SecretSize]byte
	for i := range reference {
		reference[i] = byte(91 + 7*i)
	}

	equal, referenceTrace, err := Compare32Trace(reference, reference)
	if err != nil {
		t.Fatalf("Compare32Trace equal case: %v", err)
	}
	if !equal {
		t.Fatal("equal inputs reported unequal")
	}
	if got, want := len(referenceTrace), 32*3; got != want {
		t.Fatalf("trace length = %d, want %d", got, want)
	}

	for i := 0; i < SecretSize; i++ {
		readSecret := referenceTrace[3*i]
		readCandidate := referenceTrace[3*i+1]
		loopBranch := referenceTrace[3*i+2]

		if readSecret.Kind != EventRead || readSecret.Address != SecretBase+uint32(i) {
			t.Fatalf("step %d secret read = %+v", 3*i, readSecret)
		}
		if readCandidate.Kind != EventRead || readCandidate.Address != CandidateBase+uint32(i) {
			t.Fatalf("step %d candidate read = %+v", 3*i+1, readCandidate)
		}
		if loopBranch.Kind != EventBranch || loopBranch.PC != 5 {
			t.Fatalf("step %d loop event = %+v", 3*i+2, loopBranch)
		}
		wantTaken := i != SecretSize-1
		if loopBranch.Taken != wantTaken {
			t.Fatalf("step %d loop taken = %t, want %t", 3*i+2, loopBranch.Taken, wantTaken)
		}
	}

	cases := 1
	for position := 0; position < SecretSize; position++ {
		candidate := reference
		for value := 0; value <= 255; value++ {
			if byte(value) == reference[position] {
				continue
			}
			candidate[position] = byte(value)
			equal, trace, err := Compare32Trace(reference, candidate)
			if err != nil {
				t.Fatalf("position %d value %d: %v", position, value, err)
			}
			if equal {
				t.Fatalf("position %d value %d reported equal", position, value)
			}
			if !referenceTrace.Equal(trace) {
				step, _ := referenceTrace.FirstDifference(trace)
				t.Fatalf("position %d value %d changed trace at step %d", position, value, step)
			}
			cases++
		}
	}
	if want := 1 + SecretSize*255; cases != want {
		t.Fatalf("exercised %d cases, want %d", cases, want)
	}
}

func TestLengthValidationPrecedesProtectedReads(t *testing.T) {
	valid := make([]byte, SecretSize)

	equal, trace, err := CompareTrace(make([]byte, SecretSize-1), valid)
	if !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("short secret error = %v, want ErrInvalidLength", err)
	}
	if equal || trace != nil {
		t.Fatalf("invalid length returned equal=%t trace=%v", equal, trace)
	}

	exec, err := Execute(CompareProgram(), valid, make([]byte, CandidateSize+1), RunConfig{MaxSteps: BuiltinCompareSteps})
	if !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("long candidate error = %v, want ErrInvalidLength", err)
	}
	if len(exec.Trace) != 0 || exec.Steps != 0 {
		t.Fatalf("invalid length executed %d steps with %d events", exec.Steps, len(exec.Trace))
	}
}

func TestUnknownBudgetIsNotAPass(t *testing.T) {
	_, err := Execute32(CompareProgram(), [SecretSize]byte{}, [SecretSize]byte{}, RunConfig{})
	if !errors.Is(err, ErrUnknownBudget) {
		t.Fatalf("zero budget error = %v, want ErrUnknownBudget", err)
	}

	_, err = Execute32(CompareProgram(), [SecretSize]byte{}, [SecretSize]byte{}, RunConfig{MaxSteps: 1})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("short budget error = %v, want ErrBudgetExceeded", err)
	}
}
