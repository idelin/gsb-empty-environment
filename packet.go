package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const maxPacketBody = 64 << 20

// packet is an OpenPGP packet with its tag and body.  The packet header is
// deliberately discarded; signatures are computed over packet bodies, not
// over the encoding of the header.
type packet struct {
	tag  uint8
	body []byte
}

func parsePackets(data []byte) ([]packet, error) {
	rd := bytes.NewReader(data)
	var packets []packet

	for {
		first, err := rd.ReadByte()
		if err == io.EOF {
			return packets, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: reading packet tag: %v", ErrInvalidPacket, err)
		}
		if first&0x80 == 0 {
			return nil, fmt.Errorf("%w: packet tag bit is not set", ErrInvalidPacket)
		}

		var tag uint8
		var body []byte
		if first&0x40 != 0 {
			tag = first & 0x3f
			body, err = readNewPacketBody(rd)
		} else {
			tag = (first >> 2) & 0x0f
			body, err = readOldPacketBody(rd, first&0x03)
		}
		if err != nil {
			return nil, err
		}
		if len(body) > maxPacketBody {
			return nil, fmt.Errorf("%w: packet body too large", ErrInvalidPacket)
		}
		packets = append(packets, packet{tag: tag, body: body})
	}
}

func readOldPacketBody(rd *bytes.Reader, lengthType byte) ([]byte, error) {
	var n uint32
	switch lengthType {
	case 0:
		b, err := rd.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
		}
		n = uint32(b)
	case 1:
		var buf [2]byte
		if _, err := io.ReadFull(rd, buf[:]); err != nil {
			return nil, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
		}
		n = uint32(binary.BigEndian.Uint16(buf[:]))
	case 2:
		var buf [4]byte
		if _, err := io.ReadFull(rd, buf[:]); err != nil {
			return nil, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
		}
		n = binary.BigEndian.Uint32(buf[:])
	case 3:
		// An indeterminate length consumes the rest of the input.
		if rd.Len() > maxPacketBody {
			return nil, fmt.Errorf("%w: packet body too large", ErrInvalidPacket)
		}
		n = uint32(rd.Len())
	}
	return readBody(rd, n)
}

func readNewPacketBody(rd *bytes.Reader) ([]byte, error) {
	var body []byte
	for {
		length, partial, err := readNewLength(rd)
		if err != nil {
			return nil, err
		}
		chunk, err := readBody(rd, length)
		if err != nil {
			return nil, err
		}
		if len(body)+len(chunk) > maxPacketBody {
			return nil, fmt.Errorf("%w: packet body too large", ErrInvalidPacket)
		}
		body = append(body, chunk...)
		if !partial {
			return body, nil
		}
	}
}

func readNewLength(rd *bytes.Reader) (n uint32, partial bool, err error) {
	first, err := rd.ReadByte()
	if err != nil {
		return 0, false, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
	}
	switch {
	case first < 192:
		return uint32(first), false, nil
	case first < 224:
		second, err := rd.ReadByte()
		if err != nil {
			return 0, false, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
		}
		return ((uint32(first)-192)<<8 + uint32(second) + 192), false, nil
	case first == 255:
		var buf [4]byte
		if _, err := io.ReadFull(rd, buf[:]); err != nil {
			return 0, false, fmt.Errorf("%w: truncated packet length: %v", ErrInvalidPacket, err)
		}
		return binary.BigEndian.Uint32(buf[:]), false, nil
	default:
		// 224..254 encode a partial body length.
		return 1 << (first & 0x1f), true, nil
	}
}

func readBody(rd *bytes.Reader, n uint32) ([]byte, error) {
	if n > maxPacketBody || uint64(n) > uint64(rd.Len()) {
		return nil, fmt.Errorf("%w: truncated packet body", ErrInvalidPacket)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(rd, body); err != nil {
		return nil, fmt.Errorf("%w: truncated packet body: %v", ErrInvalidPacket, err)
	}
	return body, nil
}
