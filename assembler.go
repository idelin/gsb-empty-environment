package logrec

import (
	"errors"
	"fmt"
	"sort"
)

// ErrBackpressure is returned when the total buffer budget is
// exhausted. The input was not consumed; relieve pressure (records are
// emitted as they close, FlushPending forces one out) and retry.
var ErrBackpressure = errors.New("logrec: buffer budget exhausted, apply backpressure")

// seqRef locates a consumed line inside a record.
type seqRef struct {
	RecordID string `json:"record_id"`
}

// assembler holds the reassembly state for one source/epoch pair.
type assembler struct {
	source string
	epoch  uint64
	rules  Rules

	lineBuf    []byte // partial line spanning FeedChunk boundaries
	nextAutoSq uint64 // next sequence number for chunk-fed lines

	maxSeq  uint64 // highest sequence number consumed
	haveSeq bool

	pending *Record            // currently open record
	records map[string]*Record // every record by ID, incl. closed
	bySeq   map[uint64]seqRef  // consumed seq -> owning record

	mux *Multiplexer
}

func newAssembler(m *Multiplexer, source string, epoch uint64, rules Rules) *assembler {
	return &assembler{
		source:     source,
		epoch:      epoch,
		rules:      rules,
		nextAutoSq: 1,
		records:    map[string]*Record{},
		bySeq:      map[uint64]seqRef{},
		mux:        m,
	}
}

func (a *assembler) emit(e Event) {
	e.Source = a.source
	e.Epoch = a.epoch
	a.mux.telemetry.Emit(e)
}

// feedChunk consumes raw bytes, splitting them into lines. A trailing
// partial line is buffered until the next chunk or Close. It returns
// the number of bytes consumed; when the buffer budget runs out it
// stops early and returns ErrBackpressure so the caller can retry the
// unconsumed remainder after draining.
func (a *assembler) feedChunk(data []byte) (int, []Emission, error) {
	var out []Emission
	consumed := 0
	for consumed < len(data) {
		i := indexByte(data[consumed:], '\n')
		var part []byte
		complete := false
		if i < 0 {
			part = data[consumed:]
		} else {
			part = data[consumed : consumed+i]
			complete = true
		}
		if !a.mux.reserve(int64(len(part))) {
			a.emit(Event{Kind: EvBackpressure, Detail: "budget exhausted mid-chunk"})
			return consumed, out, ErrBackpressure
		}
		a.lineBuf = append(a.lineBuf, part...)
		consumed += len(part)
		if complete {
			consumed++ // the newline itself
			line := string(a.lineBuf)
			a.mux.release(int64(len(a.lineBuf)))
			a.lineBuf = a.lineBuf[:0]
			seq := a.nextAutoSq
			a.nextAutoSq++
			recs, err := a.feedLine(seq, line)
			out = append(out, recs...)
			if err != nil {
				return consumed, out, err
			}
		}
	}
	return consumed, out, nil
}

// feedLine consumes one logical line with its original sequence number.
// The line's buffer budget is reserved up front; on ErrBackpressure
// the line was not consumed and no state changed.
func (a *assembler) feedLine(seq uint64, text string) ([]Emission, error) {
	if ref, dup := a.bySeq[seq]; dup {
		return a.correctLine(seq, text, ref)
	}
	if !a.mux.reserve(int64(len(text))) {
		a.emit(Event{Kind: EvBackpressure, Seq: seq})
		return nil, ErrBackpressure
	}
	if a.haveSeq && seq > a.maxSeq+1 {
		a.emit(Event{Kind: EvGapDetected, Seq: seq,
			Detail: fmt.Sprintf("missing seq %d..%d", a.maxSeq+1, seq-1)})
	}
	late := a.haveSeq && seq < a.maxSeq
	if !a.haveSeq || seq > a.maxSeq {
		a.maxSeq = seq
		a.haveSeq = true
	}
	// Keep auto-assigned chunk sequence numbers ahead of every
	// explicit sequence number seen so the two spaces never collide.
	if seq >= a.nextAutoSq {
		a.nextAutoSq = seq + 1
	}
	if a.rules.isStart(text) {
		if late {
			a.emit(Event{Kind: EvLateStartLine, Seq: seq})
		}
		return a.openRecord(seq, text, false), nil
	}
	if a.rules.isContinuation(text) {
		if late {
			return a.mergeLateContinuation(seq, text)
		}
		if a.pending == nil {
			a.emit(Event{Kind: EvOrphanContinued, Seq: seq})
			return a.openRecord(seq, text, true), nil
		}
		a.appendLine(a.pending, Line{Seq: seq, Text: text})
		return nil, nil
	}
	// Non-start, non-continuation line: stands alone as its own record.
	return a.openRecord(seq, text, false), nil
}

// openRecord closes the pending record (if any) and starts a new one.
// The caller has already reserved budget for the first line.
func (a *assembler) openRecord(seq uint64, text string, orphan bool) []Emission {
	var out []Emission
	if a.pending != nil {
		out = append(out, a.closeRecord(CloseStart))
	}
	rec := &Record{
		ID:       recordID(a.source, a.epoch, seq),
		Source:   a.source,
		Epoch:    a.epoch,
		Revision: 1,
		Orphan:   orphan,
	}
	a.records[rec.ID] = rec
	a.pending = rec
	a.appendLine(rec, Line{Seq: seq, Text: text})
	return out
}

// appendLine adds a line to a record, enforcing size caps. Overlong
// records are marked truncated; excess lines are counted and their
// budget released immediately, never silently dropped.
func (a *assembler) appendLine(rec *Record, l Line) {
	if rec.Truncated || a.exceedsCap(rec, l) {
		if !rec.Truncated {
			rec.Truncated = true
			a.emit(Event{Kind: EvRecordTruncated, Seq: l.Seq, RecordID: rec.ID,
				Detail: fmt.Sprintf("record exceeded cap at %d lines / %d bytes",
					len(rec.Lines), recordBytes(rec))})
		}
		rec.Dropped++
		a.mux.release(int64(len(l.Text)))
		a.bySeq[l.Seq] = seqRef{RecordID: rec.ID}
		a.accountSeq(rec, l.Seq)
		return
	}
	rec.Lines = append(rec.Lines, l)
	a.bySeq[l.Seq] = seqRef{RecordID: rec.ID}
	a.accountSeq(rec, l.Seq)
}

// exceedsCap reports whether appending l would push the record past
// its configured size caps.
func (a *assembler) exceedsCap(rec *Record, l Line) bool {
	if a.rules.MaxRecordLines > 0 && len(rec.Lines)+1 > a.rules.MaxRecordLines {
		return true
	}
	if a.rules.MaxRecordBytes > 0 && recordBytes(rec)+len(l.Text) > a.rules.MaxRecordBytes {
		return true
	}
	return false
}

// accountSeq grows the record's seq span.
func (a *assembler) accountSeq(rec *Record, seq uint64) {
	if rec.StartSeq == 0 && rec.EndSeq == 0 {
		rec.StartSeq, rec.EndSeq = seq, seq
		return
	}
	if seq < rec.StartSeq {
		rec.StartSeq = seq
	}
	if seq > rec.EndSeq {
		rec.EndSeq = seq
	}
}

func recordBytes(rec *Record) int {
	n := 0
	for _, l := range rec.Lines {
		n += len(l.Text)
	}
	return n
}

// closeRecord seals the pending record and emits it.
func (a *assembler) closeRecord(reason CloseReason) Emission {
	rec := a.pending
	a.pending = nil
	rec.closed = true
	rec.Reason = reason
	a.mux.release(int64(recordBytes(rec)))
	a.emit(Event{Kind: EvRecordClosed, RecordID: rec.ID,
		Detail: fmt.Sprintf("reason=%s lines=%d rev=%d", reason, len(rec.Lines), rec.Revision)})
	return Emission{Record: rec.clone()}
}

// correctLine handles a re-delivered sequence number: identical text
// is a duplicate, different text is a late correction that bumps the
// owning record's revision in place, preserving line identity.
func (a *assembler) correctLine(seq uint64, text string, ref seqRef) ([]Emission, error) {
	rec := a.records[ref.RecordID]
	for i, l := range rec.Lines {
		if l.Seq != seq {
			continue
		}
		if l.Text == text {
			a.emit(Event{Kind: EvDuplicateLine, Seq: seq, RecordID: rec.ID})
			return nil, nil
		}
		if rec == a.pending {
			// Only pending records hold budget; adjust by the delta.
			if delta := int64(len(text)) - int64(len(l.Text)); delta > 0 {
				if !a.mux.reserve(delta) {
					a.emit(Event{Kind: EvBackpressure, Seq: seq, RecordID: rec.ID})
					return nil, ErrBackpressure
				}
			} else if delta < 0 {
				a.mux.release(-delta)
			}
		}
		rec.Lines[i].Text = text
		rec.Revision++
		a.emit(Event{Kind: EvLineCorrected, Seq: seq, RecordID: rec.ID,
			Detail: fmt.Sprintf("rev=%d", rec.Revision)})
		return []Emission{{Record: rec.clone(), Revised: true}}, nil
	}
	// The seq maps to a line that was dropped by truncation: record the
	// correction as a revision without resurrecting dropped content.
	rec.Revision++
	a.emit(Event{Kind: EvLineCorrected, Seq: seq, RecordID: rec.ID,
		Detail: "corrected line was previously dropped by truncation"})
	return []Emission{{Record: rec.clone(), Revised: true}}, nil
}

// mergeLateContinuation inserts an out-of-order continuation line into
// the record that owns its sequence position. If that record is already
// closed, the record is revised and re-emitted; the line is never
// appended to a different, currently open record.
func (a *assembler) mergeLateContinuation(seq uint64, text string) ([]Emission, error) {
	target := a.recordOwningSeq(seq)
	if target == nil {
		a.emit(Event{Kind: EvOrphanContinued, Seq: seq,
			Detail: "late continuation with no owning record"})
		return a.openRecord(seq, text, true), nil
	}
	a.emit(Event{Kind: EvLateContinuation, Seq: seq, RecordID: target.ID,
		Detail: fmt.Sprintf("closed=%v", target.closed)})
	if target.Truncated {
		target.Dropped++
		a.mux.release(int64(len(text)))
		a.bySeq[seq] = seqRef{RecordID: target.ID}
		a.accountSeq(target, seq)
		target.Revision++
		return []Emission{{Record: target.clone(), Revised: true}}, nil
	}
	line := Line{Seq: seq, Text: text}
	pos := sort.Search(len(target.Lines), func(i int) bool {
		return target.Lines[i].Seq > seq
	})
	target.Lines = append(target.Lines, Line{})
	copy(target.Lines[pos+1:], target.Lines[pos:])
	target.Lines[pos] = line
	a.bySeq[seq] = seqRef{RecordID: target.ID}
	a.accountSeq(target, seq)
	if target.closed {
		// Closed records hold no buffer budget; release the
		// reservation feedLine made for this line.
		a.mux.release(int64(len(text)))
		target.Revision++
		return []Emission{{Record: target.clone(), Revised: true}}, nil
	}
	return nil, nil
}

// recordOwningSeq finds the record responsible for a sequence
// position: the record with the greatest StartSeq <= seq.
func (a *assembler) recordOwningSeq(seq uint64) *Record {
	var best *Record
	for _, rec := range a.records {
		if rec.StartSeq <= seq && (best == nil || rec.StartSeq > best.StartSeq) {
			best = rec
		}
	}
	return best
}

// flushPending force-closes the open record, e.g. to relieve buffer
// pressure. Late continuations may still revise it afterwards.
func (a *assembler) flushPending() []Emission {
	if a.pending == nil {
		return nil
	}
	return []Emission{a.closeRecord(CloseFlush)}
}

// close terminates the stream: a buffered partial line is completed as
// a final line, and a still-open record is emitted as an incomplete
// tail (CloseEOF) rather than being discarded.
func (a *assembler) close() []Emission {
	var out []Emission
	if len(a.lineBuf) > 0 {
		line := string(a.lineBuf)
		a.mux.release(int64(len(a.lineBuf)))
		a.lineBuf = nil
		seq := a.nextAutoSq
		a.nextAutoSq++
		recs, _ := a.feedLine(seq, line)
		out = append(out, recs...)
	}
	if a.pending != nil {
		out = append(out, a.closeRecord(CloseEOF))
	}
	a.emit(Event{Kind: EvSourceClosed,
		Detail: fmt.Sprintf("lines=%d records=%d", a.maxSeq, len(a.records))})
	return out
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
