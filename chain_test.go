package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestLaterAllowCannotOverrideEarlierDeny(t *testing.T) {
	older := Filter{
		Rules: []Rule{
			DenyRule(1, 13), // EACCES
			AllowRule(2),
		},
		Default: ActionAllow,
	}
	newer := Filter{
		Rules: []Rule{
			AllowRule(1),
			AllowRule(2),
			AllowRule(3),
		},
		Default: ActionAllow,
	}
	chain, err := NewChain(older, newer)
	if err != nil {
		t.Fatal(err)
	}

	var args [6]uint64
	if got := chain.Evaluate(1, args); got != (Decision{Action: ActionDeny, Data: 13}) {
		t.Fatalf("later allow overrode deny: %s", got)
	}
	if got := chain.Evaluate(2, args); got != (Decision{Action: ActionAllow, Data: 0}) {
		t.Fatalf("equal allow ties must use newest data: %s", got)
	}
	if got := chain.Evaluate(3, args); got != (Decision{Action: ActionAllow, Data: 0}) {
		t.Fatalf("new syscall allow: %s", got)
	}
}

func TestAppendOnlyNarrowsExhaustive(t *testing.T) {
	domains := [6][]uint64{
		{0, 1, 2, 4, math.MaxUint64 - 1},
		{0, 1, 2, 4, math.MaxUint64},
		{0, 1, 2, 3, 8},
		{0, 1, 2, 3, 8},
		{0, 1, 2, 3, 8},
		{0, 1, 2, 3, math.MaxUint64},
	}
	syscalls := []uint32{0, 1, 2, 3, 4, 5, 6, 7}

	baseA := Filter{
		Rules: []Rule{
			DenyRule(1, 13, ArgEquals(0, 1)),
			NotifyRule(2, 7, ArgAllBits(1, 2)),
			TerminateRule(3, 9, ArgMasked(2, 0x3, 0x2)),
			AllowRule(4),
		},
		Default: ActionAllow,
	}
	extraA := Filter{
		Rules: []Rule{
			AllowRule(1),
			DenyRule(1, 22, ArgPredicate{Index: 0, Op: ArgGreaterEqual, Value: 8}),
			DenyRule(2, 1, ArgNoBits(1, 2)),
			TerminateRule(4, 6),
			NotifyRule(5, 3, ArgNotEquals(5, 0)),
		},
		Default: ActionAllow,
	}

	baseB := Filter{
		Rules: []Rule{
			AllowRule(1),
			AllowRule(2, ArgEquals(0, 2)),
			AllowRule(3),
		},
		Default:     ActionDeny,
		DefaultData: 1,
	}
	extraB := Filter{
		Rules: []Rule{
			DenyRule(1, 13),
			AllowRule(2, ArgEquals(0, 2)),
			NotifyRule(3, 1, ArgAllBits(0, 1)),
		},
		Default: ActionAllow,
	}

	cases := []struct {
		name   string
		before []Filter
		extra  Filter
	}{
		{"empty-base", nil, extraA},
		{"allow-base", []Filter{baseA}, extraA},
		{"deny-default-base", []Filter{baseB}, extraB},
		{"two-filter-base", []Filter{baseA, baseB}, extraA},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, err := NewChain(tc.before...)
			if err != nil {
				t.Fatal(err)
			}
			after, err := before.Append(tc.extra)
			if err != nil {
				t.Fatal(err)
			}

			var checks int
			Enumerate(syscalls, domains, func(point Point) bool {
				checks++
				oldDecision := before.EvaluatePoint(point)
				newFilterDecision := tc.extra.Evaluate(point.Syscall, point.Args)
				got := after.EvaluatePoint(point)
				want := ComposeDecisions(oldDecision, newFilterDecision)
				if got != want {
					t.Errorf(
						"syscall=%d args=%v: got %s, want composition of %s and %s",
						point.Syscall, point.Args, got, oldDecision, newFilterDecision,
					)
					return false
				}
				if got.Action < oldDecision.Action {
					t.Errorf(
						"syscall=%d args=%v: append widened %s -> %s",
						point.Syscall, point.Args, oldDecision, got,
					)
					return false
				}
				return true
			})
			if checks == 0 {
				t.Fatal("empty verification domain")
			}

			if err := VerifyNarrowing(before, after, syscalls, domains); err != nil {
				t.Fatalf("VerifyNarrowing: %v", err)
			}
		})
	}
}

func TestThreeFilterTieUsesNewestData(t *testing.T) {
	chain, err := NewChain(
		Filter{Rules: []Rule{DenyRule(1, 1)}},
		Filter{Rules: []Rule{DenyRule(1, 2)}},
		Filter{Rules: []Rule{DenyRule(1, 3)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	var args [6]uint64
	if got := chain.Evaluate(1, args); got != (Decision{Action: ActionDeny, Data: 3}) {
		t.Fatalf("newest same-priority data not used: %s", got)
	}
}

func TestAppendIsImmutable(t *testing.T) {
	base := Filter{Rules: []Rule{DenyRule(1, 13)}}
	chain := MustNewChain(base)
	before := chain.Evaluate(1, [6]uint64{})

	next, err := chain.Append(Filter{Rules: []Rule{AllowRule(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := chain.Evaluate(1, [6]uint64{}); got != before {
		t.Fatalf("Append mutated receiver: %s", got)
	}
	if next.Len() != 2 {
		t.Fatalf("new chain length = %d", next.Len())
	}
}

func TestAppendZeroFiltersReturnsSameChain(t *testing.T) {
	chain := MustNewChain(Filter{Rules: []Rule{DenyRule(1, 1)}})
	next, err := chain.Append()
	if err != nil {
		t.Fatal(err)
	}
	if next != chain {
		t.Fatal("empty append should preserve the immutable chain")
	}
}

func TestVerifyNarrowingDetectsWidening(t *testing.T) {
	deny, err := NewChain(Filter{Default: ActionDeny, DefaultData: 1})
	if err != nil {
		t.Fatal(err)
	}
	allow, err := NewChain(Filter{Default: ActionAllow})
	if err != nil {
		t.Fatal(err)
	}
	err = VerifyNarrowing(deny, allow, []uint32{1}, [6][]uint64{{0}})
	if err == nil {
		t.Fatal("widening was not detected")
	}
	var narrowing *NarrowingError
	if !errors.As(err, &narrowing) {
		t.Fatalf("wrong error type: %T", err)
	}
	if narrowing.Before.Action != ActionDeny || narrowing.After.Action != ActionAllow {
		t.Fatalf("wrong error details: %v", err)
	}
}

func TestInvalidAppendDoesNotChangeChain(t *testing.T) {
	chain := MustNewChain(Filter{Rules: []Rule{DenyRule(1, 13)}})
	_, err := chain.Append(Filter{Rules: []Rule{{Syscall: 1, Action: Action(99)}}})
	if !errors.Is(err, ErrInvalidFilter) || !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid append error = %v", err)
	}
	if chain.Len() != 1 {
		t.Fatalf("receiver changed: len=%d", chain.Len())
	}
}

func TestEnumerateCartesianProductAndStop(t *testing.T) {
	domains := [6][]uint64{{1, 2}, {3}, nil, {5}, nil, {7, 8, 9}}
	var seen []Point
	Enumerate([]uint32{10, 11}, domains, func(point Point) bool {
		seen = append(seen, point)
		return true
	})
	if want := 2 * 2 * 1 * 1 * 1 * 1 * 3; len(seen) != want {
		t.Fatalf("enumerated %d points, want %d", len(seen), want)
	}
	if seen[0] != (Point{Syscall: 10, Args: [6]uint64{1, 3, 0, 5, 0, 7}}) {
		t.Fatalf("first point = %+v", seen[0])
	}

	seen = nil
	Enumerate([]uint32{10, 11}, domains, func(point Point) bool {
		seen = append(seen, point)
		return false
	})
	if len(seen) != 1 {
		t.Fatalf("stop produced %d points", len(seen))
	}
}
