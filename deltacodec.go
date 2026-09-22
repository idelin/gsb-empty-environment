// Package deltacodec implements a per-channel delta codec.
//
// Each channel keeps its own last-value baseline. Absolute anchor frames
// establish the baseline, delta frames carry the signed difference to the
// previous value, missing frames explicitly mark a gap in the measurements,
// and reset frames drop the baseline entirely. The encoder may also emit a
// periodic anchor so that a receiver which joined late (or lost a frame) can
// re-synchronize without rebuilding state.
//
// The wire format is a sequence of self-delimiting frames:
//
//	tag      1  byte   (TagAnchor / TagDelta / TagMissing / TagReset)
//	channel  uvarint   (uint32 channel id)
//	seq      uvarint   (per-channel frame sequence number)
//	plen     uvarint   (payload length in bytes)
//	payload  plen bytes (zig-zag uvarint for anchor/delta, empty otherwise)
//	crc32    4  bytes   (little-endian IEEE CRC over tag..payload)
//
// Sequence numbers are per channel and monotonic across its frames; an anchor
// restarts them at zero. They let the receiver notice a frame that never
// arrived (as opposed to one that arrived malformed): a missing delta makes
// every later delta undeterminable until the next anchor.
//
// A delta can only be decoded against a known baseline. When the baseline is
// unavailable (channel never anchored, reset, or an earlier delta was lost)
// the decoder reports StatusUnknown instead of guessing a value. The channel
// stays undeterminable until the next anchor for that channel; other channels
// are unaffected.
package deltacodec

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
)

// Frame tags.
const (
	TagAnchor  byte = 0x01
	TagDelta   byte = 0x02
	TagMissing byte = 0x03
	TagReset   byte = 0x04
)

// DefaultMaxPayload bounds how large a single frame payload may be before the
// decoder rejects the stream.
const DefaultMaxPayload = 64

// Status describes how a decoded sample should be interpreted.
type Status uint8

const (
	// StatusUnknown means the value cannot be derived from the data received
	// so far (no baseline, or an earlier delta was lost). Value is zero and
	// must never be treated as a measurement.
	StatusUnknown Status = iota
	// StatusOK means Value is the exact reconstructed measurement.
	StatusOK
	// StatusMissing means the sender explicitly marked this sample missing.
	StatusMissing
)

// Sample is one decoded frame. Value is meaningful only for StatusOK.
type Sample struct {
	Channel uint32
	Status  Status
	Value   int64
}

// Sentinel errors returned while parsing.
var (
	// ErrNeedMoreData means the buffered bytes do not yet contain a complete
	// frame; feeding more data and retrying is valid. No state is consumed.
	ErrNeedMoreData = errors.New("deltacodec: incomplete frame, need more data")
	// ErrCorrupt means structurally or semantically invalid data was found
	// (bad varint, oversized payload, CRC mismatch, unknown tag, impossible
	// delta). The decoder refuses to continue; start a new decoder.
	ErrCorrupt = errors.New("deltacodec: corrupt stream")
)

var (
	errNeedMore = errors.New("need more")
	errCorrupt  = errors.New("corrupt")
)

// Encoder emits frames for independent channels.
type Encoder struct {
	w      io.Writer
	period int // number of deltas per channel after which an anchor is forced
	states map[uint32]encState
}

type encState struct {
	last       int64
	have       bool
	deltaCount int    // deltas emitted since the last anchor
	seq        uint64 // next per-channel frame sequence number
}

// NewEncoder writes frames to w. A zero anchor period disables periodic
// anchors (anchors are still emitted when a channel starts, is reset, or the
// difference overflows int64).
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w, states: make(map[uint32]encState)}
}

// SetAnchorPeriod forces an anchor for a channel after every p deltas. A
// value <= 0 disables periodic anchoring.
func (e *Encoder) SetAnchorPeriod(p int) {
	if p < 0 {
		p = 0
	}
	e.period = p
}

// Encode writes v for channel ch, choosing anchor or delta automatically.
func (e *Encoder) Encode(ch uint32, v int64) error {
	st := e.states[ch]
	anchor := !st.have
	if st.have {
		d := v - st.last
		if subtractionOverflows(v, st.last, d) {
			anchor = true
		} else if e.period > 0 && st.deltaCount >= e.period {
			anchor = true
		}
		if !anchor {
			seq := st.seq
			if err := e.writeFrame(TagDelta, ch, seq, encodeZigZag(d)); err != nil {
				return err
			}
			st.last = v
			st.deltaCount++
			st.seq++
			e.states[ch] = st
			return nil
		}
	}
	if err := e.writeFrame(TagAnchor, ch, 0, encodeZigZag(v)); err != nil {
		return err
	}
	st.have = true
	st.last = v
	st.deltaCount = 0
	st.seq = 1
	e.states[ch] = st
	return nil
}

// EncodeAnchor forces an absolute anchor frame regardless of state.
func (e *Encoder) EncodeAnchor(ch uint32, v int64) error {
	if err := e.writeFrame(TagAnchor, ch, 0, encodeZigZag(v)); err != nil {
		return err
	}
	st := e.states[ch]
	st.have = true
	st.last = v
	st.deltaCount = 0
	st.seq = 1
	e.states[ch] = st
	return nil
}

// EncodeMissing explicitly marks the next sample of ch as missing. The
// channel baseline is retained, so later deltas remain decodable.
func (e *Encoder) EncodeMissing(ch uint32) error {
	st := e.states[ch]
	seq := st.seq
	if err := e.writeFrame(TagMissing, ch, seq, nil); err != nil {
		return err
	}
	if st.have {
		st.seq++
		e.states[ch] = st
	}
	return nil
}

// Reset drops the baseline of ch; later measurements start from an anchor.
func (e *Encoder) Reset(ch uint32) error {
	if err := e.writeFrame(TagReset, ch, 0, nil); err != nil {
		return err
	}
	delete(e.states, ch)
	return nil
}

func (e *Encoder) writeFrame(tag byte, ch uint32, seq uint64, payload []byte) error {
	frame := make([]byte, 0, 1+2*binary.MaxVarintLen64+len(payload)+4)
	frame = append(frame, tag)
	frame = appendUvarint(frame, uint64(ch))
	frame = appendUvarint(frame, seq)
	frame = appendUvarint(frame, uint64(len(payload)))
	frame = append(frame, payload...)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(frame))
	frame = append(frame, crc[:]...)
	_, err := e.w.Write(frame)
	return err
}

// Decoder parses frames from arbitrarily chunked byte input. Feed appends raw
// bytes; Decode consumes one frame at a time.
type Decoder struct {
	buf        []byte
	states     map[uint32]decState
	dead       bool
	maxPayload int
}

type decState struct {
	last    int64
	have    bool
	nextSeq uint64 // expected seq of the next frame for this channel
	synced  bool   // seq tracking initialized (false until first anchor)
}

// NewDecoder returns a decoder with the default payload quota.
func NewDecoder() *Decoder {
	return &Decoder{states: make(map[uint32]decState), maxPayload: DefaultMaxPayload}
}

// SetMaxPayload overrides the accepted frame payload size. The quota is
// checked before any payload-sized allocation happens.
func (d *Decoder) SetMaxPayload(n int) {
	if n < 0 {
		n = 0
	}
	d.maxPayload = n
}

// Feed appends raw bytes. Data may be split into chunks of any size; partial
// frames are retained until enough bytes arrive.
func (d *Decoder) Feed(p []byte) {
	if len(p) == 0 {
		return
	}
	d.buf = append(d.buf, p...)
}

// Decode returns the next sample. ErrNeedMoreData indicates a short buffer;
// ErrCorrupt indicates bad data and permanently invalidates the decoder.
func (d *Decoder) Decode() (Sample, error) {
	if d.dead {
		return Sample{}, ErrCorrupt
	}
	if len(d.buf) == 0 {
		return Sample{}, ErrNeedMoreData
	}

	tag := d.buf[0]
	switch tag {
	case TagAnchor, TagDelta, TagMissing, TagReset:
	default:
		d.dead = true
		return Sample{}, ErrCorrupt
	}

	p := d.buf[1:]
	ch, n1, err := readUvarint(p)
	if err != nil {
		return d.failOrWait(err)
	}
	if ch > math.MaxUint32 {
		d.dead = true
		return Sample{}, ErrCorrupt
	}
	p = p[n1:]

	seq, n2, err := readUvarint(p)
	if err != nil {
		return d.failOrWait(err)
	}
	p = p[n2:]

	plen64, n3, err := readUvarint(p)
	if err != nil {
		return d.failOrWait(err)
	}
	// Check the length quota before allocating/copying the payload.
	if plen64 > uint64(d.maxPayload) {
		d.dead = true
		return Sample{}, ErrCorrupt
	}
	plen := int(plen64)

	headerLen := 1 + n1 + n2 + n3
	frameLen := headerLen + plen + 4
	// The quota is enforced from the claimed length, before the full body
	// arrives and before any payload-sized allocation.
	if len(d.buf) < frameLen {
		// Header parsed fine, but the full frame has not arrived yet.
		return Sample{}, ErrNeedMoreData
	}

	frame := d.buf[:frameLen]
	storedCRC := binary.LittleEndian.Uint32(frame[frameLen-4:])
	if crc32.ChecksumIEEE(frame[:frameLen-4]) != storedCRC {
		d.dead = true
		return Sample{}, ErrCorrupt
	}

	payload := p[n3 : n3+plen]
	d.buf = d.buf[frameLen:]

	switch tag {
	case TagAnchor:
		v, ok := decodeExactValue(payload)
		if !ok {
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		if seq != 0 {
			// Anchors always restart the per-channel sequence at zero.
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		// An anchor is authoritative: it re-establishes the baseline and
		// heals any earlier loss on this channel.
		d.states[uint32(ch)] = decState{last: v, have: true, nextSeq: 1, synced: true}
		return Sample{Channel: uint32(ch), Status: StatusOK, Value: v}, nil

	case TagDelta:
		delta, ok := decodeExactValue(payload)
		if !ok {
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		st := d.states[uint32(ch)]
		if st.synced && seq != st.nextSeq {
			// One or more frames were lost. Stay latched on the expected
			// sequence so every following delta is undeterminable until an
			// anchor re-establishes the baseline.
			d.states[uint32(ch)] = st
			return Sample{Channel: uint32(ch), Status: StatusUnknown}, nil
		}
		if st.synced {
			st.nextSeq = seq + 1
			d.states[uint32(ch)] = st
		}
		if !st.have {
			// No trustworthy baseline: the value is not derivable.
			return Sample{Channel: uint32(ch), Status: StatusUnknown}, nil
		}
		next := st.last + delta
		if additionOverflows(st.last, delta, next) {
			// A delta that cannot exist for int64 is bad data.
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		st.last = next
		d.states[uint32(ch)] = st
		return Sample{Channel: uint32(ch), Status: StatusOK, Value: next}, nil

	case TagMissing:
		if plen != 0 {
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		st := d.states[uint32(ch)]
		if st.synced && seq != st.nextSeq {
			// Loss is unhealed: until an anchor arrives no frame for this
			// channel can be trusted, including the missing marker itself.
			d.states[uint32(ch)] = st
			return Sample{Channel: uint32(ch), Status: StatusUnknown}, nil
		}
		if st.synced {
			st.nextSeq = seq + 1
			d.states[uint32(ch)] = st
		}
		return Sample{Channel: uint32(ch), Status: StatusMissing}, nil

	case TagReset:
		if plen != 0 || seq != 0 {
			d.dead = true
			return Sample{}, ErrCorrupt
		}
		delete(d.states, uint32(ch))
		return Sample{Channel: uint32(ch), Status: StatusUnknown}, nil
	}
	panic("unreachable")
}

func (d *Decoder) failOrWait(err error) (Sample, error) {
	if err == errNeedMore {
		return Sample{}, ErrNeedMoreData
	}
	d.dead = true
	return Sample{}, ErrCorrupt
}

// StreamDecoder wraps an io.Reader, refilling an internal buffer as needed.
type StreamDecoder struct {
	r   io.Reader
	dec *Decoder
	buf [4096]byte
	eof bool
}

// NewStreamDecoder decodes frames from r.
func NewStreamDecoder(r io.Reader) *StreamDecoder {
	return &StreamDecoder{r: r, dec: NewDecoder()}
}

// SetMaxPayload mirrors Decoder.SetMaxPayload.
func (s *StreamDecoder) SetMaxPayload(n int) { s.dec.SetMaxPayload(n) }

// Decode returns the next sample. At a clean end of stream it returns io.EOF;
// a stream ending in a partial frame returns io.ErrUnexpectedEOF.
func (s *StreamDecoder) Decode() (Sample, error) {
	for {
		smp, err := s.dec.Decode()
		if err == nil {
			return smp, nil
		}
		if err == ErrCorrupt {
			return Sample{}, err
		}
		if s.eof {
			if len(s.dec.buf) > 0 {
				return Sample{}, io.ErrUnexpectedEOF
			}
			return Sample{}, io.EOF
		}
		n, rerr := s.r.Read(s.buf[:])
		if n > 0 {
			s.dec.Feed(s.buf[:n])
		}
		if rerr != nil {
			if rerr != io.EOF {
				return Sample{}, rerr
			}
			s.eof = true
		}
	}
}

// appendUvarint appends a minimal base-128 varint.
func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// readUvarint strictly reads a minimal varint. It returns errNeedMore when the
// buffer ends in a continuation sequence and errCorrupt on over-long,
// non-minimal, or uint64-overflowing encodings.
func readUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 9 {
			if c > 0x01 {
				return 0, 0, errCorrupt
			}
			v |= uint64(c) << 63
			if c == 0 {
				return 0, 0, errCorrupt // trailing zero: non-minimal
			}
			return v, 10, nil
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c&0x80 == 0 {
			if i > 0 && c == 0 {
				return 0, 0, errCorrupt // trailing zero: non-minimal
			}
			return v, i + 1, nil
		}
		if i == 9 {
			return 0, 0, errCorrupt // 11th byte would be required
		}
	}
	return 0, 0, errNeedMore
}

func encodeZigZag(v int64) []byte {
	u := uint64(v)<<1 ^ uint64(v>>63)
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], u)
	return b[:n]
}

func decodeExactValue(payload []byte) (int64, bool) {
	u, n, err := readUvarint(payload)
	if err != nil || n != len(payload) {
		return 0, false
	}
	return int64(u>>1) ^ -int64(u&1), true
}

// subtractionOverflows reports whether a-b overflows int64, given d := a-b.
func subtractionOverflows(a, b, d int64) bool {
	return (a >= 0 && b < 0 && d < 0) || (a < 0 && b >= 0 && d >= 0)
}

// additionOverflows reports whether a+b overflows int64, given s := a+b.
func additionOverflows(a, b, s int64) bool {
	return (a >= 0 && b >= 0 && s < 0) || (a < 0 && b < 0 && s >= 0)
}
