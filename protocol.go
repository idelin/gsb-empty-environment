package fragmentreassembly

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	Magic uint32 = 0x46524147

	HeaderSize = 32
)

var (
	ErrNeedMoreData = errors.New("need more data")
	ErrBadData      = errors.New("bad data")
	ErrConflict     = errors.New("fragment conflict")
)

// Header is the fixed binary header that precedes every fragment payload.
type Header struct {
	MessageID uint64
	TotalSize uint32
	Offset    uint32
	Length    uint32
	Checksum  uint32
}

// Fragment is one piece of a reassembled message.
type Fragment struct {
	Header
	Payload []byte
}

// Marshal encodes one fragment. The checksum covers the 28-byte header prefix
// and the payload; the checksum field itself is not part of its own input.
func (f Fragment) Marshal() ([]byte, error) {
	if err := f.validateLengths(); err != nil {
		return nil, err
	}

	buf := make([]byte, HeaderSize+len(f.Payload))
	binary.BigEndian.PutUint32(buf[0:4], Magic)
	binary.BigEndian.PutUint32(buf[4:8], 1)
	binary.BigEndian.PutUint64(buf[8:16], f.MessageID)
	binary.BigEndian.PutUint32(buf[16:20], f.TotalSize)
	binary.BigEndian.PutUint32(buf[20:24], f.Offset)
	binary.BigEndian.PutUint32(buf[24:28], uint32(len(f.Payload)))
	binary.BigEndian.PutUint32(buf[28:32], checksum(buf[0:28], f.Payload))
	copy(buf[HeaderSize:], f.Payload)
	return buf, nil
}

// UnmarshalFrame decodes exactly one complete frame. A short input returns
// ErrNeedMoreData; structurally bad or corrupt input returns ErrBadData.
func UnmarshalFrame(data []byte) (Fragment, int, error) {
	if len(data) < HeaderSize {
		return Fragment{}, 0, ErrNeedMoreData
	}

	header, err := parseHeader(data[:HeaderSize])
	if err != nil {
		return Fragment{}, 0, err
	}

	frameLen := HeaderSize + int(header.Length)
	if len(data) < frameLen {
		return Fragment{}, 0, ErrNeedMoreData
	}

	payload := data[HeaderSize:frameLen]
	if checksum(data[0:28], payload) != header.Checksum {
		return Fragment{}, 0, fmt.Errorf("%w: checksum mismatch", ErrBadData)
	}

	frag := Fragment{Header: header, Payload: append([]byte(nil), payload...)}
	return frag, frameLen, nil
}

// FrameGate is called after a header is parsed and validated, but before the
// payload is allocated or read.
type FrameGate func(Header) error

// ReadFrame reads one frame from any io.Reader, so byte input may be split at
// arbitrary boundaries. If gate rejects the header, the payload is drained and
// the gate's error is returned without allocating payload storage.
func ReadFrame(r io.Reader, gate FrameGate) (Fragment, error) {
	rawHeader := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, rawHeader); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Fragment{}, fmt.Errorf("%w: %w", ErrNeedMoreData, err)
		}
		return Fragment{}, err
	}

	header, err := parseHeader(rawHeader)
	if err != nil {
		return Fragment{}, err
	}
	if gate != nil {
		if err := gate(header); err != nil {
			_, _ = io.CopyN(io.Discard, r, int64(header.Length))
			return Fragment{}, err
		}
	}

	payload := make([]byte, header.Length)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Fragment{}, fmt.Errorf("%w: %w", ErrNeedMoreData, err)
		}
		return Fragment{}, err
	}

	if checksum(rawHeader[0:28], payload) != header.Checksum {
		return Fragment{}, fmt.Errorf("%w: checksum mismatch", ErrBadData)
	}
	return Fragment{Header: header, Payload: payload}, nil
}

func (f Fragment) validateLengths() error {
	return validateRange(f.TotalSize, f.Offset, uint32(len(f.Payload)))
}

func parseHeader(data []byte) (Header, error) {
	if binary.BigEndian.Uint32(data[0:4]) != Magic {
		return Header{}, fmt.Errorf("%w: bad magic", ErrBadData)
	}
	if binary.BigEndian.Uint32(data[4:8]) != 1 {
		return Header{}, fmt.Errorf("%w: unsupported version", ErrBadData)
	}

	header := Header{
		MessageID: binary.BigEndian.Uint64(data[8:16]),
		TotalSize: binary.BigEndian.Uint32(data[16:20]),
		Offset:    binary.BigEndian.Uint32(data[20:24]),
		Length:    binary.BigEndian.Uint32(data[24:28]),
		Checksum:  binary.BigEndian.Uint32(data[28:32]),
	}
	if err := validateRange(header.TotalSize, header.Offset, header.Length); err != nil {
		return Header{}, err
	}
	return header, nil
}

func validateRange(total, offset, length uint32) error {
	if uint64(offset)+uint64(length) > uint64(total) {
		return fmt.Errorf("%w: fragment extends beyond total size", ErrBadData)
	}
	return nil
}

func checksum(prefix, payload []byte) uint32 {
	h := crc32.NewIEEE()
	_, _ = h.Write(prefix)
	_, _ = h.Write(payload)
	return h.Sum32()
}
