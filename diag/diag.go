// Package diag builds security-safe diagnostic errors for parser hosts.
//
// A diagnostic carries structured byte intervals (the failing range and any
// sensitive ranges with labels) but never renders raw secret bytes. Rendered
// context is bounded by caller-declared authorized intervals, never splits a
// UTF-8 rune across a clipping or redaction boundary, and substitutes every
// hidden sensitive span with the same fixed-width Marker, so the length and
// number of redacted secrets cannot be inferred from the output.
package diag

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Marker is the single fixed token substituted for every hidden sensitive
// span. It is a compile-time constant with one fixed length: rendered output
// reveals neither how many secrets were hidden nor how long they were.
const Marker = "[REDACTED]"

// contextMargin is the number of raw bytes offered on each side of a failure
// before authorization clipping is applied.
const contextMargin = 16

// Interval is a half-open byte range [Start, End) over the host input.
type Interval struct {
	Start int
	End   int
}

func (iv Interval) valid() bool {
	return iv.Start >= 0 && iv.End >= iv.Start
}

// Secret labels a sensitive byte span. Label is structured metadata returned
// to the trusted host through the API; it is never embedded in rendered
// diagnostics, so label text and length cannot leak through output.
type Secret struct {
	Interval
	Label string
}

// Error is a parser diagnostic. It references the host input by byte
// intervals only; the raw input is supplied to the render functions and is
// never retained.
type Error struct {
	Field   string
	Message string
	Failure Interval

	// Secrets marks every sensitive byte range related to this diagnostic.
	Secrets []Secret

	// Authorized bounds what rendered context may contain. A byte outside
	// the union of these intervals is silently omitted (not marked), so the
	// output cannot disclose the shape of unauthorized neighboring data.
	Authorized []Interval
}

// NewError copies the given intervals into a normalized Error. Invalid
// intervals are dropped and the remaining ones are merged, so later mutation
// of the caller's slices cannot affect the diagnostic.
func NewError(field, message string, failure Interval, secrets []Secret, authorized []Interval) *Error {
	e := &Error{
		Field:      field,
		Message:    message,
		Failure:    failure,
		Secrets:    append([]Secret(nil), secrets...),
		Authorized: append([]Interval(nil), authorized...),
	}
	e.Secrets = mergeSecrets(e.Secrets)
	e.Authorized = mergeIntervals(e.Authorized)
	return e
}

// Error returns a context-free, leak-safe one-line message. Use Render to
// obtain clipped source context.
func (e *Error) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// Render produces the full diagnostic text for e against input:
//
//	field: message
//	<rendered source context>
//
// The context window expands a fixed number of *visible* atoms on each side
// of Failure, clipped to Authorized intervals and whole rune boundaries,
// with every secret span replaced by Marker. Counting the margin in visible
// units keeps hidden bytes out of the length channel. Failure must refer to
// input; intervals referring to a different backing buffer are undefined.
func (e *Error) Render(input []byte) string {
	return e.renderWith(input, secretIntervals(e.Secrets))
}

// RenderAll renders errs against the shared input using the union of every
// error's secret intervals, so a secret declared by one diagnostic is
// redacted consistently in the context of all the others. All failures must
// refer to the same input buffer.
func RenderAll(input []byte, errs []*Error) string {
	combined := make([]Interval, 0)
	for _, e := range errs {
		for _, s := range e.Secrets {
			combined = append(combined, s.Interval)
		}
	}
	combined = mergeIntervals(combined)

	var b strings.Builder
	for i, e := range errs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.renderWith(input, combined))
	}
	return b.String()
}

// MultiError groups several diagnostics over one parse operation. It
// implements the error interface without rendering any source context.
type MultiError []*Error

func (m MultiError) Error() string {
	parts := make([]string, 0, len(m))
	for _, e := range m {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "\n")
}

func (e *Error) renderWith(input []byte, secrets []Interval) string {
	failure := clampInterval(e.Failure, len(input))
	authorized := mergeIntervals(e.Authorized)

	var b strings.Builder
	b.WriteString(e.Error())
	b.WriteByte('\n')

	pendingMarker := false
	flush := func() {
		if pendingMarker {
			b.WriteString(Marker)
			pendingMarker = false
		}
	}

	// The context budget is counted in *visible* atomic units rather than
	// raw bytes: redacted or unauthorized atoms consume no visible budget,
	// so the amount of surrounding context never reveals how much was
	// hidden. First classify every atom, then expand outward from the
	// failure until the visible quota on each side is exhausted.
	type atom struct {
		span    Interval
		visible bool
		secret  bool
	}
	atoms := make([]atom, 0, len(input))
	for pos := 0; pos < len(input); {
		r, size := utf8.DecodeRune(input[pos:])
		if size == 0 {
			break
		}
		// An invalid or truncated encoding is handled as one atomic byte,
		// so it is never sliced into partial fragments.
		if r == utf8.RuneError && size == 1 {
			size = 1
		}
		span := Interval{Start: pos, End: pos + size}
		secret := intersectsAny(secrets, span)
		visible := !secret && coversWhole(authorized, span)
		atoms = append(atoms, atom{span: span, visible: visible, secret: secret})
		pos += size
	}
	if len(atoms) == 0 {
		return b.String()
	}

	// Locate the first atom at or after the failure start; a failure inside
	// an omitted atom still anchors the window to that position.
	anchor := 0
	for anchor < len(atoms) && atoms[anchor].span.Start < failure.Start {
		anchor++
	}
	if anchor == len(atoms) {
		anchor = len(atoms) - 1
	}

	lo := anchor
	for budget := contextMargin; lo > 0 && budget > 0; {
		lo--
		if atoms[lo].visible {
			budget--
		}
	}
	hi := anchor
	for budget := contextMargin; hi < len(atoms) && budget > 0; {
		if atoms[hi].visible {
			budget--
		}
		hi++
	}

	for i := lo; i < hi; i++ {
		a := atoms[i]
		if a.secret {
			// Adjacent secret atoms collapse onto a single marker, so the
			// number and size of hidden spans cannot be counted.
			pendingMarker = true
			continue
		}
		if !a.visible {
			// Unauthorized atoms are omitted without any marker, so their
			// presence and extent leave no trace in the output.
			continue
		}
		flush()
		b.Write(input[a.span.Start:a.span.End])
	}
	flush()
	return b.String()
}

func clampInterval(iv Interval, n int) Interval {
	if iv.Start < 0 {
		iv.Start = 0
	}
	if iv.End < iv.Start {
		iv.End = iv.Start
	}
	if iv.End > n {
		iv.End = n
	}
	if iv.Start > n {
		iv.Start = n
	}
	return iv
}

func secretIntervals(secrets []Secret) []Interval {
	out := make([]Interval, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, s.Interval)
	}
	return mergeIntervals(out)
}

func mergeSecrets(in []Secret) []Secret {
	out := append([]Secret(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})

	merged := out[:0]
	for _, s := range out {
		if !s.valid() {
			continue
		}
		// Only true overlaps merge; touching-but-distinct spans stay separate
		// at the interval level (rendering collapses adjacent markers anyway).
		if n := len(merged); n > 0 && s.Start < merged[n-1].End {
			if s.End > merged[n-1].End {
				merged[n-1].End = s.End
			}
			// Labels of overlapping secrets are retained on host-side
			// metadata; rendering only uses byte intervals.
			merged[n-1].Label = merged[n-1].Label + "|" + s.Label
		} else {
			merged = append(merged, s)
		}
	}
	return merged
}

func mergeIntervals(in []Interval) []Interval {
	out := make([]Interval, 0, len(in))
	for _, iv := range in {
		if iv.valid() {
			out = append(out, iv)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})

	merged := out[:0]
	for _, iv := range out {
		// Adjacent intervals are not merged: a rune spanning the boundary
		// between two touching intervals would otherwise be authorized when
		// neither interval fully contains it.
		if n := len(merged); n > 0 && iv.Start < merged[n-1].End {
			if iv.End > merged[n-1].End {
				merged[n-1].End = iv.End
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

// intersectsAny reports whether span shares at least one byte with any
// interval in ivs, which must be normalized (sorted and merged).
func intersectsAny(ivs []Interval, span Interval) bool {
	idx := sort.Search(len(ivs), func(i int) bool {
		return ivs[i].End > span.Start
	})
	return idx < len(ivs) && ivs[idx].Start < span.End
}

// coversWhole reports whether every byte of span lies inside ivs, which must
// be normalized. Every byte is checked so a rune cannot straddle a gap
// between two touching or nearby intervals.
func coversWhole(ivs []Interval, span Interval) bool {
	for pos := span.Start; pos < span.End; pos++ {
		if !covered(ivs, pos) {
			return false
		}
	}
	return true
}

func covered(ivs []Interval, pos int) bool {
	idx := sort.Search(len(ivs), func(i int) bool {
		return ivs[i].End > pos
	})
	return idx < len(ivs) && ivs[idx].Start <= pos
}
