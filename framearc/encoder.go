package framearc

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// AppendFrame appends one valid frame encoding data to dst.
func AppendFrame(dst []byte, seq uint32, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("framearc: empty frame")
	}
	if len(data) > MaxOrigLen {
		return nil, errors.New("framearc: frame too large")
	}
	var freq [256]uint64
	for _, b := range data {
		freq[b]++
	}
	lengths := huffmanLengths(&freq)
	var table [][2]uint8
	for sym := 0; sym < 256; sym++ {
		if lengths[sym] > 0 {
			table = append(table, [2]uint8{byte(sym), lengths[sym]})
		}
	}
	entries, err := canonicalCodes(table)
	if err != nil {
		return nil, err
	}
	codeOf := make(map[byte]codeEntry, len(entries))
	for _, e := range entries {
		codeOf[e.symbol] = e
	}
	var bw bitWriter
	for _, b := range data {
		e := codeOf[b]
		bw.writeBits(e.code, e.length)
	}
	payload := bw.finish()

	frame := make([]byte, 0, fixedHeaderLen+2*len(table)+headerCRCLen+len(payload)+payloadCRCLen)
	frame = append(frame, magicBytes...)
	frame = appendUint32(frame, seq)
	frame = appendUint32(frame, uint32(len(data)))
	frame = appendUint32(frame, uint32(len(payload)))
	frame = appendUint16(frame, uint16(len(table)))
	for _, e := range entries {
		frame = append(frame, e.symbol, e.length)
	}
	frame = appendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, payload...)
	frame = appendUint32(frame, crc32.ChecksumIEEE(data))
	return append(dst, frame...), nil
}

// BuildArchive encodes each element of frames as one frame, in order.
func BuildArchive(frames [][]byte) ([]byte, error) {
	var arc []byte
	for i, f := range frames {
		var err error
		arc, err = AppendFrame(arc, uint32(i), f)
		if err != nil {
			return nil, err
		}
	}
	return arc, nil
}

// Span describes the byte extent of one frame in an archive.
type Span struct {
	Start     int
	HeaderLen int
	FrameLen  int
	Seq       uint32
}

// ParseArchive walks a well-formed archive and returns every frame's
// span. It validates headers only, not payloads.
func ParseArchive(arc []byte) ([]Span, error) {
	var spans []Span
	off := 0
	for off < len(arc) {
		h, err := parseHeader(arc[off:])
		if err != nil {
			return nil, err
		}
		if off+h.frameLen() > len(arc) {
			return nil, errTruncated
		}
		spans = append(spans, Span{
			Start:     off,
			HeaderLen: h.headerLen,
			FrameLen:  h.frameLen(),
			Seq:       h.seq,
		})
		off += h.frameLen()
	}
	return spans, nil
}

func appendUint32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

func appendUint16(b []byte, v uint16) []byte {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	return append(b, tmp[:]...)
}
