package main

import (
	"fmt"
	"math/big"
)

func defaultEps() *big.Rat { return big.NewRat(1, 1000000000) }

type selfCase struct {
	name  string
	f, g  *piecewise
	check func(rep *report) []string
}

func mkPW(breaks []string, nums, dens [][]string) *piecewise {
	jp := jsonPiecewise{Breaks: breaks}
	for i := range nums {
		jp.Pieces = append(jp.Pieces, jsonRatFunc{Num: nums[i], Den: dens[i]})
	}
	pw, err := jp.toPiecewise()
	if err != nil {
		panic(err)
	}
	return pw
}

func exactPointMults(rep *report) map[string]int {
	m := map[string]int{}
	for _, p := range rep.points {
		if p.rt.exact != nil {
			m[ratStr(p.rt.exact)] = p.rt.mult
		}
	}
	return m
}

// buildSelfCases builds verification cases using the "constructed common
// factor + staggered breakpoints" cross-check method: difference numerators
// are built to contain known factors (so all roots and multiplicities are
// known exactly), and f/g breakpoints are deliberately misaligned to
// exercise the partition refinement.
func buildSelfCases() []selfCase {
	var cases []selfCase

	// Case 1: constructed common factor. f = x, g = N2/(x-4) with
	// N2 chosen so that f - g = (x-1)^2 (x-2) / (x-4). Known results:
	// tangency (mult 2) at x=1, crossing (mult 1) at x=2.
	cases = append(cases, selfCase{
		name: "common-factor construction: tangency x=1, crossing x=2",
		f:    mkPW([]string{"0", "3"}, [][]string{{"0", "1"}}, [][]string{{"1"}}),
		g:    mkPW([]string{"0", "3"}, [][]string{{"2", "-9", "5", "-1"}}, [][]string{{"-4", "1"}}),
		check: func(rep *report) []string {
			var fails []string
			pm := exactPointMults(rep)
			if len(rep.points) != 2 {
				fails = append(fails, fmt.Sprintf("want 2 points, got %d", len(rep.points)))
			}
			if pm["1"] != 2 {
				fails = append(fails, "want exact x=1 with mult 2 (tangency)")
			}
			if pm["2"] != 1 {
				fails = append(fails, "want exact x=2 with mult 1")
			}
			if len(rep.overlaps) != 0 || len(rep.rejected) != 0 {
				fails = append(fails, "want no overlaps and no rejected candidates")
			}
			return fails
		},
	})

	// Case 2: identically zero difference (f = (x+1)(x-2)/(x-2), g = x+1).
	// Must yield one coincident interval, not an infinite root loop; x=2 is
	// excluded because both denominators vanish there.
	cases = append(cases, selfCase{
		name: "identically-zero difference: coincident interval, excluded x=2",
		f:    mkPW([]string{"0", "3"}, [][]string{{"-2", "-1", "1"}}, [][]string{{"-2", "1"}}),
		g:    mkPW([]string{"0", "3"}, [][]string{{"1", "1"}}, [][]string{{"1"}}),
		check: func(rep *report) []string {
			var fails []string
			if len(rep.points) != 0 {
				fails = append(fails, fmt.Sprintf("want 0 points, got %d", len(rep.points)))
			}
			if len(rep.overlaps) != 1 {
				fails = append(fails, fmt.Sprintf("want 1 overlap, got %d", len(rep.overlaps)))
				return fails
			}
			ov := rep.overlaps[0]
			if ratStr(ov.lo) != "0" || ratStr(ov.hi) != "3" || !ov.rightClosed {
				fails = append(fails, "want overlap [0, 3] with closed right end")
			}
			if len(ov.excluded) != 1 || ov.excluded[0].exact == nil || ratStr(ov.excluded[0].exact) != "2" {
				fails = append(fails, "want excluded point x=2 (denominator zero)")
			}
			return fails
		},
	})

	// Case 3: staggered breakpoints (f breaks at 1, g breaks at 2) plus
	// right-continuity: f's left piece is 7 (would give f(1)=7 != g(1)),
	// the right piece 2x-1 gives f(1)=1=g(1), so x=1 must be reported.
	cases = append(cases, selfCase{
		name: "staggered breakpoints + right-continuity at x=1",
		f: mkPW([]string{"0", "1", "3"},
			[][]string{{"7"}, {"-1", "2"}}, [][]string{{"1"}, {"1"}}),
		g: mkPW([]string{"0", "2", "3"},
			[][]string{{"0", "1"}, {"0", "1"}}, [][]string{{"1"}, {"1"}}),
		check: func(rep *report) []string {
			var fails []string
			pm := exactPointMults(rep)
			if len(rep.points) != 1 || pm["1"] != 1 {
				fails = append(fails, "want exactly one point x=1 mult 1 (right-continuous value)")
			}
			if len(rep.overlaps) != 0 {
				fails = append(fails, "want no overlaps")
			}
			return fails
		},
	})

	// Case 4: every piece identical (g's last piece hides a common factor
	// of 2: (2x^2+2)/(2x+2)), staggered breaks. Adjacent coincident
	// segments must merge into a single [0, 3] interval.
	cases = append(cases, selfCase{
		name: "all pieces identical (common factor 2), merged overlap",
		f: mkPW([]string{"0", "1", "3"},
			[][]string{{"1", "0", "1"}, {"1", "0", "1"}},
			[][]string{{"1", "1"}, {"1", "1"}}),
		g: mkPW([]string{"0", "2", "3"},
			[][]string{{"1", "0", "1"}, {"2", "0", "2"}},
			[][]string{{"1", "1"}, {"2", "2"}}),
		check: func(rep *report) []string {
			var fails []string
			if len(rep.points) != 0 {
				fails = append(fails, fmt.Sprintf("want 0 points, got %d", len(rep.points)))
			}
			if len(rep.overlaps) != 1 {
				fails = append(fails, fmt.Sprintf("want 1 merged overlap, got %d", len(rep.overlaps)))
				return fails
			}
			ov := rep.overlaps[0]
			if ratStr(ov.lo) != "0" || ratStr(ov.hi) != "3" || !ov.rightClosed {
				fails = append(fails, "want merged overlap [0, 3] closed right")
			}
			if len(ov.forms) != 1 {
				fails = append(fails, fmt.Sprintf("want 1 reduced common form, got %d", len(ov.forms)))
			}
			if len(ov.excluded) != 0 {
				fails = append(fails, "want no excluded points")
			}
			return fails
		},
	})

	// Case 5: difference numerator (x-1)^2 has a tangency root exactly
	// where both denominators vanish: the candidate must be rejected.
	cases = append(cases, selfCase{
		name: "root at denominator zero is rejected",
		f:    mkPW([]string{"0", "3"}, [][]string{{"0", "1"}}, [][]string{{"-1", "1"}}),
		g:    mkPW([]string{"0", "3"}, [][]string{{"1"}}, [][]string{{"-1", "1"}}),
		check: func(rep *report) []string {
			var fails []string
			if len(rep.points) != 0 || len(rep.overlaps) != 0 {
				fails = append(fails, "want no points and no overlaps")
			}
			if len(rep.rejected) != 1 || rep.rejected[0].rt.exact == nil ||
				ratStr(rep.rejected[0].rt.exact) != "1" {
				fails = append(fails, "want one rejected candidate at x=1")
			}
			return fails
		},
	})

	// Case 6: the only equality is at the right endpoint x=3, which is
	// included separately.
	cases = append(cases, selfCase{
		name: "right endpoint inclusion",
		f:    mkPW([]string{"0", "3"}, [][]string{{"0", "1"}}, [][]string{{"1"}}),
		g:    mkPW([]string{"0", "3"}, [][]string{{"3"}}, [][]string{{"1"}}),
		check: func(rep *report) []string {
			var fails []string
			pm := exactPointMults(rep)
			if len(rep.points) != 1 || pm["3"] != 1 {
				fails = append(fails, "want exactly one point x=3 mult 1 (right endpoint)")
			}
			return fails
		},
	})

	// Case 7: irrational root x = sqrt(2) must be captured by an isolating
	// interval with rational bounds.
	cases = append(cases, selfCase{
		name: "irrational root isolation (x^2 = 2)",
		f:    mkPW([]string{"0", "3"}, [][]string{{"0", "0", "1"}}, [][]string{{"1"}}),
		g:    mkPW([]string{"0", "3"}, [][]string{{"2"}}, [][]string{{"1"}}),
		check: func(rep *report) []string {
			var fails []string
			if len(rep.points) != 1 {
				fails = append(fails, fmt.Sprintf("want 1 point, got %d", len(rep.points)))
				return fails
			}
			rt := rep.points[0].rt
			if rt.exact != nil {
				fails = append(fails, "sqrt(2) should be an isolating interval, not exact")
				return fails
			}
			if rt.mult != 1 {
				fails = append(fails, "want mult 1")
			}
			mid := new(big.Rat).Add(rt.lo, rt.hi)
			mid.Quo(mid, big.NewRat(2, 1))
			d := new(big.Rat).Sub(new(big.Rat).Mul(mid, mid), big.NewRat(2, 1))
			d.Abs(d)
			if d.Cmp(big.NewRat(1, 100000000)) > 0 {
				fails = append(fails, "isolating interval not centered on sqrt(2)")
			}
			return fails
		},
	})

	// Case 8: staggered breakpoints with a partial overlap [1,2) plus an
	// endpoint equality x=3; at x=2 the right-continuous values differ
	// (3 vs 2), so x=2 must NOT appear.
	cases = append(cases, selfCase{
		name: "partial overlap + endpoint equality, staggered breaks",
		f: mkPW([]string{"0", "1", "3"},
			[][]string{{"0", "2"}, {"1", "1"}}, [][]string{{"1"}, {"1"}}),
		g: mkPW([]string{"0", "2", "3"},
			[][]string{{"1", "1"}, {"-2", "2"}}, [][]string{{"1"}, {"1"}}),
		check: func(rep *report) []string {
			var fails []string
			pm := exactPointMults(rep)
			if len(rep.points) != 1 || pm["3"] != 1 {
				fails = append(fails, "want exactly one point x=3 mult 1")
			}
			if len(rep.overlaps) != 1 {
				fails = append(fails, fmt.Sprintf("want 1 overlap, got %d", len(rep.overlaps)))
				return fails
			}
			ov := rep.overlaps[0]
			if ratStr(ov.lo) != "1" || ratStr(ov.hi) != "2" || ov.rightClosed {
				fails = append(fails, "want overlap [1, 2)")
			}
			return fails
		},
	})

	return cases
}

func runSelftests() int {
	cases := buildSelfCases()
	failures := 0
	for _, c := range cases {
		rep, err := analyze(c.f, c.g, defaultEps())
		if err != nil {
			fmt.Printf("FAIL %s\n     analyze error: %v\n", c.name, err)
			failures++
			continue
		}
		if msgs := c.check(rep); len(msgs) > 0 {
			failures++
			fmt.Printf("FAIL %s\n", c.name)
			for _, m := range msgs {
				fmt.Printf("     - %s\n", m)
			}
			fmt.Println(rep.String())
		} else {
			fmt.Printf("PASS %s\n", c.name)
		}
	}
	fmt.Printf("selftest: %d case(s), %d failure(s)\n", len(cases), failures)
	if failures > 0 {
		return 1
	}
	return 0
}
