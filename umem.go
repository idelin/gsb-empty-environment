package netsec

import "errors"

// UMEMConfig describes the geometry of an AF_XDP UMEM: a single contiguous
// region of Size bytes carved into fixed-size frames.
type UMEMConfig struct {
	// Size is the total UMEM length in bytes.
	Size uint64
	// FrameSize is the UMEM chunk/frame size in bytes.
	FrameSize uint64
	// Unaligned selects unaligned chunk mode. When false (aligned mode) the
	// kernel requires every descriptor address to be frame aligned. When
	// true descriptors may start at any offset but must not straddle a frame
	// boundary.
	Unaligned bool
}

// Descriptor mirrors the address/length pair the tenant programs into a TX
// or FILL ring slot. Addr is an offset from the start of the UMEM.
type Descriptor struct {
	Addr uint64
	Len  uint64
}

var (
	// ErrInvalidUMEM is returned for zero-sized UMEMs or frames, or frames
	// larger than the region itself.
	ErrInvalidUMEM = errors.New("netsec: invalid UMEM geometry")
	// ErrZeroLength is returned for a descriptor with no payload.
	ErrZeroLength = errors.New("netsec: descriptor length is zero")
	// ErrAddrOverflow is returned when addr+len wraps the address space,
	// which would otherwise defeat the bounds check.
	ErrAddrOverflow = errors.New("netsec: descriptor addr+len overflow")
	// ErrOutOfUMEM is returned when the descriptor range is not fully
	// contained in the UMEM.
	ErrOutOfUMEM = errors.New("netsec: descriptor lies outside the UMEM")
	// ErrUnalignedAddr is returned in aligned mode for an address that does
	// not start on a frame boundary.
	ErrUnalignedAddr = errors.New("netsec: descriptor address is not frame aligned")
	// ErrCrossesChunk is returned in unaligned mode when a descriptor spans
	// two frames.
	ErrCrossesChunk = errors.New("netsec: descriptor crosses a UMEM chunk boundary")
)

// valid reports whether the UMEM geometry can back descriptors.
func (u UMEMConfig) valid() bool {
	return u.Size > 0 && u.FrameSize > 0 && u.FrameSize <= u.Size
}

// CheckDescriptor validates a tenant-supplied TX/FILL descriptor against the
// UMEM before it is handed to the kernel or the NIC. It enforces, in order:
//
//   - the descriptor has a non-zero length;
//   - addr+len cannot overflow uint64;
//   - the full [addr, addr+len) range lies inside the UMEM;
//   - aligned mode requires addr to start on a frame boundary;
//   - unaligned mode forbids a descriptor from straddling a frame boundary.
func (Policy) CheckDescriptor(u UMEMConfig, d Descriptor) error {
	if !u.valid() {
		return ErrInvalidUMEM
	}
	if d.Len == 0 {
		return ErrZeroLength
	}
	// Reject wraparound so the containment comparison below cannot be
	// bypassed with an address near the top of the address space.
	end := d.Addr + d.Len
	if end < d.Addr {
		return ErrAddrOverflow
	}
	if d.Addr >= u.Size || end > u.Size {
		return ErrOutOfUMEM
	}

	if !u.Unaligned {
		if d.Addr%u.FrameSize != 0 {
			return ErrUnalignedAddr
		}
		return nil
	}

	// A chunk is the half-open frame [base, base+frameSize). A descriptor is
	// contained in one chunk exactly when its start and end share a base.
	base := d.Addr - d.Addr%u.FrameSize
	if end > base+u.FrameSize {
		return ErrCrossesChunk
	}
	return nil
}
