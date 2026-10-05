package taxcalc

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, brackets []Bracket, mode RoundingMode) Schedule {
	t.Helper()
	s, err := New(brackets, mode)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestForwardTaxHandComputed(t *testing.T) {
	s := mustNew(t, []Bracket{
		{Lower: 0, Upper: 100, Rate: Rate{1, 10}},
		{Lower: 100, Upper: 300, Rate: Rate{1, 5}, Ded: 5},
		{Lower: 300, Upper: Inf, Rate: Rate{1, 2}},
	}, RoundHalfUp)
	cases := []struct{ gross, tax, net int64 }{
		{0, 0, 0},
		{50, 5, 45},       // round(50/10)
		{99, 10, 89},      // round(9.9)=10
		{100, 10 - 5, 95}, // ded of bracket 1 kicks in at its start
		{250, 10 + 30 - 5, 215},
		{300, 10 + 40 - 5 + 0, 255},
		{301, 10 + 40 - 5 + 1, 255}, // round(0.5)=1: plateau across boundary
		{302, 10 + 40 - 5 + 1, 256},
	}
	for _, c := range cases {
		if got := s.Tax(c.gross); got != c.tax {
			t.Errorf("Tax(%d) = %d, want %d", c.gross, got, c.tax)
		}
		if got := s.Net(c.gross); got != c.net {
			t.Errorf("Net(%d) = %d, want %d", c.gross, got, c.net)
		}
	}
}

func TestInvertJumpPointUnreachable(t *testing.T) {
	// Deduction of 10 granted on entering the top bracket: net jumps
	// 99 -> 110, so 100..109 are unreachable.
	s := mustNew(t, []Bracket{
		{Lower: 0, Upper: 100, Rate: Rate{0, 1}},
		{Lower: 100, Upper: Inf, Rate: Rate{0, 1}, Ded: 10},
	}, RoundHalfUp)
	if got := s.Net(99); got != 99 {
		t.Fatalf("Net(99) = %d", got)
	}
	if got := s.Net(100); got != 110 {
		t.Fatalf("Net(100) = %d", got)
	}
	if got := s.Invert(105); len(got) != 0 {
		t.Errorf("Invert(105) = %v, want empty (unreachable)", got)
	}
	for _, tg := range []int64{100, 101, 109} {
		if got := s.Invert(tg); len(got) != 0 {
			t.Errorf("Invert(%d) = %v, want empty", tg, got)
		}
	}
	if got := s.Invert(110); !reflect.DeepEqual(got, []Interval{{100, 100}}) {
		t.Errorf("Invert(110) = %v", got)
	}
	if got := s.Invert(99); !reflect.DeepEqual(got, []Interval{{99, 99}}) {
		t.Errorf("Invert(99) = %v", got)
	}
}

func TestInvertZeroRateAndPlateau(t *testing.T) {
	// Zero-tax brackets grow net 1:1; the 100% bracket keeps net flat
	// at 50 across gross 50..150 (boundary merges into one interval).
	s := mustNew(t, []Bracket{
		{Lower: 0, Upper: 50, Rate: Rate{0, 1}},
		{Lower: 50, Upper: 150, Rate: Rate{1, 1}},
		{Lower: 150, Upper: Inf, Rate: Rate{0, 1}},
	}, RoundHalfUp)
	if got := s.Invert(50); !reflect.DeepEqual(got, []Interval{{50, 150}}) {
		t.Errorf("Invert(50) = %v, want [{50 150}]", got)
	}
	if got := s.Invert(49); !reflect.DeepEqual(got, []Interval{{49, 49}}) {
		t.Errorf("Invert(49) = %v", got)
	}
	if got := s.Invert(51); !reflect.DeepEqual(got, []Interval{{151, 151}}) {
		t.Errorf("Invert(51) = %v", got)
	}
}

func TestInvertDecreasingNetMultipleIntervals(t *testing.T) {
	// Rate 200% in the middle bracket makes net decrease, so one net
	// value can have several disjoint gross preimages.
	s := mustNew(t, []Bracket{
		{Lower: 0, Upper: 10, Rate: Rate{0, 1}},
		{Lower: 10, Upper: 20, Rate: Rate{2, 1}},
		{Lower: 20, Upper: Inf, Rate: Rate{0, 1}},
	}, RoundHalfUp)
	if got := s.Invert(1); !reflect.DeepEqual(got, []Interval{{1, 1}, {19, 19}, {21, 21}}) {
		t.Errorf("Invert(1) = %v, want three singletons", got)
	}
	if got := s.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 0}, {20, 20}}) {
		t.Errorf("Invert(0) = %v", got)
	}
	// The zero-rate top bracket (net = gross - 20) mirrors every net
	// value once more, so most targets have a third preimage.
	if got := s.Invert(10); !reflect.DeepEqual(got, []Interval{{10, 10}, {30, 30}}) {
		t.Errorf("Invert(10) = %v", got)
	}
	if got := s.Invert(11); !reflect.DeepEqual(got, []Interval{{31, 31}}) {
		t.Errorf("Invert(11) = %v", got)
	}
	if got := s.Invert(5); !reflect.DeepEqual(got, []Interval{{5, 5}, {15, 15}, {25, 25}}) {
		t.Errorf("Invert(5) = %v", got)
	}
}

func TestInvertNegativeNet(t *testing.T) {
	s := mustNew(t, []Bracket{
		{Lower: 0, Upper: Inf, Rate: Rate{2, 1}},
	}, RoundHalfUp)
	if got := s.Invert(-5); !reflect.DeepEqual(got, []Interval{{5, 5}}) {
		t.Errorf("Invert(-5) = %v", got)
	}
	if got := s.Invert(5); len(got) != 0 {
		t.Errorf("Invert(5) = %v, want empty", got)
	}
	if got := s.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 0}}) {
		t.Errorf("Invert(0) = %v", got)
	}
}

func TestRoundingModes(t *testing.T) {
	brackets := []Bracket{{Lower: 0, Upper: Inf, Rate: Rate{1, 2}}}
	halfUp := mustNew(t, brackets, RoundHalfUp)
	if got := halfUp.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 1}}) {
		t.Errorf("halfUp Invert(0) = %v", got)
	}
	if got := halfUp.Invert(1); !reflect.DeepEqual(got, []Interval{{2, 3}}) {
		t.Errorf("halfUp Invert(1) = %v", got)
	}
	down := mustNew(t, brackets, RoundDown)
	if got := down.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 0}}) {
		t.Errorf("down Invert(0) = %v", got)
	}
	if got := down.Invert(1); !reflect.DeepEqual(got, []Interval{{1, 2}}) {
		t.Errorf("down Invert(1) = %v", got)
	}
	// Half-even: round(0.5)=0, round(1.5)=2, round(2.5)=2.
	halfEven := mustNew(t, brackets, RoundHalfEven)
	if got := halfEven.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 0}}) {
		t.Errorf("halfEven Invert(0) = %v", got)
	}
	if got := halfEven.Invert(1); !reflect.DeepEqual(got, []Interval{{1, 3}}) {
		t.Errorf("halfEven Invert(1) = %v", got)
	}
	if got := halfEven.Invert(3); !reflect.DeepEqual(got, []Interval{{5, 7}}) {
		t.Errorf("halfEven Invert(3) = %v", got)
	}
	// Ceil with rate 1/3: tax(1)=tax(2)=1, tax(3)=1, tax(4)=2.
	up := mustNew(t, []Bracket{{Lower: 0, Upper: Inf, Rate: Rate{1, 3}}}, RoundUp)
	if got := up.Invert(0); !reflect.DeepEqual(got, []Interval{{0, 1}}) {
		t.Errorf("up Invert(0) = %v", got)
	}
	if got := up.Invert(1); !reflect.DeepEqual(got, []Interval{{2, 2}}) {
		t.Errorf("up Invert(1) = %v", got)
	}
}

func TestValidation(t *testing.T) {
	bad := [][]Bracket{
		nil,
		{{Lower: 1, Upper: Inf, Rate: Rate{1, 2}}},                                           // must start at 0
		{{Lower: 0, Upper: 10, Rate: Rate{1, 2}}},                                            // top must be unbounded
		{{Lower: 0, Upper: 10, Rate: Rate{1, 2}}, {Lower: 11, Upper: Inf, Rate: Rate{1, 2}}}, // gap
		{{Lower: 0, Upper: Inf, Rate: Rate{1, 0}}},                                           // zero denominator
		{{Lower: 0, Upper: Inf, Rate: Rate{-1, 2}}},                                          // negative rate
		{{Lower: 0, Upper: 0, Rate: Rate{1, 2}}, {Lower: 0, Upper: Inf, Rate: Rate{1, 2}}},   // empty bracket
	}
	for i, b := range bad {
		if _, err := New(b, RoundHalfUp); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	if _, err := New([]Bracket{{Lower: 0, Upper: Inf, Rate: Rate{1, 2}}}, RoundingMode(99)); err == nil {
		t.Errorf("expected error for bad rounding mode")
	}
}

func contains(ivs []Interval, g int64) bool {
	for _, iv := range ivs {
		if g >= iv.Lo && g <= iv.Hi {
			return true
		}
	}
	return false
}

// TestRandomSchedulesVsBruteForce cross-checks Invert against direct
// evaluation: completeness over a gross range, and soundness of every
// returned interval including its immediate neighbourhood.
func TestRandomSchedulesVsBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	modes := []RoundingMode{RoundHalfUp, RoundHalfEven, RoundDown, RoundUp}
	const trials = 60
	const N = 4000
	for trial := 0; trial < trials; trial++ {
		nBr := 1 + rng.Intn(4)
		br := make([]Bracket, 0, nBr)
		lower := int64(0)
		for i := 0; i < nBr; i++ {
			upper := lower + int64(1+rng.Intn(40))
			if i == nBr-1 {
				upper = Inf
			}
			br = append(br, Bracket{
				Lower: lower,
				Upper: upper,
				Rate:  Rate{Num: int64(rng.Intn(5)), Den: int64(1 + rng.Intn(4))},
				Ded:   int64(rng.Intn(16) - 3),
			})
			lower = upper
		}
		s, err := New(br, modes[rng.Intn(len(modes))])
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}

		// Completeness: every gross in [0, N] must appear in the
		// preimage of its own net. One Invert call per distinct net.
		nets := make([]int64, N+1)
		seen := map[int64][]Interval{}
		for g := int64(0); g <= N; g++ {
			nets[g] = s.Net(g)
			if _, ok := seen[nets[g]]; !ok {
				seen[nets[g]] = s.Invert(nets[g])
			}
		}
		for g := int64(0); g <= N; g++ {
			if !contains(seen[nets[g]], g) {
				t.Fatalf("trial %d: gross %d (net %d) missing from Invert %v", trial, g, nets[g], seen[nets[g]])
			}
		}

		// Soundness: sample targets (observed nets plus off-range and
		// gap values) and verify each returned interval exactly.
		targets := map[int64]bool{-7: true, 0: true, N + 500: true}
		for g := int64(0); g <= N; g += 13 {
			targets[nets[g]] = true
			targets[nets[g]+1] = true
		}
		for tg := range targets {
			for _, iv := range s.Invert(tg) {
				if iv.Lo > iv.Hi || iv.Lo < 0 {
					t.Fatalf("trial %d: bad interval %v for target %d", trial, iv, tg)
				}
				for _, g := range []int64{iv.Lo, iv.Hi, iv.Lo + (iv.Hi-iv.Lo)/2} {
					if got := s.Net(g); got != tg {
						t.Fatalf("trial %d: Net(%d) = %d, want %d (interval %v)", trial, g, got, tg, iv)
					}
				}
				if iv.Lo > 0 {
					if got := s.Net(iv.Lo - 1); got == tg {
						t.Fatalf("trial %d: Net(%d) = %d equals target below interval %v", trial, iv.Lo-1, got, iv)
					}
				}
				if iv.Hi < Inf-1 {
					if got := s.Net(iv.Hi + 1); got == tg {
						t.Fatalf("trial %d: Net(%d) = %d equals target above interval %v", trial, iv.Hi+1, got, iv)
					}
				}
			}
		}
	}
}
