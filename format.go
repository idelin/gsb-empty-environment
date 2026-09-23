// Package runlzv2 implements a small run-length block compressor with
// explicit flush boundaries, an offline optimal mode, and an online
// bounded-buffer mode. It uses only the Go standard library.
package runlzv2

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

const (
	// Version is the only supported wire format version.
	Version = 1

	// FrameHeaderLen is the fixed frame overhead:
	// version(1) + flags(1) + payload length(4) + CRC-32/IEEE(4).
	FrameHeaderLen = 10

	// BlockHeaderLen is the fixed per-block overhead:
	// tag(1) + lengthMinusOne(2) + value(1).
	BlockHeaderLen = 4

	// MaxBlockLen limits both literal and repeat block lengths.
	MaxBlockLen = 65535

	// MaxPayloadLen is the uint32 frame payload length limit.
	MaxPayloadLen = 1<<32 - 1

	// DefaultOnlineBuffer is the default bounded input buffer for Encoder.
	DefaultOnlineBuffer = 4096

	tagLiteral = 0x00
	tagRepeat  = 0x01
)

const (
	// FlagFlush marks an explicit flush boundary. It does not end the stream.
	FlagFlush byte = 0x01

	// FlagFinal marks the last frame emitted by Close.
	FlagFinal byte = 0x02
)

var (
	// ErrFormat is returned for unsupported or malformed framing.
	ErrFormat = errors.New("runlzv2: invalid compressed format")

	// ErrChecksum is returned when a frame CRC-32 check fails.
	ErrChecksum = errors.New("runlzv2: checksum mismatch")

	// ErrOutputLimit is returned when decoding exceeds its output quota.
	ErrOutputLimit = errors.New("runlzv2: output limit exceeded")

	// ErrInvalidArgument is returned for invalid parameters.
	ErrInvalidArgument = errors.New("runlzv2: invalid argument")

	// ErrClosed is returned when writing to a closed Encoder.
	ErrClosed = errors.New("runlzv2: encoder is closed")
)

type blockKind byte

const (
	kindLiteral blockKind = tagLiteral
	kindRepeat  blockKind = tagRepeat
)

type segment struct {
	start int
	end   int
	kind  blockKind
}

func validFlags(flags byte, payloadLen int) bool {
	if flags != 0 && flags != FlagFlush && flags != FlagFinal {
		return false
	}
	// An empty non-final frame is meaningless and rejected.
	if payloadLen == 0 && flags == 0 {
		return false
	}
	return true
}

func encodeFrame(payload []byte, flags byte) ([]byte, error) {
	if !validFlags(flags, len(payload)) {
		return nil, ErrFormat
	}
	if int64(len(payload)) > MaxPayloadLen {
		return nil, ErrFormat
	}

	frame := make([]byte, FrameHeaderLen+len(payload))
	frame[0] = Version
	frame[1] = flags
	binary.BigEndian.PutUint32(frame[2:6], uint32(len(payload)))
	binary.BigEndian.PutUint32(frame[6:10], crc32.ChecksumIEEE(payload))
	copy(frame[FrameHeaderLen:], payload)
	return frame, nil
}

func encodeBlocks(src []byte, segments []segment) []byte {
	pay := make([]byte, 0, len(src)+BlockHeaderLen*len(segments))
	var header [BlockHeaderLen]byte

	for _, seg := range segments {
		length := seg.end - seg.start
		binary.BigEndian.PutUint16(header[1:3], uint16(length-1))

		if seg.kind == kindLiteral {
			header[0] = tagLiteral
			header[3] = 0
			pay = append(pay, header[:]...)
			pay = append(pay, src[seg.start:seg.end]...)
			continue
		}

		header[0] = tagRepeat
		header[3] = src[seg.start]
		pay = append(pay, header[:]...)
	}

	return pay
}
