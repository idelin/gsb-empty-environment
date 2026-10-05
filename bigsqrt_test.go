package bigsqrt

import (
	"context"
	"encoding/json"
	"math/big"
	"math/bits"
	"math/rand"
	"strings"
	"testing"
)

func verifyInvariant(t *testing.T, n, root, rem *big.Int) {
	t.Helper()
	if root.Sign() < 0 || rem.Sign() < 0 {
		t.Fatalf("negative root or remainder: r=%s rem=%s", root, rem)
	}
	r2 := new(big.Int).Mul(root, root)
	if r2.Cmp(n) > 0 {
		t.Fatalf("r^2 > N for N=%s", n)
	}
	up := new(big.Int).Add(root, big.NewInt(1))
	up.Mul(up, up)
	if up.Cmp(n) <= 0 {
		t.Fatalf("(r+1)^2 <= N for N=%s", n)
	}
	sum := new(big.Int).Add(r2, rem)
	if sum.Cmp(n) != 0 {
		t.Fatalf("r^2 + rem != N for N=%s", n)
	}
}

func TestSmallValues(t *testing.T) {
	for i := int64(0); i <= 5000; i++ {
		n := big.NewInt(i)
		root, rem, _, err := Sqrt(n)
		if err != nil {
			t.Fatalf("Sqrt(%d): %v", i, err)
		}
		verifyInvariant(t, n, root, rem)
		r := root.Int64()
		if r*r > i || (r+1)*(r+1) <= i {
			t.Fatalf("Sqrt(%d) = %d", i, r)
		}
		if rem.Int64() != i-r*r {
			t.Fatalf("Sqrt(%d) remainder = %d", i, rem.Int64())
		}
	}
}

func randInt(rnd *rand.Rand, bits int) *big.Int {
	n := new(big.Int).Rand(rnd, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
	n.SetBit(n, bits-1, 1) // force exact bit length
	return n
}

func TestPerfectSquares(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for _, size := range []int{1, 2, 64, 256, 1024, 4096} {
		k := randInt(rnd, size)
		n := new(big.Int).Mul(k, k)
		root, rem, _, err := Sqrt(n)
		if err != nil {
			t.Fatalf("bits=%d: %v", size, err)
		}
		if root.Cmp(k) != 0 || rem.Sign() != 0 {
			t.Fatalf("bits=%d: got r=%s rem=%s, want r=%s rem=0", size, root, rem, k)
		}
	}
}

func TestAdjacentIntegers(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for _, size := range []int{2, 128, 512, 2048} {
		k := randInt(rnd, size)
		k2 := new(big.Int).Mul(k, k)

		// N = k^2 - 1 -> root k-1, remainder 2k-2
		nm1 := new(big.Int).Sub(k2, big.NewInt(1))
		root, rem, _, err := Sqrt(nm1)
		if err != nil {
			t.Fatal(err)
		}
		wantRoot := new(big.Int).Sub(k, big.NewInt(1))
		wantRem := new(big.Int).Sub(new(big.Int).Lsh(k, 1), big.NewInt(2))
		if root.Cmp(wantRoot) != 0 || rem.Cmp(wantRem) != 0 {
			t.Fatalf("k^2-1 (bits=%d): got r=%s rem=%s", size, root, rem)
		}
		verifyInvariant(t, nm1, root, rem)

		// N = k^2 + 1 -> root k, remainder 1
		np1 := new(big.Int).Add(k2, big.NewInt(1))
		root, rem, _, err = Sqrt(np1)
		if err != nil {
			t.Fatal(err)
		}
		if root.Cmp(k) != 0 || rem.Cmp(big.NewInt(1)) != 0 {
			t.Fatalf("k^2+1 (bits=%d): got r=%s rem=%s", size, root, rem)
		}
		verifyInvariant(t, np1, root, rem)
	}
}

func TestNegativeRejected(t *testing.T) {
	if _, err := NewTask(big.NewInt(-1)); err == nil {
		t.Fatal("expected error for negative input")
	}
	if _, err := NewTask(nil); err == nil {
		t.Fatal("expected error for nil input")
	}
}

// runWithSnapshots drives a Task to completion, snapshotting and
// restoring (through a JSON round trip) at every budget suspension.
func runWithSnapshots(t *testing.T, task *Task, budget uint64) Result {
	t.Helper()
	ctx := context.Background()
	for {
		done, err := task.Step(ctx, budget)
		if done {
			if err != nil {
				t.Fatalf("Step: %v", err)
			}
			res, err := task.Result()
			if err != nil {
				t.Fatalf("Result: %v", err)
			}
			return res
		}
		if err != ErrBudgetExhausted {
			t.Fatalf("Step: unexpected error %v", err)
		}
		snap := task.Snapshot()
		data, merr := json.Marshal(snap)
		if merr != nil {
			t.Fatalf("marshal: %v", merr)
		}
		var restored Snapshot
		if uerr := json.Unmarshal(data, &restored); uerr != nil {
			t.Fatalf("unmarshal: %v", uerr)
		}
		task, err = Restore(restored)
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
	}
}

func TestBudgetAndResume(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	k := randInt(rnd, 512)
	n := new(big.Int).Add(new(big.Int).Mul(k, k), big.NewInt(12345))

	refRoot, refRem, _, err := Sqrt(n)
	if err != nil {
		t.Fatal(err)
	}

	task, err := NewTask(n)
	if err != nil {
		t.Fatal(err)
	}
	res := runWithSnapshots(t, task, 1) // suspend after every single op
	if res.Root.Cmp(refRoot) != 0 || res.Remainder.Cmp(refRem) != 0 {
		t.Fatalf("resumed result mismatch: r=%s rem=%s", res.Root, res.Remainder)
	}
	if res.Ops == 0 {
		t.Fatal("operation counter did not accumulate across restores")
	}
	verifyInvariant(t, n, res.Root, res.Remainder)
}

func TestCancellationAndRecovery(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	k := randInt(rnd, 1024)
	n := new(big.Int).Mul(k, k)

	task, err := NewTask(n)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := task.Step(ctx, 3); err != ErrBudgetExhausted {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	cancel()
	if _, err := task.Step(ctx, 1000); err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	// Recover from the cancelled run via a snapshot and a fresh context.
	task, err = Restore(task.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	res := runWithSnapshots(t, task, 4)
	if res.Root.Cmp(k) != 0 || res.Remainder.Sign() != 0 {
		t.Fatalf("after recovery: r=%s rem=%s, want r=%s rem=0", res.Root, res.Remainder, k)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	n := randInt(rnd, 256)

	// Mutating the caller's input after NewTask must not affect the Task.
	task, err := NewTask(n)
	if err != nil {
		t.Fatal(err)
	}
	n.SetInt64(0)

	if _, err := task.Step(context.Background(), 2); err != ErrBudgetExhausted {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	snap := task.Snapshot()
	snapJSON, _ := json.Marshal(snap)

	// Drive the original task to completion; the snapshot must not move.
	res := runWithSnapshots(t, task, 1<<20)
	snapAfter, _ := json.Marshal(snap)
	if string(snapJSON) != string(snapAfter) {
		t.Fatal("snapshot changed while the source task advanced")
	}

	// A task restored from the snapshot must converge to the same result.
	task2, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	res2 := runWithSnapshots(t, task2, 1<<20)
	if res.Root.Cmp(res2.Root) != 0 || res.Remainder.Cmp(res2.Remainder) != 0 {
		t.Fatal("restored task diverged from original")
	}

	// Mutating a returned Result must not corrupt the Task.
	res.Root.Add(res.Root, big.NewInt(1))
	res3, err := task.Result()
	if err != nil {
		t.Fatal(err)
	}
	if res3.Root.Cmp(res2.Root) != 0 {
		t.Fatal("Result shares mutable state with the Task")
	}
}

func TestMillionDigits(t *testing.T) {
	const digits = 1_000_000
	const chunk = 64 * 1024

	// k has 500_000 digits starting with 9, so k^2 has exactly 1_000_000.
	rnd := rand.New(rand.NewSource(2024))
	var sb strings.Builder
	sb.Grow(digits / 2)
	sb.WriteByte('9')
	for i := 1; i < digits/2; i++ {
		sb.WriteByte(byte('0' + rnd.Intn(10)))
	}
	k, ok := new(big.Int).SetString(sb.String(), 10)
	if !ok {
		t.Fatal("failed to build k")
	}
	n := new(big.Int).Mul(k, k)
	n.Add(n, big.NewInt(12345)) // remainder, well below 2k+1

	// Stream the decimal representation in chunks under a hard limit.
	stream := NewStream(digits)
	dec := n.String()
	if len(dec) != digits {
		t.Fatalf("N has %d digits, want %d", len(dec), digits)
	}
	for i := 0; i < len(dec); i += chunk {
		end := i + chunk
		if end > len(dec) {
			end = len(dec)
		}
		if err := stream.Feed(dec[i:end]); err != nil {
			t.Fatalf("Feed at offset %d: %v", i, err)
		}
	}
	if stream.Digits() != digits {
		t.Fatalf("stream accepted %d digits", stream.Digits())
	}
	// The limit is exactly reached: one more digit must be rejected and
	// must not grow the buffered input.
	if err := stream.Feed("7"); err != ErrDigitLimit {
		t.Fatalf("want ErrDigitLimit, got %v", err)
	}
	if err := stream.Feed("8"); err != ErrDigitLimit {
		t.Fatalf("stream not sealed after limit breach: %v", err)
	}
	if stream.Digits() != digits {
		t.Fatal("buffer grew past the digit limit")
	}

	parsed, err := stream.Int()
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Cmp(n) != 0 {
		t.Fatal("streamed value mismatch")
	}

	// Compute with a sliced budget, snapshotting across suspensions.
	task, err := NewTask(parsed)
	if err != nil {
		t.Fatal(err)
	}
	res := runWithSnapshots(t, task, 5)

	if res.Root.Cmp(k) != 0 || res.Remainder.Cmp(big.NewInt(12345)) != 0 {
		t.Fatalf("got r=%s... rem=%s", res.Root.String()[:40], res.Remainder)
	}
	verifyInvariant(t, n, res.Root, res.Remainder)

	// Resource upper bound: Newton needs O(log bitlen) iterations, each
	// a constant number of big-integer operations. No wall-clock bound.
	bitLen := n.BitLen()
	bound := uint64(6*(bits.Len(uint(bitLen))) + 16)
	if res.Ops > bound {
		t.Fatalf("used %d big-int ops, bound is %d for %d bits", res.Ops, bound, bitLen)
	}
	t.Logf("1M digits: %d big-int ops (bound %d)", res.Ops, bound)
}
