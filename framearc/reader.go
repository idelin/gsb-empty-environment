package framearc

import "bytes"

// EventKind classifies what the reader reports about a byte range.
type EventKind int

const (
	// EventFrame: a fully decoded frame; both CRCs verified.
	EventFrame EventKind = iota
	// EventBadFrame: header CRC valid, so the extent is trustworthy, but
	// the payload failed to decode or failed its CRC. The reader reports
	// the byte range only; it never claims which original symbol is wrong.
	EventBadFrame
	// EventGap: a region whose structure cannot be determined (invalid
	// header, truncated tail, or bytes skipped during resync). Nothing
	// can be said about its original content.
	EventGap
)

// Event describes one classified byte range of the input stream.
// Start and End are absolute stream offsets, [Start, End).
type Event struct {
	Kind   EventKind
	Start  int64
	End    int64
	Seq    uint32 // valid for EventFrame and EventBadFrame
	Data   []byte // decoded payload, valid for EventFrame
	Reason string // failure description, valid for EventBadFrame
}

// Reader consumes an archive stream incrementally and classifies every
// byte exactly once: decoded frame, bad frame, or gap.
//
// Normal mode consumes confirmed frame lengths only; magic bytes inside
// a payload are never treated as a header. When a header or length is
// invalid the reader enters resync mode and scans candidate offsets one
// byte at a time, accepting a candidate only when its structure and both
// CRCs check out. Budget limits how many candidate positions one call
// may examine; when the budget runs out the reader pauses with all state
// retained and resumes on the next Pump or Feed.
type Reader struct {
	buf      []byte
	base     int64 // absolute offset of buf[0]
	resync   bool
	gapStart int64 // absolute offset where the current gap began
	scanPos  int   // next candidate offset within buf (resync mode)
	closed   bool
	paused   bool

	// Budget caps candidate positions examined per call; <= 0 is unlimited.
	Budget int
}

// Feed appends data and processes as far as possible.
func (r *Reader) Feed(p []byte) []Event {
	r.buf = append(r.buf, p...)
	return r.process()
}

// Pump resumes processing after a budget pause.
func (r *Reader) Pump() []Event {
	return r.process()
}

// Paused reports whether the reader stopped because of Budget and can
// make further progress with the data it already has.
func (r *Reader) Paused() bool {
	return r.paused
}

// Close signals end of stream: any remaining undecodable bytes are
// reported as a gap.
func (r *Reader) Close() []Event {
	r.closed = true
	return r.process()
}

func (r *Reader) consume(n int) {
	r.buf = r.buf[n:]
	r.base += int64(n)
	if r.scanPos > 0 {
		r.scanPos -= n
		if r.scanPos < 0 {
			r.scanPos = 0
		}
	}
}

func (r *Reader) process() []Event {
	r.paused = false
	var events []Event
	budget := r.Budget
	if budget <= 0 {
		budget = -1 // unlimited
	}
	for {
		if r.resync {
			if ev, ok := r.scan(&events, &budget); !ok {
				return ev
			}
			continue
		}
		if len(r.buf) == 0 {
			return events
		}
		h, err := parseHeader(r.buf)
		if err == errNeedMore {
			if r.closed {
				events = append(events, Event{Kind: EventGap, Start: r.base, End: r.base + int64(len(r.buf))})
				r.consume(len(r.buf))
				continue
			}
			return events
		}
		if err != nil {
			// Invalid header at a frame boundary: start resync. The
			// current position was already rejected, so scan from +1.
			r.resync = true
			r.gapStart = r.base
			r.scanPos = 1
			continue
		}
		if len(r.buf) < h.frameLen() {
			if r.closed {
				events = append(events, Event{Kind: EventGap, Start: r.base, End: r.base + int64(len(r.buf))})
				r.consume(len(r.buf))
				continue
			}
			return events
		}
		data, derr := h.decode(r.buf[:h.frameLen()])
		end := r.base + int64(h.frameLen())
		if derr != nil {
			events = append(events, Event{Kind: EventBadFrame, Start: r.base, End: end, Seq: h.seq, Reason: derr.Error()})
		} else {
			events = append(events, Event{Kind: EventFrame, Start: r.base, End: end, Seq: h.seq, Data: data})
		}
		r.consume(h.frameLen())
	}
}

// scan examines candidate offsets in resync mode. It returns ok=false
// when processing must stop (budget exhausted, waiting for data, or
// buffer drained); events may already have been appended.
func (r *Reader) scan(events *[]Event, budget *int) ([]Event, bool) {
	for r.scanPos < len(r.buf) {
		if *budget == 0 {
			r.paused = true
			return *events, false
		}
		if *budget > 0 {
			*budget--
		}
		i := r.scanPos
		if len(r.buf)-i < len(magicBytes) {
			if r.closed {
				r.scanPos++
				continue
			}
			return *events, false // possible partial magic; wait for data
		}
		if !bytes.Equal(r.buf[i:i+len(magicBytes)], magicBytes) {
			r.scanPos++
			continue
		}
		h, err := parseHeader(r.buf[i:])
		if err == errNeedMore {
			if r.closed {
				r.scanPos++
				continue
			}
			return *events, false // anchored on a plausible header; wait
		}
		if err != nil {
			r.scanPos++
			continue
		}
		if len(r.buf)-i < h.frameLen() {
			if r.closed {
				r.scanPos++
				continue
			}
			return *events, false // wait for the rest of this frame
		}
		start := r.base + int64(i)
		end := start + int64(h.frameLen())
		if r.gapStart < start {
			*events = append(*events, Event{Kind: EventGap, Start: r.gapStart, End: start})
		}
		data, derr := h.decode(r.buf[i : i+h.frameLen()])
		if derr != nil {
			*events = append(*events, Event{Kind: EventBadFrame, Start: start, End: end, Seq: h.seq, Reason: derr.Error()})
		} else {
			*events = append(*events, Event{Kind: EventFrame, Start: start, End: end, Seq: h.seq, Data: data})
		}
		r.consume(i + h.frameLen())
		r.resync = false
		r.scanPos = 0
		return *events, true
	}
	// Scanned to the end of the buffer.
	if r.closed && len(r.buf) > 0 {
		end := r.base + int64(len(r.buf))
		if r.gapStart < end {
			*events = append(*events, Event{Kind: EventGap, Start: r.gapStart, End: end})
		}
		r.consume(len(r.buf))
	}
	return *events, false
}
