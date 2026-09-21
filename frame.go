package framemux

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Frame content layout (before escaping, after removing flags):
//
//	[0]   encoding epoch
//	[1]   stream ID
//	[2:6] sequence number (uint32 BE)
//	[6]   frame type
//	[7:9] payload length (uint16 BE)
//	[9:]  payload
//	[-4:] CRC32-IEEE of everything preceding it
const (
	headerLen     = 9
	crcLen        = 4
	minContentLen = headerLen + crcLen
)

// DefaultMaxPayload is the default per-frame payload cap in bytes.
const DefaultMaxPayload = 1024

type frameType byte

const (
	typeData frameType = iota
	typeAck
	typeEpochPropose
	typeEpochAck
	typeReset
)

func (t frameType) String() string {
	switch t {
	case typeData:
		return "DATA"
	case typeAck:
		return "ACK"
	case typeEpochPropose:
		return "EPOCH_PROPOSE"
	case typeEpochAck:
		return "EPOCH_ACK"
	case typeReset:
		return "RESET"
	}
	return fmt.Sprintf("type(%d)", byte(t))
}

type frame struct {
	epoch   uint8
	stream  uint8
	seq     uint32
	typ     frameType
	payload []byte
}

// appendContent serializes the frame header, payload and CRC (no
// escaping, no flags).
func (f frame) appendContent(dst []byte) []byte {
	start := len(dst)
	dst = append(dst, f.epoch, f.stream)
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], f.seq)
	dst = append(dst, tmp[:]...)
	dst = append(dst, byte(f.typ), 0, 0)
	binary.BigEndian.PutUint16(dst[start+7:], uint16(len(f.payload)))
	dst = append(dst, f.payload...)
	binary.BigEndian.PutUint32(tmp[:], crc32.ChecksumIEEE(dst[start:]))
	return append(dst, tmp[:]...)
}

// parseContent validates and decodes one frame's content. The payload is
// copied so the caller can safely reuse the content buffer; the copy
// happens only after every length field has been validated.
func parseContent(content []byte) (frame, error) {
	var f frame
	if len(content) < minContentLen {
		return f, fmt.Errorf("short frame: %d bytes", len(content))
	}
	body := content[:len(content)-crcLen]
	want := binary.BigEndian.Uint32(content[len(content)-crcLen:])
	if got := crc32.ChecksumIEEE(body); got != want {
		return f, fmt.Errorf("crc mismatch: got %08x, want %08x", got, want)
	}
	if n := int(binary.BigEndian.Uint16(body[7:9])); n != len(body)-headerLen {
		return f, fmt.Errorf("length field %d, actual payload %d", n, len(body)-headerLen)
	}
	if !validEpoch(content[0]) {
		return f, fmt.Errorf("unknown epoch %d", content[0])
	}
	if typ := frameType(content[6]); typ > typeReset {
		return f, fmt.Errorf("unknown frame type %d", content[6])
	}
	f.epoch = content[0]
	f.stream = content[1]
	f.seq = binary.BigEndian.Uint32(body[2:6])
	f.typ = frameType(body[6])
	f.payload = append([]byte(nil), body[headerLen:]...)
	return f, nil
}
