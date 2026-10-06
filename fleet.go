package batsim

import "math/big"

// Fleet is a set of batteries sharing one charging power limit.
type Fleet struct {
	Batteries         []*Battery
	SharedChargeLimit *big.Rat // W, shared across all batteries per step

	// ActionLog records every executed (possibly truncated) command.
	// It is append-only: executed energy actions are never rolled back
	// or rewritten, in particular not by sensor revisions.
	ActionLog []ExecutedAction
	batches   int
}

// ExecutedAction is one immutable entry of the execution history.
type ExecutedAction struct {
	Batch   int
	Index   int        // battery index within the batch
	Command Command    // command as allocated (after shared-limit clipping)
	Result  StepResult // nil-valued result when Err != nil
	Err     error
}

// NewFleet validates the shared limit and returns a Fleet.
func NewFleet(batteries []*Battery, sharedChargeLimit *big.Rat) *Fleet {
	return &Fleet{
		Batteries:         batteries,
		SharedChargeLimit: copyRat(sharedChargeLimit),
	}
}

// CommandResult pairs a per-command outcome with its shared-budget share.
type CommandResult struct {
	AllocatedChargeW *big.Rat // charge power granted from the shared budget
	Step             StepResult
	Err              error
}

// ExecuteBatch allocates the shared charging budget to one batch of
// commands atomically, then executes them. Allocation is greedy in
// command order: each charge command receives min(requested, remaining
// budget). Discharge commands do not consume the budget.
//
// Execution is not transactional across commands: once a command has
// executed it is never rolled back, even if a later command in the same
// batch is invalid. Invalid commands are recorded with their error and
// skipped; execution of the rest of the batch proceeds.
func (f *Fleet) ExecuteBatch(cmds []Command, dt *big.Rat) []CommandResult {
	remaining := copyRat(f.SharedChargeLimit)
	results := make([]CommandResult, len(cmds))

	// Atomic allocation phase for the whole batch.
	allocated := make([]Command, len(cmds))
	for i, cmd := range cmds {
		results[i].AllocatedChargeW = new(big.Rat)
		allocated[i] = cmd
		if err := cmd.validate(); err != nil {
			continue // rejected commands get no budget
		}
		if req := ratOrZero(cmd.ChargeW); req.Sign() > 0 {
			grant := req
			if grant.Cmp(remaining) > 0 {
				grant = copyRat(remaining)
			}
			remaining.Sub(remaining, grant)
			allocated[i].ChargeW = copyRat(grant)
			results[i].AllocatedChargeW = copyRat(grant)
		}
	}

	// Execution phase: executed parts are never rolled back.
	batch := f.batches
	f.batches++
	for i, cmd := range allocated {
		if err := cmd.validate(); err != nil {
			results[i].Err = err
			f.ActionLog = append(f.ActionLog, ExecutedAction{
				Batch: batch, Index: i, Command: cmd, Err: err,
			})
			continue
		}
		res, err := f.Batteries[i].Step(cmd, dt)
		results[i].Step = res
		results[i].Err = err
		f.ActionLog = append(f.ActionLog, ExecutedAction{
			Batch: batch, Index: i, Command: cmd, Result: res, Err: err,
		})
	}
	return results
}
