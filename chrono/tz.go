package chrono

import (
	"fmt"
	"sort"
)

// Transition is a point after which a new UTC offset is in effect.
type Transition struct {
	AtNanos   int64 // instant when OffsetSec takes effect
	OffsetSec int32 // UTC offset in effect from AtNanos
}

// Zone is one versioned timezone table for a named zone. Different versions
// of the same zone are distinct Zone values; the version is part of a
// WallClock's metadata and is required for resolution.
type Zone struct {
	Name        string
	Version     string
	BaseOffset  int32        // offset before the first transition
	Transitions []Transition // sorted by AtNanos
}

// OffsetAt returns the UTC offset in effect at the given instant.
func (z Zone) OffsetAt(nanos int64) int32 {
	off := z.BaseOffset
	for _, t := range z.Transitions {
		if t.AtNanos <= nanos {
			off = t.OffsetSec
		} else {
			break
		}
	}
	return off
}

// Candidates returns every instant consistent with the civil fields of w
// under this table, sorted ascending. A gap yields zero candidates, an
// overlap (fall-back) yields two. The wall reading's stored offset is
// ignored here; this is the raw set the OffsetChoice selects from.
func (z Zone) Candidates(w WallClock) []int64 {
	wall := w.wallNanos()
	seen := map[int64]bool{}
	var out []int64
	try := func(off int32) {
		inst := wall - int64(off)*1000000000
		if z.OffsetAt(inst) == off && !seen[inst] {
			seen[inst] = true
			out = append(out, inst)
		}
	}
	try(z.BaseOffset)
	for _, t := range z.Transitions {
		try(t.OffsetSec)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// Registry holds versioned zone tables. A WallClock whose zone@version is
// absent is unresolvable; Resolve reports that explicitly.
type Registry struct {
	zones map[string]Zone
}

func NewRegistry(zones ...Zone) *Registry {
	r := &Registry{zones: make(map[string]Zone, len(zones))}
	for _, z := range zones {
		r.zones[z.Name+"@"+z.Version] = z
	}
	return r
}

// Lookup finds the table for name@version, distinguishing an unknown zone
// from an unknown table version of a known zone.
func (r *Registry) Lookup(name, version string) (Zone, error) {
	if z, ok := r.zones[name+"@"+version]; ok {
		return z, nil
	}
	for _, z := range r.zones {
		if z.Name == name {
			return Zone{}, fmt.Errorf("%w: %s@%s", ErrUnknownVersion, name, version)
		}
	}
	return Zone{}, fmt.Errorf("%w: %s", ErrUnknownZone, name)
}

// Resolve is the only WallClock -> Instant conversion. It is explicit,
// requires the versioned table named by the WallClock's metadata, and
// refuses ambiguous readings that carry no OffsetChoice. A WallClock value
// is never silently reinterpreted as an Instant.
func Resolve(w WallClock, reg *Registry) (Instant, error) {
	z, err := reg.Lookup(w.Zone(), w.TZVersion())
	if err != nil {
		return Instant{}, err
	}
	cands := z.Candidates(w)
	switch len(cands) {
	case 0:
		return Instant{}, fmt.Errorf("%w: %s", ErrGap, w)
	case 1:
		return NewInstant(cands[0], w.Precision())
	}
	switch w.Choice() {
	case ChoiceEarlier:
		return NewInstant(cands[0], w.Precision())
	case ChoiceLater:
		return NewInstant(cands[1], w.Precision())
	}
	return Instant{}, fmt.Errorf("%w: %s", ErrAmbiguous, w)
}
