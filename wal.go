package idedup

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Record types stored in the write-ahead log.
const (
	recOp        = 'O' // dedup entry + business record, committed atomically
	recWatermark = 'W' // caller-confirmed reclamation watermark
)

// A WAL frame is: crc32(payload) uint32 | len(payload) uint32 | payload.
// The CRC lets recovery detect and truncate a torn tail after a power loss.
const frameHeaderLen = 8

func appendFrame(dst, payload []byte) []byte {
	var hdr [frameHeaderLen]byte
	binary.BigEndian.PutUint32(hdr[0:4], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	dst = append(dst, hdr[:]...)
	return append(dst, payload...)
}

// decodeFrame reads one frame from buf. It returns the payload and the total
// number of bytes consumed. ok is false if buf does not hold a complete,
// valid frame (short buffer or CRC mismatch).
func decodeFrame(buf []byte) (payload []byte, consumed int, ok bool) {
	if len(buf) < frameHeaderLen {
		return nil, 0, false
	}
	crc := binary.BigEndian.Uint32(buf[0:4])
	n := binary.BigEndian.Uint32(buf[4:8])
	if uint64(n) > 64<<20 || len(buf) < frameHeaderLen+int(n) {
		return nil, 0, false
	}
	payload = buf[frameHeaderLen : frameHeaderLen+int(n)]
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, 0, false
	}
	return payload, frameHeaderLen + int(n), true
}

func encodeOpRecord(id string, content, result []byte) []byte {
	buf := []byte{recOp}
	buf = appendString(buf, id)
	buf = appendBytes(buf, content)
	buf = appendBytes(buf, result)
	return buf
}

func decodeOpRecord(p []byte) (id string, content, result []byte, err error) {
	if len(p) == 0 || p[0] != recOp {
		return "", nil, nil, fmt.Errorf("bad op record")
	}
	r := &reader{buf: p[1:]}
	id = r.str()
	content = r.bytes()
	result = r.bytes()
	if r.err != nil {
		return "", nil, nil, r.err
	}
	return id, content, result, nil
}

func encodeWatermarkRecord(id string) []byte {
	return appendString([]byte{recWatermark}, id)
}

func decodeWatermarkRecord(p []byte) (string, error) {
	if len(p) == 0 || p[0] != recWatermark {
		return "", fmt.Errorf("bad watermark record")
	}
	r := &reader{buf: p[1:]}
	id := r.str()
	return id, r.err
}

func appendString(dst []byte, s string) []byte {
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(s)))
	dst = append(dst, n[:]...)
	return append(dst, s...)
}

func appendBytes(dst, b []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	dst = append(dst, n[:]...)
	return append(dst, b...)
}

type reader struct {
	buf []byte
	err error
}

func (r *reader) str() string {
	if r.err != nil {
		return ""
	}
	if len(r.buf) < 2 {
		r.err = fmt.Errorf("short string")
		return ""
	}
	n := int(binary.BigEndian.Uint16(r.buf[:2]))
	r.buf = r.buf[2:]
	if len(r.buf) < n {
		r.err = fmt.Errorf("short string body")
		return ""
	}
	s := string(r.buf[:n])
	r.buf = r.buf[n:]
	return s
}

func (r *reader) bytes() []byte {
	if r.err != nil {
		return nil
	}
	if len(r.buf) < 4 {
		r.err = fmt.Errorf("short bytes")
		return nil
	}
	n := int(binary.BigEndian.Uint32(r.buf[:4]))
	r.buf = r.buf[4:]
	if len(r.buf) < n {
		r.err = fmt.Errorf("short bytes body")
		return nil
	}
	b := r.buf[:n]
	r.buf = r.buf[n:]
	return b
}

func (r *reader) uint64() uint64 {
	if r.err != nil {
		return 0
	}
	if len(r.buf) < 8 {
		r.err = fmt.Errorf("short uint64")
		return 0
	}
	v := binary.BigEndian.Uint64(r.buf[:8])
	r.buf = r.buf[8:]
	return v
}

func (r *reader) uint32() uint32 {
	if r.err != nil {
		return 0
	}
	if len(r.buf) < 4 {
		r.err = fmt.Errorf("short uint32")
		return 0
	}
	v := binary.BigEndian.Uint32(r.buf[:4])
	r.buf = r.buf[4:]
	return v
}

// Snapshot file layout:
//
//	magic(8) | crc32(body) uint32 | body
//	body = reclaimedUpTo str | confirmedWatermark str | reclaimedCount uint64 |
//	       entryCount uint32 | entries...
//	entry = id str | content bytes | result bytes
var snapshotMagic = []byte("IDSNAP01")

type snapshot struct {
	reclaimedUpTo  string
	confirmedWM    string
	reclaimedCount uint64
	entries        []Entry // surviving entries in commit order
}

func encodeSnapshot(sn *snapshot) []byte {
	body := appendString(nil, sn.reclaimedUpTo)
	body = appendString(body, sn.confirmedWM)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], sn.reclaimedCount)
	body = append(body, num[:]...)
	binary.BigEndian.PutUint32(num[:4], uint32(len(sn.entries)))
	body = append(body, num[:4]...)
	for _, e := range sn.entries {
		body = appendString(body, e.ID)
		body = appendBytes(body, e.Content)
		body = appendBytes(body, e.Result)
	}
	out := append([]byte(nil), snapshotMagic...)
	binary.BigEndian.PutUint32(num[:4], crc32.ChecksumIEEE(body))
	out = append(out, num[:4]...)
	return append(out, body...)
}

func decodeSnapshot(data []byte) (*snapshot, error) {
	if len(data) < len(snapshotMagic)+4 || string(data[:len(snapshotMagic)]) != string(snapshotMagic) {
		return nil, fmt.Errorf("bad snapshot magic")
	}
	crc := binary.BigEndian.Uint32(data[len(snapshotMagic) : len(snapshotMagic)+4])
	body := data[len(snapshotMagic)+4:]
	if crc32.ChecksumIEEE(body) != crc {
		return nil, fmt.Errorf("snapshot CRC mismatch")
	}
	r := &reader{buf: body}
	sn := &snapshot{}
	sn.reclaimedUpTo = r.str()
	sn.confirmedWM = r.str()
	sn.reclaimedCount = r.uint64()
	count := r.uint32()
	for i := uint32(0); i < count && r.err == nil; i++ {
		var e Entry
		e.ID = r.str()
		e.Content = append([]byte(nil), r.bytes()...)
		e.Result = append([]byte(nil), r.bytes()...)
		sn.entries = append(sn.entries, e)
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(r.buf) != 0 {
		return nil, fmt.Errorf("trailing bytes in snapshot")
	}
	return sn, nil
}
