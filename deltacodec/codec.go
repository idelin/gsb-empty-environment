// Package deltacodec implements a per-channel delta codec over a byte stream.
//
// Wire format: every frame is length-prefixed so the byte stream may be
// chunked arbitrarily and truncated/corrupt frames can be identified.
//
//	[len:1][type:1][chan:1][payload:len-2]
//
// len counts type+chan+payload (so the minimum legal len is 2).
// Frame types:
//
//	0x01 anchor   payload = signed varint absolute value (full baseline)
//	0x02 delta    payload = signed varint difference from previous value
//	0x03 reset    payload = empty, clears the channel baseline
//	0x04 missing  payload = empty, marks an absent sample (baseline kept)
package deltacodec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	frameAnchor  byte = 0x01
	frameDelta   byte = 0x02
	frameReset   byte = 0x03
	frameMissing byte = 0x04
)

// MaxFrameLen is the largest legal value of the length prefix. A signed
// varint payload needs at most 10 bytes, so 2+10=12 suffices; 16 leaves
// headroom. The decoder checks the declared length against this budget
// before allocating anything.
const MaxFrameLen = 16

var (
	// ErrFrameTooShort means the length prefix is below the legal minimum.
	ErrFrameTooShort = errors.New("deltacodec: frame length below minimum")
	// ErrFrameTooLong means the length prefix exceeds MaxFrameLen.
	ErrFrameTooLong = errors.New("deltacodec: frame length exceeds budget")
)

// frame returns the wire encoding of one frame.
func frame(typ, ch byte, payload []byte) []byte {
	b := make([]byte, 0, 3+len(payload))
	b = append(b, byte(2+len(payload)), typ, ch)
	return append(b, payload...)
}

// EncodeAnchor appends a full-value anchor frame for ch.
func EncodeAnchor(w io.Writer, ch byte, v int64) error {
	var p [binary.MaxVarintLen64]byte
	n := binary.PutVarint(p[:], v)
	_, err := w.Write(frame(frameAnchor, ch, p[:n]))
	return err
}

// EncodeDelta appends a delta frame for ch. Negative deltas are supported
// (signed varint / zigzag encoding).
func EncodeDelta(w io.Writer, ch byte, delta int64) error {
	var p [binary.MaxVarintLen64]byte
	n := binary.PutVarint(p[:], delta)
	_, err := w.Write(frame(frameDelta, ch, p[:n]))
	return err
}

// EncodeReset appends a reset frame, clearing the channel baseline.
func EncodeReset(w io.Writer, ch byte) error {
	_, err := w.Write(frame(frameReset, ch, nil))
	return err
}

// EncodeMissing appends a missing-sample marker for ch. The baseline is
// unaffected, so the delta chain continues after the gap.
func EncodeMissing(w io.Writer, ch byte) error {
	_, err := w.Write(frame(frameMissing, ch, nil))
	return err
}

// Encoder is a stateful per-channel encoder. It emits deltas against the
// last emitted value and automatically falls back to a full anchor when
// the channel is new, was reset, the periodic anchor interval is reached,
// or the delta would overflow int64.
type Encoder struct {
	w        io.Writer
	interval int // anchor every interval samples per channel; <=0 disables
	last     map[byte]int64
	known    map[byte]bool
	since    map[byte]int
}

// NewEncoder returns an Encoder writing to w. anchorInterval > 0 forces a
// full anchor every anchorInterval samples per channel (periodic anchors
// bound how long a lost delta poisons the stream).
func NewEncoder(w io.Writer, anchorInterval int) *Encoder {
	return &Encoder{
		w:        w,
		interval: anchorInterval,
		last:     make(map[byte]int64),
		known:    make(map[byte]bool),
		since:    make(map[byte]int),
	}
}

// Sample encodes one measurement for ch, choosing delta or anchor.
func (e *Encoder) Sample(ch byte, v int64) error {
	if e.known[ch] && (e.interval <= 0 || e.since[ch] < e.interval) {
		delta := v - e.last[ch]
		// Overflow check: if the subtraction wrapped, the sign test fails.
		if (v >= e.last[ch]) == (delta >= 0) {
			if err := EncodeDelta(e.w, ch, delta); err != nil {
				return err
			}
			e.last[ch] = v
			e.since[ch]++
			return nil
		}
	}
	if err := EncodeAnchor(e.w, ch, v); err != nil {
		return err
	}
	e.last[ch] = v
	e.known[ch] = true
	e.since[ch] = 1 // the anchor itself counts as the first sample
	return nil
}

// Missing encodes an absent sample for ch.
func (e *Encoder) Missing(ch byte) error { return EncodeMissing(e.w, ch) }

// Reset encodes a reset frame and drops the encoder-side baseline.
func (e *Encoder) Reset(ch byte) error {
	delete(e.last, ch)
	delete(e.known, ch)
	delete(e.since, ch)
	return EncodeReset(e.w, ch)
}

// EventKind classifies decoder output events.
type EventKind int

const (
	EventValue       EventKind = iota // a decoded absolute value
	EventMissing                      // an explicitly missing sample
	EventReset                        // the channel baseline was reset
	EventUndecodable                  // baseline lost; value cannot be determined
	EventCorrupt                      // a frame failed to parse and was skipped
)

// Event is one decoded output item.
type Event struct {
	Kind EventKind
	Chan byte
	V    int64 // valid only for EventValue
	Err  error // valid only for EventCorrupt / EventUndecodable
}

func (e Event) String() string {
	switch e.Kind {
	case EventValue:
		return fmt.Sprintf("VALUE   ch=%d v=%d", e.Chan, e.V)
	case EventMissing:
		return fmt.Sprintf("MISSING ch=%d", e.Chan)
	case EventReset:
		return fmt.Sprintf("RESET   ch=%d", e.Chan)
	case EventUndecodable:
		return fmt.Sprintf("UNDECODABLE ch=%d (%v)", e.Chan, e.Err)
	case EventCorrupt:
		return fmt.Sprintf("CORRUPT ch=%d (%v)", e.Chan, e.Err)
	}
	return "?"
}
