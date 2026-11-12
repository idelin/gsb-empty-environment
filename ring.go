package netsec

import "errors"

// Ring models one AF_XDP ring (RX, TX, FILL or COMPLETION). AF_XDP rings
// require a size that is a power of two, which makes index wrapping a simple
// mask instead of a division on the hot path.
type Ring struct {
	size uint32
	mask uint32
}

var (
	// ErrInvalidRingSize is returned when a ring size is zero, smaller than
	// two, or not a power of two.
	ErrInvalidRingSize = errors.New("netsec: ring size must be a power of two >= 2")
	// ErrIndexOutOfRange is returned for a bare slot index that is not a
	// legal slot of the ring.
	ErrIndexOutOfRange = errors.New("netsec: ring index out of range")
)

// NewRing constructs a ring of the given size. The size must be a power of
// two and at least two slots, matching the kernel bind constraints.
func NewRing(size uint32) (Ring, error) {
	if size < 2 || size&(size-1) != 0 {
		return Ring{}, ErrInvalidRingSize
	}
	return Ring{size: size, mask: size - 1}, nil
}

// Size returns the number of slots in the ring.
func (r Ring) Size() uint32 { return r.size }

// Wrap folds an unbounded producer/consumer index into a slot index. Ring
// indices are monotonically increasing 32-bit values and wrap on overflow;
// because the size is a power of two the slot is index & (size-1).
func (r Ring) Wrap(index uint32) uint32 {
	return index & r.mask
}

// CheckIndex validates a direct slot index. A slot index must be in
// [0, Size). Monotonically increasing ring cursors should be folded with
// Wrap first; feeding a raw cursor here is rejected as out of range.
func (r Ring) CheckIndex(index uint32) error {
	if index >= r.size {
		return ErrIndexOutOfRange
	}
	return nil
}
