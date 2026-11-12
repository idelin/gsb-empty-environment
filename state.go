package netsec

// BitState is the abstract state of a single secret bit.
type BitState int

const (
	// BitUnknown means the bit is not known to be zero and may retain a
	// secret value. This is the conservative state used for reset proofs.
	BitUnknown BitState = iota

	// BitZero means the bit is deterministically zero (proven clean).
	BitZero
)

// String renders the abstract bit state as "unknown" or "zero".
func (b BitState) String() string {
	switch b {
	case BitUnknown:
		return "unknown"
	case BitZero:
		return "zero"
	default:
		return "invalid"
	}
}

// Mask packs a width-bit abstract register state into a uint64. Set bits are
// BitZero; unset bits within [0, width) are BitUnknown. Bits at or above
// width are always ignored.
type Mask uint64

// States expands a packed Mask into one BitState per bit, indexed by bit
// position. A set bit maps to BitZero, every other bit maps to BitUnknown.
func States(state Mask, width int) []BitState {
	if width <= 0 {
		return nil
	}
	bits := make([]BitState, width)
	for index := 0; index < width; index++ {
		if state&(1<<uint(index)) != 0 {
			bits[index] = BitZero
		} else {
			bits[index] = BitUnknown
		}
	}
	return bits
}

// Pack collapses a per-bit abstract state into a packed Mask. BitZero entries
// set the corresponding bit; every other value (including BitUnknown) leaves
// it clear. It returns ErrInvalidState when an entry is neither BitUnknown nor
// BitZero.
func Pack(bits []BitState) (Mask, error) {
	var state Mask
	for index, bit := range bits {
		switch bit {
		case BitUnknown:
		case BitZero:
			state |= 1 << uint(index)
		default:
			return 0, ErrInvalidState
		}
	}
	return state, nil
}

// Legal reports whether the action may be applied in state. Every bit named
// by RequireZero must already be BitZero.
func (a Action) Legal(state Mask) bool {
	return state&a.RequireZero == a.RequireZero
}

// Apply executes the action without a precondition check and returns the
// successor state. Bits in Clear are driven to BitZero and bits in Taint
// become BitUnknown. When the masks overlap Taint wins: re-unknowning happens
// after the clear, matching an action that disturbs other bits while it runs.
// Use Legal first to validate the precondition.
func (a Action) Apply(state Mask) Mask {
	return (state &^ a.Taint) | (a.Clear &^ a.Taint)
}

// Step applies the action to state after checking its precondition. It returns
// ErrPrecondition when RequireZero is not fully satisfied, otherwise the
// successor state.
func (a Action) Step(state Mask) (Mask, error) {
	if !a.Legal(state) {
		return 0, ErrPrecondition
	}
	return a.Apply(state), nil
}
