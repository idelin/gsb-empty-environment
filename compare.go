package netsec

import "fmt"

// BuiltinCompareSteps is the exact number of VM instructions executed by the
// built-in comparison program for 32-byte inputs: five body instructions and
// one public loop instruction for each of the 32 bytes.
const BuiltinCompareSteps uint64 = 32 * (5 + 1)

const (
	compareSecretReg = iota
	compareCandidateReg
	compareXorReg
	compareAccumulatorReg
	compareIndexReg
)

// BuiltinResultReg is the register containing the comparison accumulator.
const BuiltinResultReg uint8 = compareAccumulatorReg

// CompareProgram returns a fresh branch-free 32-byte comparison program. It
// reads byte i of both protected regions on every iteration, accumulates
// (secret[i] XOR candidate[i]) with OR, and returns the accumulator. A zero
// output means equal.
//
// The program contains no value-dependent branch. Its public loop count is
// always exactly 32.
func CompareProgram() Program {
	return Program{
		Instructions: []Instruction{
			Read(compareSecretReg, BaseSecret, compareIndexReg),
			Read(compareCandidateReg, BaseCandidate, compareIndexReg),
			Xor(compareXorReg, compareSecretReg, compareCandidateReg),
			Or(compareAccumulatorReg, compareAccumulatorReg, compareXorReg),
			Inc(compareIndexReg, compareIndexReg),
			Loop(SecretSize, 0),
		},
		Result: BuiltinResultReg,
	}
}

// Compare performs a 32-byte comparison. It returns false unless both inputs
// have exactly 32 bytes; a length failure is rejected before any protected
// byte is read. The result is the built-in program's zero accumulator.
func Compare(secret, candidate []byte) bool {
	equal, err := CompareChecked(secret, candidate)
	return err == nil && equal
}

// CompareChecked is Compare with an explicit error for invalid lengths or an
// internal execution failure.
func CompareChecked(secret, candidate []byte) (bool, error) {
	if len(secret) != SecretSize || len(candidate) != CandidateSize {
		return false, ErrInvalidLength
	}
	var s, c [SecretSize]byte
	copy(s[:], secret)
	copy(c[:], candidate)
	return Compare32Checked(s, c)
}

// Compare32 performs a comparison over two fixed-size arrays.
func Compare32(secret, candidate [SecretSize]byte) bool {
	equal, err := Compare32Checked(secret, candidate)
	return err == nil && equal
}

// Compare32Checked performs a fixed-size comparison with an explicit error.
func Compare32Checked(secret, candidate [SecretSize]byte) (bool, error) {
	exec, err := Execute32(CompareProgram(), secret, candidate, RunConfig{MaxSteps: BuiltinCompareSteps})
	if err != nil {
		return false, err
	}
	return exec.Halted && exec.Output == 0, nil
}

// CompareTrace returns the comparison result and the complete observable
// trace. Length failures return ErrInvalidLength and an empty trace before any
// protected read.
func CompareTrace(secret, candidate []byte) (bool, Trace, error) {
	if len(secret) != SecretSize || len(candidate) != CandidateSize {
		return false, nil, ErrInvalidLength
	}
	var s, c [SecretSize]byte
	copy(s[:], secret)
	copy(c[:], candidate)
	return Compare32Trace(s, c)
}

// Compare32Trace returns the fixed-size comparison result and trace.
func Compare32Trace(secret, candidate [SecretSize]byte) (bool, Trace, error) {
	exec, err := Execute32(CompareProgram(), secret, candidate, RunConfig{MaxSteps: BuiltinCompareSteps})
	if err != nil {
		return false, exec.Trace.Clone(), err
	}
	return exec.Halted && exec.Output == 0, exec.Trace.Clone(), nil
}

// SecureEqual is an alias for Compare suitable for authentication code.
func SecureEqual(secret, candidate []byte) bool {
	return Compare(secret, candidate)
}

// SecureCompare is an alias for Compare.
func SecureCompare(secret, candidate []byte) bool {
	return Compare(secret, candidate)
}

// ConstantTimeCompare is an alias for Compare. The name refers to the modeled
// trace and is not a claim about every compiled machine instruction.
func ConstantTimeCompare(secret, candidate []byte) bool {
	return Compare(secret, candidate)
}

// VerifyCompareProgram exhaustively checks the built-in program over one
// all-equal case and every single-byte difference at every position (32
// positions times 255 non-equal replacement values). Every case must return
// the correct result and have exactly the same observable trace.
func VerifyCompareProgram(maxSteps uint64) error {
	if maxSteps == 0 {
		return ErrUnknownBudget
	}
	if maxSteps < BuiltinCompareSteps {
		return fmt.Errorf("%w: need at least %d steps", ErrBudgetExceeded, BuiltinCompareSteps)
	}

	program := CompareProgram()
	var reference [SecretSize]byte
	for i := range reference {
		reference[i] = byte(37 + 11*i)
	}

	equalExec, err := Execute32(program, reference, reference, RunConfig{MaxSteps: maxSteps})
	if err != nil {
		return fmt.Errorf("all-equal case: %w", err)
	}
	if !equalExec.Halted || equalExec.Output != 0 {
		return fmt.Errorf("all-equal case returned output %d, want 0", equalExec.Output)
	}
	if equalExec.Steps != BuiltinCompareSteps {
		return fmt.Errorf("all-equal case used %d steps, want %d", equalExec.Steps, BuiltinCompareSteps)
	}
	referenceTrace := equalExec.Trace

	for position := 0; position < SecretSize; position++ {
		differing := reference
		for value := 0; value <= 255; value++ {
			if byte(value) == reference[position] {
				continue
			}
			differing[position] = byte(value)
			exec, err := Execute32(program, reference, differing, RunConfig{MaxSteps: maxSteps})
			if err != nil {
				return fmt.Errorf("position %d value %d: %w", position, value, err)
			}
			if !exec.Halted || exec.Output == 0 {
				return fmt.Errorf("position %d value %d returned output %d, want nonzero", position, value, exec.Output)
			}
			if !referenceTrace.Equal(exec.Trace) {
				step, _ := referenceTrace.FirstDifference(exec.Trace)
				return fmt.Errorf("position %d value %d changed trace at step %d", position, value, step)
			}
			if exec.Steps != BuiltinCompareSteps {
				return fmt.Errorf("position %d value %d used %d steps, want %d", position, value, exec.Steps, BuiltinCompareSteps)
			}
		}
	}
	return nil
}
