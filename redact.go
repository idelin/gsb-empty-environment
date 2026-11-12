package netsec

import (
	"bytes"
	"errors"
	"io"
	"sort"
	"sync"
)

// Placeholder replaces every detected kernel address. Its length is fixed,
// regardless of how the address was originally rendered.
const Placeholder = "[REDACTED]"

// ErrClosed is returned when data is written to a closed Redactor.
var ErrClosed = errors.New("netsec: redactor is closed")

// Redact returns a copy of s with kernel addresses replaced by Placeholder.
func Redact(s string) string {
	return string(redactBytes([]byte(s)))
}

// RedactString is an alias for Redact.
func RedactString(s string) string {
	return Redact(s)
}

// RedactBytes returns a copy of p with kernel addresses replaced by
// Placeholder.
func RedactBytes(p []byte) []byte {
	return redactBytes(p)
}

// Redactor is a streaming, chunk-boundary-safe redactor.
//
// It implements io.Writer. Callers must call Close or Flush to emit the final
// buffered bytes; a trailing newline is not required.
type Redactor struct {
	mu       sync.Mutex
	dst      io.Writer
	pending  []byte
	closed   bool
	closeDst bool
}

// RedactWriter is an alias for Redactor.
type RedactWriter = Redactor

// NewRedactor creates a streaming redactor that writes to dst. Close flushes
// buffered data and closes dst when it implements io.Closer.
func NewRedactor(dst io.Writer) *Redactor {
	return &Redactor{dst: dst, closeDst: true}
}

// NewRedactingWriter is an alias for NewRedactor.
func NewRedactingWriter(dst io.Writer) *Redactor {
	return NewRedactor(dst)
}

// NewRedactWriter is an alias for NewRedactor.
func NewRedactWriter(dst io.Writer) *Redactor {
	return NewRedactor(dst)
}

// NewWriter is an alias for NewRedactor.
func NewWriter(dst io.Writer) *Redactor {
	return NewRedactor(dst)
}

// Sanitize is an alias for Redact.
func Sanitize(s string) string {
	return Redact(s)
}

// Write accepts a chunk of data. Completed lines are redacted and written to
// the destination immediately; an incomplete trailing line is retained until
// more data arrives, Flush is called, or the writer is closed. This makes the
// result independent of the sizes of the chunks passed to Write.
func (r *Redactor) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return 0, ErrClosed
	}
	if r.dst == nil {
		return 0, errors.New("netsec: nil destination writer")
	}

	r.pending = append(r.pending, p...)
	if err := r.flushCompleteLines(); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Flush redacts and writes all buffered data without closing the destination.
func (r *Redactor) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrClosed
	}
	if err := r.flushAll(); err != nil {
		return err
	}
	return r.flushDestination()
}

// Close flushes any buffered data. If the destination implements io.Closer,
// Close closes it as well.
func (r *Redactor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	if err := r.flushAll(); err != nil {
		r.closed = true
		return err
	}
	r.closed = true

	if err := r.flushDestination(); err != nil {
		return err
	}
	if !r.closeDst {
		return nil
	}
	if c, ok := r.dst.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (r *Redactor) flushDestination() error {
	if f, ok := r.dst.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

func (r *Redactor) flushCompleteLines() error {
	lastNewline := bytes.LastIndexByte(r.pending, '\n')
	if lastNewline < 0 {
		return nil
	}
	complete := r.pending[:lastNewline+1]
	if err := r.writeRedacted(complete); err != nil {
		return err
	}

	tail := r.pending[lastNewline+1:]
	copy(r.pending, tail)
	r.pending = r.pending[:len(tail)]
	return nil
}

func (r *Redactor) flushAll() error {
	if len(r.pending) == 0 {
		return nil
	}
	if err := r.writeRedacted(r.pending); err != nil {
		return err
	}
	r.pending = r.pending[:0]
	return nil
}

func (r *Redactor) writeRedacted(p []byte) error {
	out := redactBytes(p)
	if len(out) == 0 {
		return nil
	}
	_, err := r.dst.Write(out)
	return err
}

type span struct {
	start int
	end   int
}

func redactBytes(src []byte) []byte {
	spans := findSensitiveSpans(src)
	if len(spans) == 0 {
		return append([]byte(nil), src...)
	}

	var out bytes.Buffer
	out.Grow(len(src))
	last := 0
	for _, s := range spans {
		out.Write(src[last:s.start])
		out.WriteString(Placeholder)
		last = s.end
	}
	out.Write(src[last:])
	return out.Bytes()
}

func findSensitiveSpans(src []byte) []span {
	var spans []span
	spans = appendPrefixedPointers(spans, src)
	spans = appendHexTokens(spans, src)
	spans = appendByteDumps(spans, src)
	return mergeSpans(spans)
}

func appendPrefixedPointers(spans []span, src []byte) []span {
	for i := 0; i+2 < len(src); i++ {
		if src[i] != '0' || (src[i+1] != 'x' && src[i+1] != 'X') {
			continue
		}

		j := i + 2
		for j < len(src) && isHex(src[j]) {
			j++
		}
		digits := j - (i + 2)
		if digits < 8 || digits > 16 || !hasFFFFPrefix(src[i+2:j]) {
			continue
		}
		spans = addSpan(spans, span{start: i, end: j})
		i = j - 1
	}
	return spans
}

func appendHexTokens(spans []span, src []byte) []span {
	for i := 0; i < len(src); {
		if !isHex(src[i]) {
			i++
			continue
		}

		j := i
		for j < len(src) && isHex(src[j]) {
			j++
		}
		if j-i == 16 && (hasFFFFPrefix(src[i:j]) ||
			(!hasZeroPrefix(src[i:j]) && littleEndianKernel(src[i:j]))) {
			spans = addSpan(spans, span{start: i, end: j})
		}
		i = j
	}
	return spans
}

type byteToken struct {
	start int
	end   int
	value byte
}

func appendByteDumps(spans []span, src []byte) []span {
	for i := 0; i < len(src); {
		first, ok := parseByteToken(src, i)
		if !ok {
			i++
			continue
		}

		tokens := []byteToken{first}
		pos := first.end
		for {
			next, sepLen := skipByteSeparators(src, pos)
			if sepLen == 0 {
				if !hasEscapedBytePrefix(src, pos) {
					break
				}
				next = pos
			}
			tok, ok := parseByteToken(src, next)
			if !ok {
				break
			}
			tokens = append(tokens, tok)
			pos = tok.end
		}

		if len(tokens) >= 8 {
			for start := 0; start+8 <= len(tokens); start++ {
				if tokens[start+6].value == 0xff && tokens[start+7].value == 0xff {
					spans = addSpan(spans, span{
						start: tokens[start].start,
						end:   tokens[start+7].end,
					})
				}
			}
		}

		i = pos
		if pos <= first.end {
			i = first.end
		}
	}
	return spans
}

func parseByteToken(src []byte, i int) (byteToken, bool) {
	if i >= len(src) {
		return byteToken{}, false
	}

	prefixed := false
	j := i
	if src[j] == '0' && j+1 < len(src) && (src[j+1] == 'x' || src[j+1] == 'X') {
		prefixed = true
		j += 2
	} else if src[j] == '\\' && j+1 < len(src) && (src[j+1] == 'x' || src[j+1] == 'X') {
		prefixed = true
		j += 2
	} else if j > 0 && isHex(src[j-1]) {
		return byteToken{}, false
	}

	startDigits := j
	for j < len(src) && isHex(src[j]) && j-startDigits < 2 {
		j++
	}
	if j == startDigits {
		return byteToken{}, false
	}
	if j < len(src) && isHex(src[j]) {
		return byteToken{}, false
	}
	if !prefixed && i > 0 {
		prev := src[i-1]
		if prev == 'x' || prev == 'X' {
			if i > 1 && src[i-2] == '0' {
				return byteToken{}, false
			}
		}
		if isHex(prev) {
			return byteToken{}, false
		}
	}

	value := hexValue(src[startDigits])
	if j-startDigits == 2 {
		value = value<<4 | hexValue(src[j-1])
	}
	return byteToken{start: i, end: j, value: value}, true
}

func skipByteSeparators(src []byte, i int) (int, int) {
	start := i
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', ',', ';', ':', '|', '-':
			i++
		default:
			return i, i - start
		}
	}
	return i, i - start
}

func hasEscapedBytePrefix(src []byte, i int) bool {
	return i+2 < len(src) && src[i] == '\\' &&
		(src[i+1] == 'x' || src[i+1] == 'X') && isHex(src[i+2])
}

func addSpan(spans []span, s span) []span {
	if s.end <= s.start {
		return spans
	}
	return append(spans, s)
}

func mergeSpans(spans []span) []span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].end < spans[j].end
		}
		return spans[i].start < spans[j].start
	})

	merged := spans[:1]
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if s.start < last.end {
			if s.end > last.end {
				last.end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}
	return merged
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func hasFFFFPrefix(p []byte) bool {
	if len(p) < 4 {
		return false
	}
	return (p[0] == 'f' || p[0] == 'F') &&
		(p[1] == 'f' || p[1] == 'F') &&
		(p[2] == 'f' || p[2] == 'F') &&
		(p[3] == 'f' || p[3] == 'F')
}

func hasZeroPrefix(p []byte) bool {
	if len(p) < 4 {
		return false
	}
	return p[0] == '0' && p[1] == '0' && p[2] == '0' && p[3] == '0'
}

func littleEndianKernel(p []byte) bool {
	if len(p) != 16 {
		return false
	}
	return isFFPair(p[12], p[13]) && isFFPair(p[14], p[15])
}

func isFFPair(a, b byte) bool {
	return (a == 'f' || a == 'F') && (b == 'f' || b == 'F')
}
