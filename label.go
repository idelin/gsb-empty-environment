package netsec

import (
	"fmt"
	"math/bits"
	"strings"
)

// MaxSources is the maximum number of distinct secret sources tracked by a
// label. Source identifiers are therefore in the range [0, MaxSources).
const MaxSources = 16

// Source identifies one secret input. The executor never guesses a value for
// a source that a request did not supply.
type Source uint8

// Valid reports whether s fits in the 16-bit label representation.
func (s Source) Valid() bool { return uint8(s) < MaxSources }

// Label is the set of secret sources on which a value or control decision
// depends. Bit i is set exactly when source i is in the set.
type Label uint16

// NewLabel constructs a label from source identifiers. It panics for an
// invalid source; NewLabel is intended for trusted policy configuration.
// Untrusted source identifiers in an expression are validated by the executor
// and return ErrInvalidSource.
func NewLabel(sources ...Source) Label {
	var label Label
	for _, source := range sources {
		if !source.Valid() {
			panic(fmt.Sprintf("netsec: source %d outside [0,%d)", source, MaxSources))
		}
		label |= 1 << uint(source)
	}
	return label
}

// With returns the label with source added.
func (l Label) With(source Source) Label {
	if !source.Valid() {
		panic(fmt.Sprintf("netsec: source %d outside [0,%d)", source, MaxSources))
	}
	return l | 1<<uint(source)
}

// Union returns the union of two labels.
func (l Label) Union(other Label) Label { return l | other }

// Remove returns l with every source in other removed.
func (l Label) Remove(other Label) Label { return l &^ other }

// Contains reports whether source is in the label.
func (l Label) Contains(source Source) bool {
	if !source.Valid() {
		return false
	}
	return l&(1<<uint(source)) != 0
}

// IsEmpty reports whether the label contains no sources.
func (l Label) IsEmpty() bool { return l == 0 }

// Count returns the number of sources in the label.
func (l Label) Count() int { return bits.OnesCount16(uint16(l)) }

// Sources returns the sources in the label in ascending order.
func (l Label) Sources() []Source {
	out := make([]Source, 0, l.Count())
	for source := Source(0); source < MaxSources; source++ {
		if l.Contains(source) {
			out = append(out, source)
		}
	}
	return out
}

// String renders a compact, stable representation such as "{s0,s3}".
func (l Label) String() string {
	if l.IsEmpty() {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for _, source := range l.Sources() {
		if !first {
			b.WriteByte(',')
		}
		first = false
		fmt.Fprintf(&b, "s%d", source)
	}
	b.WriteByte('}')
	return b.String()
}
