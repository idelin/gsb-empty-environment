package packetvm

import "math"

// regVal abstracts a register value. Only the packet length (the sole value
// that can justify a read) needs a precise identity; everything else is
// opaque.
type regVal uint8

const (
	valUnknown regVal = iota
	valPacketLen
)

// state is what the verifier knows at a control-flow point.
type state struct {
	// minLen is the lower bound on packet length proven on every path that
	// reaches this point. A read of s bytes at offset o is safe when
	// minLen >= o+s.
	minLen uint32
	r0     regVal
	r1     regVal
}

// join merges two reaching states at a confluence point. The length bound is
// the intersection of what the incoming paths individually prove, i.e. the
// minimum guaranteed length.
func join(a, b state) state {
	out := state{minLen: a.minLen, r0: a.r0, r1: a.r1}
	if b.minLen < out.minLen {
		out.minLen = b.minLen
	}
	if b.r0 != out.r0 {
		out.r0 = valUnknown
	}
	if b.r1 != out.r1 {
		out.r1 = valUnknown
	}
	return out
}

// edgeBound maps a conditional edge to the packet-length lower bound that
// taking that edge establishes. r is the abstract value of the compared
// register and k is the immediate threshold. It returns (bound, ok); ok is
// false when the edge proves nothing useful about the length.
//
// The bounds follow unsigned comparison semantics directly:
//
//	len == k / len >= k        => len >= k
//	len > k                    => len >= k+1
//	len <= k / len < k / != k  => no useful lower bound
func edgeBound(cond CmpOp, r regVal, k uint32, taken bool) (uint32, bool) {
	if r != valPacketLen {
		return 0, false
	}
	if !taken {
		cond = cond.negated()
	}
	switch cond {
	case Eq, Ge:
		return k, true
	case Gt:
		if k == math.MaxUint32 {
			return math.MaxUint32, true
		}
		return k + 1, true
	case Le, Lt, Ne:
		return 0, false
	default:
		return 0, false
	}
}

// verify proves that every reachable packet read is inside a length that was
// established before the read, on every reaching path.
//
// It is a monotone dataflow analysis over the forward control-flow graph:
// minLen only ever grows along an edge and joins take the minimum, so the
// worklist reaches a fixed point in a bounded number of updates.
func verify(insns []Insn) error {
	n := len(insns)
	if n == 0 {
		return &RejectError{Kind: RejectEmptyProgram, Index: -1}
	}
	if n > MaxInstructions {
		return &RejectError{Kind: RejectTooLarge, Index: -1,
			Detail: "instruction count exceeds MaxInstructions"}
	}

	states := make([]*state, n)
	work := []int{0}
	states[0] = &state{minLen: 0, r0: valUnknown, r1: valUnknown}

	for len(work) > 0 {
		pc := work[len(work)-1]
		work = work[:len(work)-1]
		cur := *states[pc]
		in := insns[pc]

		// propagate merges s into the successor at to. It rejects control
		// flow that leaves the program without reaching a RET.
		propagate := func(to int, s state) error {
			if to < 0 || to >= n {
				return &RejectError{
					Kind:   RejectDanglingJump,
					Source: in.Origin,
					Index:  pc,
					Detail: "control flow reaches end of program without RET",
				}
			}
			if states[to] == nil {
				cp := s
				states[to] = &cp
				work = append(work, to)
				return nil
			}
			merged := join(*states[to], s)
			if merged != *states[to] {
				*states[to] = merged
				work = append(work, to)
			}
			return nil
		}

		switch in.Op {
		case OpImm:
			cur.set(in.Dst, valUnknown)
			if err := propagate(pc+1, cur); err != nil {
				return err
			}
		case OpLen:
			cur.set(in.Dst, valPacketLen)
			if err := propagate(pc+1, cur); err != nil {
				return err
			}
		case OpLoad:
			if err := checkLoad(in, cur, pc); err != nil {
				return err
			}
			cur.set(in.Dst, valUnknown)
			if err := propagate(pc+1, cur); err != nil {
				return err
			}
		case OpJmp:
			if err := checkForward(in, pc, n); err != nil {
				return err
			}
			if err := propagate(pc+int(in.Off), cur); err != nil {
				return err
			}
		case OpJmpIf:
			if err := checkForward(in, pc, n); err != nil {
				return err
			}
			k, r := cur.jmpOperands(in)
			target := pc + int(in.Off)
			taken, notTaken := cur, cur
			if bound, ok := edgeBound(in.Cond, r, k, true); ok && bound > taken.minLen {
				taken.minLen = bound
			}
			if bound, ok := edgeBound(in.Cond, r, k, false); ok && bound > notTaken.minLen {
				notTaken.minLen = bound
			}
			if err := propagate(target, taken); err != nil {
				return err
			}
			if err := propagate(pc+1, notTaken); err != nil {
				return err
			}
		case OpRet:
			// Terminal; no successor.
		default:
			return &RejectError{Kind: RejectInvalidAST, Source: in.Origin,
				Index: pc, Detail: "unknown opcode"}
		}
	}
	return nil
}

func (s *state) set(r Reg, v regVal) {
	if r == R1 {
		s.r1 = v
		return
	}
	s.r0 = v
}

// jmpOperands returns the threshold and abstract register used by a JmpIf.
// In register mode the numeric threshold is irrelevant; the length guard is
// always normalized into R0 by the compiler.
func (s *state) jmpOperands(in Insn) (uint32, regVal) {
	if in.UseReg {
		return 0, s.r0
	}
	return in.Imm, s.r0
}

func checkLoad(in Insn, s state, pc int) error {
	if !in.Size.Valid() {
		return &RejectError{Kind: RejectBadOffset, Source: in.Origin, Index: pc,
			Detail: "unsupported load width"}
	}
	end, overflow := addOverflow(in.Offset, uint32(in.Size))
	if overflow {
		return &RejectError{Kind: RejectBadOffset, Source: in.Origin, Index: pc,
			Detail: "offset plus size overflows 32-bit address space"}
	}
	if s.minLen < end {
		return &RejectError{
			Kind:       RejectUnverifiedLoad,
			Source:     in.Origin,
			Index:      pc,
			Need:       end,
			Guaranteed: s.minLen,
		}
	}
	return nil
}

func checkForward(in Insn, pc, n int) error {
	target := pc + int(in.Off)
	if in.Off == 0 || target <= pc {
		return &RejectError{Kind: RejectBackwardJump, Source: in.Origin, Index: pc}
	}
	if target >= n {
		return &RejectError{Kind: RejectDanglingJump, Source: in.Origin, Index: pc}
	}
	return nil
}

func addOverflow(a, b uint32) (uint32, bool) {
	sum := a + b
	return sum, sum < a
}
