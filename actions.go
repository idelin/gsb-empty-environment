package updatestrategy

import (
	"fmt"
)

// safeWriteTarget rejects writes that would touch the only authenticated slot.
// With a single active A/B slot, writing the standby slot is always
// boot-preserving: even a torn write leaves the current Active untouched.
func safeWriteTarget(w World, target Slot) error {
	if target == w.Active {
		return fmt.Errorf("write to %s would overwrite the only authenticated active slot", target)
	}
	return nil
}

// powerOutcomes appends the interruption-branch results for an action when the
// world still has its single power-loss budget available.
func powerOutcomes(w World, variants ...World) []Outcome {
	if w.PowerLost {
		return nil
	}
	out := make([]Outcome, 0, len(variants))
	for _, v := range variants {
		v.PowerLost = true
		out = append(out, Outcome{World: v, Obs: v.Observation(ObsPowerLoss)})
	}
	return out
}

// WriteAction programs Image into Target. With Acknowledge=false the host gets
// no externally confirmable completion signal, so a clean completion and an
// interruption after the write but before the readback share one observation.
type WriteAction struct {
	Target Slot
	Image  Digest
	// Acknowledge selects the public completion channel. A false value models
	// hardware whose only write-result signal is the next reboot readback.
	Acknowledge bool
}

func (a WriteAction) Name() string {
	if a.Acknowledge {
		return fmt.Sprintf("WriteAck(%s,%s)", a.Target, a.Image)
	}
	return fmt.Sprintf("WriteNoAck(%s,%s)", a.Target, a.Image)
}

func (a WriteAction) SafeIn(w World) error { return safeWriteTarget(w, a.Target) }

func (a WriteAction) Apply(w World) []Outcome {
	out := make([]Outcome, 0, 4)

	// Clean completion: the target slot holds the fully programmed image. It is
	// persistent but still unauthenticated until a reboot selects it.
	clean := w.Clone()
	clean.Slots[a.Target] = a.Image
	kind := ObsWriteNoAck
	if a.Acknowledge {
		kind = ObsWriteAck
	}
	out = append(out, Outcome{World: clean, Obs: clean.Observation(kind)})

	if w.PowerLost {
		return out
	}

	// Interruption before the first durable byte: the previous standby content
	// is unchanged.
	preCommit := w.Clone()
	// Interruption after partial programming: the slot is an unauthenticated
	// candidate. It must never be read as a successfully written image.
	torn := w.Clone()
	torn.Slots[a.Target] = UnknownDigest
	out = append(out, powerOutcomes(w, preCommit, torn)...)
	return out
}

// SetPendingAction commits the bootloader's next-boot pointer.
type SetPendingAction struct {
	Target Slot
}

func (a SetPendingAction) Name() string { return fmt.Sprintf("SetPending(%s)", a.Target) }

func (a SetPendingAction) SafeIn(World) error { return nil }

func (a SetPendingAction) Apply(w World) []Outcome {
	clean := w.Clone()
	clean.Pending = a.Target
	out := []Outcome{{World: clean, Obs: clean.Observation(ObsPendingSet)}}
	if w.PowerLost {
		return out
	}
	// The pointer write either fully persists or is lost entirely.
	lost := w.Clone()
	out = append(out, powerOutcomes(w, clean, lost)...)
	return out
}

// BurnCounterAction advances the monotonic, non-decreasing rollback counter by
// Amount. Counter commits are atomic: an interrupted burn either landed or did
// not, and no outcome ever lowers the value.
type BurnCounterAction struct {
	Amount int
	// Floor is the target counter floor supplied by the host. Burning once the
	// floor is already reached can never be part of a recovery path: the
	// counter cannot decrease, so an overshoot permanently burns the only
	// remaining headroom. Declaring this as the action's safety rule is what
	// lets the planner detect divergent beliefs instead of guessing.
	Floor int
}

func (a BurnCounterAction) Name() string {
	return fmt.Sprintf("BurnCounter(+%d,floor=%d)", a.Amount, a.Floor)
}

func (a BurnCounterAction) SafeIn(w World) error {
	if a.Amount <= 0 {
		return fmt.Errorf("counter burn must advance by a positive amount, got %d", a.Amount)
	}
	if w.Counter >= a.Floor {
		return fmt.Errorf("counter %d already at/over floor %d: burning would permanently consume recovery headroom", w.Counter, a.Floor)
	}
	return nil
}

func (a BurnCounterAction) Apply(w World) []Outcome {
	clean := w.Clone()
	clean.Counter += a.Amount
	out := []Outcome{{World: clean, Obs: clean.Observation(ObsCounterBurnt)}}
	if w.PowerLost {
		return out
	}
	lost := w.Clone()
	out = append(out, powerOutcomes(w, clean, lost)...)
	return out
}

// RebootAction hands control to the bootloader. An authenticated pending slot
// is booted and becomes Active; an empty or torn pending slot forces a
// fallback to the last good Active slot. Either way the machine comes back
// offline bootable.
type RebootAction struct{}

func (RebootAction) Name() string       { return "Reboot" }
func (RebootAction) SafeIn(World) error { return nil }

func (RebootAction) Apply(w World) []Outcome {
	clean := w.Clone()
	if clean.Bootable(clean.Pending) {
		clean.Active = clean.Pending
	} else {
		// Fallback: keep the last known good slot and clear the bad pointer.
		clean.Pending = clean.Active
	}
	out := []Outcome{{World: clean, Obs: clean.Observation(ObsReboot)}}
	if w.PowerLost {
		return out
	}
	// Power loss before the pending slot authenticates: cold boot restores the
	// last good slot; the unauthenticated candidate never becomes Active.
	cold := w.Clone()
	cold.Pending = cold.Active
	cold.PowerLost = true
	out = append(out, Outcome{World: cold, Obs: cold.Observation(ObsPowerLoss)})
	return out
}

// ConfirmAction clears the rollback/retry flag after the new active slot has
// authenticated. A lost confirmation leaves the slot active but still
// rollback-armed, so a later reboot can revert; the planner re-confirms.
type ConfirmAction struct{}

func (ConfirmAction) Name() string       { return "Confirm" }
func (ConfirmAction) SafeIn(World) error { return nil }

func (ConfirmAction) Apply(w World) []Outcome {
	clean := w.Clone()
	clean.Pending = clean.Active
	out := []Outcome{{World: clean, Obs: clean.Observation(ObsConfirmed)}}
	if w.PowerLost {
		return out
	}
	lost := w.Clone()
	out = append(out, powerOutcomes(w, clean, lost)...)
	return out
}

// safeAction is implemented by every host action.
type safeAction interface {
	Action
	SafeIn(w World) error
}

var (
	_ safeAction = WriteAction{}
	_ safeAction = SetPendingAction{}
	_ safeAction = BurnCounterAction{}
	_ safeAction = RebootAction{}
	_ safeAction = ConfirmAction{}
)
