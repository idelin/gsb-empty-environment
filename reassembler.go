// Package logreasm reassembles log records from byte fragments that arrive
// from multiple sources. Each fragment carries a source ID, an epoch and a
// sequence number. Records begin with a timestamp line; indented lines are
// continuations. Missing fragments must never cause later continuation
// lines to be glued onto an older record; when a missing fragment arrives
// later, already emitted records are revised in place with stable IDs.
package logreasm

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// DefaultMaxLine is the default maximum line length in bytes. Longer lines
// are truncated and flagged.
const DefaultMaxLine = 1 << 20

// Fragment is a chunk of a source's byte stream.
type Fragment struct {
	Source string
	Epoch  uint64
	Seq    uint64
	Data   []byte
}

// EventKind classifies engine output events.
type EventKind int

const (
	// EventUpsert emits a new record or a revision of an existing one.
	EventUpsert EventKind = iota
	// EventRetract withdraws a previously emitted record (e.g. an orphan
	// continuation record that merged into its predecessor after backfill).
	EventRetract
	// EventWarning reports anomalies: restarts, stale epochs, duplicate or
	// conflicting fragments.
	EventWarning
)

func (k EventKind) String() string {
	switch k {
	case EventUpsert:
		return "upsert"
	case EventRetract:
		return "retract"
	case EventWarning:
		return "warning"
	}
	return "unknown"
}

// GapEvidence pinpoints an unresolved hole in the fragment sequence.
type GapEvidence struct {
	MissingAfter  uint64 // last received seq before the hole
	MissingBefore uint64 // first received seq after the hole
}

// Record is a reassembled log record.
type Record struct {
	ID        string       // stable across revisions
	Source    string       // origin source
	Epoch     uint64       // source epoch the record belongs to
	Version   int          // 1 for the first emission, bumped on every revision
	Timestamp time.Time    // parsed from the first line; zero for orphan records
	StartLine int          // 1-based line number within the source/epoch stream
	EndLine   int          // inclusive
	Lines     []string     // record lines, first line first
	Orphan    bool         // first line is not a timestamp line
	Truncated bool         // at least one line exceeded MaxLine
	Gap       *GapEvidence // non-nil when an unresolved hole precedes this record
	GapAfter  bool         // an unresolved hole follows this record's last line
}

// Event is emitted by Ingest.
type Event struct {
	Kind   EventKind
	Record Record // set for EventUpsert and EventRetract
	Source string
	Epoch  uint64
	Reason string // set for EventWarning
}

// Engine reassembles fragments from any number of sources.
type Engine struct {
	maxLine   int
	sources   map[string]*sourceState
	records   map[string]Record          // current records by ID
	streamIDs map[string]map[string]bool // stream key -> record IDs
	versions  map[string]int             // record ID -> last version
}

type sourceState struct {
	started bool
	current uint64
	epochs  map[uint64]map[uint64][]byte // epoch -> seq -> data
}

// Option configures an Engine.
type Option func(*Engine)

// WithMaxLine sets the maximum line length in bytes (0 = unlimited).
func WithMaxLine(n int) Option {
	return func(e *Engine) { e.maxLine = n }
}

// NewEngine creates an Engine.
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		maxLine:   DefaultMaxLine,
		sources:   make(map[string]*sourceState),
		records:   make(map[string]Record),
		streamIDs: make(map[string]map[string]bool),
		versions:  make(map[string]int),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func streamKey(source string, epoch uint64) string {
	return fmt.Sprintf("%s\x00%d", source, epoch)
}

// Ingest consumes one fragment and returns the events it triggered.
func (e *Engine) Ingest(f Fragment) []Event {
	src, ok := e.sources[f.Source]
	if !ok {
		src = &sourceState{epochs: make(map[uint64]map[uint64][]byte)}
		e.sources[f.Source] = src
	}
	var events []Event
	if !src.started {
		src.started = true
		src.current = f.Epoch
		src.epochs[f.Epoch] = make(map[uint64][]byte)
	} else if f.Epoch < src.current {
		return []Event{e.warn(f, fmt.Sprintf("stale epoch %d (current %d); fragment dropped", f.Epoch, src.current))}
	} else if f.Epoch > src.current {
		events = append(events, e.warn(f, fmt.Sprintf("source restart: epoch %d -> %d", src.current, f.Epoch)))
		src.current = f.Epoch
		src.epochs[f.Epoch] = make(map[uint64][]byte)
	}
	frags := src.epochs[f.Epoch]
	if prev, dup := frags[f.Seq]; dup {
		if bytes.Equal(prev, f.Data) {
			return append(events, e.warn(f, fmt.Sprintf("duplicate fragment seq=%d ignored", f.Seq)))
		}
		return append(events, e.warn(f, fmt.Sprintf("conflicting fragment seq=%d ignored; keeping first", f.Seq)))
	}
	data := make([]byte, len(f.Data))
	copy(data, f.Data)
	frags[f.Seq] = data
	return append(events, e.reparse(f.Source, f.Epoch, frags)...)
}

func (e *Engine) warn(f Fragment, reason string) Event {
	return Event{Kind: EventWarning, Source: f.Source, Epoch: f.Epoch, Reason: reason}
}

// parsedLine is one physical line carved out of the received byte stream.
type parsedLine struct {
	text      string
	seq       uint64 // fragment containing the line start
	off       int    // byte offset of the line start within that fragment
	complete  bool   // terminated by a newline
	truncated bool
	gapBefore bool
	gapAfter  uint64 // when gapBefore: last received seq before the hole
}

// splitLines orders fragments by seq and cuts the received bytes into lines.
// A line never spans an unresolved gap: each contiguous run of fragments is
// cut independently. Line numbers count received bytes only, so they are
// revised when a hole is backfilled.
func (e *Engine) splitLines(frags map[uint64][]byte) []parsedLine {
	seqs := make([]uint64, 0, len(frags))
	for s := range frags {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	var lines []parsedLine
	var prevSeq uint64
	havePrev := false
	pendingGap := false
	var gapAfter uint64
	var segBuf []byte
	var segFrags []uint64 // seq of each fragment in the current segment
	var segStarts []int   // byte offset of each fragment within segBuf
	flush := func() {
		for off := 0; off < len(segBuf); {
			start := off
			nl := bytes.IndexByte(segBuf[off:], '\n')
			complete := false
			var raw []byte
			if nl < 0 {
				raw = segBuf[off:]
				off = len(segBuf)
			} else {
				raw = segBuf[off : off+nl]
				off += nl + 1
				complete = true
			}
			raw = bytes.TrimSuffix(raw, []byte{'\r'})
			truncated := false
			if e.maxLine > 0 && len(raw) > e.maxLine {
				raw = raw[:e.maxLine]
				truncated = true
			}
			// Locate the fragment containing the line start.
			fi := sort.Search(len(segStarts), func(i int) bool { return segStarts[i] > start }) - 1
			lines = append(lines, parsedLine{
				text:      string(raw),
				seq:       segFrags[fi],
				off:       start - segStarts[fi],
				complete:  complete,
				truncated: truncated,
				gapBefore: pendingGap,
				gapAfter:  gapAfter,
			})
			pendingGap = false
		}
		segBuf = segBuf[:0]
		segFrags = segFrags[:0]
		segStarts = segStarts[:0]
	}
	for _, seq := range seqs {
		if havePrev && seq != prevSeq+1 {
			flush()
			pendingGap = true
			gapAfter = prevSeq
		}
		segFrags = append(segFrags, seq)
		segStarts = append(segStarts, len(segBuf))
		segBuf = append(segBuf, frags[seq]...)
		prevSeq = seq
		havePrev = true
	}
	flush()
	return lines
}

var tsPrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)

var tsLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
}

// parseTimestamp extracts a leading timestamp from a line.
func parseTimestamp(line string) (time.Time, bool) {
	m := tsPrefix.FindString(line)
	if m == "" {
		return time.Time{}, false
	}
	for _, layout := range tsLayouts {
		if t, err := time.Parse(layout, m); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isIndented(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// reparse rebuilds the record set of one stream and diffs it against the
// previously emitted set, producing upsert/retract events. Record IDs are
// derived from the immutable position (fragment seq + offset) of the
// record's first line, so they survive backfill revisions.
func (e *Engine) reparse(source string, epoch uint64, frags map[uint64][]byte) []Event {
	lines := e.splitLines(frags)

	// Group lines into records.
	type builder struct{ lines []parsedLine }
	var builders []*builder
	for _, ln := range lines {
		continuation := isIndented(ln.text) && !ln.gapBefore && len(builders) > 0
		if continuation {
			b := builders[len(builders)-1]
			b.lines = append(b.lines, ln)
		} else {
			// Timestamp line, non-indented line, or any line right after a
			// gap starts a new record.
			builders = append(builders, &builder{lines: []parsedLine{ln}})
		}
	}

	// Materialize records.
	fresh := make([]Record, 0, len(builders))
	lineNo := make(map[parsedLine]int, len(lines))
	for i, ln := range lines {
		lineNo[ln] = i + 1
	}
	for i, b := range builders {
		first := b.lines[0]
		last := b.lines[len(b.lines)-1]
		rec := Record{
			ID:        fmt.Sprintf("%s:e%d:f%d@%d", source, epoch, first.seq, first.off),
			Source:    source,
			Epoch:     epoch,
			StartLine: lineNo[first],
			EndLine:   lineNo[last],
		}
		if ts, ok := parseTimestamp(first.text); ok && !isIndented(first.text) {
			rec.Timestamp = ts
		} else {
			rec.Orphan = true
		}
		if first.gapBefore {
			rec.Gap = &GapEvidence{MissingAfter: first.gapAfter, MissingBefore: first.seq}
		}
		if i+1 < len(builders) && builders[i+1].lines[0].gapBefore {
			rec.GapAfter = true
		}
		for _, ln := range b.lines {
			rec.Lines = append(rec.Lines, ln.text)
			rec.Truncated = rec.Truncated || ln.truncated
		}
		fresh = append(fresh, rec)
	}

	// Diff against the previously emitted set for this stream.
	key := streamKey(source, epoch)
	oldIDs := e.streamIDs[key]
	if oldIDs == nil {
		oldIDs = make(map[string]bool)
	}
	newIDs := make(map[string]bool, len(fresh))
	var events []Event
	for _, rec := range fresh {
		newIDs[rec.ID] = true
		prev, seen := e.records[rec.ID]
		if seen && sameRecord(prev, rec) {
			continue
		}
		rec.Version = e.versions[rec.ID] + 1
		e.versions[rec.ID] = rec.Version
		e.records[rec.ID] = rec
		events = append(events, Event{Kind: EventUpsert, Record: rec, Source: source, Epoch: epoch})
	}
	for id := range oldIDs {
		if !newIDs[id] {
			gone := e.records[id]
			delete(e.records, id)
			delete(e.versions, id)
			events = append(events, Event{Kind: EventRetract, Record: gone, Source: source, Epoch: epoch})
		}
	}
	e.streamIDs[key] = newIDs
	return events
}

func sameRecord(a, b Record) bool {
	if a.Source != b.Source || a.Epoch != b.Epoch ||
		a.StartLine != b.StartLine || a.EndLine != b.EndLine ||
		a.Orphan != b.Orphan || a.Truncated != b.Truncated ||
		a.GapAfter != b.GapAfter || !a.Timestamp.Equal(b.Timestamp) {
		return false
	}
	if (a.Gap == nil) != (b.Gap == nil) {
		return false
	}
	if a.Gap != nil && *a.Gap != *b.Gap {
		return false
	}
	if len(a.Lines) != len(b.Lines) {
		return false
	}
	for i := range a.Lines {
		if a.Lines[i] != b.Lines[i] {
			return false
		}
	}
	return true
}

// Records returns a snapshot of all live records, deterministically ordered
// by (timestamp, source, epoch, start line, ID). Records without a
// timestamp sort after timestamped ones. This gives a stable cross-source
// ordering for records sharing the same tick.
func (e *Engine) Records() []Record {
	out := make([]Record, 0, len(e.records))
	for _, r := range e.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		az, bz := a.Timestamp.IsZero(), b.Timestamp.IsZero()
		if az != bz {
			return bz // timestamped first
		}
		if !az && !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Epoch != b.Epoch {
			return a.Epoch < b.Epoch
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.ID < b.ID
	})
	return out
}
