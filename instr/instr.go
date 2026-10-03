// Package instr models a structured instruction stream (a tiny VM opcode
// sequence) and runs the CABAC kernel against a context-free static model
// to demonstrate context-adaptive convergence and compression gain.
package instr

import (
	"math/rand"

	"example.com/cabac/cabac"
)

// Opcodes of the tiny instruction set.
const (
	NOP uint8 = iota
	MOV
	ADD
	SUB
	LOAD
	STORE
	JMP
	CALL
)

// NumOpcodes is the instruction set size.
const NumOpcodes = 8

// Names maps opcodes to mnemonics for display.
var Names = [NumOpcodes]string{"NOP", "MOV", "ADD", "SUB", "LOAD", "STORE", "JMP", "CALL"}

// NumContexts is the number of bin positions of the truncated-unary
// binarization of an opcode (values 0..7 need at most 7 bins); each bin
// position gets its own adaptive context, like distinct syntax elements.
const NumContexts = NumOpcodes - 1

// Binarize appends the truncated-unary bins of opcode v to bins:
// value v is v one-bins followed by a terminating zero-bin (v=7 is 7 ones).
func Binarize(v uint8, bins []uint32) []uint32 {
	for i := uint8(0); i < NumContexts; i++ {
		if v == i {
			return append(bins, 0)
		}
		bins = append(bins, 1)
	}
	return bins
}

// Generate builds a structured program of n instructions: NOP runs
// (pipeline padding) interleaved with active blocks of real opcodes. Like
// a real workload settling from an init phase into its steady-state main
// loop, NOP runs become longer and more frequent as the stream progresses,
// so the adaptive contexts visibly converge while a static model cannot.
func Generate(n int, seed int64) []uint8 {
	r := rand.New(rand.NewSource(seed))
	prog := make([]uint8, 0, n)
	for len(prog) < n {
		progress := float64(len(prog)) / float64(n)
		if r.Float64() < 0.5+0.45*progress {
			run := 20 + r.Intn(30+int(200*progress))
			for i := 0; i < run && len(prog) < n; i++ {
				prog = append(prog, NOP)
			}
		} else {
			blk := 5 + r.Intn(25)
			for i := 0; i < blk && len(prog) < n; i++ {
				prog = append(prog, uint8(1+r.Intn(NumOpcodes-1)))
			}
		}
	}
	return prog
}

// Result holds the outcome of one encode/decode experiment.
type Result struct {
	NumInstr     int       // instructions coded
	NumBins      int       // bins after binarization
	CABACBytes   int       // adaptive CABAC stream size
	StaticBytes  int       // context-free static model stream size
	RawBits      int       // fixed-length 3-bit/opcode baseline
	NOPBits      []float64 // bits per NOP per measurement window
	RoundTripOK  bool      // CABAC decode reproduced the program
	StaticOK     bool      // static model decode reproduced the program
	StaticProbM1 float64   // global probability of bin value 1
}

// encodeCABAC binarizes prog and encodes it with one adaptive context per
// bin position, returning the stream and the bit cost of each instruction.
func encodeCABAC(prog []uint8) ([]byte, []uint64) {
	cs := cabac.NewContextSet(NumContexts)
	enc := cabac.NewEncoder()
	cost := make([]uint64, len(prog))
	for i, op := range prog {
		before := enc.Bits()
		bins := Binarize(op, nil)
		for pos, b := range bins {
			enc.EncodeBin(cs.At(pos), b)
		}
		cost[i] = enc.Bits() - before
	}
	return enc.Flush(), cost
}

// decodeCABAC mirrors encodeCABAC and returns the decoded program.
func decodeCABAC(data []byte, n int) []uint8 {
	cs := cabac.NewContextSet(NumContexts)
	dec := cabac.NewDecoder(data)
	prog := make([]uint8, 0, n)
	for len(prog) < n {
		var v uint8
		for pos := 0; pos < NumContexts; pos++ {
			if dec.DecodeBin(cs.At(pos)) == 0 {
				break
			}
			v++
		}
		prog = append(prog, v)
	}
	return prog
}

// encodeStatic codes the same bins with a single context-free static model
// whose probability is the global bin histogram (a generous two-pass
// baseline: the histogram itself is transmitted for free).
func encodeStatic(bins []uint32) []byte {
	var ones int
	for _, b := range bins {
		ones += int(b)
	}
	p1 := float64(ones) / float64(len(bins))
	mps := uint32(0)
	p := 1 - p1
	if p1 > 0.5 {
		mps, p = 1, p1
	}
	m := cabac.NewStaticModel(mps, p)
	enc := cabac.NewEncoder()
	for _, b := range bins {
		enc.EncodeBin(m, b)
	}
	return enc.Flush()
}

// decodeStatic mirrors encodeStatic.
func decodeStatic(data []byte, numBins int, p1 float64) []uint32 {
	mps := uint32(0)
	p := 1 - p1
	if p1 > 0.5 {
		mps, p = 1, p1
	}
	m := cabac.NewStaticModel(mps, p)
	dec := cabac.NewDecoder(data)
	bins := make([]uint32, numBins)
	for i := range bins {
		bins[i] = dec.DecodeBin(m)
	}
	return bins
}

// RunExperiment generates (or takes) a program, CABAC-encodes it, verifies
// the round trip, and benchmarks against the static baseline. window
// controls the NOP-cost convergence measurement granularity.
func RunExperiment(prog []uint8, window int) Result {
	var res Result
	res.NumInstr = len(prog)
	res.RawBits = len(prog) * 3

	var allBins []uint32
	for _, op := range prog {
		allBins = Binarize(op, allBins)
	}
	res.NumBins = len(allBins)

	stream, cost := encodeCABAC(prog)
	res.CABACBytes = len(stream)
	res.RoundTripOK = equal(decodeCABAC(stream, len(prog)), prog)

	staticStream := encodeStatic(allBins)
	res.StaticBytes = len(staticStream)
	var ones int
	for _, b := range allBins {
		ones += int(b)
	}
	res.StaticProbM1 = float64(ones) / float64(len(allBins))
	res.StaticOK = equal32(decodeStatic(staticStream, len(allBins), res.StaticProbM1), allBins)

	// Convergence trace: average bits per NOP over successive windows.
	var acc uint64
	var cnt int
	for i, op := range prog {
		if op != NOP {
			continue
		}
		acc += cost[i]
		cnt++
		if cnt == window {
			res.NOPBits = append(res.NOPBits, float64(acc)/float64(window))
			acc, cnt = 0, 0
		}
	}
	return res
}

func equal(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equal32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
