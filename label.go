package iflow

import (
	"fmt"
	"sort"
)

// Label is the security label carried by every IR variable: a lattice level
// plus the set of receivers this value is allowed to flow to. Any=true means
// the receiver dimension is unrestricted (the bottom of that dimension).
//
// Label ordering: l <= o iff l.Level <= o.Level in the lattice AND
// l.Receivers is a superset of o.Receivers (l is no more restrictive).
// Join therefore takes the lattice join of levels and the intersection of
// allowed receivers.
type Label struct {
	lat       *Lattice
	Level     Level
	Any       bool
	Receivers map[string]bool
}

// NewLabel builds a label at the named level restricted to the given
// receivers. With no receivers the value may flow to nobody.
func NewLabel(lat *Lattice, level string, receivers ...string) Label {
	lv, err := lat.LevelOf(level)
	if err != nil {
		panic(err)
	}
	l := Label{lat: lat, Level: lv, Receivers: map[string]bool{}}
	for _, r := range receivers {
		l.Receivers[r] = true
	}
	return l
}

// BottomLabel is the least label: bottom lattice level, unrestricted receivers.
func BottomLabel(lat *Lattice) Label {
	return Label{lat: lat, Level: lat.Bottom(), Any: true}
}

// IsBottom reports whether l is the least label.
func (l Label) IsBottom() bool {
	return l.Any && l.lat.Leq(l.Level, l.lat.Bottom())
}

func copyReceivers(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Join returns the least upper bound of two labels: lattice join on levels,
// intersection on allowed receivers.
func (l Label) Join(o Label) Label {
	lat := l.lat
	if lat == nil {
		lat = o.lat
	}
	out := Label{lat: lat, Level: lat.Join(l.Level, o.Level)}
	switch {
	case l.Any && o.Any:
		out.Any = true
	case l.Any:
		out.Receivers = copyReceivers(o.Receivers)
	case o.Any:
		out.Receivers = copyReceivers(l.Receivers)
	default:
		out.Receivers = map[string]bool{}
		for r := range l.Receivers {
			if o.Receivers[r] {
				out.Receivers[r] = true
			}
		}
	}
	return out
}

// Leq reports whether l is no more restrictive than o.
func (l Label) Leq(o Label) bool {
	if !l.lat.Leq(l.Level, o.Level) {
		return false
	}
	if l.Any {
		return true
	}
	if o.Any {
		return false
	}
	for r := range o.Receivers {
		if !l.Receivers[r] {
			return false
		}
	}
	return true
}

// Allows reports whether a value with this label may flow to receiver r.
func (l Label) Allows(r string) bool {
	return l.Any || l.Receivers[r]
}

func (l Label) String() string {
	name := l.lat.Name(l.Level)
	if l.Any {
		return name + "{*}"
	}
	rs := make([]string, 0, len(l.Receivers))
	for r := range l.Receivers {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	return fmt.Sprintf("%s%v", name, rs)
}
