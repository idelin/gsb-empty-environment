package netsec

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"
)

type testFixture struct {
	N *big.Int
	E *big.Int
	D *big.Int
	B *big.Int
}

// newTestFixture builds a small, local RSA fixture with a three-byte modulus.
func newTestFixture(t *testing.T) *testFixture {
	t.Helper()
	p := big.NewInt(257)
	q := big.NewInt(263)
	n := new(big.Int).Mul(p, q)
	e := big.NewInt(3)
	pMinusOne := new(big.Int).Sub(p, big.NewInt(1))
	qMinusOne := new(big.Int).Sub(q, big.NewInt(1))
	phi := new(big.Int).Mul(pMinusOne, qMinusOne)
	d := new(big.Int).ModInverse(e, phi)
	if d == nil {
		t.Fatal("no private exponent for fixture")
	}
	return &testFixture{
		N: n,
		E: new(big.Int).Set(e),
		D: d,
		B: new(big.Int).Lsh(big.NewInt(1), uint(8*(len(n.Bytes())-2))),
	}
}

func (f *testFixture) encrypt(m *big.Int) *big.Int {
	return new(big.Int).Exp(m, f.E, f.N)
}

func (f *testFixture) decrypt(x *big.Int) *big.Int {
	return new(big.Int).Exp(x, f.D, f.N)
}

func (f *testFixture) inPrefix(x *big.Int) bool {
	r := f.decrypt(x)
	return r.Cmp(new(big.Int).Lsh(new(big.Int).Set(f.B), 1)) >= 0 &&
		r.Cmp(new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3))) < 0
}

func (f *testFixture) oracle(x *big.Int) (Feedback, error) {
	if f.inPrefix(x) {
		return FeedbackYes, nil
	}
	return FeedbackNo, nil
}

func (f *testFixture) prefixInterval() Interval {
	lo := new(big.Int).Lsh(new(big.Int).Set(f.B), 1)
	hi := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3)), big.NewInt(1))
	return Interval{Lo: lo, Hi: hi}
}

func containsAny(intervals []Interval, m *big.Int) bool {
	for _, iv := range intervals {
		if iv.Contains(m) {
			return true
		}
	}
	return false
}

func TestFilterIntervalsMatchesBruteForce(t *testing.T) {
	f := newTestFixture(t)
	lo := new(big.Int).Lsh(new(big.Int).Set(f.B), 1)
	hi := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3)), big.NewInt(1))
	initial := []Interval{{Lo: new(big.Int).Set(lo), Hi: new(big.Int).Set(hi)}}

	for s := int64(1); s <= 900; s += 7 {
		sBig := big.NewInt(s)
		for _, feedback := range []Feedback{FeedbackYes, FeedbackNo} {
			got, err := FilterIntervals(initial, sBig, f.N, f.B, feedback)
			if err != nil {
				t.Fatalf("FilterIntervals(s=%d, %s): %v", s, feedback, err)
			}
			for m := new(big.Int).Set(lo); m.Cmp(hi) <= 0; m.Add(m, big.NewInt(1)) {
				x := new(big.Int).Mul(f.encrypt(m), new(big.Int).Exp(sBig, f.E, f.N))
				x.Mod(x, f.N)
				want := f.inPrefix(x)
				if feedback == FeedbackNo {
					want = !want
				}
				if gotIt := containsAny(got, m); gotIt != want {
					t.Fatalf("s=%d feedback=%s m=%s: membership=%v want=%v in %v",
						s, feedback, m, gotIt, want, got)
				}
			}
		}
	}
}

func TestOneFeedbackCanProduceMultipleIntervals(t *testing.T) {
	f := newTestFixture(t)
	initial := []Interval{f.prefixInterval()}

	foundYes, foundNo := false, false
	for s := int64(2); s < 2000; s++ {
		for _, feedback := range []Feedback{FeedbackYes, FeedbackNo} {
			got, err := FilterIntervals(initial, big.NewInt(s), f.N, f.B, feedback)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) > 1 {
				// The union must still be sorted, disjoint, and merged.
				for i := 1; i < len(got); i++ {
					if got[i-1].Hi.Cmp(got[i].Lo) >= 0 {
						t.Fatalf("intervals overlap or touch: %v", got)
					}
					adjacent := new(big.Int).Add(got[i-1].Hi, big.NewInt(1))
					if adjacent.Cmp(got[i].Lo) == 0 {
						t.Fatalf("adjacent intervals were not merged: %v", got)
					}
				}
				if feedback == FeedbackYes {
					foundYes = true
				} else {
					foundNo = true
				}
			}
		}
		if foundYes && foundNo {
			return
		}
	}
	t.Fatalf("multiple-interval coverage: yes=%v no=%v", foundYes, foundNo)
}

func TestFilterIntervalsFromMultipleStartingIntervals(t *testing.T) {
	f := newTestFixture(t)
	twoB := new(big.Int).Lsh(new(big.Int).Set(f.B), 1)
	intervals := []Interval{
		{Lo: new(big.Int).Set(twoB), Hi: new(big.Int).Add(new(big.Int).Set(twoB), big.NewInt(40))},
		{Lo: new(big.Int).Add(new(big.Int).Set(twoB), big.NewInt(90)), Hi: new(big.Int).Add(new(big.Int).Set(twoB), big.NewInt(130))},
	}
	for _, s := range []int64{3, 17, 101, 257, 999} {
		for _, feedback := range []Feedback{FeedbackYes, FeedbackNo} {
			got, err := FilterIntervals(intervals, big.NewInt(s), f.N, f.B, feedback)
			if err != nil {
				t.Fatal(err)
			}
			for _, start := range intervals {
				for m := new(big.Int).Set(start.Lo); m.Cmp(start.Hi) <= 0; m.Add(m, big.NewInt(1)) {
					x := new(big.Int).Mul(f.encrypt(m), new(big.Int).Exp(big.NewInt(s), f.E, f.N))
					x.Mod(x, f.N)
					want := f.inPrefix(x)
					if feedback == FeedbackNo {
						want = !want
					}
					if contains := containsAny(got, m); contains != want {
						t.Fatalf("s=%d feedback=%s m=%s: got %v want %v in %v",
							s, feedback, m, contains, want, got)
					}
				}
			}
		}
	}
}

func TestPrefixBoundaries(t *testing.T) {
	f := newTestFixture(t)
	twoB := new(big.Int).Lsh(new(big.Int).Set(f.B), 1)
	threeBMinusOne := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3)), big.NewInt(1))
	threeB := new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3))

	cases := []struct {
		name string
		iv   Interval
		yes  bool
	}{
		{"2B", Interval{Lo: new(big.Int).Set(twoB), Hi: new(big.Int).Set(twoB)}, true},
		{"3B-1", Interval{Lo: new(big.Int).Set(threeBMinusOne), Hi: new(big.Int).Set(threeBMinusOne)}, true},
		{"3B", Interval{Lo: new(big.Int).Set(threeB), Hi: new(big.Int).Set(threeB)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			yes, err := FilterIntervals([]Interval{tc.iv}, big.NewInt(1), f.N, f.B, FeedbackYes)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(yes) == 1 && yes[0].Single(); got != tc.yes {
				t.Fatalf("yes result %v, want non-empty=%v", yes, tc.yes)
			}

			no, err := FilterIntervals([]Interval{tc.iv}, big.NewInt(1), f.N, f.B, FeedbackNo)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := len(no) != 0, !tc.yes; got != want {
				t.Fatalf("no result %v, want non-empty=%v", no, want)
			}
		})
	}
}

func TestEmptyIntersectionIsReported(t *testing.T) {
	f := newTestFixture(t)
	threeB := new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3))
	iv := Interval{Lo: new(big.Int).Set(threeB), Hi: new(big.Int).Set(threeB)}

	got, err := FilterIntervals([]Interval{iv}, big.NewInt(1), f.N, f.B, FeedbackYes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty intersection, got %v", got)
	}
	if _, err := IntersectFeedback([]Interval{iv}, big.NewInt(1), f.N, f.B, FeedbackYes); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("IntersectFeedback error = %v, want ErrInconsistent", err)
	}
}

func TestRecoversEveryPlaintextInPrefixRange(t *testing.T) {
	f := newTestFixture(t)
	lo := new(big.Int).Lsh(new(big.Int).Set(f.B), 1)
	hi := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(f.B), big.NewInt(3)), big.NewInt(1))
	maxQueries := 0
	for m := new(big.Int).Set(lo); m.Cmp(hi) <= 0; m.Add(m, big.NewInt(1)) {
		c := f.encrypt(m)
		s, err := New(f.N, f.E, c, OracleFunc(f.oracle), 10000)
		if err != nil {
			t.Fatalf("m=%s: %v", m, err)
		}
		out, err := s.Recover()
		if err != nil {
			t.Fatalf("m=%s: %v", m, err)
		}
		if out.Status != StatusRecovered || out.Plaintext == nil {
			t.Fatalf("m=%s: status=%s intervals=%v", m, out.Status, out.Intervals)
		}
		if out.Plaintext.Cmp(m) != 0 {
			t.Fatalf("m=%s: recovered %s", m, out.Plaintext)
		}
		if !out.Verified {
			t.Fatalf("m=%s: recovered plaintext was not marked verified", m)
		}
		if f.encrypt(out.Plaintext).Cmp(c) != 0 {
			t.Fatalf("m=%s: recovered plaintext does not re-encrypt", m)
		}
		if s.Used() > maxQueries {
			maxQueries = s.Used()
		}
	}
	t.Logf("maximum queries for one plaintext: %d", maxQueries)
}

func TestBudgetExhaustionLeavesCompleteState(t *testing.T) {
	f := newTestFixture(t)
	m := big.NewInt(600)
	c := f.encrypt(m)
	s, err := New(f.N, f.E, c, OracleFunc(f.oracle), 2)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusExhausted {
		t.Fatalf("status = %s, want exhausted", out.Status)
	}
	if out.Plaintext != nil {
		t.Fatal("exhausted result exposed an unverified plaintext")
	}
	if len(out.Intervals) == 0 || out.NextS == nil {
		t.Fatalf("exhausted result is not resumable: %+v", out)
	}
	if out.Used != 2 {
		t.Fatalf("used %d queries, want 2", out.Used)
	}
}

func TestCheckpointResumeAndBinding(t *testing.T) {
	f := newTestFixture(t)
	m := big.NewInt(600)
	c := f.encrypt(m)

	// Every third answer is temporarily unavailable.  The first budget is too
	// small to finish, which forces a checkpoint.
	var calls int
	flaky := func(x *big.Int) (Feedback, error) {
		calls++
		if calls%3 == 0 {
			return FeedbackUnavailable, nil
		}
		return f.oracle(x)
	}
	s, err := New(f.N, f.E, c, OracleFunc(flaky), 8)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusExhausted {
		t.Fatalf("first run status = %s, want exhausted", out.Status)
	}
	cp := s.Checkpoint()
	if err := cp.Verify(); err != nil {
		t.Fatal(err)
	}
	if !cp.Binds(s.Key(), c) {
		t.Fatal("checkpoint does not bind key and ciphertext")
	}

	// The checkpoint must survive a JSON round trip.
	encoded, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Checkpoint
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Verify(); err != nil {
		t.Fatalf("decoded checkpoint: %v", err)
	}

	// Extending the budget must reseal the checkpoint.
	extended, err := decoded.WithBudget(10000)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreSession(extended, OracleFunc(f.oracle))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Used() != cp.Used {
		t.Fatalf("restored used=%d, checkpoint used=%d", restored.Used(), cp.Used)
	}
	if !intervalsEqual(restored.Intervals(), cp.Intervals) {
		t.Fatalf("restored intervals %v, checkpoint %v", restored.Intervals(), cp.Intervals)
	}
	final, err := restored.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusRecovered || final.Plaintext == nil || final.Plaintext.Cmp(m) != 0 {
		t.Fatalf("resume result = %+v", final)
	}

	// A checkpoint is not valid for another modulus or target ciphertext.
	wrongC := f.encrypt(big.NewInt(601))
	if _, err := ResumeSession(cp, s.Key(), wrongC, OracleFunc(f.oracle), 10000); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("wrong ciphertext resume error = %v, want ErrCheckpointMismatch", err)
	}
	tamperedKey := s.Key()
	tamperedKey.N.Add(tamperedKey.N, big.NewInt(2))
	if _, err := ResumeSession(cp, tamperedKey, c, OracleFunc(f.oracle), 10000); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("wrong key resume error = %v, want ErrCheckpointMismatch", err)
	}

	tampered := cp.Clone()
	tampered.Queries[0].Feedback = FeedbackUnavailable
	if err := tampered.Verify(); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("tampered query verify error = %v, want ErrCheckpointMismatch", err)
	}
}

func TestUnavailableConsumesBudgetWithoutChangingCandidates(t *testing.T) {
	f := newTestFixture(t)
	m := big.NewInt(600)
	c := f.encrypt(m)
	s, err := New(f.N, f.E, c, PredicateOracle(func(x *big.Int) (bool, bool) {
		return false, false
	}), 3)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Intervals()
	out, err := s.Step()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusRunning || out.Used != 1 {
		t.Fatalf("after unavailable: %+v", out)
	}
	if !intervalsEqual(before, s.Intervals()) {
		t.Fatalf("unavailable changed candidates: %v -> %v", before, s.Intervals())
	}
	next := s.NextSearchPosition()
	if next == nil || next.Cmp(out.NextS) != 0 {
		t.Fatalf("next search position changed on unavailable: %v vs %v", next, out.NextS)
	}

	out, err = s.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != StatusExhausted || out.Used != 3 {
		t.Fatalf("budget exhaustion: %+v", out)
	}
	if out.Plaintext != nil {
		t.Fatal("unavailable-only run exposed a plaintext")
	}
}

func TestForgedCallbackIsInconsistent(t *testing.T) {
	f := newTestFixture(t)
	m := big.NewInt(512)
	c := f.encrypt(m)
	forge := func(x *big.Int) (Feedback, error) { return FeedbackYes, nil }
	s, err := New(f.N, f.E, c, OracleFunc(forge), 100000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("forged callback error = %v, want ErrInconsistent", err)
	}

	invalid := func(x *big.Int) Feedback { return Feedback(99) }
	s2, err := New(f.N, f.E, c, invalid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Step(); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("invalid feedback error = %v, want ErrInconsistent", err)
	}
}

func TestCheckpointCannotCarryDifferentQueries(t *testing.T) {
	f := newTestFixture(t)
	m := big.NewInt(600)
	c := f.encrypt(m)
	s, err := New(f.N, f.E, c, OracleFunc(f.oracle), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	cp := s.Checkpoint()
	if len(cp.Queries) == 0 {
		t.Fatal("expected recorded queries")
	}

	// Re-sealing a checkpoint whose query ciphertext was rewritten must not
	// allow it to be imported.
	forged := cp.Clone()
	forged.Queries[0].X = "1"
	forged.Seal()
	if _, err := RestoreSession(forged, OracleFunc(f.oracle)); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("forged query error = %v, want ErrCheckpointMismatch", err)
	}

	// A re-sealed history with the same multiplier answered both ways is a
	// direct contradiction, not an importable checkpoint.
	contradictory := cp.Clone()
	first := contradictory.Queries[0]
	if first.Feedback == FeedbackUnavailable {
		t.Fatal("fixture unexpectedly recorded an unavailable query first")
	}
	opposite := FeedbackNo
	if first.Feedback == FeedbackNo {
		opposite = FeedbackYes
	}
	contradictory.Queries = append(contradictory.Queries, QueryRecord{
		S:        first.S,
		X:        first.X,
		Feedback: opposite,
	})
	contradictory.Used++
	contradictory.Seal()
	if _, err := RestoreSession(contradictory, OracleFunc(f.oracle)); !errors.Is(err, ErrInconsistent) {
		t.Fatalf("contradictory query error = %v, want ErrInconsistent", err)
	}
}
