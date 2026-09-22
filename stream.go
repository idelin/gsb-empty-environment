package safeframe

import "errors"

var ErrNeedMoreData = errors.New("safeframe: more data required")

// Decoder accepts arbitrarily fragmented byte streams and emits complete
// frames. It only retains bytes that belong to one incomplete frame.
type Decoder struct {
	pending    []byte
	maxPayload int
	poisoned   bool
	lastErr    error
}

func NewDecoder(maxPayload int) *Decoder {
	return &Decoder{maxPayload: normalizePayloadLimit(maxPayload)}
}

func (d *Decoder) Reset() {
	for i := range d.pending {
		d.pending[i] = 0
	}
	d.pending = nil
	d.poisoned = false
	d.lastErr = nil
}

func (d *Decoder) Feed(chunk []byte) ([]*Frame, error) {
	if d.poisoned {
		return nil, d.lastErr
	}

	var frames []*Frame
	data := chunk

	for {
		if len(d.pending) > 0 {
			if len(d.pending) < HeaderLength {
				need := HeaderLength - len(d.pending)
				if len(data) == 0 {
					return frames, ErrTruncated
				}
				take := len(data)
				if take > need {
					take = need
				}

				joined := make([]byte, len(d.pending)+take)
				copy(joined, d.pending)
				copy(joined[len(d.pending):], data[:take])
				for i := range d.pending {
					d.pending[i] = 0
				}
				d.pending = joined
				data = data[take:]

				if len(d.pending) < HeaderLength {
					return frames, ErrTruncated
				}
				if _, err := inspectHeader(d.pending, d.maxPayload); err != nil {
					d.poisoned = true
					d.lastErr = err
					return frames, err
				}
				continue
			}

			declared, err := inspectHeader(d.pending, d.maxPayload)
			if err != nil {
				d.poisoned = true
				d.lastErr = err
				return frames, err
			}
			frameLen := HeaderLength + declared
			need := frameLen - len(d.pending)
			if len(data) < need {
				grown := make([]byte, len(d.pending)+len(data))
				copy(grown, d.pending)
				copy(grown[len(d.pending):], data)
				for i := range d.pending {
					d.pending[i] = 0
				}
				d.pending = grown
				data = nil
				return frames, ErrTruncated
			}

			complete := make([]byte, frameLen)
			copy(complete, d.pending)
			copy(complete[len(d.pending):], data[:need])
			for i := range d.pending {
				d.pending[i] = 0
			}
			d.pending = nil
			data = data[need:]

			frame, err := DecodeFrame(complete)
			if err != nil {
				d.poisoned = true
				d.lastErr = err
				return frames, err
			}
			frames = append(frames, frame)
			continue
		}

		if len(data) == 0 {
			return frames, ErrNeedMoreData
		}
		if len(data) < HeaderLength {
			d.pending = cloneBytes(data)
			return frames, ErrTruncated
		}

		declared, err := inspectHeader(data, d.maxPayload)
		if err != nil {
			d.poisoned = true
			d.lastErr = err
			return frames, err
		}
		frameLen := HeaderLength + declared
		if len(data) < frameLen {
			d.pending = cloneBytes(data)
			return frames, ErrTruncated
		}

		frame, err := DecodeFrame(data[:frameLen])
		if err != nil {
			d.poisoned = true
			d.lastErr = err
			return frames, err
		}
		frames = append(frames, frame)
		data = data[frameLen:]
	}
}

func inspectHeader(header []byte, maxPayload int) (int, error) {
	if u32(header[0:4]) != Magic {
		return 0, ErrBadMagic
	}
	if header[4] != Version1 {
		return 0, ErrBadVersion
	}
	if header[6] != 0 || header[7] != 0 {
		return 0, ErrBadReserved
	}

	declared := int(u32(header[8:12]))
	if declared > normalizePayloadLimit(maxPayload) {
		return 0, ErrPayloadLimit
	}
	return declared, nil
}

func normalizePayloadLimit(limit int) int {
	if limit <= 0 || limit > MaxPayloadSize {
		return MaxPayloadSize
	}
	return limit
}

func cloneBytes(value []byte) []byte {
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

func u32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
