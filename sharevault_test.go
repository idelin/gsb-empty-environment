package sharevault

import (
	"crypto/rand"
	"errors"
	"testing"
)

// seqReader is a deterministic io.Reader for reproducible tests.
type seqReader struct{ state uint64 }

func (r *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		r.state = r.state*6364136223846793005 + 1442695040888963407
		p[i] = byte(r.state >> 33)
	}
	return len(p), nil
}

func makeShares(t *testing.T, secret uint16, threshold int, generation uint64, n int) []Share {
	t.Helper()
	xs := make([]uint16, n)
	for i := range xs {
		xs[i] = uint16(i + 1)
	}
	shares, err := Split(rand.Reader, secret, threshold, generation, xs)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	return shares
}

func TestFieldArithmetic(t *testing.T) {
	if got := feAdd(256, 1); got != 0 {
		t.Fatalf("256 + 1 = %d, want 0", got)
	}
	if got := feSub(0, 1); got != 256 {
		t.Fatalf("0 - 1 = %d, want 256", got)
	}
	for _, a := range []uint16{1, 2, 100, 255, 256} {
		inv, ok := feInv(a)
		if !ok {
			t.Fatalf("feInv(%d) failed", a)
		}
		if got := feMul(a, inv); got != 1 {
			t.Fatalf("%d * %d^-1 = %d, want 1", a, a, got)
		}
	}
	if _, ok := feInv(0); ok {
		t.Fatal("feInv(0) should fail")
	}
}

func TestSplitReconstruct(t *testing.T) {
	shares := makeShares(t, 42, 3, 1, 5)
	for _, subset := range [][]int{{0, 1, 2}, {1, 3, 4}, {0, 2, 3, 4}, {0, 1, 2, 3, 4}} {
		var picked []Share
		for _, i := range subset {
			picked = append(picked, shares[i])
		}
		secret, err := Reconstruct(picked)
		if err != nil {
			t.Fatalf("Reconstruct(%v): %v", subset, err)
		}
		if secret != 42 {
			t.Fatalf("Reconstruct(%v) = %d, want 42", subset, secret)
		}
	}
}

func TestSplitRejectsDuplicateX(t *testing.T) {
	_, err := Split(rand.Reader, 7, 2, 1, []uint16{1, 2, 2})
	if !errors.Is(err, ErrDuplicateX) {
		t.Fatalf("Split with duplicate x: %v, want ErrDuplicateX", err)
	}
	if _, err := Split(rand.Reader, 7, 2, 1, []uint16{0, 1}); !errors.Is(err, ErrInvalidCoordinate) {
		t.Fatalf("Split with x=0: %v, want ErrInvalidCoordinate", err)
	}
}

func TestReconstructRejectsDuplicateX(t *testing.T) {
	shares := makeShares(t, 9, 2, 1, 3)
	dup := []Share{shares[0], shares[1], {X: shares[0].X, Y: 5, Generation: 1}}
	if _, err := Reconstruct(dup); !errors.Is(err, ErrDuplicateX) {
		t.Fatalf("Reconstruct with duplicate x: %v, want ErrDuplicateX", err)
	}
}

func TestReconstructRejectsCrossGenerationSplice(t *testing.T) {
	oldShares := makeShares(t, 77, 3, 1, 5)
	newShares := refreshAll(t, oldShares, 3)
	spliced := []Share{oldShares[0], oldShares[1], newShares[2]}
	if _, err := Reconstruct(spliced); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("Reconstruct of spliced generations: %v, want ErrGenerationMismatch", err)
	}
}

func refreshAll(t *testing.T, old []Share, threshold int) []Share {
	t.Helper()
	session, err := NewRefreshSession(old, threshold)
	if err != nil {
		t.Fatalf("NewRefreshSession: %v", err)
	}
	for _, x := range session.Xs() {
		c, err := GenerateContribution(rand.Reader, session.Generation(), x, session.Xs(), threshold)
		if err != nil {
			t.Fatalf("GenerateContribution: %v", err)
		}
		if err := session.AddContribution(c); err != nil {
			t.Fatalf("AddContribution: %v", err)
		}
	}
	digest := session.Digest()
	for _, x := range session.Xs() {
		if err := session.Confirm(x, digest); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
	}
	shares, err := session.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return shares
}

func TestRefreshPreservesSecretAcrossGenerations(t *testing.T) {
	oldShares := makeShares(t, 200, 3, 1, 5)
	newShares := refreshAll(t, oldShares, 3)

	for _, sh := range newShares {
		if sh.Generation != 2 {
			t.Fatalf("new share generation = %d, want 2", sh.Generation)
		}
	}
	// Both generations independently recover the same secret.
	if secret, err := Reconstruct(oldShares[:3]); err != nil || secret != 200 {
		t.Fatalf("old generation reconstruct = %d, %v; want 200", secret, err)
	}
	if secret, err := Reconstruct(newShares[2:5]); err != nil || secret != 200 {
		t.Fatalf("new generation reconstruct = %d, %v; want 200", secret, err)
	}
	// The refresh actually re-randomized the shares.
	changed := false
	for i := range oldShares {
		if oldShares[i].Y != newShares[i].Y {
			changed = true
		}
	}
	if !changed {
		t.Fatal("refresh did not change any share values")
	}
}

func TestRefreshRejectsCrossGenerationContribution(t *testing.T) {
	oldShares := makeShares(t, 5, 2, 7, 4)
	session, err := NewRefreshSession(oldShares, 2)
	if err != nil {
		t.Fatalf("NewRefreshSession: %v", err)
	}
	c, err := GenerateContribution(rand.Reader, 8, session.Xs()[0], session.Xs(), 2)
	if err != nil {
		t.Fatalf("GenerateContribution: %v", err)
	}
	if err := session.AddContribution(c); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("AddContribution wrong generation: %v, want ErrGenerationMismatch", err)
	}
}

func TestRefreshRejectsDuplicateContribution(t *testing.T) {
	oldShares := makeShares(t, 5, 2, 1, 3)
	session, _ := NewRefreshSession(oldShares, 2)
	x := session.Xs()[0]
	c, err := GenerateContribution(rand.Reader, 1, x, session.Xs(), 2)
	if err != nil {
		t.Fatalf("GenerateContribution: %v", err)
	}
	if err := session.AddContribution(c); err != nil {
		t.Fatalf("AddContribution: %v", err)
	}
	if err := session.AddContribution(c); !errors.Is(err, ErrDuplicateContribution) {
		t.Fatalf("duplicate AddContribution: %v, want ErrDuplicateContribution", err)
	}
	c.From = 200
	if err := session.AddContribution(c); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("unknown contributor: %v, want ErrUnknownParticipant", err)
	}
}

func TestRefreshRejectsNonZeroConstant(t *testing.T) {
	oldShares := makeShares(t, 5, 3, 1, 4)
	session, _ := NewRefreshSession(oldShares, 3)
	xs := session.Xs()
	// Polynomial 7 + x + x^2 has a non-zero constant term.
	coeffs := []uint16{7, 1, 1}
	c := &Contribution{Generation: 1, From: xs[0]}
	for _, x := range xs {
		c.Deltas = append(c.Deltas, Delta{X: x, Y: evalPoly(coeffs, x)})
	}
	if err := session.AddContribution(c); !errors.Is(err, ErrNonZeroConstant) {
		t.Fatalf("AddContribution non-zero constant: %v, want ErrNonZeroConstant", err)
	}
}

func TestRefreshRejectsInconsistentDeltas(t *testing.T) {
	oldShares := makeShares(t, 5, 2, 1, 4)
	session, _ := NewRefreshSession(oldShares, 2)
	xs := session.Xs()
	c, err := GenerateContribution(rand.Reader, 1, xs[0], xs, 2)
	if err != nil {
		t.Fatalf("GenerateContribution: %v", err)
	}
	// Tamper with a witness point beyond the interpolation basis.
	c.Deltas[3].Y = feAdd(c.Deltas[3].Y, 1)
	if err := session.AddContribution(c); !errors.Is(err, ErrInconsistentDeltas) {
		t.Fatalf("AddContribution tampered delta: %v, want ErrInconsistentDeltas", err)
	}
}

func TestConfirmRejectsWrongDigest(t *testing.T) {
	oldShares := makeShares(t, 5, 2, 1, 3)
	session, _ := NewRefreshSession(oldShares, 2)
	var bad [32]byte
	bad[0] = 0xff
	if err := session.Confirm(session.Xs()[0], bad); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Confirm with wrong digest: %v, want ErrDigestMismatch", err)
	}
	if err := session.Confirm(250, session.Digest()); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("Confirm from unknown participant: %v, want ErrUnknownParticipant", err)
	}
}

func TestInterruptedRefreshPublishesNothing(t *testing.T) {
	oldShares := makeShares(t, 123, 3, 1, 5)
	session, _ := NewRefreshSession(oldShares, 3)
	xs := session.Xs()

	// Interruption 1: only some contributions arrive.
	for _, x := range xs[:3] {
		c, err := GenerateContribution(rand.Reader, 1, x, xs, 3)
		if err != nil {
			t.Fatalf("GenerateContribution: %v", err)
		}
		if err := session.AddContribution(c); err != nil {
			t.Fatalf("AddContribution: %v", err)
		}
	}
	digest := session.Digest()
	for _, x := range xs {
		if err := session.Confirm(x, digest); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
	}
	if shares, err := session.Commit(); !errors.Is(err, ErrIncompleteRound) || shares != nil {
		t.Fatalf("Commit with missing contributions = %v, %v; want nil, ErrIncompleteRound", shares, err)
	}

	// Interruption 2: remaining contributions arrive (digest changes,
	// stale confirmations no longer count) but one receiver never confirms.
	for _, x := range xs[3:] {
		c, err := GenerateContribution(rand.Reader, 1, x, xs, 3)
		if err != nil {
			t.Fatalf("GenerateContribution: %v", err)
		}
		if err := session.AddContribution(c); err != nil {
			t.Fatalf("AddContribution: %v", err)
		}
	}
	newDigest := session.Digest()
	if newDigest == digest {
		t.Fatal("digest did not change after late contributions")
	}
	for _, x := range xs[:4] {
		if err := session.Confirm(x, newDigest); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
	}
	if shares, err := session.Commit(); !errors.Is(err, ErrMissingConfirmation) || shares != nil {
		t.Fatalf("Commit with missing confirmation = %v, %v; want nil, ErrMissingConfirmation", shares, err)
	}

	// No half-published set exists: the old generation still recovers
	// the secret and the session never produced new shares.
	if secret, err := Reconstruct(oldShares[:3]); err != nil || secret != 123 {
		t.Fatalf("old generation reconstruct = %d, %v; want 123", secret, err)
	}

	// Once the final confirmation arrives, the round completes atomically.
	if err := session.Confirm(xs[4], newDigest); err != nil {
		t.Fatalf("final Confirm: %v", err)
	}
	newShares, err := session.Commit()
	if err != nil {
		t.Fatalf("Commit after full confirmation: %v", err)
	}
	if len(newShares) != len(xs) {
		t.Fatalf("Commit returned %d shares, want %d", len(newShares), len(xs))
	}
	if secret, err := Reconstruct(newShares[:3]); err != nil || secret != 123 {
		t.Fatalf("new generation reconstruct = %d, %v; want 123", secret, err)
	}
	if _, err := session.Commit(); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("second Commit: %v, want ErrAlreadyCommitted", err)
	}
}

func TestDigestIsOrderIndependent(t *testing.T) {
	oldShares := makeShares(t, 5, 2, 1, 4)
	xs := []uint16{1, 2, 3, 4}

	build := func(order []int) *RefreshSession {
		session, err := NewRefreshSession(oldShares, 2)
		if err != nil {
			t.Fatalf("NewRefreshSession: %v", err)
		}
		for _, i := range order {
			rng := &seqReader{state: uint64(i + 1)}
			c, err := GenerateContribution(rng, 1, xs[i], xs, 2)
			if err != nil {
				t.Fatalf("GenerateContribution: %v", err)
			}
			if err := session.AddContribution(c); err != nil {
				t.Fatalf("AddContribution: %v", err)
			}
		}
		return session
	}
	a := build([]int{0, 1, 2, 3})
	b := build([]int{3, 1, 0, 2})
	if a.Digest() != b.Digest() {
		t.Fatal("digest depends on contribution arrival order")
	}
}
