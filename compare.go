package netsec

// CompareProgram returns the secret comparison program. It is deliberately
// branch-free: the trace consists only of the 64 fixed read addresses, so
// every 32-byte input pair produces the identical observable trace. The
// accumulated difference is left in R0; the host interprets R0 == 0 as
// "equal".
//
// Register use: R0 accumulator, R1/R2 scratch bytes, R3 loop index.
func CompareProgram() []Instruction {
	body := []Instruction{
		{Op: OpRead, Dst: 1, SrcA: 3, Imm: SecretBase},
		{Op: OpRead, Dst: 2, SrcA: 3, Imm: CandidateBase},
		{Op: OpXor, Dst: 1, SrcA: 1, SrcB: 2},
		{Op: OpOr, Dst: 0, SrcA: 0, SrcB: 1},
		{Op: OpInc, Dst: 3, SrcA: 3},
	}
	prog := make([]Instruction, 0, 1+len(body))
	prog = append(prog, Instruction{Op: OpLoop, Imm: SecretSize, Aux: uint16(len(body))})
	return append(prog, body...)
}

// compareSteps is the exact step count of CompareProgram: 1 (loop head) +
// SecretSize iterations of a 5-instruction body.
const compareSteps = 1 + SecretSize*5

// Equal reports whether secret and candidate are byte-identical. Length
// validation is public information and completes here, before the
// interpreter enters the secret region. The comparison itself runs the
// fixed-trace CompareProgram, so the observable trace (read addresses and
// branch outcomes) is independent of the secret contents.
func Equal(secret, candidate []byte) bool {
	if len(secret) != SecretSize || len(candidate) != CandidateSize {
		return false
	}
	var vm VM
	copy(vm.Mem[SecretBase:], secret)
	copy(vm.Mem[CandidateBase:], candidate)
	vm.MaxSteps = compareSteps
	if err := vm.Run(CompareProgram()); err != nil {
		return false
	}
	return vm.R[0] == 0
}
