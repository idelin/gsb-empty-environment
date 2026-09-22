// Package framemux implements a framing protocol that multiplexes several
// logical streams over a single byte channel.
//
// Wire format of a frame (before escaping):
//
//	MAGIC0 MAGIC1 | Type Stream Seq Epoch LenLo LenHi | Payload... | CRC32(4, LE)
//
// The CRC32 (IEEE) covers header and payload. MAGIC, and the escape byte of
// the currently active escape epoch, never appear unescaped inside a frame,
// so a raw MAGIC pair always marks a frame boundary.
package framemux

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

const (
	// Magic0 and Magic1 form the frame delimiter.
	Magic0 byte = 0x7E
	Magic1 byte = 0x5A

	headerLen = 6 // Type Stream Seq Epoch LenLo LenHi
	crcLen    = 4
)

var (
	// ErrNeedMoreData reports a syntactically valid but incomplete frame.
	ErrNeedMoreData = errors.New("framemux: need more data")
	// ErrBadData reports a structurally invalid frame (bad header, bad CRC,
	// unexpected epoch). The parser resynchronizes after it.
	ErrBadData = errors.New("framemux: bad data")
	// ErrNoDelimiter reports that no frame delimiter was found within the
	// configured scan budget (bounded resynchronization).
	ErrNoDelimiter = errors.New("framemux: no delimiter within scan budget")
	// ErrBackpressure reports that per-stream or global buffer quotas are
	// exhausted; the caller should drain with Recv and retry.
	ErrBackpressure = errors.New("framemux: backpressure")
	// ErrTooLarge reports a payload larger than the configured maximum.
	ErrTooLarge = errors.New("framemux: payload too large")
)

// FrameType identifies the kind of a frame.
type FrameType uint8

const (
	TypeData    FrameType = iota // application payload
	TypeAck                      // cumulative acknowledgement (Stream, Seq)
	TypePropose                  // request escape-epoch switch, payload = [epoch]
	TypeConfirm                  // confirm escape-epoch switch, payload = [epoch]
)

// Frame is one decoded frame.
type Frame struct {
	Type    FrameType
	Stream  uint8
	Seq     uint8
	Epoch   uint8
	Payload []byte
}

// EscapeConfig is the byte-stuffing rule set of one encoding epoch.
type EscapeConfig struct {
	Esc byte // escape introducer
	Xor byte // mask applied to the escaped byte
}

// escapeTable holds the per-epoch escape rules. Every entry must keep the
// escaped alphabet disjoint from {Magic0, Magic1, Esc}; this is verified by
// TestEscapeConfigSafety.
var escapeTable = []EscapeConfig{
	{Esc: 0x7D, Xor: 0x20},
	{Esc: 0x7B, Xor: 0x10},
	{Esc: 0x7C, Xor: 0x08},
	{Esc: 0x7F, Xor: 0x40},
}

func configForEpoch(e uint8) EscapeConfig { return escapeTable[int(e)%len(escapeTable)] }

func (c EscapeConfig) needsEscape(b byte) bool {
	return b == Magic0 || b == Magic1 || b == c.Esc
}

func (c EscapeConfig) appendEscaped(dst []byte, b byte) []byte {
	if c.needsEscape(b) {
		return append(dst, c.Esc, b^c.Xor)
	}
	return append(dst, b)
}

// AppendFrame encodes f using the escape rules of f.Epoch and appends the
// result (including the leading delimiter) to dst.
func AppendFrame(dst []byte, f Frame) []byte {
	cfg := configForEpoch(f.Epoch)
	var hdr [headerLen]byte
	hdr[0] = byte(f.Type)
	hdr[1] = f.Stream
	hdr[2] = f.Seq
	hdr[3] = f.Epoch
	binary.LittleEndian.PutUint16(hdr[4:], uint16(len(f.Payload)))
	h := crc32.NewIEEE()
	h.Write(hdr[:])
	h.Write(f.Payload)
	var crcb [crcLen]byte
	binary.LittleEndian.PutUint32(crcb[:], h.Sum32())

	dst = append(dst, Magic0, Magic1)
	for _, b := range hdr {
		dst = cfg.appendEscaped(dst, b)
	}
	for _, b := range f.Payload {
		dst = cfg.appendEscaped(dst, b)
	}
	for _, b := range crcb {
		dst = cfg.appendEscaped(dst, b)
	}
	return dst
}

// DecodeFrame decodes exactly one frame (without leading delimiter) from the
// front of dat using the escape rules of epoch. It returns the frame and the
// number of raw bytes consumed. ErrNeedMoreData means dat is a clean prefix
// of a frame; ErrBadData means the bytes are corrupt.
func DecodeFrame(dat []byte, epoch uint8) (Frame, int, error) {
	cfg := configForEpoch(epoch)
	i := 0
	esc := false
	next := func() (byte, error) {
		for {
			if i >= len(dat) {
				return 0, ErrNeedMoreData
			}
			b := dat[i]
			i++
			if esc {
				esc = false
				return b ^ cfg.Xor, nil
			}
			if b == cfg.Esc {
				esc = true
				continue
			}
			return b, nil
		}
	}
	var hdr [headerLen]byte
	for k := range hdr {
		b, err := next()
		if err != nil {
			return Frame{}, 0, err
		}
		hdr[k] = b
	}
	plen := int(hdr[4]) | int(hdr[5])<<8
	payload := make([]byte, plen)
	for k := range payload {
		b, err := next()
		if err != nil {
			return Frame{}, 0, err
		}
		payload[k] = b
	}
	var crcb [crcLen]byte
	for k := range crcb {
		b, err := next()
		if err != nil {
			return Frame{}, 0, err
		}
		crcb[k] = b
	}
	h := crc32.NewIEEE()
	h.Write(hdr[:])
	h.Write(payload)
	if binary.LittleEndian.Uint32(crcb[:]) != h.Sum32() {
		return Frame{}, 0, fmt.Errorf("%w: crc mismatch", ErrBadData)
	}
	return Frame{
		Type:    FrameType(hdr[0]),
		Stream:  hdr[1],
		Seq:     hdr[2],
		Epoch:   hdr[3],
		Payload: payload,
	}, i, nil
}
