package deltacodec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	// ErrNoBaseline marks undecodable deltas: the channel baseline was
	// never established, was reset, or was lost to a corrupt frame.
	ErrNoBaseline = errors.New("deltacodec: no baseline for channel")
)

type chanState struct {
	base  int64
	valid bool
}

// Decoder incrementally parses a byte stream. Input may be chunked
// arbitrarily: Write buffers partial frames and only emits events for
// complete ones. Per-channel state is fully isolated.
type Decoder struct {
	buf  []byte
	chans map[byte]chanState
}

func NewDecoder() *Decoder {
	return &Decoder{chans: make(map[byte]chanState)}
}

// Write consumes a chunk of the stream and returns events for every
// complete frame it contains. A non-nil error means the stream itself is
// fatally corrupt (a bad length prefix makes resync impossible); events
// parsed before the error are still returned.
func (d *Decoder) Write(p []byte) ([]Event, error) {
	d.buf = append(d.buf, p...)
	var events []Event
	for len(d.buf) > 0 {
		l := int(d.buf[0])
		// Length budget check BEFORE any allocation or copy.
		if l < 2 {
			return events, fmt.Errorf("%w: got %d", ErrFrameTooShort, l)
		}
		if l > MaxFrameLen {
			return events, fmt.Errorf("%w: got %d (max %d)", ErrFrameTooLong, l, MaxFrameLen)
		}
		if len(d.buf) < 1+l {
			break // incomplete frame: need more bytes, not an error
		}
		raw := d.buf[1 : 1+l]
		d.buf = d.buf[1+l:]
		events = append(events, d.apply(raw))
	}
	return events, nil
}

// apply parses one complete frame and updates channel state.
func (d *Decoder) apply(raw []byte) Event {
	typ, ch, payload := raw[0], raw[1], raw[2:]
	st := d.chans[ch]

	poison := func(err error) Event {
		// A frame we cannot interpret may have carried a delta; the
		// baseline is no longer trustworthy until the next anchor.
		st.valid = false
		d.chans[ch] = st
		return Event{Kind: EventCorrupt, Chan: ch, Err: err}
	}

	switch typ {
	case frameAnchor:
		v, err := parseVarintPayload(payload)
		if err != nil {
			return poison(err)
		}
		st.base, st.valid = v, true
		d.chans[ch] = st
		return Event{Kind: EventValue, Chan: ch, V: v}

	case frameDelta:
		delta, err := parseVarintPayload(payload)
		if err != nil {
			return poison(err)
		}
		if !st.valid {
			// Never guess: without a baseline the value is unknowable.
			return Event{Kind: EventUndecodable, Chan: ch, Err: ErrNoBaseline}
		}
		v := st.base + delta
		if (delta > 0 && v < st.base) || (delta < 0 && v > st.base) {
			return poison(fmt.Errorf("deltacodec: delta %d overflows baseline %d", delta, st.base))
		}
		st.base = v
		d.chans[ch] = st
		return Event{Kind: EventValue, Chan: ch, V: v}

	case frameReset:
		if len(payload) != 0 {
			return poison(errors.New("deltacodec: reset frame with payload"))
		}
		st.valid = false
		d.chans[ch] = st
		return Event{Kind: EventReset, Chan: ch}

	case frameMissing:
		if len(payload) != 0 {
			return poison(errors.New("deltacodec: missing frame with payload"))
		}
		// Missing sample: baseline intentionally kept, chain continues.
		return Event{Kind: EventMissing, Chan: ch}

	default:
		return poison(fmt.Errorf("deltacodec: unknown frame type 0x%02x", typ))
	}
}

// parseVarintPayload decodes a payload that must be exactly one varint.
func parseVarintPayload(payload []byte) (int64, error) {
	if len(payload) == 0 {
		return 0, errors.New("deltacodec: empty value payload")
	}
	v, n := binary.Varint(payload)
	if n == 0 {
		return 0, errors.New("deltacodec: value payload too short")
	}
	if n < 0 {
		return 0, errors.New("deltacodec: value varint overflows 64 bits")
	}
	if n != len(payload) {
		return 0, errors.New("deltacodec: trailing bytes after value")
	}
	return v, nil
}
