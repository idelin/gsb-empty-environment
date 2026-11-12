package netsec

import (
	"errors"
	"fmt"
)

// BOLT 3 weights for the second-level HTLC transactions of non-anchor
// commitment transactions.
const (
	HtlcTimeoutWeight uint64 = 663
	HtlcSuccessWeight uint64 = 703
)

// HtlcTimeoutFee returns the fee (satoshi) of an htlc-timeout transaction at
// the given feerate, rounded down.
func HtlcTimeoutFee(feeratePerKw uint64) uint64 {
	return feeratePerKw * HtlcTimeoutWeight / 1000
}

// HtlcSuccessFee returns the fee (satoshi) of an htlc-success transaction at
// the given feerate, rounded down.
func HtlcSuccessFee(feeratePerKw uint64) uint64 {
	return feeratePerKw * HtlcSuccessWeight / 1000
}

// ErrDustExposureExceeded is returned when an add_htlc or update_fee would
// push the dust exposure of a commitment transaction past the configured
// maximum.
var ErrDustExposureExceeded = errors.New("dust exposure limit exceeded")

// DustExposureError describes which commitment transaction would exceed the
// caller-supplied maximum dust exposure.
type DustExposureError struct {
	Commitment string // "local" or "remote"
	Exposure   uint64 // resulting dust exposure in satoshi
	Max        uint64 // configured maximum in satoshi
}

func (e *DustExposureError) Error() string {
	return fmt.Sprintf("%v: %s commitment dust exposure %d sat exceeds maximum %d sat",
		ErrDustExposureExceeded, e.Commitment, e.Exposure, e.Max)
}

func (e *DustExposureError) Unwrap() error { return ErrDustExposureExceeded }

// Htlc is a pending HTLC as seen from the local node's perspective.
type Htlc struct {
	ID       uint64
	Amount   uint64 // satoshi
	Outgoing bool   // true if offered by the local node, false if received
}

// DustTracker tracks the dust exposure of both commitment transactions of a
// non-anchor channel. It is consulted by the add_htlc and update_fee
// handlers of the channel state machine.
type DustTracker struct {
	localDustLimit  uint64
	remoteDustLimit uint64
	maxExposure     uint64
	feeratePerKw    uint64
	pending         map[uint64]Htlc
}

// NewDustTracker creates a tracker for a channel. maxExposureSat is the
// maximum total value of dust HTLCs allowed on either commitment
// transaction.
func NewDustTracker(localDustLimitSat, remoteDustLimitSat, maxExposureSat, feeratePerKw uint64) *DustTracker {
	return &DustTracker{
		localDustLimit:  localDustLimitSat,
		remoteDustLimit: remoteDustLimitSat,
		maxExposure:     maxExposureSat,
		feeratePerKw:    feeratePerKw,
		pending:         make(map[uint64]Htlc),
	}
}

// dustOnLocalCommitment reports whether h is trimmed from the local node's
// commitment transaction: offered HTLCs are dust below
// dust_limit + htlc_timeout_fee, received HTLCs below
// dust_limit + htlc_success_fee.
func dustOnLocalCommitment(h Htlc, dustLimit, feeratePerKw uint64) bool {
	if h.Outgoing {
		return h.Amount < dustLimit+HtlcTimeoutFee(feeratePerKw)
	}
	return h.Amount < dustLimit+HtlcSuccessFee(feeratePerKw)
}

// dustOnRemoteCommitment reports whether h is trimmed from the remote node's
// commitment transaction, where the direction is reversed and the remote
// dust limit applies.
func dustOnRemoteCommitment(h Htlc, dustLimit, feeratePerKw uint64) bool {
	if h.Outgoing {
		return h.Amount < dustLimit+HtlcSuccessFee(feeratePerKw)
	}
	return h.Amount < dustLimit+HtlcTimeoutFee(feeratePerKw)
}

// exposures sums the dust HTLC amounts on each commitment transaction at the
// given feerate, optionally including an additional candidate HTLC.
func (t *DustTracker) exposures(feeratePerKw uint64, extra *Htlc) (local, remote uint64) {
	add := func(h Htlc) {
		if dustOnLocalCommitment(h, t.localDustLimit, feeratePerKw) {
			local += h.Amount
		}
		if dustOnRemoteCommitment(h, t.remoteDustLimit, feeratePerKw) {
			remote += h.Amount
		}
	}
	for _, h := range t.pending {
		add(h)
	}
	if extra != nil {
		add(*extra)
	}
	return local, remote
}

func (t *DustTracker) checkLimits(feeratePerKw uint64, extra *Htlc) error {
	local, remote := t.exposures(feeratePerKw, extra)
	if local > t.maxExposure {
		return &DustExposureError{Commitment: "local", Exposure: local, Max: t.maxExposure}
	}
	if remote > t.maxExposure {
		return &DustExposureError{Commitment: "remote", Exposure: remote, Max: t.maxExposure}
	}
	return nil
}

// AddHtlc validates a newly offered or received HTLC against the dust
// exposure of both commitment transactions and records it as pending. It
// must be called by the add_htlc handler before the HTLC is committed.
func (t *DustTracker) AddHtlc(h Htlc) error {
	if _, ok := t.pending[h.ID]; ok {
		return fmt.Errorf("htlc %d already pending", h.ID)
	}
	if err := t.checkLimits(t.feeratePerKw, &h); err != nil {
		return err
	}
	t.pending[h.ID] = h
	return nil
}

// RemoveHtlc drops a settled (fulfilled or failed) HTLC from the pending
// set.
func (t *DustTracker) RemoveHtlc(id uint64) {
	delete(t.pending, id)
}

// UpdateFee re-evaluates the dust exposure of both commitment transactions
// at the new feerate and applies it only if neither exceeds the maximum. A
// fee increase can turn previously non-dust HTLCs into dust, so the update
// is rejected in that case. It must be called by the update_fee handler.
func (t *DustTracker) UpdateFee(newFeeratePerKw uint64) error {
	if err := t.checkLimits(newFeeratePerKw, nil); err != nil {
		return err
	}
	t.feeratePerKw = newFeeratePerKw
	return nil
}

// FeeratePerKw returns the currently applied feerate.
func (t *DustTracker) FeeratePerKw() uint64 {
	return t.feeratePerKw
}

// Exposure returns the current dust exposure of the local and remote
// commitment transactions.
func (t *DustTracker) Exposure() (local, remote uint64) {
	return t.exposures(t.feeratePerKw, nil)
}
