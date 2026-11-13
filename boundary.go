package netsec

import (
	"bytes"
	"io"
)

// delimiterClass is the result of inspecting bytes beginning with a
// boundary prefix.
type delimiterClass int

const (
	delimNeedMore delimiterClass = iota // delimiter line incomplete, read more
	delimBody                           // prefix is ordinary body content
	delimPart                           // valid part delimiter line
	delimFinal                          // valid closing delimiter line
	delimBad                            // delimiter candidate with illegal padding
)

// classifyDelimiter inspects buf, which must start with prefix, where prefix
// is either "--boundary" (start of a body) or "\r\n--boundary" (a line in
// the middle of a body). readErr carries the underlying reader error once
// the input is drained.
//
// Transport padding between the boundary and the terminating CRLF may only
// consist of spaces and tabs; a CR is legal solely as the first byte of
// the CRLF terminator.
func classifyDelimiter(buf, prefix []byte, readErr error) delimiterClass {
	if len(buf) < len(prefix) {
		return delimNeedMore
	}
	if len(buf) == len(prefix) {
		if readErr != nil {
			// A bare boundary prefix at end of input is incomplete.
			return delimBad
		}
		return delimNeedMore
	}

	closing := false
	rest := buf[len(prefix):]

	switch rest[0] {
	case '-':
		if len(rest) == 1 {
			if readErr != nil {
				return delimBad
			}
			return delimNeedMore
		}
		if rest[1] != '-' {
			// e.g. "--boundary-other" is ordinary body content.
			return delimBody
		}
		closing = true
		rest = rest[2:]
	case ' ', '\t', '\r':
	default:
		// The boundary is a prefix of a longer token, so this is body.
		return delimBody
	}

	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case ' ', '\t':
			continue
		case '\r':
			// The CR must start the terminating CRLF; any bytes after the
			// LF belong to the following line and are irrelevant here.
			if i+1 >= len(rest) || rest[i+1] != '\n' {
				if i+1 >= len(rest) && readErr == nil {
					return delimNeedMore
				}
				return delimBad
			}
			if closing {
				return delimFinal
			}
			return delimPart
		case '\n':
			// A bare LF is never a legal delimiter terminator.
			return delimBad
		default:
			// Padding permits only SP/HT; anything else is illegal.
			return delimBad
		}
	}

	// Only padding seen so far; the terminating CRLF is still missing.
	if len(rest) == 0 && readErr != nil {
		// Input ends exactly at the boundary (or closing boundary) with no
		// CRLF. Only the closing delimiter tolerates a missing final CRLF.
		if closing {
			return delimFinal
		}
		return delimBad
	}
	if readErr != nil {
		// RFC 2046 tolerates a missing final CRLF on the closing line only.
		if closing {
			return delimFinal
		}
		return delimBad
	}
	return delimNeedMore
}

// scanUntilBoundary determines how many leading bytes of buf are part body.
// It returns io.EOF when a complete delimiter line is reached and
// ErrMalformedBoundary for an illegal delimiter candidate.
func scanUntilBoundary(buf []byte, total int64, readErr error, r *Reader) (int, error) {
	// At the very start of a part body the delimiter has no leading CRLF.
	if total == 0 && bytes.HasPrefix(buf, r.dashB) {
		switch classifyDelimiter(buf, r.dashB, readErr) {
		case delimPart, delimFinal:
			return 0, io.EOF
		case delimBad:
			return 0, ErrMalformedBoundary
		case delimNeedMore:
			return 0, nil
		case delimBody:
		}
	}

	searchFrom := 0
	for {
		rel := bytes.Index(buf[searchFrom:], r.nlDashB)
		if rel < 0 {
			break
		}
		i := searchFrom + rel
		switch classifyDelimiter(buf[i:], r.nlDashB, readErr) {
		case delimPart, delimFinal:
			return i, io.EOF
		case delimBad:
			return i, ErrMalformedBoundary
		case delimNeedMore:
			// The candidate reaches the end of the buffered data and cannot
			// be classified yet. Hold back from the candidate so the refill
			// can complete it; bytes before it are definitely body.
			return i, nil
		case delimBody:
			// A longer token sharing the delimiter prefix (e.g. the prefix
			// itself followed by an ordinary character). Skip past the CRLF
			// and continue searching rather than re-scanning the same bytes.
			searchFrom = i + len(r.nlDashB)
		}
	}

	// Hold back any tail that is still a prefix of the delimiter so a
	// candidate split across reads is never handed out as body.
	if i := lastPrefixIndex(buf, r.nlDashB); i >= 0 {
		// If the input is exhausted the dangling prefix can never complete.
		if readErr != nil {
			if readErr == io.EOF {
				return i, io.ErrUnexpectedEOF
			}
			return i, readErr
		}
		return i, nil
	}
	return len(buf), readErr
}

// lastPrefixIndex returns the largest index i for which buf[i:] is a
// non-empty prefix of prefix, or -1 when no such tail exists.
func lastPrefixIndex(buf, prefix []byte) int {
	max := len(prefix)
	if max > len(buf) {
		max = len(buf)
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(buf[len(buf)-n:], prefix[:n]) {
			return len(buf) - n
		}
	}
	return -1
}
