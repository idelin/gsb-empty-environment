package bigsqrt

import (
	"crypto/rand"
	"math/big"
	"strings"
	"testing"
)

func checkInvariant(t *testing.T, n, r, rem *big.Int) {
	t.Helper()
	r2 := new(big.Int).Mul(r, r)
	if r2.Cmp(n) > 0 {
		t.Fatalf("r^2 > n")
	}
	up := new(big.Int).Add(r, big.NewInt(1))
	up.Mul(up, up)
	if up.Cmp(n) <= 0 {
		t.Fatalf("(r+1)^2 <= n")
	}
	if new(big.Int).Add(r2, rem).Cmp(n) != 0 {
		t.Fatalf("r^2 + rem != n")
	}
	if rem.Sign() < 0 {
		t.Fatalf("negative remainder")
	}
}

func TestPerfectSquares(t *testing.T) {
	ks := []*big.Int{
		big.NewInt(0), big.NewInt(1), big.NewInt(2), big.NewInt(3),
		big.NewInt(999),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(50), nil),
		new(big.Int).Exp(big.NewInt(3), big.NewInt(777), nil),
	}
	for _, k := range ks {
		n := new(big.Int).Mul(k, k)
		r, rem, err := Sqrt(n, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Cmp(k) != 0 || rem.Sign() != 0 {
			t.Fatalf("sqrt(%v^2) = %v rem %v", k, r, rem)
		}
		checkInvariant(t, n, r, rem)
	}
}

func TestAdjacentIntegers(t *testing.T) {
	for _, k := range []*big.Int{
		big.NewInt(1), big.NewInt(2), big.NewInt(1000),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil),
	} {
		n := new(big.Int).Mul(k, k)
		for _, d := range []int64{-1, 1} {
			m := new(big.Int).Add(n, big.NewInt(d))
			r, rem, err := Sqrt(m, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantR := new(big.Int).Set(k)
			var wantRem *big.Int
			if d < 0 {
				wantR.Sub(k, big.NewInt(1))
				// k^2-1 - (k-1)^2 = 2k-2
				wantRem = new(big.Int).Mul(k, big.NewInt(2))
				wantRem.Sub(wantRem, big.NewInt(2))
			} else {
				wantRem = big.NewInt(1)
			}
			if r.Cmp(wantR) != 0 || rem.Cmp(wantRem) != 0 {
				t.Fatalf("sqrt(k^2%+d): got r=%v rem=%v, want r=%v rem=%v",
					d, r, rem, wantR, wantRem)
			}
			checkInvariant(t, m, r, rem)
		}
	}
}

func TestCrossCheckWithStdlib(t *testing.T) {
	for i := 0; i < 50; i++ {
		buf := make([]byte, 1+i)
		if _, err := rand.Read(buf); err != nil {
			t.Fatal(err)
		}
		n := new(big.Int).SetBytes(buf)
		r, rem, err := Sqrt(n, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := new(big.Int).Sqrt(n); r.Cmp(want) != 0 {
			t.Fatalf("n=%v: got r=%v, stdlib %v", n, r, want)
		}
		checkInvariant(t, n, r, rem)
	}
}

func TestCancelAndResume(t *testing.T) {
	n := new(big.Int).Exp(big.NewInt(7), big.NewInt(400), nil)
	n.Add(n, big.NewInt(12345))

	wantR, wantRem, err := Sqrt(n, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Drive the computation with a trickle of budget, snapshotting
	// partway, and confirm it resumes to the same result.
	st, err := NewState(n)
	if err != nil {
		t.Fatal(err)
	}
	var snap *State
	for i := 0; !st.Done(); i++ {
		if i == 3 {
			snap = st.Snapshot()
		}
		if _, err := st.Step(NewBudget(1)); err != ErrBudgetExhausted {
			t.Fatalf("step %d: want ErrBudgetExhausted, got %v", i, err)
		}
		if _, err := st.Step(NewBudget(stepOps + finishOps)); err != nil {
			t.Fatal(err)
		}
	}
	r, rem, err := st.Result()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cmp(wantR) != 0 || rem.Cmp(wantRem) != 0 {
		t.Fatalf("resumed result r=%v rem=%v, want r=%v rem=%v", r, rem, wantR, wantRem)
	}

	// The snapshot taken mid-flight must independently converge to
	// the same answer.
	if snap == nil {
		t.Fatal("no snapshot taken")
	}
	if err := snap.Run(nil); err != nil {
		t.Fatal(err)
	}
	r2, rem2, err := snap.Result()
	if err != nil {
		t.Fatal(err)
	}
	if r2.Cmp(wantR) != 0 || rem2.Cmp(wantRem) != 0 {
		t.Fatalf("snapshot result r=%v rem=%v, want r=%v rem=%v", r2, rem2, wantR, wantRem)
	}
}

func TestSnapshotSharesNoBuffers(t *testing.T) {
	n := new(big.Int).Exp(big.NewInt(5), big.NewInt(300), nil)
	st, err := NewState(n)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Step(nil); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()

	// Mutate every reachable buffer of the original.
	st.x.SetInt64(42)
	st.n.SetInt64(7)

	// The snapshot must be untouched and still converge correctly.
	if err := snap.Run(nil); err != nil {
		t.Fatal(err)
	}
	r, rem, err := snap.Result()
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Sqrt(new(big.Int).Exp(big.NewInt(5), big.NewInt(300), nil))
	if r.Cmp(want) != 0 {
		t.Fatalf("snapshot corrupted: r=%v, want %v", r, want)
	}
	checkInvariant(t, new(big.Int).Exp(big.NewInt(5), big.NewInt(300), nil), r, rem)
}

func TestBuilderDigitLimit(t *testing.T) {
	b, err := NewBuilder(10)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Feed("12345", nil); err != nil {
		t.Fatal(err)
	}
	// 6 more digits would exceed the limit of 10: rejected, and the
	// accumulated value must not grow.
	if err := b.Feed("678901", nil); err != ErrDigitLimitExceeded {
		t.Fatalf("want ErrDigitLimitExceeded, got %v", err)
	}
	if b.Digits() != 5 || b.Num().String() != "12345" {
		t.Fatalf("state changed after rejection: digits=%d n=%v", b.Digits(), b.Num())
	}
	if err := b.Feed("67890", nil); err != nil {
		t.Fatal(err)
	}
	if b.Digits() != 10 || b.Num().String() != "1234567890" {
		t.Fatalf("digits=%d n=%v", b.Digits(), b.Num())
	}
	if err := b.Feed("0", nil); err != ErrDigitLimitExceeded {
		t.Fatalf("want ErrDigitLimitExceeded, got %v", err)
	}
}

func TestBuilderRejectsInvalidChunks(t *testing.T) {
	b, err := NewBuilder(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"-3", "12a", "1 2", "+7", "0x10"} {
		if err := b.Feed(bad, nil); err == nil {
			t.Fatalf("chunk %q: want error", bad)
		}
	}
	if err := b.Feed("", nil); err != nil {
		t.Fatalf("empty chunk should be a no-op: %v", err)
	}
	if err := b.Feed("0042", nil); err != nil {
		t.Fatal(err)
	}
	if b.Num().String() != "42" || b.Digits() != 4 {
		t.Fatalf("n=%v digits=%d", b.Num(), b.Digits())
	}
}

func TestBuilderBudget(t *testing.T) {
	b, err := NewBuilder(100)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Feed("123", NewBudget(feedOps-1)); err != ErrBudgetExhausted {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	if b.Digits() != 0 {
		t.Fatalf("failed feed mutated state: digits=%d", b.Digits())
	}
	if err := b.Feed("123", NewBudget(feedOps)); err != nil {
		t.Fatal(err)
	}
	if b.Num().String() != "123" {
		t.Fatalf("n=%v", b.Num())
	}
}

func TestMillionDigitResourceBound(t *testing.T) {
	const digits = 1000000
	// N = 16 * 10^(digits-2) = (4 * 10^((digits-2)/2))^2, a perfect
	// square with exactly one million digits.
	b, err := NewBuilder(digits)
	if err != nil {
		t.Fatal(err)
	}
	budget := NewBudget(100000)
	chunk := 10000
	if err := b.Feed("16"+strings.Repeat("0", chunk-2), budget); err != nil {
		t.Fatal(err)
	}
	fed := chunk
	zeros := strings.Repeat("0", chunk)
	for fed < digits {
		if err := b.Feed(zeros, budget); err != nil {
			t.Fatal(err)
		}
		fed += chunk
	}
	n := b.Num()
	if b.Digits() != digits {
		t.Fatalf("fed %d digits, want %d", b.Digits(), digits)
	}
	// Confirm 10^(digits-1) <= n < 10^digits without an expensive
	// decimal conversion.
	lo := new(big.Int).Exp(big.NewInt(10), big.NewInt(digits-1), nil)
	hi := new(big.Int).Mul(lo, big.NewInt(10))
	if n.Cmp(lo) < 0 || n.Cmp(hi) >= 0 {
		t.Fatalf("n does not have exactly %d digits", digits)
	}

	st, err := NewState(n)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Run(budget); err != nil {
		t.Fatal(err)
	}
	r, rem, err := st.Result()
	if err != nil {
		t.Fatal(err)
	}

	// Expected root: 4 * 10^((digits-2)/2).
	half := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64((digits-2)/2)), nil)
	wantR := new(big.Int).Mul(big.NewInt(4), half)
	if r.Cmp(wantR) != 0 || rem.Sign() != 0 {
		t.Fatalf("wrong root: r matches expected: %v, rem bitlen=%d",
			r.Cmp(wantR) == 0, rem.BitLen())
	}
	// Cross-check against the stdlib and the defining invariant.
	if s := new(big.Int).Sqrt(n); s.Cmp(r) != 0 {
		t.Fatalf("disagrees with stdlib sqrt")
	}
	checkInvariant(t, n, r, rem)

	// Resource bound: the whole job (feeding + Newton) must fit in
	// far fewer big-int ops than the digit count. No wall-clock
	// assumptions are made.
	used := 100000 - budget.Remaining()
	const bound = 10000
	if used > bound {
		t.Fatalf("used %d big-int ops, bound %d", used, bound)
	}
	t.Logf("million-digit sqrt used %d big-int ops (bound %d)", used, bound)
}
