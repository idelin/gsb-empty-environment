package netsec

// Addr is a simulated machine address inside a code page.
type Addr uint64

// Region is a half-open address interval [Start, End).
type Region struct {
	Start Addr
	End   Addr
}

// Contains reports whether addr belongs to the region.
func (r Region) Contains(addr Addr) bool {
	return addr >= r.Start && addr < r.End
}

// Size returns the byte length of the region.
func (r Region) Size() uint64 {
	return uint64(r.End - r.Start)
}

// Valid reports whether the region boundaries are ordered correctly.
func (r Region) Valid() bool {
	return r.End > r.Start
}

// Plan describes one code-page hot replacement transaction.
//
// OldBytes/NewBytes cover the whole patched region. Boundary maps selected
// instruction entry points inside the old sequence to their counterparts in
// the new sequence; a frozen thread is migratable only when its program
// counter is one of those boundary addresses.
type Plan struct {
	Region     Region
	OldBytes   []byte
	NewBytes   []byte
	OldToNewPC map[Addr]Addr
}

func (p Plan) validate() error {
	if !p.Region.Valid() {
		return ErrInvalidRegion
	}
	if uint64(len(p.OldBytes)) != p.Region.Size() || uint64(len(p.NewBytes)) != p.Region.Size() {
		return ErrSizeMismatch
	}
	if len(p.OldToNewPC) == 0 {
		return ErrEmptyBoundaryMap
	}
	for oldPC, newPC := range p.OldToNewPC {
		if !p.Region.Contains(oldPC) || !p.Region.Contains(newPC) {
			return ErrBoundaryOutOfRange
		}
	}
	return nil
}
