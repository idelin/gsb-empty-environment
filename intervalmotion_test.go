package intervalmotion

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func intervalsEqual(got []TimeInterval, want [][2]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i, w := range want {
		if got[i].Lo.Cmp(rat(w[0])) != 0 || got[i].Hi.Cmp(rat(w[1])) != 0 {
			return false
		}
	}
	return true
}

func TestIntersectionTimes(t *testing.T) {
	cases := []struct {
		name string
		a, b MovingInterval
		T    *big.Rat
		want [][2]string
	}{
		{
			name: "static overlapping",
			a:    NewMovingInterval(0, 0, 10, 0),
			b:    NewMovingInterval(5, 0, 15, 0),
			T:    big.NewRat(10, 1),
			want: [][2]string{{"0", "10"}},
		},
		{
			name: "static disjoint",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(2, 0, 3, 0),
			T:    big.NewRat(10, 1),
			want: nil,
		},
		{
			name: "approach then separate",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(5, -1, 6, -1),
			T:    big.NewRat(10, 1),
			want: [][2]string{{"4", "6"}},
		},
		{
			name: "instantaneous touch",
			a:    NewMovingInterval(0, 1, 0, 1),
			b:    NewMovingInterval(4, -1, 4, -1),
			T:    big.NewRat(4, 1),
			want: [][2]string{{"2", "2"}},
		},
		{
			name: "touch exactly at T",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(3, -1, 3, -1),
			T:    big.NewRat(2, 1),
			want: [][2]string{{"2", "2"}},
		},
		{
			name: "two disjoint intervals via endpoint flip",
			a:    NewMovingInterval(0, 0, 0, 0),
			b:    NewMovingInterval(-2, 1, 4, -1),
			T:    big.NewRat(10, 1),
			want: [][2]string{{"0", "2"}, {"4", "10"}},
		},
		{
			name: "endpoint crossing splits intersection",
			a:    NewMovingInterval(0, 1, 10, -1),
			b:    NewMovingInterval(7, 0, 9, 0),
			T:    big.NewRat(10, 1),
			want: [][2]string{{"0", "3"}, {"7", "10"}},
		},
		{
			name: "rational T clips result",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(5, -1, 6, -1),
			T:    rat("9/2"),
			want: [][2]string{{"4", "9/2"}},
		},
		{
			name: "rational crossing time",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(3, -2, 4, -2),
			T:    big.NewRat(5, 1),
			want: [][2]string{{"1", "2"}},
		},
		{
			name: "negative T",
			a:    NewMovingInterval(0, 0, 1, 0),
			b:    NewMovingInterval(0, 0, 1, 0),
			T:    big.NewRat(-1, 1),
			want: nil,
		},
		{
			name: "zero T single point",
			a:    NewMovingInterval(0, 1, 2, 1),
			b:    NewMovingInterval(1, 0, 5, 0),
			T:    big.NewRat(0, 1),
			want: [][2]string{{"0", "0"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IntersectionTimes(c.a, c.b, c.T)
			if !intervalsEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			if c.T.Sign() >= 0 {
				if err := VerifyIntersectionTimes(c.a, c.b, c.T, got, 4); err != nil {
					t.Fatalf("verification failed: %v", err)
				}
			}
		})
	}
}

func TestAllPairsIntersectionTimes(t *testing.T) {
	as := []MovingInterval{
		NewMovingInterval(0, 0, 10, 0),
		NewMovingInterval(20, -1, 25, -1),
	}
	bs := []MovingInterval{
		NewMovingInterval(5, 0, 15, 0),
		NewMovingInterval(0, 1, 2, 1),
	}
	T := big.NewRat(30, 1)
	res := AllPairsIntersectionTimes(as, bs, T)
	if len(res) != 4 {
		t.Fatalf("expected 4 pair results, got %d", len(res))
	}
	want := map[[2]int][][2]string{
		{0, 0}: {{"0", "30"}},
		{0, 1}: {{"0", "10"}},
		{1, 0}: {{"5", "20"}},
		{1, 1}: {{"9", "25/2"}},
	}
	for _, r := range res {
		w, ok := want[[2]int{r.I, r.J}]
		if !ok {
			t.Fatalf("unexpected pair (%d, %d)", r.I, r.J)
		}
		if !intervalsEqual(r.Times, w) {
			t.Fatalf("pair (%d, %d): got %v, want %v", r.I, r.J, r.Times, w)
		}
		if err := VerifyIntersectionTimes(as[r.I], bs[r.J], T, r.Times, 4); err != nil {
			t.Fatalf("pair (%d, %d) verification failed: %v", r.I, r.J, err)
		}
	}
}

func TestVerifyIntersectionTimesRejectsBadOutput(t *testing.T) {
	a := NewMovingInterval(0, 0, 1, 0)
	b := NewMovingInterval(5, -1, 6, -1)
	T := big.NewRat(10, 1)

	bad := [][]TimeInterval{
		{{rat("4"), rat("7")}},                       // too wide: gap check must fire
		{{rat("5"), rat("6")}},                       // too narrow: missed region check must fire
		{{rat("4"), rat("6")}, {rat("6"), rat("8")}}, // touching, unmerged
		{{rat("4"), rat("3")}},                       // Lo > Hi
		{{rat("-1"), rat("6")}},                      // outside [0, T]
	}
	for i, g := range bad {
		if err := VerifyIntersectionTimes(a, b, T, g, 4); err == nil {
			t.Fatalf("case %d: expected verification error for %v", i, g)
		}
	}
	good := IntersectionTimes(a, b, T)
	if err := VerifyIntersectionTimes(a, b, T, good, 4); err != nil {
		t.Fatalf("unexpected verification error: %v", err)
	}
}
