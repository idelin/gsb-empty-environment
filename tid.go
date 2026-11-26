package gin

// TID identifies a heap tuple. It is encoded as a 48-bit page number followed
// by a 16-bit offset. Keeping TID as an integer makes it directly usable as a
// map key and keeps the public API friendly to callers with their own encoding.
type TID = uint64

const (
	// TIDOffsetBits is the number of low bits used by the tuple offset.
	TIDOffsetBits = 16
	// TIDOffsetMask extracts the tuple offset from a TID.
	TIDOffsetMask = uint64(1<<TIDOffsetBits) - 1
)

// MakeTID builds a TID from a heap page and an offset within that page.
func MakeTID(page uint64, offset uint16) TID {
	return page<<TIDOffsetBits | uint64(offset)
}

// NewTID is an alias for MakeTID.
func NewTID(page uint64, offset uint16) TID {
	return MakeTID(page, offset)
}

// TIDPage returns the heap page encoded in a TID.
func TIDPage(tid TID) uint64 {
	return tid >> TIDOffsetBits
}

// TIDOffset returns the slot offset encoded in a TID.
func TIDOffset(tid TID) uint16 {
	return uint16(tid & TIDOffsetMask)
}

// ItemPointer is a compatibility alias for TID.
type ItemPointer = TID
