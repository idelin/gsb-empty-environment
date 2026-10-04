package pcf

import (
	"encoding/binary"
	"hash/crc32"
)

// EventKind classifies the regions a Reader reports.
type EventKind int

const (
	// EventFrame is a fully decoded frame whose structure and CRC are valid.
	EventFrame EventKind = iota
	// EventBad is a byte range confirmed to be undecodable: garbage bytes,
	// invalid headers, or frames whose CRC/payload check failed. The reader
	// reports the byte range only; it never guesses which original byte or
	// character inside the range was the corrupted one.
	EventBad
	// EventGap is a byte range that cannot be classified, e.g. a truncated
	// frame tail at end of stream.
	EventGap
)

func (k EventKind) String() string {
	switch k {
	case EventFrame:
		return "frame"
	case EventBad:
		return "bad"
	case EventGap:
		return "gap"
	}
	return "unknown"
}

// Event is one classified region of the input stream. Events are emitted
// in stream order, never overlap, and tile the consumed prefix of the
// stream exactly: every input byte is accounted for exactly once.
type Event struct {
	Kind   EventKind
	Offset int64  // absolute stream offset of the region
	Length int    // number of input bytes the region occupies
	Data   []byte // Frame: decoded payload. Bad/Gap: raw stream bytes.
	Seq    uint32 // Frame only: sequence number from the header
	Reason string // Bad only: why the region was rejected
}

// Options configures a Reader. Zero values select defaults.
type Options struct {
	MaxRawLen     uint32 // max decoded payload per frame (default 1 MiB)
	MaxPayloadLen uint32 // max encoded payload per frame (default 1 MiB)
	// ScanBudget bounds how many resync scan steps a single Feed/Close
	// call may perform. 0 means unlimited. When the budget is exhausted
	// the reader pauses with all state retained; call Feed(nil) to resume.
	ScanBudget int
}

const (
	DefaultMaxRawLen     = 1 << 20
	DefaultMaxPayloadLen = 1 << 20
)

// Reader is a streaming frame reader. Bytes are appended with Feed and
// classified regions are returned as Events. The reader keeps every
// unclassified byte plus its mode in its state, so it can be paused (by
// budget exhaustion or by lack of input) and resumed at any time without
// losing bytes.
type Reader struct {
	maxRaw     uint32
	maxPayload uint32
	budget     int

	buf  []byte // bytes not yet classified
	base int64  // absolute stream offset of buf[0]

	// resync is false right after a confirmed frame boundary (normal
	// mode) and true while scanning for the next valid frame.
	resync bool

	// Contiguous bad bytes accumulated but not yet emitted.
	pendingBad    []byte
	pendingBadOff int64
	pendingReason string

	closed bool
}

// NewReader creates a Reader with the given options.
func NewReader(opt Options) *Reader {
	r := &Reader{
		maxRaw:     opt.MaxRawLen,
		maxPayload: opt.MaxPayloadLen,
		budget:     opt.ScanBudget,
	}
	if r.maxRaw == 0 {
		r.maxRaw = DefaultMaxRawLen
	}
	if r.maxPayload == 0 {
		r.maxPayload = DefaultMaxPayloadLen
	}
	return r
}

// Buffered reports how many input bytes are held pending classification.
func (r *Reader) Buffered() int { return len(r.buf) }

// Feed appends chunk to the stream and classifies as much as possible,
// returning the events produced by this call. Feeding a nil or empty
// chunk simply resumes paused work.
func (r *Reader) Feed(chunk []byte) []Event {
	if r.closed {
		return nil
	}
	if len(chunk) > 0 {
		r.buf = append(r.buf, chunk...)
	}
	return r.process(false)
}

// Close finalizes the stream. Trailing garbage is reported as a Bad
// event; a truncated frame tail is reported as a Gap event because the
// reader cannot determine what it was.
func (r *Reader) Close() []Event {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.process(true)
}

type frameStatus int

const (
	stOK frameStatus = iota
	stBadHeader
	stBadFrame
	stNeedMore
)

func (r *Reader) process(final bool) []Event {
	var evs []Event
	budget := r.budget // per-call budget; 0 = unlimited
	for len(r.buf) > 0 {
		st, frameLen, hdr, decoded, reason := r.tryFrame()
		switch st {
		case stOK:
			r.flushBad(&evs)
			evs = append(evs, Event{Kind: EventFrame, Offset: r.base, Length: frameLen, Data: decoded, Seq: hdr.seq})
			r.advance(frameLen)
			r.resync = false
		case stBadFrame:
			if r.resync {
				// Resync: a candidate is accepted only if structure AND
				// CRC are fully valid. This one is not; step one byte.
				if !r.spend(&budget) {
					return evs
				}
				r.addBad(r.buf[:1], reason)
				r.advance(1)
			} else {
				// Normal mode: the header was structurally valid, so the
				// declared frame length is authoritative. Consume the
				// whole frame as bad; magic bytes inside its payload do
				// not start a new frame.
				r.addBad(r.buf[:frameLen], reason)
				r.advance(frameLen)
			}
		case stBadHeader:
			// Illegal header or length: enter resync and scan forward
			// byte by byte.
			if !r.spend(&budget) {
				return evs
			}
			r.addBad(r.buf[:1], reason)
			r.advance(1)
			r.resync = true
		case stNeedMore:
			if !final {
				return evs
			}
			// End of stream with an incomplete frame candidate: the
			// remainder is undetermined, not decodable and not provably
			// garbage.
			r.flushBad(&evs)
			evs = append(evs, Event{Kind: EventGap, Offset: r.base, Length: len(r.buf), Data: append([]byte(nil), r.buf...)})
			r.advance(len(r.buf))
		}
	}
	if final {
		r.flushBad(&evs)
	}
	return evs
}

// spend consumes one unit of scan budget and reports whether the step is
// allowed. A zero configured budget means unlimited.
func (r *Reader) spend(budget *int) bool {
	if r.budget == 0 {
		return true
	}
	if *budget == 0 {
		return false
	}
	*budget--
	return true
}

func (r *Reader) advance(n int) {
	r.buf = r.buf[n:]
	r.base += int64(n)
}

// addBad appends bytes to the pending bad range. Bytes are always added
// in stream order as the read position advances, so the pending range
// stays contiguous.
func (r *Reader) addBad(b []byte, reason string) {
	if len(r.pendingBad) == 0 {
		r.pendingBadOff = r.base
		r.pendingReason = reason
	}
	r.pendingBad = append(r.pendingBad, b...)
}

func (r *Reader) flushBad(evs *[]Event) {
	if len(r.pendingBad) == 0 {
		return
	}
	*evs = append(*evs, Event{
		Kind:   EventBad,
		Offset: r.pendingBadOff,
		Length: len(r.pendingBad),
		Data:   r.pendingBad,
		Reason: r.pendingReason,
	})
	r.pendingBad = nil
}

// tryFrame attempts to classify the frame candidate at the head of the
// buffer.
func (r *Reader) tryFrame() (st frameStatus, frameLen int, hdr header, decoded []byte, reason string) {
	buf := r.buf
	for i := 0; i < len(Magic) && i < len(buf); i++ {
		if buf[i] != Magic[i] {
			return stBadHeader, 0, hdr, nil, "bad magic"
		}
	}
	if len(buf) < HeaderSize {
		return stNeedMore, 0, hdr, nil, ""
	}
	h, err := r.parseHeader(buf)
	if err != nil {
		return stBadHeader, 0, hdr, nil, err.Error()
	}
	frameLen = HeaderSize + int(h.payloadLen) + CRCSize
	if len(buf) < frameLen {
		return stNeedMore, frameLen, h, nil, ""
	}
	end := HeaderSize + int(h.payloadLen)
	want := binary.LittleEndian.Uint32(buf[end : end+CRCSize])
	if want != crc32.ChecksumIEEE(buf[:end]) {
		return stBadFrame, frameLen, h, nil, "CRC mismatch"
	}
	dec, err := decodePayload(buf[HeaderSize:end], h.lengths, int(h.rawLen))
	if err != nil {
		return stBadFrame, frameLen, h, nil, "payload undecodable: " + err.Error()
	}
	return stOK, frameLen, h, dec, ""
}
