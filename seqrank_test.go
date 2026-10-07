package seqrank

import (
	"bytes"
	"encoding/gob"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naive is an independent reference implementation: it enumerates label
// assignments by recursion and recomputes average ranks per prefix with
// exact rational arithmetic.
func naive(cfg *Config) (cross map[int]int64, crossed, total int64) {
	n := len(cfg.Values)
	cross = make(map[int]int64)
	labels := make([]bool, n)
	var rec func(pos, left int)
	rec = func(pos, left int) {
		if pos == n {
			if left != 0 {
				return
			}
			total++
			if look, ok := naiveFirstCross(cfg, labels); ok {
				crossed++
				cross[look]++
			}
			return
		}
		if left < n-pos {
			labels[pos] = false
			rec(pos+1, left)
		}
		if left > 0 {
			labels[pos] = true
			rec(pos+1, left-1)
		}
	}
	rec(0, cfg.Treat)
	return cross, crossed, total
}

func naiveFirstCross(cfg *Config, labels []bool) (int, bool) {
	for _, k := range cfg.Looks {
		var m int
		for i := 0; i < k; i++ {
			if labels[i] {
				m++
			}
		}
		if m == 0 || m == k {
			continue // empty group in prefix: no look
		}
		w := new(big.Rat)
		for i := 0; i < k; i++ {
			if !labels[i] {
				continue
			}
			var less, eq int64
			for j := 0; j < k; j++ {
				if cfg.Values[j] < cfg.Values[i] {
					less++
				} else if cfg.Values[j] == cfg.Values[i] {
					eq++
				}
			}
			// rank = 1 + less + (eq-1)/2
			w.Add(w, big.NewRat(2+2*less+eq-1, 2))
		}
		e := big.NewRat(int64(m)*int64(k+1), 2)
		d := new(big.Rat).Sub(w, e)
		d.Abs(d)
		stat := new(big.Rat).Quo(d, cfg.Norm)
		if stat.Cmp(cfg.Threshold) >= 0 { // equality counts
			return k, true
		}
	}
	return 0, false
}

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

func stateMaps(s *State) (map[int]string, string, string) {
	m := make(map[int]string, len(s.Cross))
	for k, v := range s.Cross {
		m[k] = v.String()
	}
	return m, s.Crossed.String(), s.Total.String()
}

func TestAgainstNaiveRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	thresholds := []*big.Rat{rat(0, 1), rat(1, 4), rat(1, 2), rat(3, 4), rat(1, 1), rat(2, 1), rat(9, 10)}
	norms := []*big.Rat{rat(1, 1), rat(1, 2), rat(2, 3), rat(3, 2), rat(5, 1)}
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(8) // 2..9
		cfg := &Config{
			Treat:     rng.Intn(n + 1), // includes 0 and n
			Threshold: thresholds[rng.Intn(len(thresholds))],
			Norm:      norms[rng.Intn(len(norms))],
		}
		for i := 0; i < n; i++ {
			cfg.Values = append(cfg.Values, float64(rng.Intn(4))) // heavy ties
		}
		seen := map[int]bool{}
		for len(cfg.Looks) < 1+rng.Intn(n) {
			k := 1 + rng.Intn(n)
			if !seen[k] {
				seen[k] = true
				cfg.Looks = append(cfg.Looks, k)
			}
		}
		sort.Ints(cfg.Looks)

		s, err := Run(cfg)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if !s.Complete {
			t.Fatalf("trial %d: incomplete after full Run", trial)
		}
		wantCross, wantCrossed, wantTotal := naive(cfg)
		gotCross, gotCrossed, gotTotal := stateMaps(s)
		if gotTotal != big.NewInt(wantTotal).String() {
			t.Fatalf("trial %d cfg=%+v: total %s, want %d", trial, cfg, gotTotal, wantTotal)
		}
		if gotCrossed != big.NewInt(wantCrossed).String() {
			t.Errorf("trial %d cfg=%+v: crossed %s, want %d", trial, cfg, gotCrossed, wantCrossed)
		}
		wantMap := make(map[int]string, len(wantCross))
		for k, v := range wantCross {
			wantMap[k] = big.NewInt(v).String()
		}
		if !reflect.DeepEqual(gotCross, wantMap) {
			t.Errorf("trial %d cfg=%+v: first-cross %v, want %v", trial, cfg, gotCross, wantMap)
		}
		// Probabilities must sum to 1.
		first, never, ok := s.Distribution()
		if !ok {
			t.Fatalf("trial %d: distribution unavailable on complete state", trial)
		}
		sum := new(big.Rat).Set(never)
		for _, p := range first {
			sum.Add(sum, p)
		}
		if sum.Cmp(rat(1, 1)) != 0 {
			t.Errorf("trial %d: probabilities sum to %s", trial, sum)
		}
	}
}

func TestEmptyGroupSkip(t *testing.T) {
	// n=3, Treat=1, look at prefix 1 only: the prefix is either all
	// treated or all control, so no look ever happens.
	s, err := Run(&Config{
		Values: []float64{1, 2, 3}, Treat: 1, Looks: []int{1},
		Threshold: rat(0, 1), Norm: rat(1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Crossed.Sign() != 0 {
		t.Fatalf("crossed %s, want 0", s.Crossed)
	}
	// Looks at 1 and 2: only assignments with the treated sample at
	// position 1 or 2 have both groups present at prefix 2, and with
	// threshold 0 they cross there. Treated at position 3 never looks.
	s, err = Run(&Config{
		Values: []float64{1, 2, 3}, Treat: 1, Looks: []int{1, 2},
		Threshold: rat(0, 1), Norm: rat(1, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	p, ok := s.ProbEverCrossed()
	if !ok || p.Cmp(rat(2, 3)) != 0 {
		t.Fatalf("P(cross) = %v (ok=%v), want 2/3", p, ok)
	}
	p2, _ := s.ProbFirstCross(2)
	if p2.Cmp(rat(2, 3)) != 0 {
		t.Fatalf("P(first cross at 2) = %v, want 2/3", p2)
	}
}

func TestEqualityCountsAsCrossing(t *testing.T) {
	// n=4, Treat=2, distinct values, look at k=2 only.
	// Prefix {1,2}: ranks 1,2. One treated in prefix => |W-E| = |1-1.5| = 0.5.
	cfg := &Config{
		Values: []float64{1, 2, 3, 4}, Treat: 2, Looks: []int{2},
		Threshold: rat(1, 2), Norm: rat(1, 1),
	}
	s, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 4 of 6 assignments have exactly one treated in the prefix and
	// |W-E| == Threshold exactly; equality must count as crossing.
	if got := s.Crossed.String(); got != "4" {
		t.Fatalf("crossed %s, want 4 (equality must count)", got)
	}
	// A hair above 1/2 nothing crosses.
	cfg.Threshold = rat(3, 4)
	s, err = Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Crossed.Sign() != 0 {
		t.Fatalf("crossed %s, want 0 above threshold", s.Crossed)
	}
}

func TestTiesAverageRanks(t *testing.T) {
	// All values tied: every prefix rank is (k+1)/2, so W == E always.
	// With threshold 0 every assignment crosses at the first real look;
	// with any positive threshold none does.
	cfg := &Config{
		Values: []float64{5, 5, 5, 5}, Treat: 2, Looks: []int{2, 3, 4},
		Threshold: rat(0, 1), Norm: rat(1, 1),
	}
	s, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Crossed.String(); got != "6" {
		t.Fatalf("crossed %s, want all 6 at threshold 0", got)
	}
	// At k=2 the two assignments with an empty group in the prefix are
	// skipped; they cross at k=3 instead.
	if got := s.Cross[2].String(); got != "4" {
		t.Fatalf("first cross at k=2: %s, want 4", got)
	}
	if got := s.Cross[3].String(); got != "2" {
		t.Fatalf("first cross at k=3: %s, want 2", got)
	}
	cfg.Threshold = rat(1, 100)
	s, err = Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Crossed.Sign() != 0 {
		t.Fatalf("crossed %s, want 0 for all-tied values", s.Crossed)
	}
}

func TestBudgetResumeAndGob(t *testing.T) {
	cfg := &Config{
		Values:    []float64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8},
		Treat:     6,
		Looks:     []int{2, 4, 6, 8, 10, 12},
		Threshold: rat(7, 10), Norm: rat(3, 2),
	}
	p, err := Prepare(cfg)
	if err != nil {
		t.Fatal(err)
	}
	full := NewState(p)
	full.Step(p, 0)

	part := NewState(p)
	if _, ok := part.ProbEverCrossed(); ok {
		t.Fatal("partial state must not report exact probabilities")
	}
	var buf bytes.Buffer
	steps := 0
	for !part.Complete {
		part.Step(p, 100)
		steps++
		// Serialize and restore mid-enumeration; unfinished counts survive.
		buf.Reset()
		if err := gob.NewEncoder(&buf).Encode(part); err != nil {
			t.Fatal(err)
		}
		var restored State
		if err := gob.NewDecoder(&buf).Decode(&restored); err != nil {
			t.Fatal(err)
		}
		*part = restored
	}
	if steps < 2 {
		t.Fatalf("expected multiple budgeted steps, got %d", steps)
	}
	gotCross, gotCrossed, _ := stateMaps(part)
	wantCross, wantCrossed, _ := stateMaps(full)
	if gotCrossed != wantCrossed || !reflect.DeepEqual(gotCross, wantCross) {
		t.Fatalf("resumed result (%v, %s) != one-shot (%v, %s)",
			gotCross, gotCrossed, wantCross, wantCrossed)
	}
	if part.Done.Cmp(part.Total) != 0 {
		t.Fatalf("done %s != total %s", part.Done, part.Total)
	}
	if part.Remaining().Sign() != 0 {
		t.Fatalf("remaining %s, want 0", part.Remaining())
	}
}

func TestLargeSmoke(t *testing.T) {
	// n=24, Treat=12: 2,704,156 assignments.
	values := make([]float64, 24)
	for i := range values {
		values[i] = float64((i*7 + 3) % 11) // ties present
	}
	s, err := Run(&Config{
		Values: values, Treat: 12,
		Looks:     []int{4, 8, 12, 16, 20, 24},
		Threshold: rat(1, 1), Norm: rat(2, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Total.String() != "2704156" {
		t.Fatalf("total %s, want 2704156", s.Total)
	}
	if s.Done.Cmp(s.Total) != 0 {
		t.Fatalf("done %s != total %s", s.Done, s.Total)
	}
	sum := new(big.Int).Add(s.Crossed, s.NeverCrossed())
	if sum.Cmp(s.Total) != 0 {
		t.Fatalf("crossed+never %s != total %s", sum, s.Total)
	}
}

func TestValidation(t *testing.T) {
	good := &Config{Values: []float64{1, 2}, Treat: 1, Looks: []int{2},
		Threshold: rat(0, 1), Norm: rat(1, 1)}
	if _, err := Prepare(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	bad := []Config{
		{Values: nil, Treat: 0, Threshold: rat(0, 1), Norm: rat(1, 1)},
		{Values: []float64{1, 2}, Treat: 3, Threshold: rat(0, 1), Norm: rat(1, 1)},
		{Values: []float64{1, 2}, Treat: 1, Threshold: nil, Norm: rat(1, 1)},
		{Values: []float64{1, 2}, Treat: 1, Threshold: rat(0, 1), Norm: rat(0, 1)},
		{Values: []float64{1, 2}, Treat: 1, Looks: []int{3}, Threshold: rat(0, 1), Norm: rat(1, 1)},
		{Values: []float64{1, 2}, Treat: 1, Looks: []int{2, 2}, Threshold: rat(0, 1), Norm: rat(1, 1)},
		{Values: []float64{1, 2}, Treat: 1, Looks: []int{2, 1}, Threshold: rat(0, 1), Norm: rat(1, 1)},
	}
	for i := range bad {
		if _, err := Prepare(&bad[i]); err == nil {
			t.Errorf("bad config %d accepted", i)
		}
	}
}

func Example() {
	cfg := &Config{
		Values:    []float64{2.1, 1.5, 3.3, 1.5, 2.8, 0.9},
		Treat:     3,
		Looks:     []int{2, 4, 6},
		Threshold: big.NewRat(1, 2),
		Norm:      big.NewRat(3, 2),
	}
	s, err := Run(cfg)
	if err != nil {
		panic(err)
	}
	first, never, _ := s.Distribution()
	looks := []int{2, 4, 6}
	for _, k := range looks {
		if p, ok := first[k]; ok {
			println("P(first cross at", k, ") =", p.RatString())
		}
	}
	println("P(never) =", never.RatString())
}
