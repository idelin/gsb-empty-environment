package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// CanonMode identifies a DKIM body canonicalization algorithm.
type CanonMode string

const (
	CanonSimple  CanonMode = "simple"
	CanonRelaxed CanonMode = "relaxed"

	SimpleCanonicalization  = CanonSimple
	RelaxedCanonicalization = CanonRelaxed
)

// RawSpan is a half-open byte range [Start, End) in the original body.
type RawSpan struct {
	Start int64
	End   int64
}

// Len returns the number of bytes in the span.
func (s RawSpan) Len() int64 {
	if s.End <= s.Start {
		return 0
	}
	return s.End - s.Start
}

// Valid reports whether the span has a non-negative, ordered range.
func (s RawSpan) Valid() bool {
	return s.Start >= 0 && s.End >= s.Start
}

// MapKind describes how a canonical byte range relates to raw body bytes.
type MapKind string

const (
	MapCopy               MapKind = "copy"
	MapWhitespaceRun      MapKind = "whitespace-run"
	MapNormalizedLineEnd  MapKind = "normalized-line-end"
	MapSynthesizedLineEnd MapKind = "synthesized-line-end"
)

// CanonicalSegment maps a canonical output range to the raw bytes that
// produced it. Copy segments have equal canonical and raw lengths and are
// aligned one-for-one from their starts.
type CanonicalSegment struct {
	CanonicalStart int64
	CanonicalEnd   int64
	RawStart       int64
	RawEnd         int64
	Kind           MapKind
}

func (s CanonicalSegment) isCopy() bool {
	return s.Kind == MapCopy &&
		s.CanonicalEnd-s.CanonicalStart == s.RawEnd-s.RawStart
}

// CanonicalBody is the canonicalized body plus the raw-byte provenance needed
// to reason about a DKIM body length count.
type CanonicalBody struct {
	Mode          CanonMode
	Canonical     []byte
	RawLength     int64
	Complete      bool
	Segments      []CanonicalSegment
	DiscardedTail []RawSpan
}

// Len returns the number of canonical body bytes.
func (b CanonicalBody) Len() int64 {
	return int64(len(b.Canonical))
}

// PrefixCoverage describes the raw body dependency of a canonical prefix.
type PrefixCoverage struct {
	CanonicalBytes     int64
	RawStart           int64
	RawEnd             int64
	Dependencies       []RawSpan
	TailOnlyDiscarded  bool
	CoversAllCanonical bool
}

// MapPrefix maps the first n canonical bytes back to their original MIME bytes.
// RawEnd is a conservative prefix end: every byte before it may have been
// consumed, while Dependencies contains the exact contributing spans.
func (b CanonicalBody) MapPrefix(n int64) (PrefixCoverage, error) {
	if !b.Complete {
		return PrefixCoverage{}, errors.New("canonical body is incomplete")
	}
	if n < 0 || n > int64(len(b.Canonical)) {
		return PrefixCoverage{}, fmt.Errorf("canonical prefix %d outside body of %d bytes", n, len(b.Canonical))
	}

	out := PrefixCoverage{
		CanonicalBytes:     n,
		CoversAllCanonical: n == int64(len(b.Canonical)),
	}
	for _, seg := range b.Segments {
		if seg.CanonicalStart >= n {
			break
		}
		taken := seg.CanonicalEnd - seg.CanonicalStart
		if taken > n-seg.CanonicalStart {
			taken = n - seg.CanonicalStart
		}
		if taken <= 0 {
			continue
		}

		var end int64
		if seg.isCopy() {
			end = seg.RawStart + taken
		} else {
			end = seg.RawEnd
		}
		if end > out.RawEnd {
			out.RawEnd = end
		}
		if end > seg.RawStart {
			out.Dependencies = append(out.Dependencies, RawSpan{Start: seg.RawStart, End: end})
		}
	}
	if out.RawEnd > b.RawLength {
		out.RawEnd = b.RawLength
	}
	out.TailOnlyDiscarded = b.tailOnlyDiscarded(out.RawEnd)
	return out, nil
}

func (b CanonicalBody) tailOnlyDiscarded(from int64) bool {
	if from > b.RawLength {
		return false
	}
	pos := from
	for _, span := range b.DiscardedTail {
		if span.End <= pos {
			continue
		}
		if span.Start > pos {
			return false
		}
		pos = span.End
		if pos >= b.RawLength {
			return true
		}
	}
	return pos >= b.RawLength
}

// BodyViews holds the simple and relaxed canonical forms of one body.
type BodyViews struct {
	Simple    CanonicalBody
	Relaxed   CanonicalBody
	RawLength int64
	Complete  bool
}

type pendingLine struct {
	span      RawSpan
	termStart int64
	termEnd   int64
	termLen   int
}

type bodyBuilder struct {
	mode      CanonMode
	out       []byte
	segments  []CanonicalSegment
	pending   []pendingLine
	discarded []RawSpan
	rawLength int64
}

func newBodyBuilder(mode CanonMode) *bodyBuilder {
	return &bodyBuilder{mode: mode}
}

func (b *bodyBuilder) addLine(content []byte, contentStart, termStart, termEnd int64, termLen int) {
	canonicalLen := 0
	if b.mode == CanonRelaxed {
		content = bytes.TrimRight(content, " \t")
		canonicalLen = len(content)
	} else {
		canonicalLen = len(content)
	}

	if canonicalLen == 0 {
		b.pending = append(b.pending, pendingLine{
			span:      RawSpan{Start: contentStart, End: termEnd},
			termStart: termStart,
			termEnd:   termEnd,
			termLen:   termLen,
		})
		return
	}

	b.flushPendingEmptyLines()
	if b.mode == CanonRelaxed {
		b.addRelaxedContent(content, contentStart)
	} else {
		b.addCopy(content, contentStart)
	}
	b.addTerminator(termStart, termEnd, termLen)
}

func (b *bodyBuilder) flushPendingEmptyLines() {
	for _, line := range b.pending {
		b.addTerminator(line.termStart, line.termEnd, line.termLen)
	}
	b.pending = b.pending[:0]
}

func (b *bodyBuilder) addCopy(src []byte, rawStart int64) {
	if len(src) == 0 {
		return
	}
	outStart := int64(len(b.out))
	b.out = append(b.out, src...)
	b.segments = append(b.segments, CanonicalSegment{
		CanonicalStart: outStart,
		CanonicalEnd:   int64(len(b.out)),
		RawStart:       rawStart,
		RawEnd:         rawStart + int64(len(src)),
		Kind:           MapCopy,
	})
}

func (b *bodyBuilder) addRelaxedContent(src []byte, rawStart int64) {
	for i := 0; i < len(src); {
		if src[i] == ' ' || src[i] == '\t' {
			j := i + 1
			for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			outStart := int64(len(b.out))
			b.out = append(b.out, ' ')
			b.segments = append(b.segments, CanonicalSegment{
				CanonicalStart: outStart,
				CanonicalEnd:   int64(len(b.out)),
				RawStart:       rawStart + int64(i),
				RawEnd:         rawStart + int64(j),
				Kind:           MapWhitespaceRun,
			})
			i = j
			continue
		}

		j := i + 1
		for j < len(src) && src[j] != ' ' && src[j] != '\t' {
			j++
		}
		b.addCopy(src[i:j], rawStart+int64(i))
		i = j
	}
}

func (b *bodyBuilder) addTerminator(rawStart, rawEnd int64, rawLen int) {
	outStart := int64(len(b.out))
	b.out = append(b.out, '\r', '\n')
	kind := MapSynthesizedLineEnd
	switch rawLen {
	case 2:
		kind = MapCopy
	case 1:
		kind = MapNormalizedLineEnd
	}
	b.segments = append(b.segments, CanonicalSegment{
		CanonicalStart: outStart,
		CanonicalEnd:   int64(len(b.out)),
		RawStart:       rawStart,
		RawEnd:         rawEnd,
		Kind:           kind,
	})
}

func (b *bodyBuilder) finish() {
	for _, line := range b.pending {
		b.discarded = append(b.discarded, line.span)
	}
	b.pending = b.pending[:0]
}

func (b *bodyBuilder) snapshot(complete bool) CanonicalBody {
	return CanonicalBody{
		Mode:          b.mode,
		Canonical:     bytes.Clone(b.out),
		RawLength:     b.rawLength,
		Complete:      complete,
		Segments:      append([]CanonicalSegment(nil), b.segments...),
		DiscardedTail: append([]RawSpan(nil), b.discarded...),
	}
}

// BodyReceiver performs simple and relaxed canonicalization simultaneously as
// body bytes are received. Chunk boundaries may split a CRLF anywhere.
type BodyReceiver struct {
	simple    *bodyBuilder
	relaxed   *bodyBuilder
	buf       []byte
	lineStart int64
	rawLength int64
	finished  bool
}

// NewBodyReceiver returns a streaming receiver for one message body.
func NewBodyReceiver() *BodyReceiver {
	return &BodyReceiver{
		simple:  newBodyBuilder(CanonSimple),
		relaxed: newBodyBuilder(CanonRelaxed),
	}
}

// NewBodyCanonicalizer returns a streaming receiver restricted to mode. Both
// canonical forms are still computed so a caller can change mode later.
func NewBodyCanonicalizer(mode CanonMode) (*BodyReceiver, error) {
	if mode != CanonSimple && mode != CanonRelaxed {
		return nil, fmt.Errorf("unknown canonicalization %q", mode)
	}
	return NewBodyReceiver(), nil
}

var errReceiverFinished = errors.New("body receiver already finished")

// Write implements io.Writer.
func (r *BodyReceiver) Write(p []byte) (int, error) {
	if r.finished {
		return 0, errReceiverFinished
	}

	r.rawLength += int64(len(p))
	r.buf = append(r.buf, p...)
	for {
		idx := bytes.IndexByte(r.buf, '\n')
		if idx < 0 {
			break
		}

		content := r.buf[:idx]
		termStart := r.lineStart + int64(idx)
		termLen := 1
		if idx > 0 && r.buf[idx-1] == '\r' {
			content = r.buf[:idx-1]
			termStart--
			termLen = 2
		}
		termEnd := termStart + int64(termLen)
		r.addLine(content, r.lineStart, termStart, termEnd, termLen)

		advance := int64(idx + 1)
		r.buf = r.buf[idx+1:]
		r.lineStart += advance
	}
	return len(p), nil
}

func (r *BodyReceiver) addLine(content []byte, contentStart, termStart, termEnd int64, termLen int) {
	r.simple.addLine(content, contentStart, termStart, termEnd, termLen)
	r.relaxed.addLine(content, contentStart, termStart, termEnd, termLen)
}

// Finish completes the stream and returns both canonical forms.
func (r *BodyReceiver) Finish() (*BodyViews, error) {
	if r.finished {
		return nil, errReceiverFinished
	}
	if len(r.buf) > 0 {
		start := r.lineStart
		end := start + int64(len(r.buf))
		r.addLine(r.buf, start, end, end, 0)
	}
	r.buf = nil
	r.finished = true

	r.simple.rawLength = r.rawLength
	r.relaxed.rawLength = r.rawLength
	r.simple.finish()
	r.relaxed.finish()
	views := &BodyViews{
		Simple:    r.simple.snapshot(true),
		Relaxed:   r.relaxed.snapshot(true),
		RawLength: r.rawLength,
		Complete:  true,
	}
	return views, nil
}

// Snapshot returns the canonical prefix seen so far. Its Complete field is
// always false, so it can never authorize a whole body.
func (r *BodyReceiver) Snapshot() *BodyViews {
	r.simple.rawLength = r.rawLength
	r.relaxed.rawLength = r.rawLength
	return &BodyViews{
		Simple:    r.simple.snapshot(false),
		Relaxed:   r.relaxed.snapshot(false),
		RawLength: r.rawLength,
		Complete:  false,
	}
}

// Complete reports whether Finish has successfully completed the stream.
func (r *BodyReceiver) Complete() bool {
	return r.finished
}

// CanonicalizeBody returns the canonical body bytes for mode.
func CanonicalizeBody(body []byte, mode CanonMode) ([]byte, error) {
	if mode != CanonSimple && mode != CanonRelaxed {
		return nil, fmt.Errorf("unknown canonicalization %q", mode)
	}
	r := NewBodyReceiver()
	if _, err := io.Copy(r, bytes.NewReader(body)); err != nil {
		return nil, err
	}
	views, err := r.Finish()
	if err != nil {
		return nil, err
	}
	if mode == CanonRelaxed {
		return views.Relaxed.Canonical, nil
	}
	return views.Simple.Canonical, nil
}

// CanonicalizeBoth returns the simple and relaxed canonical bodies.
func CanonicalizeBoth(body []byte) (simple, relaxed []byte, err error) {
	r := NewBodyReceiver()
	if _, err := io.Copy(r, bytes.NewReader(body)); err != nil {
		return nil, nil, err
	}
	views, err := r.Finish()
	if err != nil {
		return nil, nil, err
	}
	return views.Simple.Canonical, views.Relaxed.Canonical, nil
}
