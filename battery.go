// Package batsim simulates batteries with exact rational arithmetic.
//
// Step semantics: self-discharge is applied first, then the charge or
// discharge command. A command requesting charge and discharge in the
// same step is rejected. All energy that leaves the system books
// (self-discharge loss and conversion loss) is accounted separately so
// the exact conservation identity
//
//	Stored + SelfLoss + ConvLoss + GridOut == InitialStored + GridIn
//
// holds at every step.
package batsim

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrSimultaneousChargeDischarge is returned when a command requests
// charging and discharging in the same step.
var ErrSimultaneousChargeDischarge = errors.New("batsim: charge and discharge in the same step are mutually exclusive")

// ErrNegativePower is returned when a command requests negative power.
var ErrNegativePower = errors.New("batsim: command power must be >= 0")

func ratOrZero(r *big.Rat) *big.Rat {
	if r == nil {
		return new(big.Rat)
	}
	return r
}

func copyRat(r *big.Rat) *big.Rat { return new(big.Rat).Set(ratOrZero(r)) }

// Command is a per-step control request, in watts. At most one of
// ChargeW and DischargeW may be positive.
type Command struct {
	ChargeW    *big.Rat
	DischargeW *big.Rat
}

func (c Command) validate() error {
	ch, dis := ratOrZero(c.ChargeW), ratOrZero(c.DischargeW)
	if ch.Sign() < 0 || dis.Sign() < 0 {
		return ErrNegativePower
	}
	if ch.Sign() > 0 && dis.Sign() > 0 {
		return ErrSimultaneousChargeDischarge
	}
	return nil
}

// Battery simulates one cell with rational efficiencies and limits.
type Battery struct {
	Capacity          *big.Rat // Wh
	MaxChargeW        *big.Rat // W
	MaxDischargeW     *big.Rat // W
	EffCharge         *big.Rat // stored / grid-in, in (0, 1]
	EffDischarge      *big.Rat // grid-out / stored, in (0, 1]
	SelfDischargeRate *big.Rat // fraction of Stored lost per step, in [0, 1)

	Stored        *big.Rat // Wh currently stored
	InitialStored *big.Rat // Wh stored at construction

	// Separate energy books (all Wh, cumulative).
	TotalGridIn            *big.Rat // energy drawn from the grid
	TotalGridOut           *big.Rat // energy delivered to the grid
	TotalSelfDischargeLoss *big.Rat // energy lost to self-discharge
	TotalConversionLoss    *big.Rat // energy lost in (dis)charge conversion
}

// NewBattery validates parameters and returns a Battery.
func NewBattery(capacity, maxChargeW, maxDischargeW, effCharge, effDischarge, selfRate, initialStored *big.Rat) (*Battery, error) {
	one := big.NewRat(1, 1)
	if capacity == nil || capacity.Sign() <= 0 {
		return nil, fmt.Errorf("batsim: capacity must be > 0")
	}
	if ratOrZero(maxChargeW).Sign() < 0 || ratOrZero(maxDischargeW).Sign() < 0 {
		return nil, fmt.Errorf("batsim: power limits must be >= 0")
	}
	for name, eff := range map[string]*big.Rat{"effCharge": effCharge, "effDischarge": effDischarge} {
		if eff == nil || eff.Sign() <= 0 || eff.Cmp(one) > 0 {
			return nil, fmt.Errorf("batsim: %s must be in (0, 1]", name)
		}
	}
	if ratOrZero(selfRate).Sign() < 0 || ratOrZero(selfRate).Cmp(one) >= 0 {
		return nil, fmt.Errorf("batsim: self-discharge rate must be in [0, 1)")
	}
	if ratOrZero(initialStored).Sign() < 0 || ratOrZero(initialStored).Cmp(capacity) > 0 {
		return nil, fmt.Errorf("batsim: initial stored energy must be in [0, capacity]")
	}
	return &Battery{
		Capacity:               copyRat(capacity),
		MaxChargeW:             copyRat(maxChargeW),
		MaxDischargeW:          copyRat(maxDischargeW),
		EffCharge:              copyRat(effCharge),
		EffDischarge:           copyRat(effDischarge),
		SelfDischargeRate:      copyRat(selfRate),
		Stored:                 copyRat(initialStored),
		InitialStored:          copyRat(initialStored),
		TotalGridIn:            new(big.Rat),
		TotalGridOut:           new(big.Rat),
		TotalSelfDischargeLoss: new(big.Rat),
		TotalConversionLoss:    new(big.Rat),
	}, nil
}

// StepResult reports what actually happened in one step.
type StepResult struct {
	SelfDischargeLoss *big.Rat // Wh lost to self-discharge this step
	GridIn            *big.Rat // Wh drawn from the grid this step
	GridOut           *big.Rat // Wh delivered to the grid this step
	ConversionLoss    *big.Rat // Wh lost in conversion this step
	StoredAfter       *big.Rat // Wh stored after the step
	Truncated         bool     // command was clipped by a limit
}

// Step advances the battery by dt hours: self-discharge first, then the
// command. A command violating validate() is rejected before any mutation.
func (b *Battery) Step(cmd Command, dt *big.Rat) (StepResult, error) {
	res := StepResult{
		SelfDischargeLoss: new(big.Rat),
		GridIn:            new(big.Rat),
		GridOut:           new(big.Rat),
		ConversionLoss:    new(big.Rat),
	}
	if err := cmd.validate(); err != nil {
		res.StoredAfter = copyRat(b.Stored)
		return res, err
	}
	if dt == nil || dt.Sign() <= 0 {
		res.StoredAfter = copyRat(b.Stored)
		return res, fmt.Errorf("batsim: dt must be > 0")
	}

	// 1. Self-discharge.
	res.SelfDischargeLoss.Mul(b.Stored, b.SelfDischargeRate)
	b.Stored.Sub(b.Stored, res.SelfDischargeLoss)
	b.TotalSelfDischargeLoss.Add(b.TotalSelfDischargeLoss, res.SelfDischargeLoss)

	// 2. Command.
	if req := ratOrZero(cmd.ChargeW); req.Sign() > 0 {
		gridIn := new(big.Rat).Mul(req, dt)
		if maxIn := new(big.Rat).Mul(b.MaxChargeW, dt); gridIn.Cmp(maxIn) > 0 {
			gridIn = maxIn
			res.Truncated = true
		}
		storedAdd := new(big.Rat).Mul(gridIn, b.EffCharge)
		if headroom := new(big.Rat).Sub(b.Capacity, b.Stored); storedAdd.Cmp(headroom) > 0 {
			storedAdd = headroom
			gridIn = new(big.Rat).Quo(headroom, b.EffCharge)
			res.Truncated = true
		}
		res.GridIn = gridIn
		res.ConversionLoss.Sub(gridIn, storedAdd)
		b.Stored.Add(b.Stored, storedAdd)
	} else if req := ratOrZero(cmd.DischargeW); req.Sign() > 0 {
		gridOut := new(big.Rat).Mul(req, dt)
		if maxOut := new(big.Rat).Mul(b.MaxDischargeW, dt); gridOut.Cmp(maxOut) > 0 {
			gridOut = maxOut
			res.Truncated = true
		}
		storedRemove := new(big.Rat).Quo(gridOut, b.EffDischarge)
		if storedRemove.Cmp(b.Stored) > 0 {
			storedRemove = copyRat(b.Stored)
			gridOut = new(big.Rat).Mul(storedRemove, b.EffDischarge)
			res.Truncated = true
		}
		res.GridOut = gridOut
		res.ConversionLoss.Sub(storedRemove, gridOut)
		b.Stored.Sub(b.Stored, storedRemove)
	}

	b.TotalGridIn.Add(b.TotalGridIn, res.GridIn)
	b.TotalGridOut.Add(b.TotalGridOut, res.GridOut)
	b.TotalConversionLoss.Add(b.TotalConversionLoss, res.ConversionLoss)
	res.StoredAfter = copyRat(b.Stored)
	return res, nil
}

// ConservationError returns the exact residual of the conservation
// identity; it is zero iff the energy books are consistent:
//
//	Stored + SelfLoss + ConvLoss + GridOut - InitialStored - GridIn == 0
func (b *Battery) ConservationError() *big.Rat {
	lhs := new(big.Rat).Add(b.Stored, b.TotalSelfDischargeLoss)
	lhs.Add(lhs, b.TotalConversionLoss)
	lhs.Add(lhs, b.TotalGridOut)
	rhs := new(big.Rat).Add(b.InitialStored, b.TotalGridIn)
	return lhs.Sub(lhs, rhs)
}

// Clone returns a deep copy, usable as a replay starting point.
func (b *Battery) Clone() *Battery {
	c := *b
	c.Capacity = copyRat(b.Capacity)
	c.MaxChargeW = copyRat(b.MaxChargeW)
	c.MaxDischargeW = copyRat(b.MaxDischargeW)
	c.EffCharge = copyRat(b.EffCharge)
	c.EffDischarge = copyRat(b.EffDischarge)
	c.SelfDischargeRate = copyRat(b.SelfDischargeRate)
	c.Stored = copyRat(b.Stored)
	c.InitialStored = copyRat(b.InitialStored)
	c.TotalGridIn = copyRat(b.TotalGridIn)
	c.TotalGridOut = copyRat(b.TotalGridOut)
	c.TotalSelfDischargeLoss = copyRat(b.TotalSelfDischargeLoss)
	c.TotalConversionLoss = copyRat(b.TotalConversionLoss)
	return &c
}
