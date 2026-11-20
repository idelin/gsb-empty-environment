// Package updatestrategy plans observation-branching update policies for a
// two-slot A/B update host.
//
// The host can only inspect public, on-device observations: the digest and
// rollback counter of the currently active (authenticated) slot, a reboot
// readback, or a power-loss indicator. It cannot read back a standby slot that
// has been written but not yet authenticated by a successful reboot. Planning
// therefore happens over *belief states*: the set of concrete persistent
// worlds that are jointly compatible with the observations seen so far.
//
// The planner guarantees, under at most one power loss at any legal
// interruption point, that every step remains offline bootable and that every
// possible world eventually reaches the requested signed image. When no
// observation-based action can guarantee that, planning is refused with a
// structured reason instead of guessing the hidden slot contents.
package updatestrategy

import (
	"errors"
	"fmt"
)

// Digest names the persistent content of a slot. It is supplied by the caller
// (component + image summary). The special value UnknownDigest means the slot
// is an unauthenticated candidate: after a power loss its real contents cannot
// be distinguished and it must never be treated as a successfully written
// image.
type Digest string

const (
	// UnknownDigest marks a slot whose persistent content cannot be certified.
	UnknownDigest Digest = ""
	// EmptyDigest marks a factory-empty slot that has never held an image.
	EmptyDigest Digest = "<empty>"
)

// Slot is one of the two physical update slots.
type Slot int

const (
	SlotA Slot = iota
	SlotB
)

func (s Slot) String() string {
	if s == SlotA {
		return "A"
	}
	return "B"
}

// Other returns the partner slot.
func (s Slot) Other() Slot {
	if s == SlotA {
		return SlotB
	}
	return SlotA
}

// World is one concrete, fully specified persistent device state. Worlds are
// only used inside the planner and the in-memory simulator; a real host can
// never observe the whole World, only its public Observation.
//
// Invariants of every World emitted by the transition function:
//   - if powerLost is false, Active always holds an authenticated, bootable
//     digest (never UnknownDigest);
//   - a non-active slot whose digest is UnknownDigest is an unauthenticated
//     candidate and cannot be booted;
//   - Counter is monotonic non-decreasing along every transition.
type World struct {
	Active    Slot
	Slots     [2]Digest
	Pending   Slot
	Counter   int
	PowerLost bool
}

// ID is a canonical, value-stable identifier used as a map key.
func (w World) ID() string {
	return fmt.Sprintf("%s|%s|%s|p=%s|c=%d|pl=%t",
		w.Active, w.Slots[SlotA], w.Slots[SlotB], w.Pending, w.Counter, w.PowerLost)
}

// Bootable reports whether the slot carries an authenticated image that can be
// booted offline. Unknown and empty slots are not bootable.
func (w World) Bootable(s Slot) bool {
	d := w.Slots[s]
	return d != UnknownDigest && d != EmptyDigest
}

// Clone returns an independent copy of the world.
func (w World) Clone() World {
	out := w
	return out
}

// Observation is everything the host is allowed to know after an action. Two
// worlds that emit equal Observations are indistinguishable and must share
// every future decision.
type Observation struct {
	// Kind identifies which public channel produced the observation.
	Kind ObsKind
	// ActiveDigest is the authenticated digest read from the active slot. Valid
	// for ObsReboot and ObsInitial; empty otherwise.
	ActiveDigest Digest
	// ActiveSlot identifies which physical slot is active after a readback.
	ActiveSlot Slot
	// Counter is the publicly verifiable monotonic rollback counter.
	Counter int
	// Ack distinguishes a positively acknowledged write from a write whose
	// completion is not externally confirmable.
	Ack bool
}

// ObsKind enumerates the public observation channels.
type ObsKind int

const (
	ObsInitial ObsKind = iota
	ObsWriteAck
	ObsWriteNoAck
	ObsPendingSet
	ObsCounterBurnt
	ObsPowerLoss
	ObsReboot
	ObsConfirmed
)

func (k ObsKind) String() string {
	switch k {
	case ObsInitial:
		return "initial"
	case ObsWriteAck:
		return "write-ack"
	case ObsWriteNoAck:
		return "write-no-ack"
	case ObsPendingSet:
		return "pending-set"
	case ObsCounterBurnt:
		return "counter-burnt"
	case ObsPowerLoss:
		return "power-loss"
	case ObsReboot:
		return "reboot"
	case ObsConfirmed:
		return "confirmed"
	default:
		return "unknown"
	}
}

// Observation returns the public observation available in this world. It is a
// pure function of persistent state plus the action that produced it, so two
// hosts holding indistinguishable worlds compute the same value.
func (w World) Observation(kind ObsKind) Observation {
	obs := Observation{Kind: kind, Counter: w.Counter}
	switch kind {
	case ObsInitial, ObsReboot:
		obs.ActiveDigest = w.Slots[w.Active]
		obs.ActiveSlot = w.Active
	case ObsWriteAck:
		obs.Ack = true
	case ObsWriteNoAck:
		obs.Ack = false
	}
	return obs
}

// Outcome is one possible persistent result of an action: the concrete world
// and the public observation it yields.
type Outcome struct {
	World World
	Obs   Observation
}

// Action is a host operation with a declared set of possible persistent
// outcomes. Implementations must be pure value types; the same Action applied
// to the same World always returns the same Outcome set.
type Action interface {
	// Name is a stable identifier used in policy dumps and refusal reasons.
	Name() string
	// Apply returns every result this action may produce from w, including the
	// clean result and, when w has not yet consumed its single power-loss
	// budget, every legal interruption result.
	Apply(w World) []Outcome
}

// ErrPlanningRefused wraps a Refusal describing why no observation-based
// policy exists. Callers can use errors.As to inspect the detail.
var ErrPlanningRefused = errors.New("updatestrategy: planning refused")
