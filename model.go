// Package netsec plans shortest write sequences for permission/lock
// register bitfields used by a Go configuration host.
//
// The register model is intentionally small and explicit:
//
//   - Each protected region occupies two adjacent bits: an enable (writable
//     permission) bit followed by a lock bit.
//   - Lock bits are write-one-to-set: a zero stays zero unless written with a
//     one, and a one can never be cleared.
//   - While a region is unlocked the host may freely change its enable bit;
//     once locked the enable bit is frozen at its pre-lock value.
//   - A single register write is a (writeMask, value) pair and may touch any
//     subset of the mutable bits, optionally restricted to one region when
//     the hardware only supports single-field updates.
//
// All searches operate on at most 24 mutable bits (12 regions).
package netsec

import "errors"

// MaxMutableBits is the largest supported mutable register width.
const MaxMutableBits = 24

const (
	enableShift = 0
	lockShift   = 1
	regionBits  = 2
)

// Model describes a finite register bitfield and the goal the configuration
// host wants to reach.
type Model struct {
	// Regions is the number of protected regions; the register therefore
	// exposes 2*Regions mutable bits and must not exceed MaxMutableBits.
	Regions int

	// Start is the current register value laid out as enable/lock bit pairs.
	Start uint32

	// Target is the desired value; only bits set in TargetMask are required
	// to match, so don't-care fields are expressible.
	Target uint32

	// TargetMask selects which Target bits are observed for goal testing.
	TargetMask uint32

	// ForbiddenMask selects enable bits that must read zero in every state
	// the planner ever visits, including Start. Every such bit denotes a
	// region whose writable sensitive area must never be opened.
	ForbiddenMask uint32

	// SingleField limits each write to at most one region (plus the global
	// identity write). When false a write may update any combination of
	// regions in one bus transaction.
	SingleField bool
}

// Write is one register bus transaction: Value is applied through WriteMask.
type Write struct {
	WriteMask uint32
	Value     uint32
}

// EnableBit returns the enable (permission) bit position for region r.
func EnableBit(r int) uint { return uint(r)*regionBits + enableShift }

// LockBit returns the lock bit position for region r.
func LockBit(r int) uint { return uint(r)*regionBits + lockShift }

func (m Model) lockMask() uint32 {
	var mask uint32
	for r := 0; r < m.Regions; r++ {
		mask |= 1 << LockBit(r)
	}
	return mask
}

func (m Model) fullMask() uint32 {
	if m.Regions == 0 {
		return 0
	}
	return 1<<(2*m.Regions) - 1
}

// Validate reports a descriptive error for an impossible or malformed Model.
func (m Model) Validate() error {
	if m.Regions <= 0 {
		return errors.New("netsec: Regions must be positive")
	}
	if 2*m.Regions > MaxMutableBits {
		return errors.New("netsec: more than 24 mutable bits")
	}
	full := m.fullMask()
	if m.Start&^full != 0 {
		return errors.New("netsec: Start contains bits outside the register")
	}
	if m.Target&^full != 0 || m.TargetMask&^full != 0 || m.ForbiddenMask&^full != 0 {
		return errors.New("netsec: mask contains bits outside the register")
	}
	if m.Target&^m.TargetMask != 0 {
		return errors.New("netsec: Target bits must be subset of TargetMask")
	}
	if m.ForbiddenMask&m.lockMask() != 0 {
		return errors.New("netsec: ForbiddenMask may only select enable bits")
	}
	if m.TargetMask&m.ForbiddenMask != 0 && m.Target&m.ForbiddenMask != 0 {
		return errors.New("netsec: target would open a forbidden region")
	}
	return nil
}

// Safe reports whether state leaves every forbidden enable bit cleared.
func (m Model) Safe(state uint32) bool {
	return state&m.ForbiddenMask == 0
}

// AtGoal reports whether state matches every observed target bit.
func (m Model) AtGoal(state uint32) bool {
	return state&m.TargetMask == m.Target&m.TargetMask
}

// Apply executes one write against state using the hardware semantics.
//
// Lock bits are sticky: only a written one can set them and nothing clears
// them. An enable bit changes only when its lock bit reads zero before the
// write; a locked enable value is retained regardless of WriteMask/Value.
func Apply(state uint32, w Write, regions int) uint32 {
	out := state
	for r := 0; r < regions; r++ {
		lockPos := LockBit(r)
		enablePos := EnableBit(r)
		lockBit := uint32(1) << lockPos
		enableBit := uint32(1) << enablePos

		if w.WriteMask&lockBit != 0 && w.Value&lockBit != 0 {
			out |= lockBit
		}
		if state&lockBit == 0 && w.WriteMask&enableBit != 0 {
			if w.Value&enableBit != 0 {
				out |= enableBit
			} else {
				out &^= enableBit
			}
		}
	}
	return out
}

// writeBetween reconstructs the canonical (mask, value) transaction that moves
// state into next for region r. It assumes next is a legal successor.
func writeBetween(state, next uint32, r int) Write {
	enablePos := EnableBit(r)
	lockPos := LockBit(r)
	enableBit := uint32(1) << enablePos
	lockBit := uint32(1) << lockPos

	var mask, value uint32
	if state&lockBit == 0 {
		mask |= enableBit
		if next&enableBit != 0 {
			value |= enableBit
		}
	}
	if next&lockBit != state&lockBit {
		mask |= lockBit
		value |= lockBit
	}
	return Write{WriteMask: mask, Value: value}
}

// localOption is one legal post-write local state for a region plus the
// canonical single-region write that reaches it.
type localOption struct {
	state uint32 // two-bit local fragment shifted into place
	write Write
}

// localOptions enumerates the distinct legal register fragments for one region
// after a single write. The returned fragments are shifted to their real
// positions, and each carries the minimal (mask, value) that produces it.
func localOptions(state uint32, r int) []localOption {
	enableBit := uint32(1) << EnableBit(r)
	lockBit := uint32(1) << LockBit(r)
	local := state & (enableBit | lockBit)

	identity := localOption{state: local, write: Write{}}
	if local&lockBit != 0 {
		// Locked: neither bit can change any more.
		return []localOption{identity}
	}

	enableOff := uint32(0)
	enableOn := enableBit
	lockOff := uint32(0)
	lockOn := lockBit
	fragments := []uint32{
		enableOff | lockOff,
		enableOn | lockOff,
		enableOff | lockOn,
		enableOn | lockOn,
	}

	out := make([]localOption, 0, len(fragments))
	for _, fragment := range fragments {
		write := writeBetween(state, fragment, r)
		out = append(out, localOption{state: fragment, write: write})
	}
	return out
}

// Expand visits every legal, safe successor of state reachable in one write.
// Visiting stops early if visit returns false. The identity successor (a write
// that changes nothing) is skipped because it cannot help a shortest path.
//
// Regions are independent, so successors are the Cartesian product of each
// region's local options. Local option fragments are disjoint, which makes
// every combined state unique. In SingleField mode at most one region may
// contribute a non-identity fragment per write.
func (m Model) Expand(state uint32, visit func(next uint32, w Write) bool) bool {
	options := make([][]localOption, m.Regions)
	identity := make([]int, m.Regions)
	for r := 0; r < m.Regions; r++ {
		options[r] = localOptions(state, r)
		enableBit := uint32(1) << EnableBit(r)
		lockBit := uint32(1) << LockBit(r)
		local := state & (enableBit | lockBit)
		for idx, option := range options[r] {
			if option.state == local {
				identity[r] = idx
				break
			}
		}
	}

	var combine func(region int, next uint32, mask, value uint32, touched int) bool
	combine = func(region int, next uint32, mask, value uint32, touched int) bool {
		if region == m.Regions {
			if touched == 0 || !m.Safe(next) {
				return true
			}
			return visit(next, Write{WriteMask: mask, Value: value})
		}
		for idx, option := range options[region] {
			if idx == identity[region] {
				// Identity fragment contributes no write bits; recurse once
				// without counting it as a touched region.
				if !combine(region+1, next|option.state, mask, value, touched) {
					return false
				}
				continue
			}
			nextTouched := touched
			nextTouched++
			if m.SingleField && nextTouched > 1 {
				continue
			}
			if !combine(region+1, next|option.state, mask|option.write.WriteMask,
				value|option.write.Value, nextTouched) {
				return false
			}
		}
		return true
	}
	return combine(0, 0, 0, 0, 0)
}

// goalWrite returns a one-write transaction taking state to a safe goal state
// and true when such a write exists under the register semantics. The
// resulting state only has to satisfy the observed target bits.
func (m Model) goalWrite(state uint32) (Write, uint32, bool) {
	var mask, value uint32
	next := state

	for r := 0; r < m.Regions; r++ {
		enableBit := uint32(1) << EnableBit(r)
		lockBit := uint32(1) << LockBit(r)
		local := state & (enableBit | lockBit)

		wantEnable := m.Target & enableBit
		wantLock := m.Target & lockBit
		careEnable := m.TargetMask&enableBit != 0
		careLock := m.TargetMask&lockBit != 0

		// A locked region cannot change; any observed mismatch is fatal.
		if local&lockBit != 0 {
			if careEnable && local&enableBit != wantEnable {
				return Write{}, 0, false
			}
			if careLock && wantLock == 0 {
				return Write{}, 0, false
			}
			continue
		}

		// Choose the post-write local fragment, defaulting don't-care bits to
		// the current value so the write stays minimal.
		fragment := local
		if careEnable {
			fragment &^= enableBit
			fragment |= wantEnable
		}
		nextLock := uint32(0)
		if careLock {
			nextLock = wantLock
		} else if local&lockBit != 0 {
			nextLock = lockBit
		}
		fragment &^= lockBit
		fragment |= nextLock

		w := writeBetween(state, fragment, r)
		mask |= w.WriteMask
		value |= w.Value
		next &^= enableBit | lockBit
		next |= fragment
	}

	if !m.Safe(next) {
		return Write{}, 0, false
	}
	if m.SingleField && m.regionsTouched(mask) > 1 {
		return Write{}, 0, false
	}
	return Write{WriteMask: mask, Value: value}, next, true
}

func (m Model) regionsTouched(mask uint32) int {
	count := 0
	for r := 0; r < m.Regions; r++ {
		regionMask := uint32(1)<<EnableBit(r) | uint32(1)<<LockBit(r)
		if mask&regionMask != 0 {
			count++
		}
	}
	return count
}

// Predecessors enumerates every state that can reach state in one legal write
// while keeping every visited state safe. It is used by the exhaustive
// dead-end verification for small bitfields.
func (m Model) Predecessors(state uint32, emit func(prev uint32) bool) bool {
	choices := make([][]uint32, m.Regions)
	for r := 0; r < m.Regions; r++ {
		enableBit := uint32(1) << EnableBit(r)
		lockBit := uint32(1) << LockBit(r)
		local := state & (enableBit | lockBit)

		seen := map[uint32]bool{}
		add := func(fragment uint32) {
			if !seen[fragment] {
				seen[fragment] = true
				choices[r] = append(choices[r], fragment)
			}
		}
		switch {
		case local&lockBit != 0:
			// Locked now. Three legal predecessors exist:
			//   - already locked, enable frozen at the same value;
			//   - unlocked with either enable, the same write sets the lock
			//     and the enable write is ignored because the lock was clear.
			add(local)
			add(0)
			add(enableBit)
		default:
			// Unlocked now: the predecessor must also be unlocked; the write
			// may have driven the enable bit to either value.
			add(0)
			add(enableBit)
		}
	}

	emitted := map[uint32]bool{}
	var combine func(region int, prev uint32) bool
	combine = func(region int, prev uint32) bool {
		if region == m.Regions {
			if prev == state || !m.Safe(prev) {
				return true
			}
			if emitted[prev] {
				return true
			}
			emitted[prev] = true
			return emit(prev)
		}
		for _, fragment := range choices[region] {
			if !combine(region+1, prev|fragment) {
				return false
			}
		}
		return true
	}
	return combine(0, 0)
}
