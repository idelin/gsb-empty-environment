package main

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
)

// This file implements the auxiliary semantic cross-check: both
// sequences are executed from a finite set of initial states and their
// final states compared. A divergence is a hard counterexample to
// legality. Agreement is NEVER taken as proof of legality -- distinct
// programs may coincide on every sampled state yet still differ.

const (
	maxSimCells     = 1 << 16 // cap on tracked cells; beyond this, skip
	exhaustiveLimit = 1 << 20 // max states enumerated exhaustively
	sampleCount     = 1 << 20 // random states used when not exhaustive
)

// layout assigns a dense index to every register and every memory byte
// touched by either program.
type layout struct {
	regs   []string       // sorted register names
	regIdx map[string]int //
	mem    []uint64       // sorted memory byte addresses
	memIdx map[uint64]int //
}

func buildLayout(progs ...[]*Instr) *layout {
	l := &layout{regIdx: map[string]int{}, memIdx: map[uint64]int{}}
	regSet := map[string]bool{}
	memSet := map[uint64]bool{}
	for _, prog := range progs {
		for _, in := range prog {
			for _, r := range append(append([]Ref{}, in.Reads...), in.Writes...) {
				if r.IsMem {
					for a := r.Lo; a < r.Hi; a++ {
						memSet[a] = true
					}
				} else {
					regSet[r.Name] = true
				}
			}
		}
	}
	for name := range regSet {
		l.regs = append(l.regs, name)
	}
	sort.Strings(l.regs)
	for i, name := range l.regs {
		l.regIdx[name] = i
	}
	for a := range memSet {
		l.mem = append(l.mem, a)
	}
	sort.Slice(l.mem, func(i, j int) bool { return l.mem[i] < l.mem[j] })
	for i, a := range l.mem {
		l.memIdx[a] = i
	}
	return l
}

func (l *layout) cells() int { return len(l.regs) + len(l.mem) }

// cinstr is an instruction compiled against a layout.
type cinstr struct {
	op     OpKind
	alu    string
	dst    int // register index
	a, b   int // register indices
	imm    uint64
	memIdx []int // memory byte indices, little-endian order
}

func compile(prog []*Instr, l *layout) []cinstr {
	out := make([]cinstr, 0, len(prog))
	for _, in := range prog {
		ci := cinstr{op: in.Op, alu: in.AluOp, imm: in.Imm}
		switch in.Op {
		case OpConst:
			ci.dst = l.regIdx[in.Dst]
		case OpAlu:
			ci.dst = l.regIdx[in.Dst]
			ci.a = l.regIdx[in.A]
			ci.b = l.regIdx[in.B]
		case OpLoad:
			ci.dst = l.regIdx[in.Dst]
			for a := in.Lo; a < in.Hi; a++ {
				ci.memIdx = append(ci.memIdx, l.memIdx[a])
			}
		case OpStore:
			ci.a = l.regIdx[in.A]
			for a := in.Lo; a < in.Hi; a++ {
				ci.memIdx = append(ci.memIdx, l.memIdx[a])
			}
		}
		out = append(out, ci)
	}
	return out
}

func aluEval(op string, x, y uint64) uint64 {
	switch op {
	case "add":
		return x + y
	case "sub":
		return x - y
	case "mul":
		return x * y
	case "xor":
		return x ^ y
	case "and":
		return x & y
	case "or":
		return x | y
	}
	return 0
}

func exec(prog []cinstr, regs []uint64, mem []byte) {
	for _, ci := range prog {
		switch ci.op {
		case OpConst:
			regs[ci.dst] = ci.imm
		case OpAlu:
			regs[ci.dst] = aluEval(ci.alu, regs[ci.a], regs[ci.b])
		case OpLoad:
			var v uint64
			for k, mi := range ci.memIdx {
				v |= uint64(mem[mi]) << (8 * uint(k))
			}
			regs[ci.dst] = v
		case OpStore:
			v := regs[ci.a]
			for k, mi := range ci.memIdx {
				mem[mi] = byte(v >> (8 * uint(k)))
			}
		case OpFence:
		}
	}
}

// Mismatch describes one initial state on which the two sequences
// produce different final states.
type Mismatch struct {
	Init    string // compact description of the initial state
	Cell    string // first differing cell
	OrigVal uint64
	CandVal uint64
}

// SimResult summarizes the auxiliary cross-check.
type SimResult struct {
	Skipped  bool   // state space too large to track
	Mode     string // "exhaustive" or "sampled"
	States   uint64 // states actually executed
	Mismatch *Mismatch
}

// CrossCheck executes both sequences over finite initial states and
// reports the first diverging state, if any.
func CrossCheck(orig, cand []*Instr) SimResult {
	l := buildLayout(orig, cand)
	n := l.cells()
	if n > maxSimCells {
		return SimResult{Skipped: true}
	}
	po := compile(orig, l)
	pc := compile(cand, l)

	domain := []uint64{0, 1, 0xFF}
	states := uint64(1)
	for i := 0; i < n; i++ {
		states *= uint64(len(domain))
	}
	exhaustive := states <= exhaustiveLimit
	if !exhaustive {
		domain = []uint64{0, 1}
		states = 1
		for i := 0; i < n; i++ {
			states *= 2
		}
		exhaustive = states <= exhaustiveLimit
	}

	res := SimResult{Mode: "sampled"}
	if exhaustive {
		res.Mode = "exhaustive"
	} else {
		states = sampleCount
	}

	initRegs := make([]uint64, len(l.regs))
	initMem := make([]byte, len(l.mem))
	regsA := make([]uint64, len(l.regs))
	regsB := make([]uint64, len(l.regs))
	memA := make([]byte, len(l.mem))
	memB := make([]byte, len(l.mem))

	rng := rand.New(rand.NewSource(1))
	counter := make([]int, n) // odometer for exhaustive enumeration

	for s := uint64(0); s < states; s++ {
		if exhaustive {
			for i := 0; i < n; i++ {
				v := domain[counter[i]]
				if i < len(l.regs) {
					initRegs[i] = v
				} else {
					initMem[i-len(l.regs)] = byte(v)
				}
			}
			// increment odometer
			for i := 0; i < n; i++ {
				counter[i]++
				if counter[i] < len(domain) {
					break
				}
				counter[i] = 0
			}
		} else {
			for i := range initRegs {
				initRegs[i] = domain[rng.Intn(len(domain))]
			}
			for i := range initMem {
				initMem[i] = byte(domain[rng.Intn(len(domain))])
			}
		}

		copy(regsA, initRegs)
		copy(regsB, initRegs)
		copy(memA, initMem)
		copy(memB, initMem)
		exec(po, regsA, memA)
		exec(pc, regsB, memB)
		res.States++

		if m := compareStates(l, regsA, memA, regsB, memB, initRegs, initMem); m != nil {
			res.Mismatch = m
			return res
		}
	}
	return res
}

func compareStates(l *layout, regsA []uint64, memA []byte, regsB []uint64, memB []byte, initRegs []uint64, initMem []byte) *Mismatch {
	for i, name := range l.regs {
		if regsA[i] != regsB[i] {
			return &Mismatch{
				Init:    describeInit(l, initRegs, initMem),
				Cell:    name,
				OrigVal: regsA[i],
				CandVal: regsB[i],
			}
		}
	}
	for i, a := range l.mem {
		if memA[i] != memB[i] {
			return &Mismatch{
				Init:    describeInit(l, initRegs, initMem),
				Cell:    fmt.Sprintf("[%d]", a),
				OrigVal: uint64(memA[i]),
				CandVal: uint64(memB[i]),
			}
		}
	}
	return nil
}

func describeInit(l *layout, initRegs []uint64, initMem []byte) string {
	var parts []string
	for i, name := range l.regs {
		if initRegs[i] != 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", name, initRegs[i]))
		}
	}
	for i, a := range l.mem {
		if initMem[i] != 0 {
			parts = append(parts, fmt.Sprintf("[%d]=%d", a, initMem[i]))
		}
	}
	if len(parts) == 0 {
		return "all zero"
	}
	return strings.Join(parts, " ")
}
