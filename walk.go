package taggedunion

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Envelope is one item seen by the independent structural parser.
// Payload and Raw are sub-slices of the input buffer.
type Envelope struct {
	Tag     uint16
	Version uint16
	Offset  int // absolute offset of the item in the batch buffer
	Payload []byte
	Raw     []byte
}

// Walk is an independent parser: it knows only the envelope framing
// (tag, version, length, crc) and nothing about field schemas. It is used
// to verify opaque passthrough without sharing code with the schema-aware
// decoder.
func Walk(buf []byte) ([]Envelope, error) {
	if len(buf) < 4 {
		return nil, fmt.Errorf("walk: truncated batch header")
	}
	n := binary.BigEndian.Uint32(buf)
	if n > MaxItems {
		return nil, fmt.Errorf("walk: item count %d exceeds limit", n)
	}
	var out []Envelope
	off := 4
	for i := uint32(0); i < n; i++ {
		if len(buf)-off < headerSize+crcSize {
			return nil, fmt.Errorf("walk: item %d truncated at offset %d", i, off)
		}
		tag := binary.BigEndian.Uint16(buf[off:])
		version := binary.BigEndian.Uint16(buf[off+2:])
		payloadLen := binary.BigEndian.Uint32(buf[off+4:])
		if payloadLen > MaxPayload {
			return nil, fmt.Errorf("walk: item %d payload length %d exceeds limit", i, payloadLen)
		}
		total := headerSize + int(payloadLen) + crcSize
		if len(buf)-off < total {
			return nil, fmt.Errorf("walk: item %d payload length %d exceeds remaining input", i, payloadLen)
		}
		raw := buf[off : off+total]
		want := binary.BigEndian.Uint32(raw[len(raw)-crcSize:])
		if crc32.ChecksumIEEE(raw[:len(raw)-crcSize]) != want {
			return nil, fmt.Errorf("walk: item %d checksum mismatch", i)
		}
		out = append(out, Envelope{
			Tag:     tag,
			Version: version,
			Offset:  off,
			Payload: raw[headerSize : len(raw)-crcSize],
			Raw:     raw,
		})
		off += total
	}
	if off != len(buf) {
		return nil, fmt.Errorf("walk: %d trailing bytes", len(buf)-off)
	}
	return out, nil
}
