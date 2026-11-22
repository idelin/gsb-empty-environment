package refresh_test

import (
	"math/big"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

// interpolate recovers f(0) from len(points) points (x_i, y_i) via Lagrange
// interpolation in the field. It works for any subset of >= k points, which is
// exactly the threshold property the refresh must preserve.
func interpolate(f *refresh.Field, xs, ys []*big.Int) *big.Int {
	secret := big.NewInt(0)
	for i := range xs {
		num, den := big.NewInt(1), big.NewInt(1)
		for j := range xs {
			if i == j {
				continue
			}
			num = f.Mul(num, f.Neg(xs[j]))
			den = f.Mul(den, f.Sub(xs[i], xs[j]))
		}
		term := f.Mul(ys[i], f.Mul(num, new(big.Int).ModInverse(den, f.P())))
		secret = f.Add(secret, term)
	}
	return secret
}

func TestEndToEndRefreshPreservesSecret(t *testing.T) {
	h := standardHarness(t)
	secret := big.NewInt(42)
	master := masterPolynomial(h, secret)
	store := refresh.NewPausableStore()

	// Seed every recipient with its share of the original master polynomial.
	for _, r := range h.recipients {
		share := refresh.StoredShare{
			Round:       0,
			RecipientID: r,
			Scalar:      h.field.Eval(master, h.coord(t, r)),
		}
		if err := store.PutCurrent(share); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Run three refresh rounds back to back, each with fresh zero-constant
	// polynomials and full public verification before any share is rewritten.
	var masterCommitment []refresh.Point
	for round := uint64(1); round <= 3; round++ {
		h.round = round
		h.rollPolynomials(t)
		h.digest = h.transcriptDigest(t)

		res, err := h.engine.VerifyPublicEvidence(refresh.PublicGateConfig{
			Round:            round,
			Threshold:        h.threshold,
			OldMaster:        masterCommitmentOrIdentity(h, masterCommitment, secret),
			ExpectedConstant: h.expectedConstant(secret),
			SenderIDs:        h.senders,
			RecipientIDs:     h.recipients,
			Views:            h.healthyViews(),
		}, h.healthyBundle(t))
		if err != nil {
			t.Fatalf("round %d verify: %v", round, err)
		}
		if !res.Decision.Allowed {
			t.Fatalf("round %d blocked: %v", round, res.Decision.Reasons)
		}

		for _, r := range h.recipients {
			req := commitRequest(t, h, store, r)
			result, err := h.engine.StageAndCommit(store, req)
			if err != nil {
				t.Fatalf("round %d commit %s: %v", round, r, err)
			}
			if result.CurrentShare.Round != round {
				t.Fatalf("share round = %d, want %d", result.CurrentShare.Round, round)
			}
		}
		masterCommitment = h.oldMasterCommitment(secret)
	}

	// Any k of the refreshed shares reconstruct the original secret.
	xs := make([]*big.Int, 0, h.threshold)
	ys := make([]*big.Int, 0, h.threshold)
	for _, r := range h.recipients[:h.threshold] {
		share, ok, err := store.Current(r)
		if err != nil || !ok {
			t.Fatalf("load share %s: %v", r, err)
		}
		xs = append(xs, h.coord(t, r))
		ys = append(ys, share.Scalar)
	}
	recovered := interpolate(h.field, xs, ys)
	if recovered.Cmp(secret) != 0 {
		t.Fatalf("reconstructed secret = %v, want %v", recovered, secret)
	}
}

func masterCommitmentOrIdentity(h *harness, existing []refresh.Point, secret *big.Int) []refresh.Point {
	if existing != nil {
		return existing
	}
	return h.oldMasterCommitment(secret)
}
