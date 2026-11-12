package unwind

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"testing"
)

// windowReader authorizes reads only inside fixed memory windows.
type windowReader struct {
	windows map[uint64][]byte // base -> contents
	calls   int
	shortN  int // if > 0, report a short read of this many bytes
	err     error
	onRead  func()
}

func (w *windowReader) ReadAt(addr uint64, buf []byte) (int, error) {
	w.calls++
	if w.onRead != nil {
		w.onRead()
	}
	if w.err != nil {
		return 0, w.err
	}
	if w.shortN > 0 {
		return w.shortN, nil
	}
	for base, data := range w.windows {
		if addr >= base && addr-base+uint64(len(buf)) <= uint64(len(data)) {
			copy(buf, data[addr-base:])
			return len(buf), nil
		}
	}
	return 0, fmt.Errorf("unauthorized address %#x", addr)
}

func window(base uint64, vals ...uint64) (uint64, []byte) {
	data := make([]byte, 0, len(vals)*8)
	for _, v := range vals {
		data = binary.LittleEndian.AppendUint64(data, v)
	}
	return base, data
}

func mustEval(t *testing.T, prog []Instr, regs []uint64, r Reader, lim Limits) (uint64, Stats) {
	t.Helper()
	v, st, err := Eval(prog, regs, r, lim)
	if err != nil {
		t.Fatalf("Eval: unexpected error: %v", err)
	}
	return v, st
}

// mustFail asserts the failure contract: non-nil error of the expected
// kind and a zero result, never a forged complete value.
func mustFail(t *testing.T, want error, prog []Instr, regs []uint64, r Reader, lim Limits) Stats {
	t.Helper()
	v, st, err := Eval(prog, regs, r, lim)
	if err == nil {
		t.Fatalf("Eval: expected error %v, got result %#x", want, v)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Eval: error %v does not match %v", err, want)
	}
	if v != 0 {
		t.Fatalf("Eval: failure returned forged result %#x", v)
	}
	return st
}

func TestFrameAddressComputation(t *testing.T) {
	// CFA = r1 (SP) + 0x20; return address = *(CFA - 8).
	base, data := window(0x5000, 0, 0, 0xdeadbeef)
	r := &windowReader{windows: map[uint64][]byte{base: data}}
	prog := []Instr{
		{Op: OpReg, Imm: 1},
		{Op: OpConst, Imm: 0x20},
		{Op: OpAdd},
		{Op: OpConst, Imm: 8},
		{Op: OpSub},
		{Op: OpDeref},
	}
	v, st := mustEval(t, prog, []uint64{0, 0x4ff8}, r, DefaultLimits())
	if v != 0xdeadbeef {
		t.Fatalf("got %#x, want 0xdeadbeef", v)
	}
	if st.Steps != uint64(len(prog)) || st.ReadBytes != DerefSize {
		t.Fatalf("unexpected billing: %+v", st)
	}
}

func TestInfiniteLoopHitsStepLimit(t *testing.T) {
	// 0: const 1; 1: jmpif 0 — loops forever.
	prog := []Instr{
		{Op: OpConst, Imm: 1},
		{Op: OpJmpIf, Imm: 0},
	}
	r := &windowReader{}
	st := mustFail(t, ErrStepLimit, prog, nil, r, Limits{MaxSteps: 100, MaxReadBytes: 64})
	if st.Steps != 100 {
		t.Fatalf("steps = %d, want exactly the 100-step budget", st.Steps)
	}
	if r.calls != 0 {
		t.Fatalf("loop must not touch memory, got %d reads", r.calls)
	}
}

func TestZeroLimitsDenyAllWork(t *testing.T) {
	prog := []Instr{{Op: OpConst, Imm: 1}}
	mustFail(t, ErrStepLimit, prog, nil, &windowReader{}, Limits{})
}

func TestAddressWraparoundRejected(t *testing.T) {
	// Address near the top of the address space: addr+8 wraps to low
	// memory and must be refused before any read happens.
	prog := []Instr{
		{Op: OpConst, Imm: math.MaxUint64 - 3},
		{Op: OpDeref},
	}
	r := &windowReader{}
	mustFail(t, ErrAddrWrap, prog, nil, r, DefaultLimits())
	if r.calls != 0 {
		t.Fatalf("wrapping address reached the reader %d times", r.calls)
	}
}

func TestArithmeticWrapThenDeref(t *testing.T) {
	// 0 - 8 wraps to a huge address; the read must go to the wrapped
	// address (deterministic uint64 semantics) and be denied by the
	// authorizing reader, not silently clamped.
	prog := []Instr{
		{Op: OpConst, Imm: 0},
		{Op: OpConst, Imm: 8},
		{Op: OpSub},
		{Op: OpDeref},
	}
	r := &windowReader{}
	v, _, err := Eval(prog, nil, r, DefaultLimits())
	if err == nil || v != 0 {
		t.Fatalf("expected failure with zero result, got %#x, %v", v, err)
	}
	if r.calls != 1 {
		t.Fatalf("wrapped address should reach the authorizer exactly once, got %d", r.calls)
	}
}

func TestShortReadIsFatal(t *testing.T) {
	base, data := window(0x1000, 0x42)
	r := &windowReader{windows: map[uint64][]byte{base: data}, shortN: 4}
	prog := []Instr{
		{Op: OpConst, Imm: 0x1000},
		{Op: OpDeref},
	}
	mustFail(t, ErrShortRead, prog, nil, r, DefaultLimits())
}

func TestUnauthorizedAddressIsFatal(t *testing.T) {
	r := &windowReader{} // no windows: everything denied
	prog := []Instr{
		{Op: OpConst, Imm: 0x1000},
		{Op: OpDeref},
	}
	v, _, err := Eval(prog, nil, r, DefaultLimits())
	if err == nil || v != 0 {
		t.Fatalf("expected failure with zero result, got %#x, %v", v, err)
	}
}

func TestReadByteBudget(t *testing.T) {
	base, data := window(0x1000, 1, 2)
	r := &windowReader{windows: map[uint64][]byte{base: data}}
	prog := []Instr{
		{Op: OpConst, Imm: 0x1000},
		{Op: OpDeref},
		{Op: OpConst, Imm: 0x1008},
		{Op: OpDeref},
		{Op: OpAdd},
	}
	// Budget for exactly one dereference: the second must be refused
	// before reaching the reader.
	st := mustFail(t, ErrReadLimit, prog, nil, r, Limits{MaxSteps: 100, MaxReadBytes: DerefSize})
	if st.ReadBytes != DerefSize {
		t.Fatalf("billed bytes = %d, want %d", st.ReadBytes, DerefSize)
	}
	if r.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", r.calls)
	}
}

func TestNestedDerefChainFailure(t *testing.T) {
	// Pointer chain: *0x1000 -> 0x9000, but 0x9000 is unauthorized.
	// The inner success must not leak out as a result.
	base, data := window(0x1000, 0x9000)
	r := &windowReader{windows: map[uint64][]byte{base: data}}
	prog := []Instr{
		{Op: OpConst, Imm: 0x1000},
		{Op: OpDeref},
		{Op: OpDeref},
	}
	v, _, err := Eval(prog, nil, r, DefaultLimits())
	if err == nil {
		t.Fatalf("expected failure, got forged result %#x", v)
	}
	if v != 0 {
		t.Fatalf("failure leaked partial chain value %#x", v)
	}
}

func TestNestedExpressionComposition(t *testing.T) {
	// Host composes two expressions: inner computes an address, outer
	// dereferences it. When the inner expression fails, the host must
	// see the error and must not feed a fabricated address onward.
	badInner := []Instr{{Op: OpAdd}} // underflows immediately
	addr, _, err := Eval(badInner, nil, &windowReader{}, DefaultLimits())
	if err == nil || addr != 0 {
		t.Fatalf("inner expression should fail with zero result, got %#x, %v", addr, err)
	}

	// Successful composition for contrast: inner = 0x1000, outer = *inner.
	inner := []Instr{{Op: OpConst, Imm: 0x1000}}
	addr, _ = mustEval(t, inner, nil, &windowReader{}, DefaultLimits())
	base, data := window(0x1000, 0x77)
	r := &windowReader{windows: map[uint64][]byte{base: data}}
	outer := []Instr{
		{Op: OpConst, Imm: addr},
		{Op: OpDeref},
	}
	v, _ := mustEval(t, outer, nil, r, DefaultLimits())
	if v != 0x77 {
		t.Fatalf("composed evaluation got %#x, want 0x77", v)
	}
}

func TestRegisterSnapshotIsFixed(t *testing.T) {
	// The reader callback mutates the caller's register slice
	// mid-evaluation; results must still reflect the entry snapshot.
	regs := []uint64{5}
	base, data := window(0x2000, 10)
	r := &windowReader{
		windows: map[uint64][]byte{base: data},
		onRead:  func() { regs[0] = 0xbad },
	}
	// (r0 + *0x2000) - r0 == 10 only if both r0 reads see the snapshot.
	prog := []Instr{
		{Op: OpReg, Imm: 0},
		{Op: OpConst, Imm: 0x2000},
		{Op: OpDeref},
		{Op: OpAdd},
		{Op: OpReg, Imm: 0},
		{Op: OpSub},
	}
	v, _ := mustEval(t, prog, regs, r, DefaultLimits())
	if v != 10 {
		t.Fatalf("snapshot violated: got %d, want 10", v)
	}
}

func TestConditionalJumpNotTaken(t *testing.T) {
	prog := []Instr{
		{Op: OpConst, Imm: 0}, // condition: false
		{Op: OpJmpIf, Imm: 3},
		{Op: OpConst, Imm: 111},
		{Op: OpConst, Imm: 222},
	}
	// Falls through: pushes 111 then 222 -> two values left, must fail.
	mustFail(t, ErrExtraResult, prog, nil, &windowReader{}, DefaultLimits())

	prog = []Instr{
		{Op: OpConst, Imm: 1}, // condition: true
		{Op: OpJmpIf, Imm: 3},
		{Op: OpConst, Imm: 111},
		{Op: OpConst, Imm: 222},
	}
	v, _ := mustEval(t, prog, nil, &windowReader{}, DefaultLimits())
	if v != 222 {
		t.Fatalf("got %d, want 222", v)
	}
}

func TestStackAndProgramValidation(t *testing.T) {
	r := &windowReader{}
	lim := DefaultLimits()

	mustFail(t, ErrUnderflow, []Instr{{Op: OpAdd}}, nil, r, lim)
	mustFail(t, ErrNoResult, []Instr{{Op: OpConst, Imm: 0}, {Op: OpJmpIf, Imm: 1}}, nil, r, lim)
	mustFail(t, ErrBadProgram, []Instr{{Op: OpJmpIf, Imm: 7}}, nil, r, lim)
	mustFail(t, ErrBadProgram, []Instr{{Op: Op(0x7f)}}, nil, r, lim)
	mustFail(t, ErrBadProgram, nil, nil, r, lim)
	mustFail(t, ErrBadRegister, []Instr{{Op: OpReg, Imm: 4}}, []uint64{1}, r, lim)
	mustFail(t, ErrNilReader, []Instr{{Op: OpConst, Imm: 1}}, nil, nil, lim)

	// Stack depth cap: MaxStack 1, two pushes.
	deep := []Instr{{Op: OpConst, Imm: 1}, {Op: OpConst, Imm: 2}}
	mustFail(t, ErrStackLimit, deep, nil, r, Limits{MaxSteps: 10, MaxStack: 1})
}

func TestDecode(t *testing.T) {
	blob := []byte{
		byte(OpConst), 0x2a, 0, 0, 0, 0, 0, 0, 0, // const 42
		byte(OpReg), 0x03, // reg 3
		byte(OpAdd),
		byte(OpJmpIf), 0x04, 0x00, // jmpif -> instr 4
		byte(OpDeref),
	}
	prog, err := Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []Instr{
		{Op: OpConst, Imm: 42},
		{Op: OpReg, Imm: 3},
		{Op: OpAdd},
		{Op: OpJmpIf, Imm: 4},
		{Op: OpDeref},
	}
	if len(prog) != len(want) {
		t.Fatalf("decoded %d instructions, want %d", len(prog), len(want))
	}
	for i := range want {
		if prog[i] != want[i] {
			t.Fatalf("instr %d = %+v, want %+v", i, prog[i], want[i])
		}
	}

	for name, bad := range map[string][]byte{
		"empty":           {},
		"unknown opcode":  {0x99},
		"truncated const": {byte(OpConst), 1, 2},
		"truncated reg":   {byte(OpReg)},
		"truncated jmpif": {byte(OpJmpIf), 0x01},
		"jump out of range": {
			byte(OpConst), 1, 0, 0, 0, 0, 0, 0, 0,
			byte(OpJmpIf), 0x09, 0x00,
		},
	} {
		if _, err := Decode(bad); !errors.Is(err, ErrBadProgram) {
			t.Fatalf("Decode %s: got %v, want ErrBadProgram", name, err)
		}
	}
}

func TestDecodedLoopFromUntrustedBytes(t *testing.T) {
	// End-to-end: an untrusted blob encoding an infinite loop is
	// decoded and then stopped by the step meter.
	blob := []byte{
		byte(OpConst), 1, 0, 0, 0, 0, 0, 0, 0,
		byte(OpJmpIf), 0x00, 0x00,
	}
	prog, err := Decode(blob)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	mustFail(t, ErrStepLimit, prog, nil, &windowReader{}, Limits{MaxSteps: 50})
}
