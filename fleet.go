package batsim

import (
	"errors"
	"math/big"
)

// ErrFleetCommandCount is returned when the command batch size does not
// match the number of batteries.
var ErrFleetCommandCount = errors.New("batsim: command count does not match battery count")

// Fleet is a set of batteries sharing one grid-side charge power limit.
// Commands are applied batch-atomically: the whole batch is validated
// and the shared limit allocated before any battery steps; once a batch
// executes, its records are appended to an immutable history and can
// never be rolled back.
type Fleet struct {
	batteries    []*Battery
	sharedCharge *big.Rat
	history      [][]StepRecord
}

// NewFleet creates a fleet with a shared per-step charge limit.
func NewFleet(sharedChargeLimit *big.Rat, batteries ...*Battery) (*Fleet, error) {
	if sharedChargeLimit == nil || sharedChargeLimit.Sign() < 0 {
		return nil, ErrInvalidSpec
	}
	if len(batteries) == 0 {
		return nil, ErrInvalidSpec
	}
	return &Fleet{
		batteries:    batteries,
		sharedCharge: clone(sharedChargeLimit),
	}, nil
}

// Step validates the whole batch, allocates the shared charge limit in
// battery order, then applies every battery's step. If any command is
// invalid, the entire batch is rejected and nothing executes.
func (f *Fleet) Step(cmds []Command) ([]StepRecord, error) {
	if len(cmds) != len(f.batteries) {
		return nil, ErrFleetCommandCount
	}
	for _, c := range cmds {
		if err := c.validate(); err != nil {
			return nil, err
		}
	}

	remaining := clone(f.sharedCharge)
	adjusted := make([]Command, len(cmds))
	for i, c := range cmds {
		charge := clone(nz(c.Charge))
		if charge.Cmp(remaining) > 0 {
			charge = clone(remaining)
		}
		remaining.Sub(remaining, charge)
		adjusted[i] = Command{Charge: charge, Discharge: c.Discharge}
	}

	recs := make([]StepRecord, len(f.batteries))
	for i, b := range f.batteries {
		rec, err := b.Step(adjusted[i])
		if err != nil {
			return nil, err // unreachable: commands pre-validated
		}
		recs[i] = rec
	}
	committed := make([]StepRecord, len(recs))
	for i, r := range recs {
		committed[i] = cloneRecord(r)
	}
	f.history = append(f.history, committed)
	return recs, nil
}

// History returns deep copies of the committed batch history. Executed
// batches are append-only; there is intentionally no rollback API.
func (f *Fleet) History() [][]StepRecord {
	out := make([][]StepRecord, len(f.history))
	for i, batch := range f.history {
		out[i] = make([]StepRecord, len(batch))
		for j, r := range batch {
			out[i][j] = cloneRecord(r)
		}
	}
	return out
}

// Battery returns the i-th battery.
func (f *Fleet) Battery(i int) *Battery { return f.batteries[i] }

// RecordSource supplies executed step records to an Estimator.
type RecordSource interface {
	Records() []StepRecord
}

// batteryView exposes one fleet battery's executed records.
type batteryView struct {
	fleet *Fleet
	index int
}

func (v batteryView) Records() []StepRecord { return v.fleet.batteries[v.index].Records() }

// BatteryView returns a RecordSource for the i-th battery, suitable for
// building an Estimator over the fleet's executed history.
func (f *Fleet) BatteryView(i int) RecordSource { return batteryView{fleet: f, index: i} }
