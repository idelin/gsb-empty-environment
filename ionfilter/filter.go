// Package ionfilter provides a read-side Ion filter for untrusted ingestion
// streams. It enforces three security invariants on top of the Ion data model:
//
//   - Symbol table directives ($ion_symbol_table annotated values) are only
//     honored when they appear inside a segment explicitly marked trusted by the
//     caller. A directive appearing in user data is rejected outright.
//   - An Ion Version Marker (the text token $ion_1_0 or the binary magic
//     bytes E0 01 00 EA) is only accepted at the very start of the logical
//     stream. An embedded IVM, including one hidden in the middle of user data,
//     is rejected.
//   - Every annotation on every value must resolve through the trusted symbol
//     table and must be present in the caller supplied allow list. Values
//     carrying any other annotation are rejected.
//
// The filter understands both text Ion and binary Ion and has no dependencies
// beyond the Go standard library, so it can be placed in front of any Ion
// decoder in the ingestion pipeline.
package ionfilter

import (
	"bytes"
	"errors"
	"fmt"
)

// Kind identifies why a stream was rejected. Errors returned by this package
// can be inspected with errors.As to obtain a Violation.
type Kind string

const (
	// KindMalformed marks input that is not well formed Ion or Ion framing.
	KindMalformed Kind = "malformed ion"
	// KindSymbolTable marks a symbol table directive outside a trusted
	// segment, or a malformed trusted directive.
	KindSymbolTable Kind = "untrusted symbol table directive"
	// KindIVM marks an Ion Version Marker found anywhere other than the
	// start of the logical stream.
	KindIVM Kind = "embedded ion version marker"
	// KindAnnotation marks an annotation that cannot be resolved or is not
	// present in the configured allow list.
	KindAnnotation Kind = "annotation not allowed"
)

// Violation describes a rejected stream.
type Violation struct {
	Kind   Kind
	Offset int
	Detail string
}

func (v *Violation) Error() string {
	if v.Offset >= 0 {
		return fmt.Sprintf("ionfilter: %s at offset %d: %s", v.Kind, v.Offset, v.Detail)
	}
	return fmt.Sprintf("ionfilter: %s: %s", v.Kind, v.Detail)
}

func malformed(offset int, format string, args ...any) error {
	return &Violation{Kind: KindMalformed, Offset: offset, Detail: fmt.Sprintf(format, args...)}
}

func policyError(kind Kind, offset int, format string, args ...any) error {
	return &Violation{Kind: kind, Offset: offset, Detail: fmt.Sprintf(format, args...)}
}

// IsViolation reports whether err (or any error in its chain) is a Violation
// produced by this package.
func IsViolation(err error) bool {
	var v *Violation
	return errors.As(err, &v)
}

// Segment is one contiguous byte range of a logical Ion stream. Trusted
// segments are typically a service controlled prefix (for example a standard
// local symbol table header); untrusted segments carry uploaded user data.
type Segment struct {
	Data    []byte
	Trusted bool
}

// TrustedSegment wraps service controlled bytes in which symbol table
// directives are permitted.
func TrustedSegment(data []byte) Segment { return Segment{Data: data, Trusted: true} }

// UserSegment wraps uploaded bytes in which symbol table directives are
// forbidden.
func UserSegment(data []byte) Segment { return Segment{Data: data, Trusted: false} }

// Filter holds the policy for a logical stream. The zero value is not usable;
// create one with New.
type Filter struct {
	allowedAnnotations map[string]struct{}
	segments           []Segment
}

// New builds a filter over the given logical stream. annotationAllowList is
// the set of annotation texts permitted on any value (the symbol table
// directive is governed by segment trust, not by this list). An empty allow
// list means no annotations are permitted on ordinary values.
func New(annotationAllowList []string, segments ...Segment) *Filter {
	f := &Filter{
		allowedAnnotations: make(map[string]struct{}, len(annotationAllowList)),
		segments:           segments,
	}
	for _, ann := range annotationAllowList {
		f.allowedAnnotations[ann] = struct{}{}
	}
	return f
}

// Validate scans the whole logical stream and returns the first policy
// violation or malformed-input error encountered, or nil when the stream is
// acceptable and safe to hand to a regular Ion decoder.
func (f *Filter) Validate() error {
	logical, trust, err := joinSegments(f.segments)
	if err != nil {
		return err
	}
	if len(logical) == 0 {
		return nil
	}
	ctx := &scanContext{
		allowedSet: f.allowedAnnotations,
		trust:      trust,
		table:      newSymbolTable(nil),
	}
	if isBinaryStream(logical) {
		return ctx.scanBinary(logical)
	}
	if looksBinary(logical) {
		return malformed(0, "binary Ion stream must begin with the IVM")
	}
	return ctx.scanText(logical)
}

// Validate is a convenience wrapper for streams consisting of a single trusted
// or untrusted byte range.
func Validate(data []byte, trusted bool, annotationAllowList []string) error {
	return New(annotationAllowList, Segment{Data: data, Trusted: trusted}).Validate()
}

// joinSegments concatenates the physical segments into one logical buffer and
// builds a parallel trust boundary table: for every byte offset, whether that
// offset belongs to a trusted segment. Only offsets where values begin are
// ever queried, so storing boundaries rather than a per-byte bitmap keeps the
// footprint small.
func joinSegments(segments []Segment) ([]byte, boundaryList, error) {
	var logical bytes.Buffer
	boundaries := make(boundaryList, 0, len(segments))
	offset := 0
	for _, seg := range segments {
		if len(seg.Data) == 0 {
			continue
		}
		boundaries = append(boundaries, trustBoundary{start: offset, end: offset + len(seg.Data), trusted: seg.Trusted})
		logical.Write(seg.Data)
		offset += len(seg.Data)
	}
	return logical.Bytes(), boundaries, nil
}

type trustBoundary struct {
	start, end int
	trusted    bool
}

type boundaryList []trustBoundary

// trustedAt reports the trust label of the segment containing offset.
func (b boundaryList) trustedAt(offset int) bool {
	for _, boundary := range b {
		if offset >= boundary.start && offset < boundary.end {
			return boundary.trusted
		}
	}
	return false
}

// binaryIVM is the Ion 1.0 binary version marker.
var binaryIVM = []byte{0xE0, 0x01, 0x00, 0xEA}

func isBinaryStream(data []byte) bool {
	rest := data
	if len(rest) >= 3 && rest[0] == 0xEF && rest[1] == 0xBB && rest[2] == 0xBF {
		rest = rest[3:]
	}
	return len(rest) >= 4 && bytes.Equal(rest[:4], binaryIVM)
}

// looksBinary reports bytes that cannot begin a text Ion document. A binary
// document always opens with the IVM magic; anything else starting with a
// high-bit byte is mislabeled binary framing and must be rejected rather than
// scanned as text.
func looksBinary(data []byte) bool {
	rest := data
	if len(rest) >= 3 && rest[0] == 0xEF && rest[1] == 0xBB && rest[2] == 0xBF {
		rest = rest[3:]
	}
	if len(rest) == 0 {
		return false
	}
	return rest[0] == 0xE0 || rest[0]&0x80 != 0
}
