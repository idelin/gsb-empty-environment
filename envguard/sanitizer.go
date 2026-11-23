package envguard

import "fmt"

// Sanitizer is a host-declared, trusted rewriting relation.
//
// The host must state the exact input/output relation rather than merely
// naming a function. Two relation shapes are supported:
//
//   - Remove: the output is the input with every rune in Remove deleted; all
//     other runes keep their identity. Deleting "dangerous" characters only
//     vouches for the output content: it cannot erase the fact that an
//     untrusted value still decided how long the result is and where the
//     surviving characters landed. Such data control dependencies are kept.
//   - FixedLengthMap: the output always has exactly OutLen runes and each
//     output rune is a deterministic function of the input. Because the shape
//     cannot vary with the input, this relation may also launder the data
//     control dependency; branch control (selector influence) is retained.
type Sanitizer struct {
	Name string

	// Remove is non-nil for the deletion relation.
	Remove *RemoveRelation

	// Fixed is non-nil for the fixed-length mapping relation.
	Fixed *FixedRelation

	// Map computes the output rune at offset out for the fixed-length
	// relation. It must be a pure function of the input sequence.
	mapFn func(seq RuneSeq, out int64) rune
}

// RemoveRelation deletes every rune belonging to Ranges.
type RemoveRelation struct {
	Ranges []runeRange
}

// FixedRelation produces an output of exactly OutLen runes.
type FixedRelation struct {
	OutLen int64
}

// NewRemoveSanitizer declares a deletion sanitizer.
func NewRemoveSanitizer(name string, removed []RuneRange) *Sanitizer {
	return &Sanitizer{
		Name:   name,
		Remove: &RemoveRelation{Ranges: mergeRanges(toInternalRanges(removed))},
	}
}

// NewFixedSanitizer declares a fixed-length mapping sanitizer. mapFn maps an
// output offset to the rune produced from the input sequence.
func NewFixedSanitizer(name string, outLen int64, mapFn func(RuneSeq, int64) rune) *Sanitizer {
	return &Sanitizer{
		Name:  name,
		Fixed: &FixedRelation{OutLen: outLen},
		mapFn: mapFn,
	}
}

func (s *Sanitizer) validate() error {
	remove := s.Remove != nil
	fixed := s.Fixed != nil
	if remove == fixed {
		return fmt.Errorf("envguard: sanitizer %q must declare exactly one relation", s.Name)
	}
	if fixed && s.Fixed.OutLen < 0 {
		return fmt.Errorf("envguard: sanitizer %q has negative output length", s.Name)
	}
	if fixed && s.mapFn == nil {
		return fmt.Errorf("envguard: sanitizer %q fixed relation needs a mapping function", s.Name)
	}
	return nil
}

func (s *Sanitizer) removes(r rune) bool {
	for _, rg := range s.Remove.Ranges {
		if rg.contains(r) {
			return true
		}
	}
	return false
}
