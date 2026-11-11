package seccomp

import (
	"fmt"
	"math"
	"testing"
)

// checkVerdicts compiles the policy and compares the interpreter's verdict
// against the reference semantics for each context, item by item.
func checkVerdicts(t *testing.T, p *Policy, ctxs []Context) []Instruction {
	t.Helper()
	prog, err := Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for i := range ctxs {
		ctx := &ctxs[i]
		got, err := Run(prog, ctx)
		if err != nil {
			t.Errorf("case %d (%+v): Run: %v", i, *ctx, err)
			continue
		}
		want := ActionReject
		if p.Allows(ctx) {
			want = ActionAllow
		}
		if got != want {
			t.Errorf("case %d (%+v): got %#08x, want %#08x", i, *ctx, got, want)
		}
	}
	return prog
}

// around returns v together with its immediate neighbours, clipped at the
// uint64 boundaries.
func around(v uint64) []uint64 {
	out := []uint64{v}
	if v > 0 {
		out = append(out, v-1)
	}
	if v < math.MaxUint64 {
		out = append(out, v+1)
	}
	return out
}

// argContexts builds one context per argument value for a fixed syscall.
func argContexts(arch Arch, nr int, arg int, vals []uint64) []Context {
	ctxs := make([]Context, 0, len(vals))
	for _, v := range vals {
		var c Context
		c.NR = int32(nr)
		c.Arch = uint32(arch)
		c.Args[arg] = v
		ctxs = append(ctxs, c)
	}
	return ctxs
}

func TestMaskedEqual(t *testing.T) {
	p := &Policy{
		Arch: ArchX86_64,
		Rules: []Rule{
			{NR: 1, Constraints: []Constraint{
				MaskedEqual{Arg: 0, Mask: 0xFFFFFFFF00000000, Value: 0xDEADBEEF00000000},
			}},
			{NR: 2, Constraints: []Constraint{
				MaskedEqual{Arg: 1, Mask: 0x00000000FFFFFFFF, Value: 0x0000000000001234},
				MaskedEqual{Arg: 2, Mask: 0xFFFF0000FFFF0000, Value: 0x00AA000000BB0000},
			}},
			{NR: 3}, // no constraints: always matches
		},
	}
	var ctxs []Context
	mk := func(nr int, args ...uint64) Context {
		c := Context{NR: int32(nr), Arch: uint32(ArchX86_64)}
		copy(c.Args[:], args)
		return c
	}
	// Rule 1: high word masked equality.
	ctxs = append(ctxs,
		mk(1, 0xDEADBEEF00000000),
		mk(1, 0xDEADBEEF12345678), // low word unconstrained
		mk(1, 0xDEADBEEE00000000), // off by one in high word
		mk(1, 0x00000000DEADBEEF),
		// Rule 2: two constraints ANDed.
		mk(2, 0, 0x1234, 0x00AA000000BB0000),
		mk(2, 0, 0xFFFFFFFF00001234, 0x00AAFFFF00BBFFFF), // masked bits ignored
		mk(2, 0, 0x1235, 0x00AA000000BB0000),             // low word off by one
		mk(2, 0, 0x1234, 0x00AA000000BB0001),             // masked-out bit differs: match
		mk(2, 0, 0x1234, 0x00AA000100BB0000),             // masked-in bit differs: no match
		mk(2, 0, 0x1234),                                 // missing second constraint value
		// Rule 3: unconstrained.
		mk(3),
		mk(3, math.MaxUint64, math.MaxUint64),
		// Unknown syscall.
		mk(4, 0xDEADBEEF00000000),
	)
	// Wrong architecture is always rejected, even for a matching rule.
	bad := mk(1, 0xDEADBEEF00000000)
	bad.Arch = uint32(ArchAArch64)
	ctxs = append(ctxs, bad)
	checkVerdicts(t, p, ctxs)
}

func TestInRangeCarry(t *testing.T) {
	// Bounds straddle the 32-bit word boundary so the low word carries
	// into the high word.
	p := &Policy{
		Arch: ArchAArch64,
		Rules: []Rule{
			{NR: 10, Constraints: []Constraint{
				InRange{Arg: 0, Min: 0x00000000FFFFFFF0, Max: 0x000000010000000F},
			}},
			{NR: 11, Constraints: []Constraint{
				// Bounds share one high word.
				InRange{Arg: 0, Min: 0x0000000500000010, Max: 0x0000000500000020},
			}},
			{NR: 12, Constraints: []Constraint{
				// Single value.
				InRange{Arg: 0, Min: 0xFFFFFFFFFFFFFFFF, Max: 0xFFFFFFFFFFFFFFFF},
			}},
			{NR: 13, Constraints: []Constraint{
				// Full range: no-op constraint.
				InRange{Arg: 0, Min: 0, Max: math.MaxUint64},
			}},
			{NR: 14, Constraints: []Constraint{
				// High words only; low word unconstrained.
				InRange{Arg: 0, Min: 0x0000000200000000, Max: 0x00000003FFFFFFFF},
			}},
		},
	}
	var ctxs []Context
	add := func(nr int, vals ...uint64) {
		ctxs = append(ctxs, argContexts(ArchAArch64, nr, 0, vals)...)
	}
	var v []uint64
	for _, b := range []uint64{0xFFFFFFF0, 0x10000000F} {
		v = append(v, around(b)...)
	}
	v = append(v, 0, 0xFFFFFFFF, 0x100000000, 0x100000001, 0x1FFFFFFFF, math.MaxUint64)
	add(10, v...)

	v = nil
	for _, b := range []uint64{0x500000010, 0x500000020} {
		v = append(v, around(b)...)
	}
	v = append(v, 0x4FFFFFFFF, 0x600000000)
	add(11, v...)

	add(12, math.MaxUint64, math.MaxUint64-1, 0)
	add(13, 0, 1, math.MaxUint64)
	add(14, 0x1FFFFFFFF, 0x200000000, 0x2FFFFFFFF, 0x300000000, 0x3FFFFFFFF, 0x400000000)
	add(15, 0x200000000) // unknown nr
	checkVerdicts(t, p, ctxs)
}

func TestInRangeExhaustiveBoundaries(t *testing.T) {
	// Sweep boundary values for a range whose low word wraps: Min has a
	// large low word, Max has a small one in the next high word.
	ranges := [][2]uint64{
		{0x00000000FFFFFFFF, 0x0000000100000000},
		{0x00000000FFFFFFFE, 0x0000000100000001},
		{0x0000000100000000, 0x00000001FFFFFFFF},
		{0xFFFFFFFFFFFFFFF0, 0xFFFFFFFFFFFFFFFF},
		{0, 0x00000000FFFFFFFF},
		{0, 0x0000000100000000},
		{0x123456789ABCDEF0, 0x123456789ABCDEF0},
	}
	for i, r := range ranges {
		p := &Policy{
			Arch:  ArchX86_64,
			Rules: []Rule{{NR: 100 + i, Constraints: []Constraint{InRange{Arg: 3, Min: r[0], Max: r[1]}}}},
		}
		var vals []uint64
		for _, b := range r {
			vals = append(vals, around(b)...)
		}
		vals = append(vals, 0, math.MaxUint64, r[0]/2+r[1]/2)
		t.Run(fmt.Sprintf("range_%d", i), func(t *testing.T) {
			checkVerdicts(t, p, argContexts(ArchX86_64, 100+i, 3, vals))
		})
	}
}

func TestManyRulesFarJumps(t *testing.T) {
	// More than 255 rules force dispatch and failure jumps beyond the
	// 8-bit conditional jump span, exercising far trampolines.
	const n = 300
	p := &Policy{Arch: ArchX86_64}
	for i := 0; i < n; i++ {
		p.Rules = append(p.Rules, Rule{
			NR: 1000 + i,
			Constraints: []Constraint{
				MaskedEqual{Arg: 0, Mask: math.MaxUint64, Value: 0xAAAA00000000 + uint64(i)},
			},
		})
	}
	prog, err := Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(prog) > maxProgramLen {
		t.Fatalf("program length %d exceeds %d", len(prog), maxProgramLen)
	}
	far := 0
	for _, in := range prog {
		if in.Code == opJa && in.K > maxCondJump {
			far++
		}
	}
	if far == 0 {
		t.Errorf("expected far jump trampolines in %d-instruction program, found none", len(prog))
	}

	var ctxs []Context
	for i := 0; i < n; i++ {
		good := Context{NR: int32(1000 + i), Arch: uint32(ArchX86_64)}
		good.Args[0] = 0xAAAA00000000 + uint64(i)
		ctxs = append(ctxs, good)

		badArg := good
		badArg.Args[0]++
		ctxs = append(ctxs, badArg)

		badNR := good
		badNR.NR = int32(1000 + (i+1)%n)
		ctxs = append(ctxs, badNR)
	}
	ctxs = append(ctxs, Context{NR: 999, Arch: uint32(ArchX86_64)})
	ctxs = append(ctxs, Context{NR: 1000 + n, Arch: uint32(ArchX86_64)})
	for i := range ctxs {
		ctx := &ctxs[i]
		got, err := Run(prog, ctx)
		if err != nil {
			t.Errorf("case %d (%+v): Run: %v", i, *ctx, err)
			continue
		}
		want := ActionReject
		if p.Allows(ctx) {
			want = ActionAllow
		}
		if got != want {
			t.Errorf("case %d (%+v): got %#08x, want %#08x", i, *ctx, got, want)
		}
	}
}

func TestDuplicateNumbers(t *testing.T) {
	// Several rules share a syscall number; each is tried in order.
	p := &Policy{
		Arch: ArchARM,
		Rules: []Rule{
			{NR: 5, Constraints: []Constraint{InRange{Arg: 0, Min: 0, Max: 10}}},
			{NR: 5, Constraints: []Constraint{InRange{Arg: 0, Min: 20, Max: 30}}},
			{NR: 5, Constraints: []Constraint{MaskedEqual{Arg: 0, Mask: math.MaxUint64, Value: 99}}},
		},
	}
	ctxs := argContexts(ArchARM, 5, 0, []uint64{0, 5, 10, 11, 19, 20, 30, 31, 98, 99, 100})
	checkVerdicts(t, p, ctxs)
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		policy *Policy
	}{
		{"unknown arch", &Policy{Arch: 0xDEADBEEF}},
		{"zero arch", &Policy{Arch: 0}},
		{"negative nr", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: -1}}}},
		{"nr too large", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: math.MaxInt32 + 1}}}},
		{"arg index", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: 1,
			Constraints: []Constraint{MaskedEqual{Arg: 6, Mask: 1}}}}}},
		{"negative arg index", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: 1,
			Constraints: []Constraint{InRange{Arg: -1, Min: 0, Max: 1}}}}}},
		{"value outside mask", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: 1,
			Constraints: []Constraint{MaskedEqual{Arg: 0, Mask: 0xFF00, Value: 0x00FF}}}}}},
		{"empty range", &Policy{Arch: ArchX86_64, Rules: []Rule{{NR: 1,
			Constraints: []Constraint{InRange{Arg: 0, Min: 2, Max: 1}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile(tc.policy); err == nil {
				t.Errorf("Compile(%+v): expected error, got nil", tc.policy)
			}
		})
	}
}

func TestKnownArchitectures(t *testing.T) {
	for _, arch := range []Arch{ArchX86, ArchX86_64, ArchARM, ArchAArch64} {
		p := &Policy{Arch: arch, Rules: []Rule{{NR: 1}}}
		prog, err := Compile(p)
		if err != nil {
			t.Errorf("Compile(arch %#x): %v", uint32(arch), err)
			continue
		}
		ok := Context{NR: 1, Arch: uint32(arch)}
		if got, _ := Run(prog, &ok); got != ActionAllow {
			t.Errorf("arch %#x: matching context rejected", uint32(arch))
		}
		other := Context{NR: 1, Arch: uint32(ArchX86_64)}
		if arch == ArchX86_64 {
			other.Arch = uint32(ArchAArch64)
		}
		if got, _ := Run(prog, &other); got != ActionReject {
			t.Errorf("arch %#x: foreign arch context allowed", uint32(arch))
		}
	}
}
