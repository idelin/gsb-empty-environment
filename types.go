package taskpkg

import "fmt"

// BoundaryKind identifies how a response body was terminated.
type BoundaryKind string

const (
	// BoundaryNone means the response has no body.
	BoundaryNone BoundaryKind = "none"
	// BoundaryContentLength means a unique decimal Content-Length delimited
	// the body.
	BoundaryContentLength BoundaryKind = "content-length"
	// BoundaryClose means the connection closing delimited the body.
	BoundaryClose BoundaryKind = "close"
	// BoundaryChunked means chunked transfer coding delimited the body.
	BoundaryChunked BoundaryKind = "chunked"
	// BoundaryInvalid means the framing headers were present but the response
	// could not be safely delimited.
	BoundaryInvalid BoundaryKind = "invalid"
	// BoundaryUnknown means no boundary could be established.
	BoundaryUnknown BoundaryKind = "unknown"
)

// ErrorKind classifies a parser rejection.
type ErrorKind string

const (
	ErrStatusLine               ErrorKind = "status-line"
	ErrHeaderLine               ErrorKind = "header-line"
	ErrHeaderEOF                ErrorKind = "header-eof"
	ErrInvalidContentLength     ErrorKind = "invalid-content-length"
	ErrRepeatedContentLength    ErrorKind = "repeated-content-length"
	ErrConflictingContentLength ErrorKind = "conflicting-content-length"
	ErrTransferEncoding         ErrorKind = "transfer-encoding"
	ErrChunked                  ErrorKind = "chunked"
	ErrConflictingFraming       ErrorKind = "conflicting-framing"
	ErrEarlyClose               ErrorKind = "early-close"
	ErrChunkedTruncated         ErrorKind = "chunked-truncated"
	ErrTrailingData             ErrorKind = "trailing-data"
	ErrCallback                 ErrorKind = "callback"
	ErrNoProgress               ErrorKind = "no-progress"
)

// FindingKind enumerates the boundary conditions the verifier calls out.
type FindingKind string

const (
	FindingChunked              FindingKind = "chunked"
	FindingRepeatedLength       FindingKind = "repeated-content-length"
	FindingEarlyClose           FindingKind = "early-close"
	FindingInformational        FindingKind = "informational"
	FindingConflictingFraming   FindingKind = "conflicting-framing"
	FindingCloseDelimited       FindingKind = "close-delimited"
	FindingUnattributedResponse FindingKind = "unattributed-response"
	FindingUnansweredRequest    FindingKind = "unanswered-request"
	FindingHeadNoBody           FindingKind = "head-no-body"
	FindingNoContentNoBody      FindingKind = "204-no-body"
)

// DifferenceKind classifies the first disagreement between two strategies.
type DifferenceKind string

const (
	DiffOwnership          DifferenceKind = "ownership"
	DiffResponseIndex      DifferenceKind = "response-index"
	DiffMissingOnStrict    DifferenceKind = "missing-on-strict"
	DiffMissingOnAlternate DifferenceKind = "missing-on-alternate"
	DiffStatus             DifferenceKind = "status"
	DiffHeaderBoundary     DifferenceKind = "header-boundary"
	DiffBodyBoundary       DifferenceKind = "body-boundary"
	DiffBoundaryPolicy     DifferenceKind = "boundary-policy"
	DiffTruncation         DifferenceKind = "truncation"
	DiffInformational      DifferenceKind = "informational"
	DiffParseError         DifferenceKind = "parse-error"
)

// Verdict is the connection-level outcome of verification.
type Verdict string

const (
	VerdictConsistent Verdict = "consistent"
	VerdictDiverged   Verdict = "diverged"
	VerdictBlocked    Verdict = "blocked"
)

// Interval is a half-open byte range [Start, End).
type Interval struct {
	Start int
	End   int
}

// Len returns the number of bytes in the interval.
func (iv Interval) Len() int {
	if iv.End <= iv.Start {
		return 0
	}
	return iv.End - iv.Start
}

// Contains reports whether off is inside the half-open interval.
func (iv Interval) Contains(off int) bool {
	return iv.Start <= off && off < iv.End
}

// Header is one response header field with its byte offset in the raw stream.
type Header struct {
	Name   string
	Value  string
	Offset int
}

// Response is one parsed HTTP/1.x response. Start, HeaderEnd, BodyStart,
// BodyEnd, and End are byte offsets into ParseResult.Raw. End and all other
// End fields are exclusive.
type Response struct {
	Index int
	// RequestIndex is the index into the request method queue that owns this
	// response. It can equal len(methods) for a trailing, unattributed
	// response. A value of -1 means ownership was not established.
	RequestIndex int
	StatusCode   int
	Protocol     string
	Reason       string
	Headers      []Header
	Trailers     []Header
	Start        int
	HeaderEnd    int
	BodyStart    int
	BodyEnd      int
	End          int
	Boundary     BoundaryKind
	// DeclaredLength is the Content-Length value, even for responses where the
	// header is not used to delimit a body. It is -1 when absent or invalid.
	DeclaredLength int64
	// ExpectedEnd is the required end offset for a length-delimited body. It
	// can be beyond the end of the raw stream when the connection closed early.
	ExpectedEnd   int
	Informational bool
	NoBody        bool
	Chunked       bool
	Truncated     bool
	Body          []byte
	Raw           []byte
	Error         string
}

// Interval returns the response's complete half-open byte interval.
func (r Response) Interval() Interval {
	return Interval{Start: r.Start, End: r.End}
}

// BodyInterval returns the half-open interval occupied by the response body,
// including chunk framing for chunked responses.
func (r Response) BodyInterval() Interval {
	return Interval{Start: r.BodyStart, End: r.BodyEnd}
}

// IsFinal reports whether the response consumes the pending request. Only
// 1xx informational responses leave the request pending.
func (r Response) IsFinal() bool {
	return !r.Informational
}

// ParseError describes the byte position at which a strategy rejected the
// stream. Offset is the point where parsing stopped; DetailOffset points at a
// specific offending header or token when that is more precise.
type ParseError struct {
	Kind          ErrorKind
	Offset        int
	DetailOffset  int
	Message       string
	ResponseIndex int
	RequestIndex  int
}

func (e *ParseError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.DetailOffset >= 0 && e.DetailOffset != e.Offset {
		return fmt.Sprintf("%s at byte %d (detail byte %d): %s", e.Kind, e.Offset, e.DetailOffset, e.Message)
	}
	return fmt.Sprintf("%s at byte %d: %s", e.Kind, e.Offset, e.Message)
}

// Finding is a non-fatal observation about a boundary or request attribution.
type Finding struct {
	Kind          FindingKind
	Strategy      string
	ResponseIndex int
	RequestIndex  int
	Offset        int
	Message       string
}

// ParseResult is the complete evidence produced by one parsing strategy.
type ParseResult struct {
	Strategy  string
	Methods   []string
	Raw       []byte
	Responses []Response
	Err       *ParseError
	Findings  []Finding
	// Minimal marks a result that provides only request ownership and byte
	// intervals (for example, a callback returning []Segment).
	Minimal bool
}

// OK reports whether the strategy accepted the complete stream.
func (r ParseResult) OK() bool {
	return r.Err == nil
}

// Difference is the earliest disagreement found by the verifier. The
// StrictResponse and AlternateResponse fields point into the corresponding
// ParseResult.Responses slice and therefore carry the full evidence record.
type Difference struct {
	Offset                 int
	Kind                   DifferenceKind
	Message                string
	CrossRequest           bool
	StrictRequestIndex     int
	AlternateRequestIndex  int
	StrictResponseIndex    int
	AlternateResponseIndex int
	StrictRange            Interval
	AlternateRange         Interval
	StrictBytes            []byte
	AlternateBytes         []byte
	StrictResponse         *Response
	AlternateResponse      *Response
	StrictError            *ParseError
	AlternateError         *ParseError
}

// Report is the result of Verify. Earliest is the first byte-order
// disagreement; Differences contains every distinct disagreement in byte order.
type Report struct {
	Methods      []string
	Raw          []byte
	Strict       ParseResult
	Alternate    ParseResult
	Earliest     *Difference
	Differences  []Difference
	Findings     []Finding
	CrossRequest bool
	Blocked      bool
	BlockReason  string
	Verdict      Verdict
}

// OK reports whether both strategies agreed on a clean, fully attributed
// stream.
func (r Report) OK() bool {
	return r.Verdict == VerdictConsistent
}

// Segment is a minimal alternate-strategy layout. A callback may return
// segments instead of complete Response records.
type Segment struct {
	RequestIndex  int
	Start         int
	End           int
	StatusCode    int
	Informational bool
	Boundary      BoundaryKind
}

// ParserFunc is the canonical callback shape for an alternate strategy.
type ParserFunc func(methods []string, raw []byte) ParseResult
