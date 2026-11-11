package admission

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

const testE = int64(65537)

// genPrime returns a random bits-bit prime guaranteed to be distinct from all
// primes in avoid and compatible with the test public exponent (i.e.
// gcd(E, p-1) == 1).
func genPrime(t *testing.T, bits int, avoid ...*big.Int) *big.Int {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		p, err := rand.Prime(rand.Reader, bits)
		if err != nil {
			t.Fatalf("rand.Prime: %v", err)
		}
		g := new(big.Int).GCD(nil, nil, big.NewInt(testE), new(big.Int).Sub(p, big.NewInt(1)))
		if g.Cmp(big.NewInt(1)) != 0 {
			continue
		}
		duplicate := false
		for _, q := range avoid {
			if p.Cmp(q) == 0 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			return p
		}
	}
	t.Fatalf("could not generate a distinct %d-bit prime", bits)
	return nil
}

// modulusFrom builds N = product(factors).
func modulusFrom(factors ...*big.Int) *big.Int {
	n := new(big.Int).SetInt64(1)
	for _, p := range factors {
		n.Mul(n, p)
	}
	return n
}

// newKey builds an ImportRequest for a fresh two-prime key.
func newKey(t *testing.T, id string, p, q *big.Int) ImportRequest {
	t.Helper()
	return ImportRequest{
		KeyID:   id,
		N:       modulusFrom(p, q),
		E:       big.NewInt(testE),
		Factors: []*big.Int{p, q},
	}
}

// invalidExponent returns a prime exponent E > 1 that divides (p-1), so
// gcd(E, lambda(n)) != 1.
func invalidExponent(t *testing.T, p *big.Int) *big.Int {
	t.Helper()
	pm1 := new(big.Int).Sub(p, big.NewInt(1))
	// Trial-divide the small (64-bit in tests) value p-1 and return its first
	// prime divisor other than 2.
	rem := new(big.Int).Set(pm1)
	for rem.Bit(0) == 0 {
		rem.Rsh(rem, 1)
	}
	if rem.ProbablyPrime(20) && rem.Cmp(big.NewInt(3)) >= 0 {
		return new(big.Int).Set(rem)
	}
	for d := big.NewInt(3); d.Cmp(rem) <= 0; d.Add(d, big.NewInt(2)) {
		if d.BitLen() > 32 {
			break
		}
		if new(big.Int).Mod(rem, d).Sign() == 0 {
			return new(big.Int).Set(d)
		}
	}
	t.Fatalf("no small prime divisor of p-1 found")
	return nil
}

// coprimeExponent returns a small prime exponent distinct from testE that is
// coprime with every (factor-1), so it is valid over the given factors.
func coprimeExponent(t *testing.T, factors ...*big.Int) *big.Int {
	t.Helper()
	for _, e := range []int64{3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47} {
		if e == testE {
			continue
		}
		eb := big.NewInt(e)
		ok := true
		for _, f := range factors {
			g := new(big.Int).GCD(nil, nil, eb, new(big.Int).Sub(f, big.NewInt(1)))
			if g.Cmp(big.NewInt(1)) != 0 {
				ok = false
				break
			}
		}
		if ok {
			return eb
		}
	}
	t.Fatalf("no alternative coprime exponent found")
	return nil
}

func batchErr(t *testing.T, err error) *BatchError {
	t.Helper()
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BatchError, got %v", err)
	}
	return be
}

// TestIdempotentReplayAndExponentReuse distinguishes the three same-modulus
// cases: identical (N, E) retransmission is an idempotent no-op, while a
// different exponent on the same modulus is rejected.
func TestIdempotentReplayAndExponentReuse(t *testing.T) {
	svc := NewService()
	p, q := genPrime(t, 64), genPrime(t, 64)

	first := newKey(t, "key-a", p, q)
	results, err := svc.ImportBatch([]ImportRequest{first})
	if err != nil {
		t.Fatalf("initial import failed: %v", err)
	}
	if len(results) != 1 || results[0].Replayed || svc.PublishedCount() != 1 {
		t.Fatalf("unexpected initial state: %+v count=%d", results, svc.PublishedCount())
	}

	// Identical retransmission of the same public key under the same ID.
	replay := newKey(t, "key-a", p, q)
	results, err = svc.ImportBatch([]ImportRequest{replay})
	if err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if !results[0].Replayed || svc.PublishedCount() != 1 {
		t.Fatalf("replay not recognized: %+v count=%d", results, svc.PublishedCount())
	}
	if owner := svc.FactorOwner(p); owner != "key-a" {
		t.Fatalf("factor owner = %q, want key-a", owner)
	}

	// Same modulus, different public exponent, under a different ID: the
	// shared prime factors must be detected and rejected.
	otherE := coprimeExponent(t, p, q)
	reuseN := ImportRequest{
		KeyID:   "key-b",
		N:       modulusFrom(p, q),
		E:       otherE,
		Factors: []*big.Int{p, q},
	}
	_, err = svc.ImportBatch([]ImportRequest{reuseN})
	be := batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrFactorConflict) {
		t.Fatalf("want ErrFactorConflict, got %v", be.Results[0].Err)
	}
	if be.Results[0].ConflictKeyID != "key-a" {
		t.Fatalf("conflict owner = %q, want key-a", be.Results[0].ConflictKeyID)
	}
	if svc.PublishedCount() != 1 {
		t.Fatalf("rejected import changed published count: %d", svc.PublishedCount())
	}
	if _, _, ok := svc.PublishedKey("key-b"); ok {
		t.Fatal("rejected key-b was published")
	}

	// Same ID but a different public key is an ID-reuse error.
	r, s := genPrime(t, 64, p, q), genPrime(t, 64, p, q)
	idReuse := newKey(t, "key-a", r, s)
	_, err = svc.ImportBatch([]ImportRequest{idReuse})
	be = batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrIDReused) {
		t.Fatalf("want ErrIDReused, got %v", be.Results[0].Err)
	}

	// Replayed key with a bogus (non-equivalent) factorization is rejected.
	badReplay := ImportRequest{
		KeyID:   "key-a",
		N:       modulusFrom(p, q),
		E:       big.NewInt(testE),
		Factors: []*big.Int{p, r},
	}
	_, err = svc.ImportBatch([]ImportRequest{badReplay})
	be = batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrInvalidRequest) {
		t.Fatalf("want ErrInvalidRequest for wrong factorization, got %v", be.Results[0].Err)
	}
}

// TestSameBatchCollisions verifies that prime-factor sharing between items of
// the same batch is detected even though nothing has been published yet, and
// that such a batch commits nothing at all.
func TestSameBatchCollisions(t *testing.T) {
	svc := NewService()
	p, q, r, s := genPrime(t, 64), genPrime(t, 64), genPrime(t, 64), genPrime(t, 64)

	// key-x and key-y share prime p; key-z is fully independent.
	keyX := newKey(t, "key-x", p, q)
	keyY := newKey(t, "key-y", p, r)
	keyZ := newKey(t, "key-z", s, genPrime(t, 64, p, q, r, s))

	_, err := svc.ImportBatch([]ImportRequest{keyX, keyY, keyZ})
	be := batchErr(t, err)
	if len(be.Results) != 3 {
		t.Fatalf("want 3 results, got %d", len(be.Results))
	}
	if be.Results[0].Err != nil {
		t.Fatalf("first claimant of p should pass dry run, got %v", be.Results[0].Err)
	}
	if !errors.Is(be.Results[1].Err, ErrFactorConflict) || be.Results[1].ConflictKeyID != "key-x" {
		t.Fatalf("key-y result = %+v, want conflict with key-x", be.Results[1])
	}
	if be.Results[2].Err != nil {
		t.Fatalf("independent key-z should pass dry run, got %v", be.Results[2].Err)
	}
	if svc.PublishedCount() != 0 {
		t.Fatalf("rejected batch polluted index: count=%d", svc.PublishedCount())
	}
	for _, prime := range []*big.Int{p, q, r, s} {
		if owner := svc.FactorOwner(prime); owner != "" {
			t.Fatalf("factor %s published as %q after rejected batch", prime, owner)
		}
	}

	// Re-submitting without the colliding item publishes both survivors.
	results, err := svc.ImportBatch([]ImportRequest{keyX, keyZ})
	if err != nil {
		t.Fatalf("clean batch failed: %v", err)
	}
	for i, res := range results {
		if res.Err != nil || res.Replayed {
			t.Fatalf("item %d unexpectedly not admitted: %+v", i, res)
		}
	}
	if svc.PublishedCount() != 2 || svc.FactorOwner(p) != "key-x" {
		t.Fatalf("post-commit state wrong: count=%d owner(p)=%q", svc.PublishedCount(), svc.FactorOwner(p))
	}

	// Three-way collision over a single modulus with different exponents.
	svc2 := NewService()
	n := modulusFrom(p, q)
	mk := func(id string, e int64) ImportRequest {
		return ImportRequest{KeyID: id, N: n, E: big.NewInt(e), Factors: []*big.Int{p, q}}
	}
	// The same exponent is fine: the collision under test is factor reuse,
	// and testE was already proven valid against p and q at generation time.
	_, err = svc2.ImportBatch([]ImportRequest{mk("a", testE), mk("b", testE), mk("c", testE)})
	be = batchErr(t, err)
	if !errors.Is(be.Results[1].Err, ErrFactorConflict) || !errors.Is(be.Results[2].Err, ErrFactorConflict) {
		t.Fatalf("three-way collision results: %+v", be.Results)
	}
	if svc2.PublishedCount() != 0 {
		t.Fatalf("three-way collision batch published keys")
	}

	// Duplicate key ID within one batch is rejected outright.
	dup1 := newKey(t, "dup", p, q)
	dup2 := newKey(t, "dup", r, s)
	_, err = svc2.ImportBatch([]ImportRequest{dup1, dup2})
	be = batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrInvalidRequest) || !errors.Is(be.Results[1].Err, ErrInvalidRequest) {
		t.Fatalf("duplicate-ID results: %+v", be.Results)
	}
}

// TestConcurrentContention races many goroutines trying to import keys built
// from a shared pool of primes. Batches are serialized by the service mutex,
// so exactly one goroutine per contested prime pair can win; run with -race to
// exercise the locking.
func TestConcurrentContention(t *testing.T) {
	svc := NewService()

	// Three disjoint prime pairs, each contested by a group of goroutines.
	// Within a group every request uses the same pair, so exactly one
	// goroutine per group can be admitted and every loser must see a conflict.
	pairs := [][2]*big.Int{
		{genPrime(t, 64), genPrime(t, 64)},
		{genPrime(t, 64), genPrime(t, 64)},
		{genPrime(t, 64), genPrime(t, 64)},
	}

	const perGroup = 12
	goroutines := perGroup * len(pairs)
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	winCh := make(chan string, goroutines)
	wg.Add(goroutines)
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			group := g % len(pairs)
			id := fmt.Sprintf("g-%d-%02d", group, g/len(pairs))
			req := newKey(t, id, pairs[group][0], pairs[group][1])
			results, err := svc.ImportBatch([]ImportRequest{req})
			if err != nil {
				var be *BatchError
				if !errors.As(err, &be) || !errors.Is(be.Results[0].Err, ErrFactorConflict) {
					errCh <- fmt.Errorf("goroutine %d: unexpected error %v", g, err)
					return
				}
			} else if results[0].Replayed {
				errCh <- fmt.Errorf("goroutine %d: fresh key reported as replay", g)
				return
			} else {
				winCh <- id
			}
			errCh <- nil
		}(g)
	}
	close(start)
	wg.Wait()
	close(errCh)
	close(winCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	// Exactly one winner per group.
	winners := make([]string, 0, len(pairs))
	for id := range winCh {
		winners = append(winners, id)
	}
	if len(winners) != len(pairs) || svc.PublishedCount() != len(pairs) {
		t.Fatalf("want %d admitted keys, got winners=%v count=%d", len(pairs), winners, svc.PublishedCount())
	}
	for group, pair := range pairs {
		ownerA := svc.FactorOwner(pair[0])
		ownerB := svc.FactorOwner(pair[1])
		if ownerA == "" || ownerA != ownerB {
			t.Fatalf("group %d factor ownership inconsistent: %q vs %q", group, ownerA, ownerB)
		}
	}

	// After the race settles, replaying an admitted key stays idempotent while
	// a loser retrying under a new ID is still blocked on the same factors.
	owner := svc.FactorOwner(pairs[0][0])
	n, e, ok := svc.PublishedKey(owner)
	if !ok {
		t.Fatalf("owner %q not found in index", owner)
	}
	results, err := svc.ImportBatch([]ImportRequest{{
		KeyID:   owner,
		N:       n,
		E:       e,
		Factors: []*big.Int{pairs[0][0], pairs[0][1]},
	}})
	if err != nil || !results[0].Replayed {
		t.Fatalf("post-race replay failed: results=%+v err=%v", results, err)
	}

	loser := newKey(t, "late-loser", pairs[2][0], pairs[2][1])
	_, err = svc.ImportBatch([]ImportRequest{loser})
	be := batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrFactorConflict) {
		t.Fatalf("post-race loser result = %v", be.Results[0].Err)
	}
}

// TestSplitFactorsAcrossOldKeys covers the case where the two prime factors of
// a new modulus were each published by a different, older key. Neither factor
// alone equals an old modulus, so only the per-factor index can catch it.
func TestSplitFactorsAcrossOldKeys(t *testing.T) {
	svc := NewService()
	p, q, r, s := genPrime(t, 64), genPrime(t, 64), genPrime(t, 64), genPrime(t, 64)

	// Publish two disjoint keys: old-1 owns {p, q}, old-2 owns {r, s}.
	if _, err := svc.ImportBatch([]ImportRequest{
		newKey(t, "old-1", p, q),
		newKey(t, "old-2", r, s),
	}); err != nil {
		t.Fatalf("seed batch failed: %v", err)
	}

	// New modulus p*r: each factor is owned by a different previously
	// published key.
	split := newKey(t, "split", p, r)
	_, err := svc.ImportBatch([]ImportRequest{split})
	be := batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrFactorConflict) {
		t.Fatalf("want ErrFactorConflict, got %v", be.Results[0].Err)
	}
	if be.Results[0].ConflictKeyID != "old-1" {
		t.Fatalf("first conflicting owner = %q, want old-1", be.Results[0].ConflictKeyID)
	}

	// Same scenario inside a fresh batch, mixed with one clean item: the whole
	// batch, clean item included, must roll back.
	clean := newKey(t, "clean", genPrime(t, 64, p, q, r, s), genPrime(t, 64, p, q, r, s))
	_, err = svc.ImportBatch([]ImportRequest{clean, split})
	be = batchErr(t, err)
	if be.Results[0].Err != nil {
		t.Fatalf("clean item failed dry run: %v", be.Results[0].Err)
	}
	if !errors.Is(be.Results[1].Err, ErrFactorConflict) {
		t.Fatalf("split item result = %v", be.Results[1].Err)
	}
	if svc.PublishedCount() != 2 {
		t.Fatalf("atomicity broken: count=%d, want 2", svc.PublishedCount())
	}
	if _, _, ok := svc.PublishedKey("clean"); ok {
		t.Fatal("clean item from rejected batch leaked into the index")
	}
}

// TestRejectedBatchLeavesIndexUntouched snapshots the full published state
// around a rejected batch and asserts it is byte-for-byte unchanged.
func TestRejectedBatchLeavesIndexUntouched(t *testing.T) {
	svc := NewService()
	p, q, r := genPrime(t, 64), genPrime(t, 64), genPrime(t, 64)
	if _, err := svc.ImportBatch([]ImportRequest{newKey(t, "seed", p, q)}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	snapshotN, snapshotE, ok := svc.PublishedKey("seed")
	if !ok {
		t.Fatal("seed key missing")
	}

	bad := []ImportRequest{
		newKey(t, "collides", p, r), // shares p with seed
		newKey(t, "also-bad", q, r), // shares q with seed
	}
	results, err := svc.ImportBatch(bad)
	if err == nil || results != nil {
		t.Fatalf("expected rejection, got results=%v err=%v", results, err)
	}

	afterN, afterE, ok := svc.PublishedKey("seed")
	if !ok || afterN.Cmp(snapshotN) != 0 || afterE.Cmp(snapshotE) != 0 {
		t.Fatal("seeded key changed after rejected batch")
	}
	if svc.PublishedCount() != 1 {
		t.Fatalf("count = %d, want 1", svc.PublishedCount())
	}
	if owner := svc.FactorOwner(r); owner != "" {
		t.Fatalf("unpublished factor r leaked with owner %q", owner)
	}
}

// TestInvalidRequests exercises per-item validation failures.
func TestInvalidRequests(t *testing.T) {
	svc := NewService()
	p, q, r := genPrime(t, 64), genPrime(t, 64), genPrime(t, 64)
	n := modulusFrom(p, q)

	good := newKey(t, "good", p, q)
	bigP, bigQ := genPrime(t, 128), genPrime(t, 128)
	bigN := modulusFrom(bigP, bigQ)
	cases := []struct {
		name string
		req  ImportRequest
	}{
		{"empty id", ImportRequest{KeyID: "", N: bigN, E: big.NewInt(testE), Factors: []*big.Int{bigP, bigQ}}},
		{"single factor", ImportRequest{KeyID: "x", N: bigP, E: big.NewInt(testE), Factors: []*big.Int{bigP}}},
		{"composite factor", ImportRequest{KeyID: "x", N: modulusFrom(p, q, r), E: big.NewInt(testE), Factors: []*big.Int{p, modulusFrom(q, r)}}},
		{"wrong product", ImportRequest{KeyID: "x", N: n, E: big.NewInt(testE), Factors: []*big.Int{p, genPrime(t, 64, p, q)}}},
		{"repeated factor", ImportRequest{KeyID: "x", N: modulusFrom(p, p, bigP), E: big.NewInt(testE), Factors: []*big.Int{p, p, bigP}}},
		{"bad exponent", ImportRequest{KeyID: "x", N: n, E: invalidExponent(t, p), Factors: []*big.Int{p, q}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ImportBatch([]ImportRequest{good, tc.req})
			be := batchErr(t, err)
			if !errors.Is(be.Results[1].Err, ErrInvalidRequest) {
				t.Fatalf("want ErrInvalidRequest, got %v", be.Results[1].Err)
			}
			if be.Results[0].Err != nil {
				t.Fatalf("good item was marked failed: %v", be.Results[0].Err)
			}
			if svc.PublishedCount() != 0 {
				t.Fatalf("invalid batch published state: count=%d", svc.PublishedCount())
			}
		})
	}

	// The good item alone still admits, proving rollback left it untouched.
	if _, err := svc.ImportBatch([]ImportRequest{good}); err != nil {
		t.Fatalf("clean import failed: %v", err)
	}
	if svc.PublishedCount() != 1 {
		t.Fatalf("count = %d, want 1", svc.PublishedCount())
	}
}

// TestMultiPrimeKey admits a modulus with three distinct prime factors and
// then blocks any key touching any one of them.
func TestMultiPrimeKey(t *testing.T) {
	svc := NewService()
	p, q, r := genPrime(t, 64), genPrime(t, 64), genPrime(t, 64)
	tri := ImportRequest{
		KeyID:   "tri",
		N:       modulusFrom(p, q, r),
		E:       big.NewInt(testE),
		Factors: []*big.Int{p, q, r},
	}
	results, err := svc.ImportBatch([]ImportRequest{tri})
	if err != nil {
		t.Fatalf("multi-prime import failed: %v", err)
	}
	if results[0].Replayed {
		t.Fatal("fresh key reported as replay")
	}
	for _, prime := range []*big.Int{p, q, r} {
		if owner := svc.FactorOwner(prime); owner != "tri" {
			t.Fatalf("factor owner = %q, want tri", owner)
		}
	}

	s := genPrime(t, 64, p, q, r)
	_, err = svc.ImportBatch([]ImportRequest{newKey(t, "touch", r, s)})
	be := batchErr(t, err)
	if !errors.Is(be.Results[0].Err, ErrFactorConflict) || be.Results[0].ConflictKeyID != "tri" {
		t.Fatalf("multi-prime conflict result = %+v", be.Results[0])
	}
}
