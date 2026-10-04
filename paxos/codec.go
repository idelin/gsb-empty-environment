package paxos

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// On-disk format (all integers little-endian):
//
//	File header (16 bytes):
//	  magic   [8]byte  = "MPAXLOG1"
//	  version uint32   = FormatVersion
//	  flags   uint32   = reserved, must be 0 for now
//
//	Record:
//	  crc32   uint32   IEEE checksum of payload
//	  length  uint32   payload length in bytes (<= MaxRecordSize)
//	  payload []byte:
//	    term   uint64  ballot number of the entry
//	    node   uint32  ballot owner id
//	    index  uint64  log slot
//	    cmdLen uint32  command length
//	    cmd    []byte  command bytes
//	    ...    []byte  reserved tail, ignored by this version (forward compat)
const (
	MagicString   = "MPAXLOG1"
	FormatVersion = 1
	HeaderSize    = 16
	RecordHdrSize = 8
	payloadMin    = 24
	// MaxRecordSize bounds a single record so a corrupt length field can
	// never trigger a huge allocation or an overflow.
	MaxRecordSize = 64 << 20
)

var (
	ErrTooShort           = errors.New("paxos: buffer too short")
	ErrCorrupt            = errors.New("paxos: corrupt record")
	ErrBadMagic           = errors.New("paxos: bad magic")
	ErrUnsupportedVersion = errors.New("paxos: unsupported format version")
	ErrRecordSizeExceeds  = errors.New("paxos: record length exceeds limit")
)

// Entry is a single replicated log record. After a zero-copy decode, Cmd
// aliases the underlying buffer (e.g. an mmap'd region); use CloneEntry to
// obtain an owned copy.
type Entry struct {
	Term  uint64
	Node  uint32
	Index uint64
	Cmd   []byte
}

// NoopCmd fills log holes during leader repair.
var NoopCmd = []byte{0x00}

// IsNoop reports whether the entry is a hole-filling no-op.
func (e Entry) IsNoop() bool { return len(e.Cmd) == 1 && e.Cmd[0] == 0x00 }

func payloadSize(e Entry) int { return payloadMin + len(e.Cmd) }

// RecordSize returns the total encoded size of e including the record header.
func RecordSize(e Entry) int { return RecordHdrSize + payloadSize(e) }

// EncodeHeader appends the file header to dst.
func EncodeHeader(dst []byte) []byte {
	var buf [HeaderSize]byte
	copy(buf[0:8], MagicString)
	binary.LittleEndian.PutUint32(buf[8:12], FormatVersion)
	return append(dst, buf[:]...)
}

// CheckHeader validates the file header. Older versions are accepted
// (backward compatible); newer versions are rejected explicitly.
func CheckHeader(buf []byte) error {
	if len(buf) < HeaderSize {
		return ErrTooShort
	}
	if string(buf[0:8]) != MagicString {
		return ErrBadMagic
	}
	v := binary.LittleEndian.Uint32(buf[8:12])
	if v > FormatVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, v)
	}
	return nil
}

// AppendRecord appends the encoded record for e to dst and returns the
// extended slice.
func AppendRecord(dst []byte, e Entry) []byte {
	off := len(dst)
	dst = append(dst, make([]byte, RecordSize(e))...)
	rec := dst[off:]
	binary.LittleEndian.PutUint32(rec[4:8], uint32(payloadSize(e)))
	p := rec[RecordHdrSize:]
	binary.LittleEndian.PutUint64(p[0:8], e.Term)
	binary.LittleEndian.PutUint32(p[8:12], e.Node)
	binary.LittleEndian.PutUint64(p[12:20], e.Index)
	binary.LittleEndian.PutUint32(p[20:24], uint32(len(e.Cmd)))
	copy(p[24:], e.Cmd)
	binary.LittleEndian.PutUint32(rec[0:4], crc32.ChecksumIEEE(p))
	return dst
}

// DecodeRecord decodes one record from the front of buf. The returned
// Entry.Cmd aliases buf (zero-copy). It returns the entry and the number of
// bytes consumed. ErrTooShort signals a partial/torn tail; callers scanning a
// file should stop there and keep the valid prefix.
func DecodeRecord(buf []byte) (Entry, int, error) {
	var e Entry
	if len(buf) < RecordHdrSize {
		return e, 0, ErrTooShort
	}
	crc := binary.LittleEndian.Uint32(buf[0:4])
	l := binary.LittleEndian.Uint32(buf[4:8])
	if l > MaxRecordSize {
		return e, 0, ErrRecordSizeExceeds
	}
	if uint64(l) > uint64(len(buf))-RecordHdrSize {
		return e, 0, ErrTooShort
	}
	p := buf[RecordHdrSize : RecordHdrSize+int(l)]
	if crc32.ChecksumIEEE(p) != crc {
		return e, 0, ErrCorrupt
	}
	if l < payloadMin {
		return e, 0, ErrCorrupt
	}
	e.Term = binary.LittleEndian.Uint64(p[0:8])
	e.Node = binary.LittleEndian.Uint32(p[8:12])
	e.Index = binary.LittleEndian.Uint64(p[12:20])
	cmdLen := binary.LittleEndian.Uint32(p[20:24])
	if uint64(cmdLen) > uint64(l)-payloadMin {
		return e, 0, ErrCorrupt
	}
	e.Cmd = p[24 : 24+int(cmdLen)]
	// Any bytes in p[24+cmdLen:] are reserved for future fields and are
	// ignored, so newer writers remain readable (forward compat).
	return e, RecordHdrSize + int(l), nil
}

// Scan iterates all valid records in buf (which must start right after the
// file header), invoking fn with the offset and decoded entry for each. It
// stops silently at a torn tail and returns the length of the valid prefix.
func Scan(buf []byte, fn func(off int64, e Entry) error) (int, error) {
	off := 0
	for off < len(buf) {
		e, n, err := DecodeRecord(buf[off:])
		if err == ErrTooShort {
			break
		}
		if err != nil {
			return off, err
		}
		if fn != nil {
			if err := fn(int64(off), e); err != nil {
				return off, err
			}
		}
		off += n
	}
	return off, nil
}

// CloneEntry returns a deep copy of e that owns its command bytes.
func CloneEntry(e Entry) Entry {
	c := e
	if e.Cmd != nil {
		c.Cmd = append([]byte(nil), e.Cmd...)
	}
	return c
}
