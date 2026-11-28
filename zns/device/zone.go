package device

// ZoneState mirrors the NVMe ZNS zone descriptor states.
type ZoneState int

const (
	// ZoneEmpty: no logical block has been written; not counted as active.
	ZoneEmpty ZoneState = iota
	// ZoneImplicitlyOpen: opened implicitly by a write to an EMPTY/CLOSED zone.
	ZoneImplicitlyOpen
	// ZoneExplicitlyOpen: opened with the Zone Management Open action.
	ZoneExplicitlyOpen
	// ZoneClosed: an open zone that retained its active resources.
	ZoneClosed
	// ZoneFull: ZoneCapacity has been consumed (or Zone Finish was issued).
	ZoneFull
	// ZoneReadOnly: host writes are rejected, reads remain allowed.
	ZoneReadOnly
	// ZoneOffline: all access except descriptor queries is rejected.
	ZoneOffline
)

// String returns the NVMe-style name of the state.
func (s ZoneState) String() string {
	switch s {
	case ZoneEmpty:
		return "EMPTY"
	case ZoneImplicitlyOpen:
		return "IMPLICITLY_OPEN"
	case ZoneExplicitlyOpen:
		return "EXPLICITLY_OPEN"
	case ZoneClosed:
		return "CLOSED"
	case ZoneFull:
		return "FULL"
	case ZoneReadOnly:
		return "READ_ONLY"
	case ZoneOffline:
		return "OFFLINE"
	default:
		return "UNKNOWN"
	}
}

// IsOpen reports whether the zone currently holds an Open resource
// (implicitly or explicitly open).
func (s ZoneState) IsOpen() bool {
	return s == ZoneImplicitlyOpen || s == ZoneExplicitlyOpen
}

// IsActive reports whether the zone currently holds an Active resource
// (open zones plus CLOSED zones).
func (s ZoneState) IsActive() bool {
	return s.IsOpen() || s == ZoneClosed
}

// IsWritable reports whether ordinary writes / appends are legal in this state.
func (s ZoneState) IsWritable() bool {
	return s == ZoneImplicitlyOpen ||
		s == ZoneExplicitlyOpen ||
		s == ZoneEmpty ||
		s == ZoneClosed
}

// zone is the internal mutable descriptor. All access happens under Device.mu.
type zone struct {
	id       int
	state    ZoneState
	slba     int64
	size     int64 // ZoneSize in logical blocks (descriptor span)
	capacity int64 // ZoneCapacity in logical blocks; capacity <= size
	wp       int64 // WritePointer as an absolute LBA; equals slba when EMPTY
}

// writtenBlocks returns how many logical blocks have been written in the zone.
func (z *zone) writtenBlocks() int64 { return z.wp - z.slba }

// capacityEnd returns the absolute LBA one past the last writable block. Note
// that this is slba+ZoneCapacity, which may be strictly below slba+ZoneSize.
func (z *zone) capacityEnd() int64 { return z.slba + z.capacity }

// ZoneInfo is the exported, lock-free snapshot of a zone descriptor.
type ZoneInfo struct {
	ID            int
	State         ZoneState
	SLBA          int64
	ZoneSize      int64
	ZoneCapacity  int64
	WritePointer  int64
	WrittenBlocks int64
}

func (z *zone) info() ZoneInfo {
	return ZoneInfo{
		ID:            z.id,
		State:         z.state,
		SLBA:          z.slba,
		ZoneSize:      z.size,
		ZoneCapacity:  z.capacity,
		WritePointer:  z.wp,
		WrittenBlocks: z.writtenBlocks(),
	}
}

// managementOp identifies a Zone Management Send action for error context.
type managementOp string

const (
	opOpen    managementOp = "ZoneOpen"
	opClose   managementOp = "ZoneClose"
	opFinish  managementOp = "ZoneFinish"
	opReset   managementOp = "ZoneReset"
	opOffline managementOp = "ZoneSetOffline"
	opRO      managementOp = "ZoneSetReadOnly"
)

// resetDescriptor restores a zone to EMPTY: write pointer back to SLBA and all
// stored blocks dropped by the caller.
func (z *zone) resetDescriptor() {
	z.state = ZoneEmpty
	z.wp = z.slba
}
