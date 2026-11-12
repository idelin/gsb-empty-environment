// Package chain exposes the audit window chain verifier under a dedicated
// import path. It is a thin alias over the parent netsec package.
package chain

import netsec "example.org/netsec/q0277"

type (
	Entry         = netsec.Entry
	WindowHeader  = netsec.WindowHeader
	Header        = netsec.WindowHeader
	Window        = netsec.Window
	Segment       = netsec.Window
	WindowSegment = netsec.Window
	Anchor        = netsec.Anchor
	Checkpoint    = netsec.Anchor
	ChainAnchor   = netsec.Anchor
	Record        = netsec.Record
	ErrorKind     = netsec.ErrorKind
	ChainError    = netsec.ChainError
	ForkError     = netsec.ForkError
	Report        = netsec.Report
	Verifier      = netsec.Verifier
	Chain         = netsec.Chain
)

const (
	KindInvalid           = netsec.KindInvalid
	KindFork              = netsec.KindFork
	KindWindowGap         = netsec.KindWindowGap
	KindSequenceGap       = netsec.KindSequenceGap
	KindCountMismatch     = netsec.KindCountMismatch
	KindPrevHashMismatch  = netsec.KindPrevHashMismatch
	KindEntryHashMismatch = netsec.KindEntryHashMismatch
	KindAnchorMismatch    = netsec.KindAnchorMismatch
	KindAnchorMissing     = netsec.KindAnchorMissing
	KindAnchorConflict    = netsec.KindAnchorConflict
)

var (
	ErrInvalid           = netsec.ErrInvalid
	ErrFork              = netsec.ErrFork
	ErrWindowGap         = netsec.ErrWindowGap
	ErrSequenceGap       = netsec.ErrSequenceGap
	ErrCountMismatch     = netsec.ErrCountMismatch
	ErrPrevHashMismatch  = netsec.ErrPrevHashMismatch
	ErrEntryHashMismatch = netsec.ErrEntryHashMismatch
	ErrAnchorMismatch    = netsec.ErrAnchorMismatch
	ErrAnchorMissing     = netsec.ErrAnchorMissing
	ErrAnchorConflict    = netsec.ErrAnchorConflict

	ErrSeqGap        = netsec.ErrSequenceGap
	ErrBadCount      = netsec.ErrCountMismatch
	ErrHashMismatch  = netsec.ErrEntryHashMismatch
	ErrPrevMismatch  = netsec.ErrPrevHashMismatch
	ErrMissingAnchor = netsec.ErrAnchorMissing
)

func ComputeEntryHash(prevHash []byte, seq uint64, payload []byte) []byte {
	return netsec.ComputeEntryHash(prevHash, seq, payload)
}

func HashEntry(prevHash []byte, seq uint64, payload []byte) []byte {
	return netsec.HashEntry(prevHash, seq, payload)
}

func WindowHead(w Window) ([]byte, error) {
	return netsec.WindowHead(w)
}

func SealWindow(w *Window) error {
	return netsec.SealWindow(w)
}

func NewWindow(number uint64, prevHash []byte, records []Record) (Window, error) {
	return netsec.NewWindow(number, prevHash, records)
}

func BuildWindows(records []Record, windowSize int, firstWindow uint64, prevHash []byte) ([]Window, error) {
	return netsec.BuildWindows(records, windowSize, firstWindow, prevHash)
}

func WindowAnchor(w Window) (Anchor, error) {
	return netsec.WindowAnchor(w)
}

func AnchorsForWindows(windows []Window, numbers ...uint64) ([]Anchor, error) {
	return netsec.AnchorsForWindows(windows, numbers...)
}

func VerifyChain(windows []Window, anchors []Anchor) error {
	return netsec.VerifyChain(windows, anchors)
}

func Verify(windows []Window, anchors []Anchor) error {
	return netsec.Verify(windows, anchors)
}

func VerifyArchive(windows []Window, anchors []Anchor) error {
	return netsec.VerifyArchive(windows, anchors)
}

func VerifySegments(windows []Window, anchors []Anchor) error {
	return netsec.VerifySegments(windows, anchors)
}

func CheckChain(windows []Window, anchors []Anchor) *Report {
	return netsec.CheckChain(windows, anchors)
}

func AuditChain(windows []Window, anchors []Anchor) *Report {
	return netsec.AuditChain(windows, anchors)
}

func DetectForks(windows []Window) []*ForkError {
	return netsec.DetectForks(windows)
}

func NewVerifier(anchors []Anchor) *Verifier {
	return netsec.NewVerifier(anchors)
}
