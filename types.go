// Package logrec reassembles multi-source log streams into records.
//
// A record starts at a line matching the configured start pattern and
// absorbs following indented continuation lines (e.g. exception stack
// frames). Streams may arrive in arbitrary byte chunks, lines may arrive
// out of order with gaps or late corrections, and assembly rules may be
// swapped at runtime; original line identity (source, epoch, sequence
// number) is preserved throughout.
package logrec

import (
	"fmt"
	"regexp"
	"strings"
)

// CloseReason explains why a record was closed.
type CloseReason string

const (
	// CloseStart: a new start line closed the record (normal completion).
	CloseStart CloseReason = "start"
	// CloseEOF: the source stream ended with the record still open.
	CloseEOF CloseReason = "eof"
	// CloseFlush: the record was force-closed to relieve buffer pressure.
	CloseFlush CloseReason = "flush"
)

// Rules control how lines are assembled into records for one
// source/epoch. A Rules value may be hot-swapped at any time via
// Multiplexer.SetRules; the swap only affects lines classified
// afterwards and never rewrites the identity of lines already seen.
type Rules struct {
	// StartPattern marks a line as the beginning of a new record.
	StartPattern *regexp.Regexp
	// ContinuationIndent reports whether a line is an indented
	// continuation of the currently open record. Nil means the
	// default: a leading space or tab.
	ContinuationIndent func(string) bool
	// MaxRecordBytes caps the bytes retained per record (0 = no cap).
	// Excess content is dropped but counted, never silently lost.
	MaxRecordBytes int
	// MaxRecordLines caps the lines retained per record (0 = no cap).
	MaxRecordLines int
}

// DefaultRules matches "2024-01-01 ..." style timestamps as record
// starts and leading whitespace as continuation.
func DefaultRules() Rules {
	return Rules{StartPattern: regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`)}
}

func (r Rules) isStart(line string) bool {
	return r.StartPattern != nil && r.StartPattern.MatchString(line)
}

func (r Rules) isContinuation(line string) bool {
	if r.ContinuationIndent != nil {
		return r.ContinuationIndent(line)
	}
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

// Line is a single physical input line with its original identity.
type Line struct {
	Seq  uint64 `json:"seq"`  // original line number within the source/epoch stream
	Text string `json:"text"` // line content without the trailing newline
}

// Record is an assembled log record. ID is stable across revisions and
// checkpoint/restore; Revision starts at 1 and increments whenever a
// late or corrected line is merged into an already emitted record.
type Record struct {
	ID        string      `json:"id"`
	Source    string      `json:"source"`
	Epoch     uint64      `json:"epoch"`
	StartSeq  uint64      `json:"start_seq"`
	EndSeq    uint64      `json:"end_seq"`
	Lines     []Line      `json:"lines"`
	Revision  int         `json:"revision"`
	Truncated bool        `json:"truncated"`
	Dropped   int         `json:"dropped_lines"`
	Reason    CloseReason `json:"close_reason"`
	Orphan    bool        `json:"orphan,omitempty"`

	closed bool
}

// Text renders the record body, lines joined with newlines.
func (r *Record) Text() string {
	var b strings.Builder
	for i, l := range r.Lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.Text)
	}
	return b.String()
}

// clone returns a deep copy safe to hand out as an emission: later
// revisions must not mutate records already emitted.
func (r *Record) clone() Record {
	out := *r
	out.Lines = make([]Line, len(r.Lines))
	copy(out.Lines, r.Lines)
	return out
}

// Complete reports whether the record was closed by a following start
// line, as opposed to an EOF/flush boundary.
func (r *Record) Complete() bool { return r.Reason == CloseStart }

// recordID derives the stable record identity.
func recordID(source string, epoch, startSeq uint64) string {
	return fmt.Sprintf("%s/%d/%d", source, epoch, startSeq)
}

// Emission is a record output event: either a record that was just
// closed or a new revision of a previously closed record.
type Emission struct {
	Record  Record `json:"record"`
	Revised bool   `json:"revised"` // true when this is a revision, not a first close
}
