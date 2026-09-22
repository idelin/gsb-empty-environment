package framemux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// parseState is the state of the incremental parser.
type parseState uint8

const (
	stHunt       parseState = iota // scanning for the delimiter
	stHeader                       // accumulating the header
	stWaitBudget                   // header parsed, waiting for buffer budget
	stPayload                      // accumulating the payload
	stCRC                          // accumulating the CRC
	stGraceHunt                    // epoch grace: collecting a possible old-epoch tail frame
	stStalled                      // a complete frame could not be dispatched (backpressure)
)

// parser incrementally decodes frames from arbitrarily chunked input. All
// buffers are bounded by the configured limits; escape state (escPending)
// survives chunk boundaries, pauses and epoch negotiations.
type parser struct {
	cfg       EscapeConfig // escape rules of the current epoch
	epoch     uint8
	prevEpoch uint8 // previous epoch, accepted during the grace window
	grace     int   // remaining old-epoch tail frames still accepted
	seenNew   bool  // a current-epoch frame closed the grace window

	maxPayload int
	maxRaw     int // bound on raw bytes of one frame (escaped worst case)
	maxScan    int // bound on delimiter scan run before ErrNoDelimiter
	budget     func() int

	state      parseState
	stallNext  parseState // state to return to after a stall clears
	in         []byte     // unconsumed input
	last       byte       // last byte seen while hunting (split delimiter)
	escPending bool       // previous byte was the escape introducer

	hdr  [headerLen]byte
	hdrN int
	plen int

	payload []byte
	payN    int

	crcb [crcLen]byte
	crcN int

	raw      []byte // raw wire bytes of the in-flight frame, for resync/tail decode
	huntRun  int
	overflow bool // raw exceeded maxRaw

	holdFrame Frame // complete frame awaiting dispatch
	hasHold   bool

	deliver func(Frame) bool // false => apply backpressure
	onBad   func(error)
	stats   *Stats
}

func (p *parser) resetFrame() {
	p.hdrN = 0
	p.plen = 0
	p.payload = nil
	p.payN = 0
	p.crcN = 0
	p.escPending = false
	p.raw = p.raw[:0]
}

func (p *parser) feed(buf []byte) {
	p.in = append(p.in, buf...)
	p.run()
}

func (p *parser) run() {
	for {
		if p.overflow {
			p.overflow = false
			if p.grace > 0 && !p.seenNew {
				p.state = stGraceHunt // its own cap will finalize the resync
			} else {
				p.bad(fmt.Errorf("%w: frame exceeds %d raw bytes", ErrBadData, p.maxRaw))
				p.resync()
			}
			continue
		}
		switch p.state {
		case stHunt:
			if !p.hunt() {
				return
			}
		case stHeader:
			if !p.readHeader() {
				return
			}
		case stWaitBudget:
			if p.plen > p.budget() {
				return // wait until Recv frees budget
			}
			p.allocPayload()
		case stPayload:
			if !p.readPayload() {
				return
			}
		case stCRC:
			if !p.readCRC() {
				return
			}
		case stGraceHunt:
			if !p.graceHunt() {
				return
			}
		case stStalled:
			return
		}
	}
}

// retryStall re-attempts a stalled dispatch or a budget wait. Parser state
// (including escPending and negotiation state) is untouched.
func (p *parser) retryStall() {
	switch p.state {
	case stStalled:
		if p.hasHold && p.deliver(p.holdFrame) {
			p.hasHold = false
			p.state = p.stallNext
			p.run()
		}
	case stWaitBudget:
		p.run()
	}
}

// nextDataByte returns the next unescaped byte. Every consumed wire byte is
// recorded in raw. ok=false means the input is exhausted (need more data).
func (p *parser) nextDataByte() (b byte, ok bool) {
	for len(p.in) > 0 {
		c := p.in[0]
		p.in = p.in[1:]
		p.raw = append(p.raw, c)
		if len(p.raw) > p.stats.MaxRawBuffer {
			p.stats.MaxRawBuffer = len(p.raw)
		}
		if len(p.raw) > p.maxRaw {
			p.overflow = true
			return 0, false
		}
		if p.escPending {
			p.escPending = false
			return c ^ p.cfg.Xor, true
		}
		if c == p.cfg.Esc {
			p.escPending = true
			continue
		}
		return c, true
	}
	return 0, false
}

func (p *parser) hunt() bool {
	for len(p.in) > 0 {
		b := p.in[0]
		p.in = p.in[1:]
		p.stats.ScanBytes++
		p.huntRun++
		if p.last == Magic0 && b == Magic1 {
			p.last = 0
			p.huntRun = 0
			p.resetFrame()
			p.state = stHeader
			return true
		}
		p.last = b
		if p.huntRun > p.maxScan {
			p.huntRun = 0
			p.bad(ErrNoDelimiter) // bounded resync: keep hunting
		}
	}
	return false
}

func (p *parser) readHeader() bool {
	for p.hdrN < headerLen {
		b, ok := p.nextDataByte()
		if !ok {
			return false
		}
		p.hdr[p.hdrN] = b
		p.hdrN++
	}
	p.plen = int(p.hdr[4]) | int(p.hdr[5])<<8
	// Check the length quota before allocating anything.
	if p.plen > p.maxPayload {
		p.badHeader()
		return true
	}
	if p.plen > p.budget() {
		p.state = stWaitBudget
		return false
	}
	p.allocPayload()
	return true
}

func (p *parser) allocPayload() {
	p.payload = make([]byte, p.plen)
	if p.plen == 0 {
		p.state = stCRC
	} else {
		p.state = stPayload
	}
}

func (p *parser) readPayload() bool {
	for p.payN < p.plen {
		b, ok := p.nextDataByte()
		if !ok {
			return false
		}
		p.payload[p.payN] = b
		p.payN++
	}
	p.state = stCRC
	return true
}

func (p *parser) readCRC() bool {
	for p.crcN < crcLen {
		b, ok := p.nextDataByte()
		if !ok {
			return false
		}
		p.crcb[p.crcN] = b
		p.crcN++
	}
	p.completeFrame()
	return true
}

func (p *parser) completeFrame() {
	f := Frame{
		Type:   FrameType(p.hdr[0]),
		Stream: p.hdr[1],
		Seq:    p.hdr[2],
		Epoch:  p.hdr[3],
	}
	h := crc32.NewIEEE()
	h.Write(p.hdr[:])
	h.Write(p.payload)
	if binary.LittleEndian.Uint32(p.crcb[:]) == h.Sum32() {
		f.Payload = p.payload
		switch {
		case f.Epoch == p.epoch:
			p.seenNew = true
			p.grace = 0
			p.emit(f, stHunt)
		case p.grace > 0 && f.Epoch == p.prevEpoch:
			p.grace-- // old-epoch tail frame, still inside the grace window
			p.emit(f, stHunt)
		default:
			p.bad(fmt.Errorf("%w: unexpected epoch %d", ErrBadData, f.Epoch))
			p.resync()
		}
		return
	}
	// CRC mismatch under the current rules: the frame may be an old-epoch
	// tail frame interleaved with the epoch switch confirmation.
	if p.grace > 0 && !p.seenNew {
		tf, n, err := DecodeFrame(p.raw, p.prevEpoch)
		switch {
		case err == nil && tf.Epoch == p.prevEpoch:
			p.grace--
			rest := append([]byte(nil), p.raw[n:]...)
			p.in = append(rest, p.in...)
			p.emit(tf, stHunt)
			return
		case errors.Is(err, ErrNeedMoreData):
			p.state = stGraceHunt // collect the rest of the tail frame
			return
		}
	}
	p.bad(fmt.Errorf("%w: crc mismatch", ErrBadData))
	p.resync()
}

// graceHunt collects bytes until the next delimiter, then tries to decode
// the collected segment as an old-epoch tail frame. The segment buffer is
// bounded by maxRaw.
func (p *parser) graceHunt() bool {
	for len(p.in) > 0 {
		b := p.in[0]
		p.in = p.in[1:]
		p.stats.ScanBytes++
		if n := len(p.raw); n > 0 && p.raw[n-1] == Magic0 && b == Magic1 {
			seg := p.raw[:n-1]
			p.tryTail(seg)
			if p.state == stGraceHunt { // tryTail did not redirect
				p.state = stHeader // delimiter consumed; next frame starts
			}
			return true
		}
		p.raw = append(p.raw, b)
		if len(p.raw) > p.stats.MaxRawBuffer {
			p.stats.MaxRawBuffer = len(p.raw)
		}
		if len(p.raw) > p.maxRaw {
			p.bad(ErrNoDelimiter)
			p.resync()
			return true
		}
	}
	// Input drained: the segment may already be a complete tail frame.
	if len(p.raw) > 0 {
		if tf, n, err := DecodeFrame(p.raw, p.prevEpoch); err == nil && tf.Epoch == p.prevEpoch && p.grace > 0 {
			p.grace--
			rest := append([]byte(nil), p.raw[n:]...)
			p.in = append(rest, p.in...)
			p.emit(tf, stHunt)
		}
		// Otherwise keep waiting: the segment is an incomplete tail frame.
	}
	return false
}

// tryTail attempts to decode seg as an old-epoch tail frame and deliver it.
func (p *parser) tryTail(seg []byte) {
	tf, n, err := DecodeFrame(seg, p.prevEpoch)
	if err == nil && tf.Epoch == p.prevEpoch && p.grace > 0 {
		p.grace--
		rest := append([]byte(nil), seg[n:]...)
		p.in = append(rest, p.in...)
		p.emit(tf, stHeader)
		return
	}
	p.bad(fmt.Errorf("%w: undecodable tail segment", ErrBadData))
	p.resetFrame()
}

func (p *parser) emit(f Frame, next parseState) {
	p.resetFrame()
	if p.deliver(f) {
		p.state = next
	} else {
		p.holdFrame = f
		p.hasHold = true
		p.stallNext = next
		p.state = stStalled
	}
}

// resync recovers after bad data: it rescans the current frame's raw bytes
// for an embedded delimiter (a lost delimiter may hide the next frame start
// inside what was parsed as payload) and falls back to hunting. Bounded by
// maxRaw.
func (p *parser) resync() {
	p.stats.Resyncs++
	p.stats.ScanBytes += int64(len(p.raw))
	idx := -1
	for i := 1; i+1 < len(p.raw); i++ {
		if p.raw[i] == Magic0 && p.raw[i+1] == Magic1 {
			idx = i
			break
		}
	}
	switch {
	case idx >= 0:
		keep := append([]byte(nil), p.raw[idx:]...)
		p.in = append(keep, p.in...)
		p.last = 0
	case len(p.raw) > 0 && p.raw[len(p.raw)-1] == Magic0:
		p.last = Magic0 // delimiter split across the boundary
	default:
		p.last = 0
	}
	p.resetFrame()
	p.state = stHunt
}

func (p *parser) badHeader() {
	if p.grace > 0 && !p.seenNew {
		p.state = stGraceHunt
		return
	}
	p.bad(fmt.Errorf("%w: invalid header (len %d)", ErrBadData, p.plen))
	p.resync()
}

func (p *parser) bad(err error) {
	p.stats.BadFrames++
	if p.onBad != nil {
		p.onBad(err)
	}
}
