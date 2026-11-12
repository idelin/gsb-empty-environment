package netsec

import (
	"errors"
	"sync"
)

// ErrEpochRegression is returned when an epoch root is not newer than the
// newest epoch already verified locally.
var ErrEpochRegression = errors.New("netsec: epoch is not greater than the newest locally seen epoch")

// Verifier checks prefix-tree proofs for query callbacks and tracks the
// newest locally seen epoch so a server cannot roll a client back to a
// stale epoch root. It is safe for concurrent use.
type Verifier struct {
	// VRF is the caller-injected VRF proof verifier.
	VRF VRFVerifier

	mu        sync.Mutex
	lastEpoch uint64
	hasEpoch  bool
}

// LastEpoch returns the newest verified epoch and whether any epoch has
// been verified yet.
func (v *Verifier) LastEpoch() (epoch uint64, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastEpoch, v.hasEpoch
}

func (v *Verifier) checkEpochLocked(epoch uint64) error {
	if v.hasEpoch && epoch <= v.lastEpoch {
		return ErrEpochRegression
	}
	return nil
}

// VerifyInclusion verifies an inclusion proof against the root of the
// given epoch. The epoch must be greater than every previously verified
// epoch; it is recorded only after the proof verifies.
func (v *Verifier) VerifyInclusion(epoch uint64, root []byte, p *Proof) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkEpochLocked(epoch); err != nil {
		return nil, err
	}
	value, err := VerifyInclusion(v.VRF, root, p)
	if err != nil {
		return nil, err
	}
	v.lastEpoch, v.hasEpoch = epoch, true
	return value, nil
}

// VerifyNonInclusion verifies a non-inclusion proof against the root of
// the given epoch, with the same monotonic-epoch rule as VerifyInclusion.
func (v *Verifier) VerifyNonInclusion(epoch uint64, root []byte, p *Proof) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.checkEpochLocked(epoch); err != nil {
		return err
	}
	if err := VerifyNonInclusion(v.VRF, root, p); err != nil {
		return err
	}
	v.lastEpoch, v.hasEpoch = epoch, true
	return nil
}
