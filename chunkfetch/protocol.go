package chunkfetch

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

// Wire frame:
//
//	[4 bytes big-endian payload length][32 bytes payload SHA-256][payload]
const (
	headerLen   = 4
	checksumLen = sha256.Size
)

// ErrNeedMore means the buffered bytes are a valid prefix of a frame but the
// frame is not fully present yet ("missing data").
var ErrNeedMore = errors.New("chunkfetch: incomplete frame: need more bytes")

// ErrBadFrame means the bytes already received cannot start/complete any valid
// frame ("bad data"): more bytes will never make it valid.
var ErrBadFrame = errors.New("chunkfetch: malformed frame")

// ReadFrame reads one frame from r.
//
// maxPayload caps how much memory may be allocated for the payload; 0 means the
// declared length is the quota. If the declared length exceeds the quota,
// ErrBadFrame is returned without allocating the payload: the check happens
// before ReadFull.
//
// A clean EOF is returned as io.EOF. Any other cut connection yields
// ErrNeedMore, because retrying later can complete the frame.
func ReadFrame(r io.Reader, maxPayload uint32) ([]byte, error) {
	var head [headerLen]byte
	n, err := io.ReadFull(r, head[:])
	if errors.Is(err, io.EOF) && n == 0 {
		return nil, io.EOF
	}
	if err != nil {
		return nil, ErrNeedMore
	}

	declared := binary.BigEndian.Uint32(head[:])
	if maxPayload > 0 && declared > maxPayload {
		return nil, ErrBadFrame
	}

	buf := make([]byte, checksumLen+int(declared))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, ErrNeedMore
	}
	return decodeFrame(declared, buf)
}

// DecodeFrame is the pure, non-streaming counterpart of ReadFrame. It lets the
// caller feed bytes however they arrived (arbitrary chunking) and decide what
// to do: buffer more on ErrNeedMore, reject on ErrBadFrame.
func DecodeFrame(b []byte) (payload []byte, consumed int, err error) {
	if len(b) < headerLen {
		return nil, 0, ErrNeedMore
	}
	declared := binary.BigEndian.Uint32(b[:headerLen])
	total := headerLen + checksumLen + int(declared)
	if len(b) < total {
		// A length prefix was parsed, so "extra" bytes exist relative to the
		// bare header. Distinguish a truncated stream (need more) from
		// trailing garbage: valid frames never carry bytes past the end.
		if len(b) > total {
			return nil, 0, ErrBadFrame
		}
		return nil, 0, ErrNeedMore
	}
	if len(b) > total {
		return nil, 0, ErrBadFrame
	}
	p, err := decodeFrame(declared, b[headerLen:total])
	if err != nil {
		return nil, 0, err
	}
	return p, total, nil
}

func decodeFrame(declared uint32, frame []byte) ([]byte, error) {
	if len(frame) != checksumLen+int(declared) {
		return nil, ErrNeedMore
	}
	want := frame[:checksumLen]
	payload := frame[checksumLen:]
	sum := sha256.Sum256(payload)
	for i := range sum {
		if sum[i] != want[i] {
			return nil, ErrBadFrame
		}
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

// EncodeFrame builds a frame for payload. It is used by sources (and tests).
func EncodeFrame(payload []byte) []byte {
	sum := sha256.Sum256(payload)
	out := make([]byte, headerLen+checksumLen+len(payload))
	binary.BigEndian.PutUint32(out[:headerLen], uint32(len(payload)))
	copy(out[headerLen:headerLen+checksumLen], sum[:])
	copy(out[headerLen+checksumLen:], payload)
	return out
}
