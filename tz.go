package chronos

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrGap: the wall clock time falls inside a spring-forward gap.
	ErrGap = errors.New("chronos: wall clock time falls in a UTC offset gap")
	// ErrUnknownTZ: the timezone table version is not in the registry, so the
	// value is marked unresolvable instead of being guessed.
	ErrUnknownTZ = errors.New("chronos: unknown timezone table version")
	// ErrVersionMismatch: the supplied table does not match the value metadata.
	ErrVersionMismatch = errors.New("chronos: timezone table version mismatch")
)

const (
	MaxZoneLabelLen    = 64
	MaxVersionLabelLen = 32
	MaxOffsetSec       = 18 * 3600
)

// validLabel rejects illegal zone/version labels before object publication.
func validLabel(s string, max int) bool {
	if len(s) == 0 || len(s) > max {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '-' || c == '/' || c == '+' || c == '.'
		if !ok {
			return false
		}
	}
	return true
}

// OffsetChoice records which UTC offset was selected for a wall clock time.
// Fold disambiguates overlapping (fall-back) wall times: it is the index of
// the chosen candidate ordered by instant, earliest first.
type OffsetChoice struct {
	OffsetSec int32
	Fold      uint8
}

func NewOffsetChoice(offsetSec int32, fold uint8) (OffsetChoice, error) {
	if offsetSec > MaxOffsetSec || offsetSec < -MaxOffsetSec {
		return OffsetChoice{}, fmt.Errorf("chronos: offset %d s outside supported range +/-%d s", offsetSec, MaxOffsetSec)
	}
	if fold > 3 {
		return OffsetChoice{}, fmt.Errorf("chronos: illegal fold label %d", fold)
	}
	return OffsetChoice{OffsetSec: offsetSec, Fold: fold}, nil
}

// Transition is a UTC-offset change at an instant.
type Transition struct {
	AtSec        int64 // UTC instant when the change takes effect
	OffsetBefore int32 // offset seconds before AtSec
	OffsetAfter  int32 // offset seconds from AtSec onward
}

// TZTable is a versioned timezone table for one zone.
type TZTable struct {
	Zone       string
	Version    string
	BaseOffset int32 // offset before the first transition
	Trans      []Transition
}

func NewTZTable(zone, version string, baseOffset int32, trans []Transition) (*TZTable, error) {
	if !validLabel(zone, MaxZoneLabelLen) {
		return nil, fmt.Errorf("chronos: illegal zone label %q", zone)
	}
	if !validLabel(version, MaxVersionLabelLen) {
		return nil, fmt.Errorf("chronos: illegal timezone version label %q", version)
	}
	if baseOffset > MaxOffsetSec || baseOffset < -MaxOffsetSec {
		return nil, fmt.Errorf("chronos: base offset %d out of range", baseOffset)
	}
	for k, tr := range trans {
		if tr.OffsetBefore > MaxOffsetSec || tr.OffsetBefore < -MaxOffsetSec ||
			tr.OffsetAfter > MaxOffsetSec || tr.OffsetAfter < -MaxOffsetSec {
			return nil, fmt.Errorf("chronos: transition %d offset out of range", k)
		}
		if k > 0 && trans[k-1].AtSec >= tr.AtSec {
			return nil, fmt.Errorf("chronos: transitions not strictly increasing at %d", k)
		}
		if k > 0 && trans[k-1].OffsetAfter != tr.OffsetBefore {
			return nil, fmt.Errorf("chronos: transition %d offset chain broken", k)
		}
	}
	cp := make([]Transition, len(trans))
	copy(cp, trans)
	return &TZTable{Zone: zone, Version: version, BaseOffset: baseOffset, Trans: cp}, nil
}

// OffsetAt returns the UTC offset in effect at the given instant.
func (t *TZTable) OffsetAt(sec int64) int32 {
	off := t.BaseOffset
	for _, tr := range t.Trans {
		if sec < tr.AtSec {
			break
		}
		off = tr.OffsetAfter
	}
	return off
}

// ResolveWall returns every offset under which wallSec (local seconds) is
// valid, ordered by instant (earliest first) with Fold set to the index.
// Zero candidates means the wall time falls in a gap.
func (t *TZTable) ResolveWall(wallSec int64) []OffsetChoice {
	seen := map[int32]bool{t.BaseOffset: true}
	offs := []int32{t.BaseOffset}
	for _, tr := range t.Trans {
		if !seen[tr.OffsetAfter] {
			seen[tr.OffsetAfter] = true
			offs = append(offs, tr.OffsetAfter)
		}
	}
	var cands []OffsetChoice
	for _, o := range offs {
		if t.OffsetAt(wallSec-int64(o)) == o {
			cands = append(cands, OffsetChoice{OffsetSec: o})
		}
	}
	sort.Slice(cands, func(a, b int) bool {
		return wallSec-int64(cands[a].OffsetSec) < wallSec-int64(cands[b].OffsetSec)
	})
	for k := range cands {
		cands[k].Fold = uint8(k)
	}
	return cands
}

// Registry holds the known timezone table versions. A value whose version is
// not registered is unresolvable; it is never resolved by guessing.
type Registry struct {
	tables map[string]*TZTable
}

func NewRegistry() *Registry { return &Registry{tables: map[string]*TZTable{}} }

func (r *Registry) Register(t *TZTable) {
	r.tables[t.Zone+"@"+t.Version] = t
}

func (r *Registry) Lookup(zone, version string) (*TZTable, error) {
	if t, ok := r.tables[zone+"@"+version]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("%w: %s@%s", ErrUnknownTZ, zone, version)
}
