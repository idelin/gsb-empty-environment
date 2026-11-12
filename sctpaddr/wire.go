package sctpaddr

import (
	"bytes"
	"encoding/binary"
)

// Chunk type values.
const (
	ChunkINIT         uint8 = 1
	ChunkHeartbeat    uint8 = 4
	ChunkHeartbeatACK uint8 = 5
	ChunkAUTH         uint8 = 15
	ChunkASCONF       uint8 = 193 // 0xC1
	ChunkASCONFACK    uint8 = 128 // 0x80
)

// TLV parameter type values.
const (
	ParamHeartbeatInfo    uint16 = 0x0001
	ParamIPv4Addr         uint16 = 0x0005
	ParamIPv6Addr         uint16 = 0x0006
	ParamSuccessInd       uint16 = 0xC001
	ParamErrorCauseInd    uint16 = 0xC007
	ParamASCONFAddIP      uint16 = 0xC008
	ParamASCONFDeleteIP   uint16 = 0xC009
	ParamASCONFSetPrimary uint16 = 0xC00E
)

// ASCONF error-cause values (RFC 4960 3.3.10, RFC 6525 section 5).
const (
	CauseUnrecognizedParam uint16 = 0x0008
	CauseDeleteLastAddr    uint16 = 0x0100
	CauseOperationRefused  uint16 = 0x0101
	CauseDeleteSourceAddr  uint16 = 0x0102
)

// HMAC identifiers carried in the AUTH chunk (RFC 4895 section 4).
const (
	HMACSHA1   uint16 = 3
	HMACSHA256 uint16 = 4
)

// CommonHeader is the 12-byte SCTP common header. The checksum field is always
// transmitted as zero inside this package: the transport layer owns CRC32C,
// and zeroing it keeps the AUTH HMAC deterministic.
type CommonHeader struct {
	SrcPort         uint16
	DstPort         uint16
	VerificationTag uint32
}

// Chunk is one SCTP chunk. Data holds the chunk value without its 4-byte
// header and without trailing padding.
type Chunk struct {
	Type  uint8
	Flags uint8
	Data  []byte
}

type tlv struct {
	typ uint16
	raw []byte // full TLV including the 4-byte parameter header, unpadded
}

func pad4(n int) int { return (n + 3) &^ 3 }

func buildParam(ptype uint16, body []byte) []byte {
	raw := make([]byte, 4+len(body))
	binary.BigEndian.PutUint16(raw[0:], ptype)
	binary.BigEndian.PutUint16(raw[2:], uint16(len(raw)))
	copy(raw[4:], body)
	return raw
}

func (p tlv) wire() []byte {
	out := make([]byte, pad4(len(p.raw)))
	copy(out, p.raw)
	return out
}

// parseChunkParams parses TLV parameters from a chunk value. The end offset is
// derived from each parameter's 4-byte aligned length, so the chunk's
// trailing padding bytes are consumed as padding rather than parsed again.
func parseChunkParams(data []byte) ([]tlv, error) {
	total := len(data)
	end := 0
	var out []tlv
	for end < total {
		// Fewer than four bytes remain: this is only legal as chunk padding.
		if total-end < 4 {
			for _, b := range data[end:] {
				if b != 0 {
					return nil, ErrMalformed
				}
			}
			break
		}
		ptype := binary.BigEndian.Uint16(data[end:])
		plen := int(binary.BigEndian.Uint16(data[end+2:]))
		if plen < 4 || end+plen > total {
			return nil, ErrMalformed
		}
		out = append(out, tlv{typ: ptype, raw: append([]byte(nil), data[end:end+plen]...)})
		// The raw TLV must fit inside the chunk when padded, but the padding
		// itself may sit in the chunk's own trailing space.
		if end+pad4(plen) > total {
			end = total
		} else {
			end += pad4(plen)
		}
	}
	return out, nil
}

func marshalChunk(c Chunk) []byte {
	raw := make([]byte, 4+len(c.Data))
	raw[0] = c.Type
	raw[1] = c.Flags
	binary.BigEndian.PutUint16(raw[2:], uint16(len(raw)))
	copy(raw[4:], c.Data)
	return raw
}

func parseChunks(rest []byte) ([]Chunk, error) {
	var out []Chunk
	for len(rest) >= 4 {
		ctype, flags := rest[0], rest[1]
		clen := int(binary.BigEndian.Uint16(rest[2:]))
		if clen < 4 || clen > len(rest) {
			return nil, ErrMalformed
		}
		out = append(out, Chunk{
			Type:  ctype,
			Flags: flags,
			Data:  append([]byte(nil), rest[4:clen]...),
		})
		rest = rest[pad4(clen):]
	}
	// A final chunk may be followed only by zero-valued padding to the next
	// 4-byte boundary (RFC 4960 3.2).
	for _, b := range rest {
		if b != 0 {
			return nil, ErrMalformed
		}
	}
	return out, nil
}

func marshalPacket(hdr CommonHeader, chunks ...Chunk) []byte {
	var buf bytes.Buffer
	raw := make([]byte, 12)
	binary.BigEndian.PutUint16(raw[0:], hdr.SrcPort)
	binary.BigEndian.PutUint16(raw[2:], hdr.DstPort)
	binary.BigEndian.PutUint32(raw[4:], hdr.VerificationTag)
	// raw[8:12] is CRC32C, intentionally zero for HMAC purposes.
	buf.Write(raw)
	for _, c := range chunks {
		wire := marshalChunk(c)
		buf.Write(wire)
		buf.Write(make([]byte, pad4(len(wire))-len(wire)))
	}
	return buf.Bytes()
}

// ParsePacket splits a datagram into its common header and chunks.
func ParsePacket(pkt []byte) (CommonHeader, []Chunk, error) {
	if len(pkt) < 12 {
		return CommonHeader{}, nil, ErrMalformed
	}
	hdr := CommonHeader{
		SrcPort:         binary.BigEndian.Uint16(pkt[0:]),
		DstPort:         binary.BigEndian.Uint16(pkt[2:]),
		VerificationTag: binary.BigEndian.Uint32(pkt[4:]),
	}
	chunks, err := parseChunks(pkt[12:])
	if err != nil {
		return CommonHeader{}, nil, err
	}
	if len(chunks) == 0 {
		return CommonHeader{}, nil, ErrMalformed
	}
	return hdr, chunks, nil
}
