package takeover

import (
	"encoding/json"
	"sort"
)

// Dimension identifies one delegated terminal surface. DisplayID alone is not
// a key: two windows may share a display. WindowID distinguishes those
// windows. A dimension with an empty WindowID means the whole display.
type Dimension struct {
	DisplayID string `json:"display_id"`
	WindowID  string `json:"window_id,omitempty"`
}

// TerminalDimension is an alias for Dimension.
type TerminalDimension = Dimension

// NewDimension constructs a terminal dimension.
func NewDimension(displayID, windowID string) Dimension {
	return Dimension{DisplayID: displayID, WindowID: windowID}
}

// WindowDimension is an alias for NewDimension.
func WindowDimension(displayID, windowID string) Dimension {
	return NewDimension(displayID, windowID)
}

// DisplayDimension constructs a dimension that covers an entire display.
func DisplayDimension(displayID string) Dimension {
	return Dimension{DisplayID: displayID}
}

// Valid reports whether the dimension has at least one identifying part.
func (d Dimension) Valid() bool {
	return d.DisplayID != "" || d.WindowID != ""
}

// Overlaps reports whether the two dimensions can refer to the same surface.
// An empty WindowID denotes the whole display and therefore overlaps every
// window on that display.
func (d Dimension) Overlaps(other Dimension) bool {
	if !d.Valid() || !other.Valid() {
		return false
	}
	if d.DisplayID == "" || other.DisplayID == "" {
		if d.DisplayID == "" && other.DisplayID == "" {
			return d.WindowID == "" || other.WindowID == "" || d.WindowID == other.WindowID
		}
		return false
	}
	if d.DisplayID != other.DisplayID {
		return false
	}
	if d.WindowID == "" || other.WindowID == "" {
		return true
	}
	return d.WindowID == other.WindowID
}

// DimensionSet is a set of terminal surfaces. A nil set is empty.
type DimensionSet map[Dimension]struct{}

// NewDimensionSet constructs a dimension set.
func NewDimensionSet(dimensions ...Dimension) (DimensionSet, error) {
	set := make(DimensionSet, len(dimensions))
	for _, dimension := range dimensions {
		if !dimension.Valid() {
			return nil, ErrInvalidDimension
		}
		if _, exists := set[dimension]; exists {
			continue
		}
		for existing := range set {
			if existing.Overlaps(dimension) {
				return nil, &DimensionConflictError{A: existing, B: dimension}
			}
		}
		set[dimension] = struct{}{}
	}
	return set, nil
}

// Dimensions constructs a dimension set and panics on an invalid dimension.
// It is intended for literals and tests.
func Dimensions(dimensions ...Dimension) DimensionSet {
	set, err := NewDimensionSet(dimensions...)
	if err != nil {
		panic(err)
	}
	return set
}

// Clone returns an independent copy.
func (s DimensionSet) Clone() DimensionSet {
	if s == nil {
		return nil
	}
	out := make(DimensionSet, len(s))
	for dimension := range s {
		out[dimension] = struct{}{}
	}
	return out
}

// Contains reports exact membership.
func (s DimensionSet) Contains(dimension Dimension) bool {
	_, ok := s[dimension]
	return ok
}

// IsEmpty reports whether the set has no members.
func (s DimensionSet) IsEmpty() bool {
	return len(s) == 0
}

// Equal reports exact set equality.
func (s DimensionSet) Equal(other DimensionSet) bool {
	if len(s) != len(other) {
		return false
	}
	for dimension := range s {
		if _, ok := other[dimension]; !ok {
			return false
		}
	}
	return true
}

// Intersects reports whether any pair of members overlaps.
func (s DimensionSet) Intersects(other DimensionSet) bool {
	for a := range s {
		for b := range other {
			if a.Overlaps(b) {
				return true
			}
		}
	}
	return false
}

// SubsetOf reports whether every member of s is contained in parent. A
// display-wide member of s is contained by a parent only if parent contains a
// display-wide member; a window member is contained by either the same window
// or the whole display.
func (s DimensionSet) SubsetOf(parent DimensionSet) bool {
	for dimension := range s {
		covered := false
		for candidate := range parent {
			if candidate == dimension || (candidate.DisplayID == dimension.DisplayID &&
				candidate.WindowID == "" && dimension.DisplayID != "") {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// Union returns the members of both sets. Invalid overlapping combinations
// are retained; use Canonical to reject them.
func (s DimensionSet) Union(other DimensionSet) DimensionSet {
	out := s.Clone()
	if out == nil {
		out = make(DimensionSet)
	}
	for dimension := range other {
		out[dimension] = struct{}{}
	}
	return out
}

// Subtract removes exact members of other.
func (s DimensionSet) Subtract(other DimensionSet) DimensionSet {
	out := make(DimensionSet, len(s))
	for dimension := range s {
		if _, ok := other[dimension]; !ok {
			out[dimension] = struct{}{}
		}
	}
	return out
}

// Intersect returns members that overlap in both sets.
func (s DimensionSet) Intersect(other DimensionSet) DimensionSet {
	out := make(DimensionSet)
	for a := range s {
		for b := range other {
			if a.Overlaps(b) {
				// Prefer the more specific member so a window intersection
				// with a display-wide scope remains a window.
				if a.WindowID != "" {
					out[a] = struct{}{}
				} else {
					out[b] = struct{}{}
				}
			}
		}
	}
	return out
}

// List returns members in stable order.
func (s DimensionSet) List() []Dimension {
	out := make([]Dimension, 0, len(s))
	for dimension := range s {
		out = append(out, dimension)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DisplayID != out[j].DisplayID {
			return out[i].DisplayID < out[j].DisplayID
		}
		return out[i].WindowID < out[j].WindowID
	})
	return out
}

// MarshalJSON encodes dimensions as a stable array rather than a map with
// struct keys.
func (s DimensionSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.List())
}

// UnmarshalJSON decodes an array of dimensions and rejects overlaps.
func (s *DimensionSet) UnmarshalJSON(data []byte) error {
	var dimensions []Dimension
	if err := json.Unmarshal(data, &dimensions); err != nil {
		return err
	}
	set, err := NewDimensionSet(dimensions...)
	if err != nil {
		return err
	}
	*s = set
	return nil
}
