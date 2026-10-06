// Package batsim simulates battery energy storage with exact rational
// accounting. All energy quantities are *big.Rat so conservation
// identities hold exactly, with no floating point drift.
package batsim

import (
	"errors"
	"math/big"
)

var (
	// ErrSimultaneousChargeDischarge is returned when a command requests
	// both charging and discharging in the same step.
	ErrSimultaneousChargeDischarge = errors.New("batsim: simultaneous charge and discharge rejected")
	// ErrNegativeCommand is returned when a command energy is negative.
	ErrNegativeCommand = errors.New("batsim: negative command energy")
	// ErrInvalidSpec is returned for physically meaningless specs.
	ErrInvalidSpec = errors.New("batsim: invalid spec")
)

// Rat builds an exact rational num/denom.
func Rat(num, denom int64) *big.Rat { return big.NewRat(num, denom) }

func clone(x *big.Rat) *big.Rat { return new(big.Rat).Set(x) }

// nz treats a nil rational as zero.
func nz(x *big.Rat) *big.Rat {
	if x == nil {
		return Rat(0, 1)
	}
	return x
}

// Spec describes the physical parameters of one battery.
// Efficiencies are in (0, 1]; SelfDischarge is the fraction of stored
// energy lost per step, in [0, 1].
type Spec struct {
	Capacity          *big.Rat // maximum stored energy
	MaxChargePower    *big.Rat // max grid-side charge energy per step
	MaxDischargePower *big.Rat // max grid-side discharge energy per step
	ChargeEff         *big.Rat // stored = gridIn * ChargeEff
	DischargeEff      *big.Rat // gridOut = drawn * DischargeEff
	SelfDischarge     *big.Rat // fraction of stored energy lost per step
}

func (s Spec) validate() error {
	if s.Capacity == nil || s.Capacity.Sign() <= 0 {
		return ErrInvalidSpec
	}
	if s.MaxChargePower == nil || s.MaxChargePower.Sign() < 0 {
		return ErrInvalidSpec
	}
	if s.MaxDischargePower == nil || s.MaxDischargePower.Sign() < 0 {
		return ErrInvalidSpec
	}
	one := Rat(1, 1)
	if s.ChargeEff == nil || s.ChargeEff.Sign() <= 0 || s.ChargeEff.Cmp(one) > 0 {
		return ErrInvalidSpec
	}
	if s.DischargeEff == nil || s.DischargeEff.Sign() <= 0 || s.DischargeEff.Cmp(one) > 0 {
		return ErrInvalidSpec
	}
	if s.SelfDischarge == nil || s.SelfDischarge.Sign() < 0 || s.SelfDischarge.Cmp(one) > 0 {
		return ErrInvalidSpec
	}
	return nil
}

// Command requests grid-side energy for one step. Charge and Discharge
// are mutually exclusive; a nil field means zero.
type Command struct {
	Charge    *big.Rat
	Discharge *big.Rat
}

func (c Command) validate() error {
	if nz(c.Charge).Sign() < 0 || nz(c.Discharge).Sign() < 0 {
		return ErrNegativeCommand
	}
	if nz(c.Charge).Sign() > 0 && nz(c.Discharge).Sign() > 0 {
		return ErrSimultaneousChargeDischarge
	}
	return nil
}

// StepRecord is the immutable, exact account of one applied step.
//
// Per-step conservation identities:
//   StoredBefore - SelfLoss + StoredAdd - Drawn == StoredAfter
//   GridIn  == StoredAdd + ChargeLoss
//   Drawn   == GridOut + DischargeLoss
type StepRecord struct {
	Step          int
	StoredBefore  *big.Rat
	StoredAfter   *big.Rat
	GridIn        *big.Rat // grid-side energy drawn from the grid
	GridOut       *big.Rat // grid-side energy delivered to the grid
	StoredAdd     *big.Rat // energy actually added to storage
	Drawn         *big.Rat // energy actually removed from storage
	SelfLoss      *big.Rat // self-discharge loss this step
	ChargeLoss    *big.Rat // conversion loss while charging
	DischargeLoss *big.Rat // conversion loss while discharging
}

func cloneRecord(r StepRecord) StepRecord {
	return StepRecord{
		Step:          r.Step,
		StoredBefore:  clone(r.StoredBefore),
		StoredAfter:   clone(r.StoredAfter),
		GridIn:        clone(r.GridIn),
		GridOut:       clone(r.GridOut),
		StoredAdd:     clone(r.StoredAdd),
		Drawn:         clone(r.Drawn),
		SelfLoss:      clone(r.SelfLoss),
		ChargeLoss:    clone(r.ChargeLoss),
		DischargeLoss: clone(r.DischargeLoss),
	}
}

// applyStep is the pure step function: self-discharge first, then the
// charge/discharge command truncated to power and capacity bounds.
func applyStep(spec Spec, stored *big.Rat, cmd Command) (*big.Rat, StepRecord) {
	rec := StepRecord{StoredBefore: clone(stored)}

	selfLoss := new(big.Rat).Mul(stored, spec.SelfDischarge)
	cur := new(big.Rat).Sub(stored, selfLoss)
	rec.SelfLoss = selfLoss

	gridIn := clone(nz(cmd.Charge))
	gridOut := clone(nz(cmd.Discharge))

	if gridIn.Sign() > 0 {
		if gridIn.Cmp(spec.MaxChargePower) > 0 {
			gridIn = clone(spec.MaxChargePower)
		}
		storedAdd := new(big.Rat).Mul(gridIn, spec.ChargeEff)
		headroom := new(big.Rat).Sub(spec.Capacity, cur)
		if storedAdd.Cmp(headroom) > 0 {
			storedAdd = headroom
			gridIn = new(big.Rat).Quo(storedAdd, spec.ChargeEff)
		}
		rec.GridIn = gridIn
		rec.StoredAdd = storedAdd
		rec.ChargeLoss = new(big.Rat).Sub(gridIn, storedAdd)
		cur.Add(cur, storedAdd)
	} else {
		rec.GridIn = Rat(0, 1)
		rec.StoredAdd = Rat(0, 1)
		rec.ChargeLoss = Rat(0, 1)
	}

	if gridOut.Sign() > 0 {
		if gridOut.Cmp(spec.MaxDischargePower) > 0 {
			gridOut = clone(spec.MaxDischargePower)
		}
		drawn := new(big.Rat).Quo(gridOut, spec.DischargeEff)
		if drawn.Cmp(cur) > 0 {
			drawn = clone(cur)
			gridOut = new(big.Rat).Mul(drawn, spec.DischargeEff)
		}
		rec.GridOut = gridOut
		rec.Drawn = drawn
		rec.DischargeLoss = new(big.Rat).Sub(drawn, gridOut)
		cur.Sub(cur, drawn)
	} else {
		rec.GridOut = Rat(0, 1)
		rec.Drawn = Rat(0, 1)
		rec.DischargeLoss = Rat(0, 1)
	}

	rec.StoredAfter = clone(cur)
	return cur, rec
}

// Totals accumulates exact energy accounts over a battery's life.
type Totals struct {
	GridIn        *big.Rat
	GridOut       *big.Rat
	SelfLoss      *big.Rat
	ChargeLoss    *big.Rat
	DischargeLoss *big.Rat
}

func zeroTotals() Totals {
	return Totals{Rat(0, 1), Rat(0, 1), Rat(0, 1), Rat(0, 1), Rat(0, 1)}
}

// TotalLoss is the sum of all separately accounted losses.
func (t Totals) TotalLoss() *big.Rat {
	return new(big.Rat).Add(new(big.Rat).Add(t.SelfLoss, t.ChargeLoss), t.DischargeLoss)
}

// Battery is a single simulated battery with an append-only step log.
type Battery struct {
	spec   Spec
	stored *big.Rat
	totals Totals
	log    []StepRecord
}

// NewBattery creates a battery with the given initial stored energy,
// which must lie within [0, Capacity].
func NewBattery(spec Spec, initial *big.Rat) (*Battery, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if initial == nil || initial.Sign() < 0 || initial.Cmp(spec.Capacity) > 0 {
		return nil, ErrInvalidSpec
	}
	return &Battery{spec: spec, stored: clone(initial), totals: zeroTotals()}, nil
}

// Stored returns the current stored energy.
func (b *Battery) Stored() *big.Rat { return clone(b.stored) }

// Totals returns a copy of the cumulative energy accounts.
func (b *Battery) Totals() Totals {
	return Totals{
		GridIn:        clone(b.totals.GridIn),
		GridOut:       clone(b.totals.GridOut),
		SelfLoss:      clone(b.totals.SelfLoss),
		ChargeLoss:    clone(b.totals.ChargeLoss),
		DischargeLoss: clone(b.totals.DischargeLoss),
	}
}

// Step validates and applies one command. An invalid command is
// rejected atomically: no self-discharge, no state change, no log entry.
func (b *Battery) Step(cmd Command) (StepRecord, error) {
	if err := cmd.validate(); err != nil {
		return StepRecord{}, err
	}
	next, rec := applyStep(b.spec, b.stored, cmd)
	rec.Step = len(b.log)
	b.stored = next
	b.totals.GridIn.Add(b.totals.GridIn, rec.GridIn)
	b.totals.GridOut.Add(b.totals.GridOut, rec.GridOut)
	b.totals.SelfLoss.Add(b.totals.SelfLoss, rec.SelfLoss)
	b.totals.ChargeLoss.Add(b.totals.ChargeLoss, rec.ChargeLoss)
	b.totals.DischargeLoss.Add(b.totals.DischargeLoss, rec.DischargeLoss)
	b.log = append(b.log, rec)
	return cloneRecord(rec), nil
}

// Records returns deep copies of the executed step log.
func (b *Battery) Records() []StepRecord {
	out := make([]StepRecord, len(b.log))
	for i, r := range b.log {
		out[i] = cloneRecord(r)
	}
	return out
}

// CheckConservation verifies the global exact identity
//   initial + totalGridIn == stored + totalGridOut + totalLosses.
func (b *Battery) CheckConservation(initial *big.Rat) bool {
	lhs := new(big.Rat).Add(initial, b.totals.GridIn)
	rhs := new(big.Rat).Add(b.stored, b.totals.GridOut)
	rhs.Add(rhs, b.totals.TotalLoss())
	return lhs.Cmp(rhs) == 0
}
