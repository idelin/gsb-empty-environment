package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Span is a half-open byte interval [Start, End).
type Span struct {
	Start int64
	End   int64
}

// Interval is an alias for Span used when describing signed or omitted byte
// ranges.
type Interval = Span

// Len returns the number of bytes in the interval.
func (s Span) Len() int64 {
	if s.End <= s.Start {
		return 0
	}
	return s.End - s.Start
}

// ByteRange is the sequence of offset/length pairs stored in a PDF signature
// dictionary.
type ByteRange []int64

var (
	// ErrNoSignatures reports that the document has no signature dictionaries.
	ErrNoSignatures = errors.New("pdf: no signatures found")

	// ErrNoCurrentSignature reports that signatures exist, but none of them
	// covers the current revision of the document.
	ErrNoCurrentSignature = errors.New("pdf: no signature covers the current revision")

	// ErrInvalidByteRange reports a malformed, out-of-bounds, or overlapping
	// byte range.
	ErrInvalidByteRange = errors.New("pdf: invalid byte range")

	// ErrUncoveredBytes reports bytes in the current document that are neither
	// covered by the signature nor part of its signature value.
	ErrUncoveredBytes = errors.New("pdf: signature byte range leaves current bytes uncovered")

	// ErrMalformedSignature reports a signature dictionary that cannot be
	// validated, for example because it has no /Contents value.
	ErrMalformedSignature = errors.New("pdf: malformed signature")
)

// CoverageError describes why a byte range does not cover a document.
type CoverageError struct {
	Kind       error
	FileSize   int64
	ByteRange  ByteRange
	Contents   Span
	Gaps       []Interval
	Diagnostic string
}

func (e *CoverageError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if e.Kind != nil {
		b.WriteString(e.Kind.Error())
	} else {
		b.WriteString("pdf: byte range does not cover the document")
	}
	if e.Diagnostic != "" {
		b.WriteString(": ")
		b.WriteString(e.Diagnostic)
	}
	if len(e.Gaps) > 0 {
		b.WriteString(" (uncovered")
		for _, gap := range e.Gaps {
			fmt.Fprintf(&b, " [%d,%d)", gap.Start, gap.End)
		}
		b.WriteString(")")
	}
	return b.String()
}

// Unwrap returns the sentinel error describing the failure.
func (e *CoverageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// ValidateByteRange checks that byteRange covers every byte of a file of
// fileSize bytes except the signature value in [contentsStart, contentsEnd).
//
// A PDF signature's byte range has the form [offset1 length1 offset2 length2].
// The omitted interval between the two signed intervals must lie entirely
// inside the signature value.  A gap before or after the signature value, a
// trailing revision after the last signed byte, or any other hole is rejected.
func ValidateByteRange(fileSize int64, byteRange []int64, contentsStart, contentsEnd int64) error {
	return ValidateByteRangeSpan(fileSize, byteRange, Span{Start: contentsStart, End: contentsEnd})
}

// ValidateByteRangeSpan is the Span-based form of ValidateByteRange.
func ValidateByteRangeSpan(fileSize int64, byteRange []int64, contents Span) error {
	_, gaps, err := analyzeByteRange(fileSize, byteRange)
	if err != nil {
		return err
	}

	if len(gaps) > 1 {
		return &CoverageError{
			Kind:       ErrUncoveredBytes,
			FileSize:   fileSize,
			ByteRange:  ByteRange(byteRange),
			Contents:   contents,
			Gaps:       gaps,
			Diagnostic: "byte range contains more than one hole",
		}
	}

	if len(gaps) == 0 {
		// Every current byte is signed.  This is stronger than the usual
		// detached-signature layout and does not leave an attacker-controlled
		// hole, so accept it for coverage purposes.
		return nil
	}

	gap := gaps[0]
	if contents.Len() == 0 {
		return &CoverageError{
			Kind:       ErrMalformedSignature,
			FileSize:   fileSize,
			ByteRange:  ByteRange(byteRange),
			Contents:   contents,
			Gaps:       gaps,
			Diagnostic: "the omitted range cannot be matched to a signature value",
		}
	}
	if gap.Start < contents.Start || gap.End > contents.End {
		return &CoverageError{
			Kind:       ErrUncoveredBytes,
			FileSize:   fileSize,
			ByteRange:  ByteRange(byteRange),
			Contents:   contents,
			Gaps:       gaps,
			Diagnostic: "the byte range hole is outside the signature value",
		}
	}

	return nil
}

// CheckByteRange is an alias for ValidateByteRange.
func CheckByteRange(fileSize int64, byteRange []int64, contentsStart, contentsEnd int64) error {
	return ValidateByteRange(fileSize, byteRange, contentsStart, contentsEnd)
}

// ValidateByteRangeCoverage is an alias for ValidateByteRange.
func ValidateByteRangeCoverage(fileSize int64, byteRange []int64, contentsStart, contentsEnd int64) error {
	return ValidateByteRange(fileSize, byteRange, contentsStart, contentsEnd)
}

// VerifyByteRangeCoverage is an alias for ValidateByteRange.
func VerifyByteRangeCoverage(fileSize int64, byteRange []int64, contentsStart, contentsEnd int64) error {
	return ValidateByteRange(fileSize, byteRange, contentsStart, contentsEnd)
}

func analyzeByteRange(fileSize int64, byteRange []int64) ([]Interval, []Interval, error) {
	if fileSize < 0 {
		return nil, nil, &CoverageError{
			Kind:       ErrInvalidByteRange,
			FileSize:   fileSize,
			ByteRange:  ByteRange(byteRange),
			Diagnostic: "negative file size",
		}
	}
	if len(byteRange) < 4 || len(byteRange)%2 != 0 {
		return nil, nil, &CoverageError{
			Kind:       ErrInvalidByteRange,
			FileSize:   fileSize,
			ByteRange:  ByteRange(byteRange),
			Diagnostic: "a byte range must contain at least two offset/length pairs",
		}
	}

	intervals := make([]Interval, 0, len(byteRange)/2)
	for i := 0; i < len(byteRange); i += 2 {
		offset, length := byteRange[i], byteRange[i+1]
		if offset < 0 || length < 0 {
			return nil, nil, &CoverageError{
				Kind:       ErrInvalidByteRange,
				FileSize:   fileSize,
				ByteRange:  ByteRange(byteRange),
				Diagnostic: "negative offset or length",
			}
		}
		if offset > fileSize || length > fileSize-offset {
			return nil, nil, &CoverageError{
				Kind:       ErrInvalidByteRange,
				FileSize:   fileSize,
				ByteRange:  ByteRange(byteRange),
				Diagnostic: fmt.Sprintf("pair %d extends beyond the end of the file", i/2+1),
			}
		}
		if length == 0 {
			continue
		}
		intervals = append(intervals, Interval{Start: offset, End: offset + length})
	}

	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].Start == intervals[j].Start {
			return intervals[i].End < intervals[j].End
		}
		return intervals[i].Start < intervals[j].Start
	})

	// Overlapping signed intervals are malformed.  Adjacent intervals are
	// harmless and are coalesced.
	merged := make([]Interval, 0, len(intervals))
	for _, interval := range intervals {
		if len(merged) == 0 {
			merged = append(merged, interval)
			continue
		}
		last := &merged[len(merged)-1]
		if interval.Start < last.End {
			return nil, nil, &CoverageError{
				Kind:       ErrInvalidByteRange,
				FileSize:   fileSize,
				ByteRange:  ByteRange(byteRange),
				Diagnostic: "signed intervals overlap",
			}
		}
		if interval.Start == last.End {
			last.End = interval.End
			continue
		}
		merged = append(merged, interval)
	}

	var gaps []Interval
	cursor := int64(0)
	for _, interval := range merged {
		if interval.Start > cursor {
			gaps = append(gaps, Interval{Start: cursor, End: interval.Start})
		}
		if interval.End > cursor {
			cursor = interval.End
		}
	}
	if cursor < fileSize {
		gaps = append(gaps, Interval{Start: cursor, End: fileSize})
	}

	return merged, gaps, nil
}

// SignedIntervals returns the byte intervals covered by s's byte range.
func (s Signature) SignedIntervals() ([]Interval, error) {
	size := s.documentSize
	if size == 0 {
		for i := 0; i+1 < len(s.ByteRange); i += 2 {
			end := s.ByteRange[i] + s.ByteRange[i+1]
			if end > size {
				size = end
			}
		}
	}
	intervals, _, err := analyzeByteRange(size, s.ByteRange)
	if err != nil {
		return nil, err
	}
	return intervals, nil
}

// UncoveredIntervals returns the byte intervals not covered by s's byte range.
func (s Signature) UncoveredIntervals(fileSize int64) ([]Interval, error) {
	_, gaps, err := analyzeByteRange(fileSize, s.ByteRange)
	if err != nil {
		return nil, err
	}
	return gaps, nil
}

// Validate checks s against a document of fileSize bytes.
func (s Signature) Validate(fileSize int64) error {
	if s.ParseErr != nil {
		return s.ParseErr
	}
	if len(s.ByteRange) == 0 {
		return &CoverageError{
			Kind:       ErrMalformedSignature,
			FileSize:   fileSize,
			Diagnostic: "signature has no /ByteRange",
		}
	}
	if s.ContentsSpan.Len() == 0 {
		return &CoverageError{
			Kind:       ErrMalformedSignature,
			FileSize:   fileSize,
			ByteRange:  s.ByteRange,
			Diagnostic: "signature has no /Contents value",
		}
	}
	return ValidateByteRangeSpan(fileSize, s.ByteRange, s.ContentsSpan)
}

// Covers reports whether s covers every byte of a document of fileSize bytes
// except its own signature value.
func (s Signature) Covers(fileSize int64) bool {
	return s.Validate(fileSize) == nil
}
