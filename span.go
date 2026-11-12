package netsec

import "sort"

// Kind classifies how the bytes of an image interval are evidenced.
type Kind int

const (
	// Missing marks an interval with no backing evidence. Reads fail.
	Missing Kind = iota
	// Original marks an interval backed directly by a registered
	// evidence source (a device image, an acquired file, ...).
	Original
	// Derived marks an interval whose bytes were produced by analysis
	// (carving, decryption, decompression, ...). Its evidentiary value
	// is inherited from the dependency intervals it declares.
	Derived
)

func (k Kind) String() string {
	switch k {
	case Original:
		return "original"
	case Derived:
		return "derived"
	default:
		return "missing"
	}
}

// Span is a half-open interval [Off, Off+Len) of the sparse image.
type Span struct {
	Off int64
	Len int64
}

// End returns the exclusive end offset of the span.
func (s Span) End() int64 { return s.Off + s.Len }

// Empty reports whether the span covers no bytes.
func (s Span) Empty() bool { return s.Len <= 0 }

// Overlaps reports whether the two spans share at least one byte.
func (s Span) Overlaps(o Span) bool {
	return s.Off < o.End() && o.Off < s.End()
}

// Intersect returns the overlap of the two spans, or an empty span.
func (s Span) Intersect(o Span) Span {
	off := max(s.Off, o.Off)
	end := min(s.End(), o.End())
	if end <= off {
		return Span{Off: off}
	}
	return Span{Off: off, Len: end - off}
}

// Segment declares the provenance of one interval of the sparse image.
//
// For Original segments, Source names a reader registered with AddSource
// and SrcOff is the offset inside that source where the interval starts.
// For Derived segments, Data supplies the synthesized bytes (SrcOff
// applies likewise) and Deps lists the image intervals the bytes were
// derived from. Missing segments carry neither bytes nor provenance.
type Segment struct {
	Span   Span
	Kind   Kind
	Source string
	SrcOff int64
	Deps   []Span
	Data   interface {
		ReadAt(p []byte, off int64) (n int, err error)
	}
}

// Fragment is a minimal piece of original evidence: Len bytes of Source
// starting at Span.Off within the source. Reads report the minimal set
// of fragments backing the returned bytes.
type Fragment struct {
	Source string
	Span   Span
}

// MinimizeFragments sorts fragments and merges overlapping or adjacent
// fragments that name the same source, yielding the minimal set.
func MinimizeFragments(frags []Fragment) []Fragment {
	if len(frags) == 0 {
		return nil
	}
	sorted := make([]Fragment, len(frags))
	copy(sorted, frags)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Source != sorted[j].Source {
			return sorted[i].Source < sorted[j].Source
		}
		return sorted[i].Span.Off < sorted[j].Span.Off
	})
	out := sorted[:1]
	for _, f := range sorted[1:] {
		last := &out[len(out)-1]
		if f.Source == last.Source && f.Span.Off <= last.Span.End() {
			if end := f.Span.End(); end > last.Span.End() {
				last.Span.Len = end - last.Span.Off
			}
			continue
		}
		out = append(out, f)
	}
	return out
}
