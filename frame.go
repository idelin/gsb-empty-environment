package bridge

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrNeedMoreData describes the incomplete-input condition. FrameDecoder.Feed
// signals it implicitly (nil error, no frame); it is exported so callers can
// name the condition when wrapping the decoder.
var ErrNeedMoreData = errors.New("bridge: need more data")

// EncodeFrame wraps a payload with a 4-byte big-endian length prefix.
func EncodeFrame(payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
	copy(out[4:], payload)
	return out
}

func framePayload(frame []byte) []byte { return frame[4:] }

// FrameDecoder reassembles length-prefixed frames from arbitrarily chunked
// byte input. It distinguishes "need more data" (nil error, no frame) from
// "bad data" (ErrBadData), and checks the length quota before allocating.
type FrameDecoder struct {
	// MaxFrame is the length quota: any declared frame length above it is
	// rejected as bad data before a single byte of the frame is buffered.
	MaxFrame uint32
	// OnFrameStart fires once per frame, right after its length prefix is
	// accepted and before any of its body is consumed.
	OnFrameStart func()

	buf      []byte
	frameLen int // -1 while waiting for a length prefix
}

func NewFrameDecoder(maxFrame uint32) *FrameDecoder {
	return &FrameDecoder{MaxFrame: maxFrame, frameLen: -1}
}

// Feed consumes one arbitrary chunk and returns every frame completed by
// it. Incomplete input is not an error; malformed input returns ErrBadData.
func (d *FrameDecoder) Feed(p []byte) ([][]byte, error) {
	d.buf = append(d.buf, p...)
	var out [][]byte
	for {
		if d.frameLen < 0 {
			if len(d.buf) < 4 {
				return out, nil // need more data
			}
			l := binary.BigEndian.Uint32(d.buf[:4])
			if l > d.MaxFrame {
				// Reject before allocating anything for the frame body and
				// drop all buffered state: the stream is unusable.
				d.buf = nil
				return nil, fmt.Errorf("%w: frame length %d exceeds quota %d", ErrBadData, l, d.MaxFrame)
			}
			if d.OnFrameStart != nil {
				d.OnFrameStart()
			}
			d.frameLen = int(l)
			d.buf = d.buf[4:]
		}
		if len(d.buf) < d.frameLen {
			return out, nil // need more data
		}
		frame := make([]byte, d.frameLen)
		copy(frame, d.buf[:d.frameLen])
		d.buf = d.buf[d.frameLen:]
		d.frameLen = -1
		out = append(out, frame)
	}
}
