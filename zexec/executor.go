// Package zexec executes client operations against zdevice with crash-safe
// recovery across both allowed boot slots.
package zexec

import (
	"errors"
	"fmt"

	"prescreen.local/ps-7e522fc03b/zdevice"
	"prescreen.local/ps-7e522fc03b/zplanner"
)

// Executor drives a zdevice through an ordered client operation sequence.
// All journaling lives on the device in the two root table slots, so a new
// Executor can resume after power loss purely from durable state.
type Executor struct {
	dev     *zdevice.Device
	ops     []zplanner.Op
	table   *zplanner.Table
	lastAck int
}

// NewExecutor binds an operation sequence. Call Resume after powering the
// device on before processing operations.
func NewExecutor(dev *zdevice.Device, ops []zplanner.Op) *Executor {
	return &Executor{dev: dev, ops: ops, lastAck: -1}
}

// Resume loads the chosen boot slot, retires the other retained slot when a
// publish is unacknowledged, and replans the remaining client work.
func (e *Executor) Resume(bootSlot int) error {
	tbl, _, err := e.dev.PowerOn(bootSlot)
	if err != nil {
		return err
	}
	pending, err := e.dev.Pending()
	if err != nil {
		return err
	}
	if pending {
		// Two bootable slots exist. Firmware retains both until Ack; after
		// Ack the current (booted) slot is the sole survivor. Any work only
		// present in the dropped slot gets redone below, so acknowledging is
		// safe regardless of which slot firmware chose.
		if err := e.dev.Ack(); err != nil {
			return err
		}
	}
	e.table = tbl
	e.lastAck = tbl.LastOp
	return nil
}

// LastAck returns the ID of the last operation durable in the boot slot.
func (e *Executor) LastAck() int { return e.lastAck }

// Table returns the currently committed root table.
func (e *Executor) Table() *zplanner.Table { return e.table }

// ProcessNext handles the next operation in the sequence, or redelivers an
// already-acknowledged one without reapplying it. It returns true when the
// operation had already been acknowledged (a duplicate delivery).
func (e *Executor) ProcessNext() (duplicate bool, err error) {
	if e.table == nil {
		return false, errors.New("zexec: executor not resumed")
	}
	idx := e.nextIndex()
	if idx >= len(e.ops) {
		return false, fmt.Errorf("zexec: no remaining operations")
	}
	return e.processIndex(idx)
}

// ProcessOp handles one operation by its client-assigned ID. A redelivery of
// an already-acknowledged operation returns duplicate=true without reapplying.
func (e *Executor) ProcessOp(opID int) (duplicate bool, err error) {
	if e.table == nil {
		return false, errors.New("zexec: executor not resumed")
	}
	idx := -1
	for i, op := range e.ops {
		if op.ID == opID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, fmt.Errorf("zexec: unknown operation %d", opID)
	}
	return e.processIndex(idx)
}

func (e *Executor) processIndex(idx int) (bool, error) {
	op := e.ops[idx]
	if op.ID <= e.lastAck {
		// Reply was lost: the operation is durable in every bootable slot,
		// so the redelivery is answered without reapplication.
		return true, nil
	}
	plan, err := e.planFrom(idx)
	if err != nil {
		return false, err
	}
	prefix := prefixForOp(plan, op.ID)
	if err := e.executePlan(prefix); err != nil {
		return false, err
	}
	e.lastAck = op.ID
	return false, nil
}

// prefixForOp truncates a multi-operation plan to the steps through the
// publish that finalizes opID, so one client operation commits per call.
func prefixForOp(plan *zplanner.CommitPlan, opID int) *zplanner.CommitPlan {
	for i, step := range plan.Steps {
		if step.Kind == zplanner.StepPublish && step.OpID == opID {
			return &zplanner.CommitPlan{Steps: append([]zplanner.Step(nil), plan.Steps[:i+1]...)}
		}
	}
	return plan
}

// Run drives the full sequence to completion and returns the number of
// operations actually applied (excluding duplicate redeliveries).
func (e *Executor) Run() (applied int, err error) {
	if len(e.ops) == 0 {
		return 0, nil
	}
	for e.lastAck < e.ops[len(e.ops)-1].ID {
		dup, perr := e.ProcessNext()
		if perr != nil {
			return applied, perr
		}
		if !dup {
			applied++
		}
	}
	return applied, nil
}

func (e *Executor) nextIndex() int {
	for i, op := range e.ops {
		if op.ID > e.lastAck {
			return i
		}
	}
	return len(e.ops)
}

func (e *Executor) planFrom(idx int) (*zplanner.CommitPlan, error) {
	fill, err := e.dev.Fill()
	if err != nil {
		return nil, err
	}
	init := zplanner.InitialState{Fill: fill, Table: e.table.Clone()}
	return zplanner.Plan(init, e.ops[idx:])
}

func (e *Executor) executePlan(plan *zplanner.CommitPlan) error {
	_, err := e.executePlanCounted(plan, nil, -1)
	return err
}

// executePlanCounted runs plan while incrementing *actions for every
// low-level device action. When actions reaches faultAt, it stops after that
// action without completing the current publish and returns stop=true, so the
// caller can cut power and exercise recovery.
func (e *Executor) executePlanCounted(plan *zplanner.CommitPlan, actions *int, faultAt int) (stop bool, err error) {
	for _, step := range plan.Steps {
		tick := func() bool {
			if actions == nil {
				return false
			}
			*actions++
			return *actions-1 == faultAt
		}
		switch step.Kind {
		case zplanner.StepWrite:
			if _, err := e.dev.Append(step.Zone, payload(step.CID)); err != nil {
				return false, err
			}
			if err := e.dev.Flush(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
		case zplanner.StepCopy:
			src, ok := e.table.Loc[step.CID]
			if !ok {
				return false, fmt.Errorf("zexec: copy source CID %d not located", step.CID)
			}
			data, err := e.dev.Read(src)
			if err != nil {
				return false, err
			}
			if _, err := e.dev.Append(step.Zone, data); err != nil {
				return false, err
			}
			if err := e.dev.Flush(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
		case zplanner.StepReset:
			if err := e.dev.ResetZone(step.Zone); err != nil {
				return false, err
			}
			if err := e.dev.Flush(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
		case zplanner.StepPublish:
			if err := e.dev.WriteSlot(step.Table); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
			if err := e.dev.Flush(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
			if err := e.dev.Swap(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
			if err := e.dev.Flush(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
			if err := e.dev.Ack(); err != nil {
				return false, err
			}
			if tick() {
				return true, nil
			}
			tbl := step.Table.Clone()
			e.table = &tbl
		default:
			return false, fmt.Errorf("zexec: unknown plan step %d", step.Kind)
		}
	}
	return false, nil
}

// ExecutePlanWithFault is the test-only fault injection entry point: it
// processes the next client operation and cuts power after the faultAt-th
// device action. It returns stop=true when the fault fired.
func (e *Executor) ExecutePlanWithFault(faultAt int, actions *int) (stop bool, err error) {
	idx := e.nextIndex()
	if idx >= len(e.ops) {
		return false, fmt.Errorf("zexec: no remaining operations")
	}
	plan, err := e.planFrom(idx)
	if err != nil {
		return false, err
	}
	plan = prefixForOp(plan, e.ops[idx].ID)
	stop, err = e.executePlanCounted(plan, actions, faultAt)
	if err != nil {
		return false, err
	}
	if !stop {
		e.lastAck = e.ops[idx].ID
	}
	return stop, nil
}

// payload makes a CID's on-page content deterministic so shared copies can be
// compared byte for byte.
func payload(id zplanner.CID) []byte {
	return []byte{byte('C'), byte('I'), byte('D'), byte(id)}
}
