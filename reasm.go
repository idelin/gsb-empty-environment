// Package logreasm reassembles log records from byte fragments arriving
// from multiple sources, each tagged with an epoch and a fragment sequence
// number.
//
// Records begin with a timestamp line; indented lines are continuations.
// When fragments are missing, continuation lines after the gap are never
// joined to a record from before the gap. Late fragments that fill a gap
// trigger a revision of already emitted records; record IDs stay stable
// across revisions.
package logreasm

import (
	"bytes"
	"sort"
	"time"
)

// DefaultMaxLineLen is the default maximum line length in bytes. Longer
// lines are truncated and flagged.
const DefaultMaxLineLen = 1 << 20

// Fragment is a chunk of the byte stream of one source within one epoch.
type Fragment struct {
	Source string
	Epoch  uint64
	Seq    uint64
	Data   []byte
}

// GapInfo is evidence of missing fragments immediately before a record.
// MissingFrom and MissingTo are inclusive sequence numbers.
type GapInfo struct {
	MissingFrom uint64 `json:"missingFrom"`
	MissingTo   uint64 `json:"missingTo"`
}

// Record is a reassembled log record.
type Record struct {
	ID        uint64   `json:"id"`
	Source    string   `json:"source"`
	Epoch     uint64   `json:"epoch"`
	Rev       int      `json:"rev"`
	Lines     []string `json:"lines"`
	StartLine int      `json:"startLine"`
	EndLine   int      `json:"endLine"`
	// LineBaseKnown is false when fragments before this record's segment
	// are missing, so line numbers are relative to the segment start.
	LineBaseKnown bool `json:"lineBaseKnown"`
	// Tick is the parsed timestamp of the first line, if any.
	Tick    time.Time `json:"-"`
	TickStr string    `json:"tick,omitempty"`
	HasTick bool      `json:"hasTick"`
	// GapBefore records a fragment gap immediately preceding this record.
	GapBefore *GapInfo `json:"gapBefore,omitempty"`
	// CutByGap is true when a fragment gap follows this record, so
	// continuation lines may be missing.
	CutByGap bool `json:"cutByGap,omitempty"`
	// Truncated is true when at least one line exceeded the max length.
	Truncated bool `json:"truncated,omitempty"`

	anchor anchor
}

// EventKind classifies change events.
type EventKind string

const (
	// EventUpsert signals a new or revised record.
	EventUpsert EventKind = "upsert"
	// EventRetract signals a record removed by a merge after a gap fill.
	EventRetract EventKind = "retract"
)

// Event is emitted by Ingest for every record change.
type Event struct {
	Kind   EventKind `json:"kind"`
	Record Record    `json:"record"`
}

// Conflict is evidence of two different payloads for the same fragment.
type Conflict struct {
	Source string `json:"source"`
	Epoch  uint64 `json:"epoch"`
	Seq    uint64 `json:"seq"`
}

// anchor identifies a record by the stream position of its first line;
// it is stable across gap fills and renumbering.
type anchor struct {
	seq uint64
	off int
}

type streamKey struct {
	source string
	epoch  uint64
}

type stream struct {
	frags   map[uint64][]byte
	records map[anchor]*Record
	order   []anchor
}

// Reassembler holds the reassembly state for all sources and epochs.
type Reassembler struct {
	maxLineLen int
	streams    map[streamKey]*stream
	nextID     uint64

	// Dups counts duplicate fragments (same payload, ignored).
	Dups int
	// Conflicts lists fragments whose payload differed from the payload
	// already stored for the same (source, epoch, seq); the first payload
	// is kept.
	Conflicts []Conflict
}

// Option configures a Reassembler.
type Option func(*Reassembler)

// WithMaxLineLen sets the maximum line length in bytes.
func WithMaxLineLen(n int) Option {
	return func(r *Reassembler) { r.maxLineLen = n }
}

// NewReassembler creates a Reassembler.
func NewReassembler(opts ...Option) *Reassembler {
	r := &Reassembler{
		maxLineLen: DefaultMaxLineLen,
		streams:    make(map[streamKey]*stream),
		nextID:     1,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Ingest adds one fragment and returns the resulting change events.
func (r *Reassembler) Ingest(f Fragment) []Event {
	key := streamKey{source: f.Source, epoch: f.Epoch}
	st := r.streams[key]
	if st == nil {
		st = &stream{frags: make(map[uint64][]byte), records: make(map[anchor]*Record)}
		r.streams[key] = st
	}
	if existing, ok := st.frags[f.Seq]; ok {
		if bytes.Equal(existing, f.Data) {
			r.Dups++
		} else {
			r.Conflicts = append(r.Conflicts, Conflict{Source: f.Source, Epoch: f.Epoch, Seq: f.Seq})
		}
		return nil
	}
	data := make([]byte, len(f.Data))
	copy(data, f.Data)
	st.frags[f.Seq] = data
	return r.rebuild(key, st)
}

// Records returns all live records ordered by (tick, source, epoch, line).
// Records sharing a tick are ordered deterministically across sources.
func (r *Reassembler) Records() []Record {
	var out []Record
	for key, st := range r.streams {
		for _, a := range st.order {
			rec := *st.records[a]
			rec.Source = key.source
			rec.Epoch = key.epoch
			out = append(out, rec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.HasTick != b.HasTick {
			return a.HasTick
		}
		if a.HasTick && !a.Tick.Equal(b.Tick) {
			return a.Tick.Before(b.Tick)
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

type fragPiece struct {
	seq  uint64
	data []byte
}

type segment struct {
	start uint64
	end   uint64
}

func (st *stream) segments() ([]segment, map[uint64][]byte) {
	seqs := make([]int, 0, len(st.frags))
	for s := range st.frags {
		seqs = append(seqs, int(s))
	}
	sort.Ints(seqs)
	var segs []segment
	for _, s := range seqs {
		u := uint64(s)
		if n := len(segs); n > 0 && segs[n-1].end+1 == u {
			segs[n-1].end = u
		} else {
			segs = append(segs, segment{start: u, end: u})
		}
	}
	return segs, st.frags
}

type rawLine struct {
	text      string
	a         anchor
	truncated bool
}

// splitSegment concatenates the fragments of one contiguous segment and
// splits them into lines, tracking the anchor of each line.
func (r *Reassembler) splitSegment(seg segment, frags map[uint64][]byte) []rawLine {
	var lines []rawLine
	var buf []byte
	var cur anchor
	have := false
	flush := func() {
		line := string(buf)
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		truncated := false
		if len(line) > r.maxLineLen {
			line = line[:r.maxLineLen]
			truncated = true
		}
		lines = append(lines, rawLine{text: line, a: cur, truncated: truncated})
		buf = buf[:0]
		have = false
	}
	for s := seg.start; s <= seg.end; s++ {
		data := frags[s]
		for i := 0; i < len(data); i++ {
			if !have {
				cur = anchor{seq: s, off: i}
				have = true
			}
			if data[i] == '\n' {
				flush()
			} else {
				buf = append(buf, data[i])
			}
		}
	}
	if have {
		flush()
	}
	return lines
}

// rebuild re-derives all records of one stream and diffs them against the
// previously emitted records, preserving IDs via anchors.
func (r *Reassembler) rebuild(key streamKey, st *stream) []Event {
	segs, frags := st.segments()
	var fresh []*Record
	for i, seg := range segs {
		lines := r.splitSegment(seg, frags)
		baseKnown := i == 0 && seg.start == 0
		var gapBefore *GapInfo
		if i > 0 {
			gapBefore = &GapInfo{MissingFrom: segs[i-1].end + 1, MissingTo: seg.start - 1}
		}
		cutByGap := i < len(segs)-1
		fresh = append(fresh, groupRecords(lines, baseKnown, gapBefore, cutByGap)...)
	}

	var events []Event
	next := make(map[anchor]*Record, len(fresh))
	var order []anchor
	for _, rec := range fresh {
		rec.Source = key.source
		rec.Epoch = key.epoch
		if old, ok := st.records[rec.anchor]; ok {
			rec.ID = old.ID
			rec.Rev = old.Rev
			if !equalRecords(old, rec) {
				rec.Rev = old.Rev + 1
				events = append(events, Event{Kind: EventUpsert, Record: *rec})
			}
		} else {
			rec.ID = r.nextID
			r.nextID++
			events = append(events, Event{Kind: EventUpsert, Record: *rec})
		}
		next[rec.anchor] = rec
		order = append(order, rec.anchor)
	}
	// Anchors that vanished were merged into an earlier record: retract.
	var retracted []*Record
	for a, old := range st.records {
		if _, ok := next[a]; !ok {
			retracted = append(retracted, old)
		}
	}
	sort.Slice(retracted, func(i, j int) bool { return retracted[i].ID < retracted[j].ID })
	for _, old := range retracted {
		events = append(events, Event{Kind: EventRetract, Record: *old})
	}
	st.records = next
	st.order = order
	return events
}

// groupRecords groups the lines of one segment into records. A record
// starts at a non-indented line; indented lines continue the current
// record. Leading continuation lines of a segment form an orphan record
// that is never joined to a record from an earlier segment.
func groupRecords(lines []rawLine, baseKnown bool, gapBefore *GapInfo, cutByGap bool) []*Record {
	var out []*Record
	var cur *Record
	for i, ln := range lines {
		start := isRecordStart(ln.text)
		if cur == nil || start {
			cur = &Record{
				Lines:         []string{ln.text},
				StartLine:     i,
				EndLine:       i,
				LineBaseKnown: baseKnown,
				anchor:        ln.a,
			}
			if tick, ok := parseTick(ln.text); ok {
				cur.Tick = tick
				cur.TickStr = tick.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
				cur.HasTick = true
			}
			out = append(out, cur)
		} else {
			cur.Lines = append(cur.Lines, ln.text)
			cur.EndLine = i
		}
		if ln.truncated {
			cur.Truncated = true
		}
	}
	if len(out) > 0 {
		out[0].GapBefore = gapBefore
		out[len(out)-1].CutByGap = cutByGap
	}
	return out
}

func equalRecords(a, b *Record) bool {
	if a.StartLine != b.StartLine || a.EndLine != b.EndLine ||
		a.LineBaseKnown != b.LineBaseKnown || a.CutByGap != b.CutByGap ||
		a.Truncated != b.Truncated || a.HasTick != b.HasTick {
		return false
	}
	if a.HasTick && !a.Tick.Equal(b.Tick) {
		return false
	}
	if (a.GapBefore == nil) != (b.GapBefore == nil) {
		return false
	}
	if a.GapBefore != nil && *a.GapBefore != *b.GapBefore {
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
