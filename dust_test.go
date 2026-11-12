package netsec

import (
	"errors"
	"testing"
)

func TestHtlcFeesRoundDown(t *testing.T) {
	// 2500 * 663 / 1000 = 1657.5 -> 1657; 2500 * 703 / 1000 = 1757.5 -> 1757.
	if got := HtlcTimeoutFee(2500); got != 1657 {
		t.Fatalf("HtlcTimeoutFee(2500) = %d, want 1657", got)
	}
	if got := HtlcSuccessFee(2500); got != 1757 {
		t.Fatalf("HtlcSuccessFee(2500) = %d, want 1757", got)
	}
	if got := HtlcTimeoutFee(0); got != 0 {
		t.Fatalf("HtlcTimeoutFee(0) = %d, want 0", got)
	}
}

// An HTLC whose amount equals dust_limit + fee exactly is NOT dust; only
// strictly smaller amounts are trimmed. Checked for both directions on both
// commitment views.
func TestExactlyAtThresholdIsNotDust(t *testing.T) {
	const (
		dustLimit = 546
		feerate   = 2500 // timeout fee 1657, success fee 1757
	)
	timeoutThreshold := uint64(546 + 1657)
	successThreshold := uint64(546 + 1757)

	cases := []struct {
		name      string
		h         Htlc
		threshold uint64
		isDust    func(Htlc, uint64, uint64) bool
	}{
		{"local outgoing", Htlc{Amount: 0, Outgoing: true}, timeoutThreshold, dustOnLocalCommitment},
		{"local incoming", Htlc{Amount: 0, Outgoing: false}, successThreshold, dustOnLocalCommitment},
		{"remote outgoing", Htlc{Amount: 0, Outgoing: true}, successThreshold, dustOnRemoteCommitment},
		{"remote incoming", Htlc{Amount: 0, Outgoing: false}, timeoutThreshold, dustOnRemoteCommitment},
	}
	for _, tc := range cases {
		at := tc.h
		at.Amount = tc.threshold
		if tc.isDust(at, dustLimit, feerate) {
			t.Fatalf("%s: amount == threshold (%d) must not be dust", tc.name, tc.threshold)
		}
		below := tc.h
		below.Amount = tc.threshold - 1
		if !tc.isDust(below, dustLimit, feerate) {
			t.Fatalf("%s: amount == threshold-1 (%d) must be dust", tc.name, tc.threshold-1)
		}
	}

	// Through the public API with max exposure 0: HTLCs exactly at the
	// strictest (success-fee) threshold are not dust on either view and are
	// accepted; one satoshi below is dust on at least one view and rejected.
	tr := NewDustTracker(dustLimit, dustLimit, 0, feerate)
	for i, outgoing := range []bool{true, false} {
		h := Htlc{ID: uint64(i + 1), Amount: successThreshold, Outgoing: outgoing}
		if err := tr.AddHtlc(h); err != nil {
			t.Fatalf("AddHtlc(%+v) at exact threshold rejected: %v", h, err)
		}
	}
	if local, remote := tr.Exposure(); local != 0 || remote != 0 {
		t.Fatalf("exposure = (%d, %d), want (0, 0)", local, remote)
	}
	for i, outgoing := range []bool{true, false} {
		h := Htlc{ID: uint64(i + 3), Amount: successThreshold - 1, Outgoing: outgoing}
		if err := tr.AddHtlc(h); !errors.Is(err, ErrDustExposureExceeded) {
			t.Fatalf("AddHtlc(%+v) one sat below threshold = %v, want ErrDustExposureExceeded", h, err)
		}
	}
}

func TestAddHtlcExposureLimit(t *testing.T) {
	// feerate 0: threshold is just the dust limit, so every HTLC below
	// 546 sat is dust.
	tr := NewDustTracker(546, 546, 1000, 0)

	if err := tr.AddHtlc(Htlc{ID: 1, Amount: 400, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc(1): %v", err)
	}
	if err := tr.AddHtlc(Htlc{ID: 2, Amount: 400, Outgoing: false}); err != nil {
		t.Fatalf("AddHtlc(2): %v", err)
	}
	// 400 + 400 + 400 = 1200 > 1000 on both commitments.
	err := tr.AddHtlc(Htlc{ID: 3, Amount: 400, Outgoing: true})
	if !errors.Is(err, ErrDustExposureExceeded) {
		t.Fatalf("AddHtlc(3) = %v, want ErrDustExposureExceeded", err)
	}
	// The rejected HTLC must not be recorded.
	if local, remote := tr.Exposure(); local != 800 || remote != 800 {
		t.Fatalf("exposure = (%d, %d), want (800, 800)", local, remote)
	}

	// Settling one HTLC frees room for the next.
	tr.RemoveHtlc(1)
	if err := tr.AddHtlc(Htlc{ID: 3, Amount: 400, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc(3) after RemoveHtlc(1): %v", err)
	}
}

// A fee increase turns a previously non-dust HTLC into dust; the update must
// be rejected and the old feerate retained.
func TestUpdateFeeMakesExistingHtlcDust(t *testing.T) {
	const dustLimit = 546
	// At feerate 1000: timeout fee 663, threshold 1209. 1500 sat is safe.
	tr := NewDustTracker(dustLimit, dustLimit, 1000, 1000)
	if err := tr.AddHtlc(Htlc{ID: 1, Amount: 1500, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc: %v", err)
	}
	if local, remote := tr.Exposure(); local != 0 || remote != 0 {
		t.Fatalf("exposure = (%d, %d), want (0, 0)", local, remote)
	}

	// At feerate 5000: timeout fee 3315, threshold 3861 -> 1500 sat is dust,
	// and 1500 > max exposure 1000 on both commitments.
	if err := tr.UpdateFee(5000); !errors.Is(err, ErrDustExposureExceeded) {
		t.Fatalf("UpdateFee(5000) = %v, want ErrDustExposureExceeded", err)
	}
	if got := tr.FeeratePerKw(); got != 1000 {
		t.Fatalf("feerate after rejected update = %d, want 1000", got)
	}

	// A moderate increase that keeps the HTLC non-dust on both views is
	// accepted. Feerate 1300: timeout fee 861 -> local threshold 1407;
	// success fee 913 -> remote threshold 1459; both <= 1500.
	if err := tr.UpdateFee(1300); err != nil {
		t.Fatalf("UpdateFee(1300): %v", err)
	}
	if got := tr.FeeratePerKw(); got != 1300 {
		t.Fatalf("feerate = %d, want 1300", got)
	}

	// With a larger allowance the same jump to 5000 is accepted and the
	// HTLC now counts as dust on both commitments.
	tr2 := NewDustTracker(dustLimit, dustLimit, 2000, 1000)
	if err := tr2.AddHtlc(Htlc{ID: 1, Amount: 1500, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc: %v", err)
	}
	if err := tr2.UpdateFee(5000); err != nil {
		t.Fatalf("UpdateFee(5000) with max 2000: %v", err)
	}
	if local, remote := tr2.Exposure(); local != 1500 || remote != 1500 {
		t.Fatalf("exposure = (%d, %d), want (1500, 1500)", local, remote)
	}
}

// The two commitment transactions disagree: the remote commitment uses the
// remote dust limit and the reversed direction.
func TestDivergentCommitmentViews(t *testing.T) {
	const (
		localDust  = 546
		remoteDust = 2000
		feerate    = 1000 // timeout fee 663, success fee 703
	)
	tr := NewDustTracker(localDust, remoteDust, 1000, feerate)

	// Outgoing 1500 sat: on the local commitment the threshold is
	// 546+663=1209 (not dust); on the remote commitment it is a received
	// HTLC with threshold 2000+703=2703 (dust).
	err := tr.AddHtlc(Htlc{ID: 1, Amount: 1500, Outgoing: true})
	var de *DustExposureError
	if !errors.As(err, &de) || de.Commitment != "remote" {
		t.Fatalf("AddHtlc(outgoing 1500) = %v, want remote DustExposureError", err)
	}

	// Incoming 1500 sat: locally 546+703=1249 (not dust); on the remote
	// commitment it is offered with threshold 2000+663=2663 (dust).
	err = tr.AddHtlc(Htlc{ID: 2, Amount: 1500, Outgoing: false})
	if !errors.As(err, &de) || de.Commitment != "remote" {
		t.Fatalf("AddHtlc(incoming 1500) = %v, want remote DustExposureError", err)
	}

	// Mirror image: with the dust limits swapped the local commitment is
	// the one that exceeds.
	tr2 := NewDustTracker(remoteDust, localDust, 1000, feerate)
	err = tr2.AddHtlc(Htlc{ID: 1, Amount: 1500, Outgoing: false})
	if !errors.As(err, &de) || de.Commitment != "local" {
		t.Fatalf("AddHtlc(incoming 1500) swapped limits = %v, want local DustExposureError", err)
	}

	// With enough allowance the asymmetric exposure is visible per view.
	tr3 := NewDustTracker(localDust, remoteDust, 5000, feerate)
	if err := tr3.AddHtlc(Htlc{ID: 1, Amount: 1500, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc: %v", err)
	}
	if local, remote := tr3.Exposure(); local != 0 || remote != 1500 {
		t.Fatalf("exposure = (%d, %d), want (0, 1500)", local, remote)
	}
}

// update_fee is rejected when only one of the two views exceeds the limit.
func TestUpdateFeeDivergentViews(t *testing.T) {
	const (
		localDust  = 546
		remoteDust = 2000
		max        = 2500
	)
	// At feerate 1000 (timeout fee 663, success fee 703):
	//  - incoming 2700: local threshold 546+703=1249, remote 2000+663=2663
	//    -> not dust on either view.
	//  - incoming 1500: dust only on the remote view -> remote exposure 1500.
	tr := NewDustTracker(localDust, remoteDust, max, 1000)
	if err := tr.AddHtlc(Htlc{ID: 1, Amount: 2700, Outgoing: false}); err != nil {
		t.Fatalf("AddHtlc(2700): %v", err)
	}
	if err := tr.AddHtlc(Htlc{ID: 2, Amount: 1500, Outgoing: false}); err != nil {
		t.Fatalf("AddHtlc(1500): %v", err)
	}
	if local, remote := tr.Exposure(); local != 0 || remote != 1500 {
		t.Fatalf("exposure = (%d, %d), want (0, 1500)", local, remote)
	}

	// At feerate 2000 the remote threshold becomes 2000+1326=3326, so the
	// 2700 sat HTLC turns to dust only on the remote view: remote exposure
	// 4200 > 2500 while local exposure is just 1500. The update is rejected
	// because of the remote commitment alone.
	var de *DustExposureError
	err := tr.UpdateFee(2000)
	if !errors.As(err, &de) || de.Commitment != "remote" {
		t.Fatalf("UpdateFee(2000) = %v, want remote DustExposureError", err)
	}
	if got := tr.FeeratePerKw(); got != 1000 {
		t.Fatalf("feerate after rejected update = %d, want 1000", got)
	}

	// Mirror image with swapped dust limits and outgoing HTLCs: now the
	// local commitment is the one that exceeds.
	tr2 := NewDustTracker(remoteDust, localDust, max, 1000)
	if err := tr2.AddHtlc(Htlc{ID: 1, Amount: 2700, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc(2700): %v", err)
	}
	if err := tr2.AddHtlc(Htlc{ID: 2, Amount: 1500, Outgoing: true}); err != nil {
		t.Fatalf("AddHtlc(1500): %v", err)
	}
	err = tr2.UpdateFee(2000)
	if !errors.As(err, &de) || de.Commitment != "local" {
		t.Fatalf("UpdateFee(2000) swapped = %v, want local DustExposureError", err)
	}
}
