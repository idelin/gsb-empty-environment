package taxcalc

import (
	"math/rand"
	"reflect"
	"testing"
)

func checkInverse(t *testing.T, r Rules, target int64, want []Interval) {
	t.Helper()
	got := r.InverseNet(target)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("InverseNet(%d) = %v, want %v", target, got, want)
	}
}

func TestInverseMonotone(t *testing.T) {
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 1000, Rate: RatePercent(10)},
		{Lower: 1000, Upper: 2000, Rate: RatePercent(20)},
		{Lower: 2000, Upper: Inf, Rate: RatePercent(30)},
	}}
	checkInverse(t, rules, 1300, []Interval{{1500, 1500}})
	checkInverse(t, rules, 0, []Interval{{0, 0}})
	checkInverse(t, rules, 2050, []Interval{{2500, 2500}})
}

func TestInverseRoundingPlateau(t *testing.T) {
	// Rate 1/3: net goes 0,1,1,2,3,3,4,... so most targets come from
	// two-cent gross intervals; net is not strictly increasing.
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: Inf, Rate: Rate{1, 3}},
	}}
	checkInverse(t, rules, 0, []Interval{{0, 0}})
	checkInverse(t, rules, 1, []Interval{{1, 2}})
	checkInverse(t, rules, 2, []Interval{{3, 3}})
	checkInverse(t, rules, 3, []Interval{{4, 5}})
	checkInverse(t, rules, 4, []Interval{{6, 6}})
}

func TestInverseFlatBracket(t *testing.T) {
	// A 100% bracket keeps the net pinned at 100 for its whole width.
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 100, Rate: Rate{0, 1}},
		{Lower: 100, Upper: 200, Rate: Rate{1, 1}},
		{Lower: 200, Upper: Inf, Rate: RatePercent(10)},
	}}
	checkInverse(t, rules, 99, []Interval{{99, 99}})
	checkInverse(t, rules, 100, []Interval{{100, 200}})
	checkInverse(t, rules, 101, []Interval{{201, 201}})
}

func TestInverseDeductionCliff(t *testing.T) {
	// The deduction jumps from 0 to 300 at gross 1000: net jumps from
	// 899 straight to 1000, leaving 900..999 unreachable, and creates a
	// zero-tax segment where net == gross.
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 1000, Rate: RatePercent(10)},
		{Lower: 1000, Upper: Inf, Rate: RatePercent(10), Deduction: 300},
	}}
	checkInverse(t, rules, 899, []Interval{{999, 999}})
	checkInverse(t, rules, 900, nil) // unreachable: jump point
	checkInverse(t, rules, 950, nil) // unreachable: jump point
	checkInverse(t, rules, 999, nil) // unreachable: jump point
	checkInverse(t, rules, 1000, []Interval{{1000, 1000}})
	checkInverse(t, rules, 2000, []Interval{{2000, 2000}}) // zero-tax segment
	checkInverse(t, rules, 3004, []Interval{{3004, 3005}}) // zero-tax end + taxed point
	checkInverse(t, rules, 3005, []Interval{{3006, 3006}})
}

func TestInverseSuperRate(t *testing.T) {
	// A 150% bracket makes the net decrease, so one target can come from
	// two disjoint gross intervals.
	rules := Rules{Brackets: []Bracket{
		{Lower: 0, Upper: 100, Rate: Rate{0, 1}},
		{Lower: 100, Upper: 200, Rate: Rate{3, 2}},
		{Lower: 200, Upper: Inf, Rate: Rate{0, 1}},
	}}
	checkInverse(t, rules, 100, []Interval{{100, 100}, {250, 250}})
	checkInverse(t, rules, 99, []Interval{{99, 99}, {101, 102}, {249, 249}})
	checkInverse(t, rules, 75, []Interval{{75, 75}, {149, 150}, {225, 225}})
	checkInverse(t, rules, 50, []Interval{{50, 50}, {199, 200}})
	checkInverse(t, rules, -50, nil) // net never goes negative here
}

func TestInverseRoundModes(t *testing.T) {
	mk := func(m RoundMode) Rules {
		return Rules{Mode: m, Brackets: []Bracket{
			{Lower: 0, Upper: Inf, Rate: Rate{1, 2}},
		}}
	}
	checkInverse(t, mk(RoundHalfUp), 1, []Interval{{2, 3}})
	checkInverse(t, mk(RoundDown), 1, []Interval{{1, 2}})
	checkInverse(t, mk(RoundDown), 2, []Interval{{3, 4}})
	checkInverse(t, mk(RoundUp), 0, []Interval{{0, 1}})
	checkInverse(t, mk(RoundUp), 1, []Interval{{2, 3}})
}

// netIntervals brute-forces net(gross) for gross in [0, maxGross] and
// groups the gross amounts by net value into intervals.
func netIntervals(r Rules, maxGross int64) map[int64][]Interval {
	m := map[int64][]Interval{}
	last := map[int64]int64{}
	for g := int64(0); g <= maxGross; g++ {
		n := r.Net(g)
		if lg, ok := last[n]; ok && lg == g-1 {
			m[n][len(m[n])-1].Hi = g
		} else {
			m[n] = append(m[n], Interval{Lo: g, Hi: g})
		}
		last[n] = g
	}
	return m
}

func TestInverseBruteForce(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(5)
		var brackets []Bracket
		var lower, widthSum int64
		for i := 0; i < n; i++ {
			width := int64(1 + rng.Intn(300))
			den := int64(1 + rng.Intn(4))
			num := int64(rng.Intn(int(2*den) + 1)) // rate in [0, 2]
			upper := Inf
			if i < n-1 {
				upper = lower + width
				widthSum += width
			} else if num == den {
				num = den - 1 // avoid a flat unbounded top bracket
			}
			brackets = append(brackets, Bracket{
				Lower:     lower,
				Upper:     upper,
				Rate:      Rate{Num: num, Den: den},
				Deduction: int64(rng.Intn(401)) - 100,
			})
			if upper != Inf {
				lower = upper
			}
		}
		rules := Rules{Brackets: brackets, Mode: RoundMode(rng.Intn(3))}
		if err := rules.Validate(); err != nil {
			t.Fatalf("seed %d: generated invalid rules: %v", seed, err)
		}

		maxGross := 20*widthSum + 5000
		brute := netIntervals(rules, maxGross)

		minNet, maxNet := int64(0), int64(0)
		for net := range brute {
			if net < minNet {
				minNet = net
			}
			if net > maxNet {
				maxNet = net
			}
		}
		limit := widthSum + 500 // solutions stay below maxGross in this window
		lo, hi := minNet-100, maxNet+100
		if lo < -limit {
			lo = -limit
		}
		if hi > limit {
			hi = limit
		}
		for target := lo; target <= hi; target++ {
			want := brute[target]
			got := rules.InverseNet(target)
			if len(got) == 0 && len(want) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d target %d: InverseNet = %v, brute = %v (rules %+v)",
					seed, target, got, want, rules)
			}
			for i, iv := range got {
				if iv.Lo > iv.Hi {
					t.Fatalf("seed %d target %d: inverted interval %v", seed, target, iv)
				}
				if i > 0 && got[i-1].Hi != Inf && iv.Lo <= got[i-1].Hi+1 {
					t.Fatalf("seed %d target %d: intervals not disjoint: %v", seed, target, got)
				}
			}
		}
	}
}
