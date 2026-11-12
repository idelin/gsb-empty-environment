package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// WebSocket opcodes (RFC 6455).
const (
	opcodeContinuation = 0x0
	opcodeText         = 0x1
	opcodeBinary       = 0x2
	opcodeClose        = 0x8
	opcodePing         = 0x9
	opcodePong         = 0xa
)

// maxFramePayload bounds a single frame so a hostile length prefix cannot
// force unbounded buffering.
const maxFramePayload = 1 << 20

var errIncompleteFrame = errors.New("netsec: incomplete frame")

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func validOpcode(op byte) bool {
	switch op {
	case opcodeContinuation, opcodeText, opcodeBinary, opcodeClose, opcodePing, opcodePong:
		return true
	}
	return false
}

func isControlOpcode(op byte) bool { return op >= 0x8 }

// frameParser incrementally decodes a byte stream into frames. Bytes of a
// frame may arrive split across any number of writes (half-written frames
// are buffered until the rest shows up).
type frameParser struct {
	buf []byte
}

func (p *frameParser) feed(data []byte) ([]frame, error) {
	p.buf = append(p.buf, data...)
	var frames []frame
	for {
		f, n, err := parseFrame(p.buf)
		if err == errIncompleteFrame {
			break
		}
		if err != nil {
			return frames, err
		}
		frames = append(frames, f)
		p.buf = p.buf[n:]
	}
	return frames, nil
}

func parseFrame(buf []byte) (frame, int, error) {
	var f frame
	if len(buf) < 2 {
		return f, 0, errIncompleteFrame
	}
	f.fin = buf[0]&0x80 != 0
	f.opcode = buf[0] & 0x0f
	if !validOpcode(f.opcode) {
		return f, 0, fmt.Errorf("netsec: unknown opcode 0x%x", f.opcode)
	}
	masked := buf[1]&0x80 != 0
	length := uint64(buf[1] & 0x7f)
	off := 2
	switch length {
	case 126:
		if len(buf) < off+2 {
			return f, 0, errIncompleteFrame
		}
		length = uint64(binary.BigEndian.Uint16(buf[off:]))
		off += 2
	case 127:
		if len(buf) < off+8 {
			return f, 0, errIncompleteFrame
		}
		length = binary.BigEndian.Uint64(buf[off:])
		off += 8
	}
	if isControlOpcode(f.opcode) {
		if !f.fin {
			return f, 0, errors.New("netsec: fragmented control frame")
		}
		if length > 125 {
			return f, 0, errors.New("netsec: control frame payload too large")
		}
	}
	var maskKey [4]byte
	if masked {
		if len(buf) < off+4 {
			return f, 0, errIncompleteFrame
		}
		copy(maskKey[:], buf[off:])
		off += 4
	}
	if length > maxFramePayload {
		return f, 0, fmt.Errorf("netsec: frame payload %d bytes exceeds limit", length)
	}
	if uint64(len(buf)-off) < length {
		return f, 0, errIncompleteFrame
	}
	payload := make([]byte, length)
	copy(payload, buf[off:off+int(length)])
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	f.payload = payload
	return f, off + int(length), nil
}

// marshalFrame serializes an unmasked, FIN-set frame.
func marshalFrame(opcode byte, payload []byte) []byte {
	out := []byte{0x80 | opcode}
	switch n := len(payload); {
	case n < 126:
		out = append(out, byte(n))
	case n <= 0xffff:
		out = append(out, 126, byte(n>>8), byte(n))
	default:
		out = append(out, 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		out = append(out, b[:]...)
	}
	return append(out, payload...)
}
