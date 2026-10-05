// Package merge implements a resumable, memory-bounded multi-way file merge.
//
// Records are triples (key, source, sequence) with an opaque payload.
// The merge order is the stable total order: key, then source, then sequence.
package merge

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Record is a single merge record.
type Record struct {
	Key     string
	Source  uint32
	Seq     uint64
	Payload []byte
}

// Less is the stable ordering: key, then source, then sequence.
func Less(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Seq < b.Seq
}

func equalRecord(a, b Record) bool {
	if a.Key != b.Key || a.Source != b.Source || a.Seq != b.Seq {
		return false
	}
	if len(a.Payload) != len(b.Payload) {
		return false
	}
	for i := range a.Payload {
		if a.Payload[i] != b.Payload[i] {
			return false
		}
	}
	return true
}

// ErrIncomplete is returned by DecodeRecord when the buffer does not yet hold
// a full record (e.g. a truncated file tail).
var ErrIncomplete = errors.New("incomplete record")

func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

func (r Record) bodyLen() int {
	return uvarintLen(uint64(len(r.Key))) + len(r.Key) +
		uvarintLen(uint64(r.Source)) + uvarintLen(r.Seq) +
		uvarintLen(uint64(len(r.Payload))) + len(r.Payload)
}

// framedSize returns the encoded size of r including the length frame.
func (r Record) framedSize() int {
	body := r.bodyLen()
	return uvarintLen(uint64(body)) + body
}

// AppendRecord appends the framed encoding of r to dst and returns the
// extended slice. Frame layout:
//
//	uvarint bodyLen | uvarint keyLen key | uvarint source | uvarint seq |
//	uvarint payloadLen payload
func AppendRecord(dst []byte, r Record) []byte {
	dst = binary.AppendUvarint(dst, uint64(r.bodyLen()))
	dst = binary.AppendUvarint(dst, uint64(len(r.Key)))
	dst = append(dst, r.Key...)
	dst = binary.AppendUvarint(dst, uint64(r.Source))
	dst = binary.AppendUvarint(dst, r.Seq)
	dst = binary.AppendUvarint(dst, uint64(len(r.Payload)))
	dst = append(dst, r.Payload...)
	return dst
}

// DecodeRecord decodes one framed record from buf, returning the record and
// the number of bytes consumed. It returns ErrIncomplete if buf does not yet
// hold a full record, and a corruption error if the framing is invalid.
func DecodeRecord(buf []byte) (Record, int, error) {
	bodyLen, n := binary.Uvarint(buf)
	if n == 0 {
		return Record{}, 0, ErrIncomplete
	}
	if n < 0 {
		return Record{}, 0, fmt.Errorf("corrupt length frame")
	}
	if uint64(len(buf)-n) < bodyLen {
		return Record{}, 0, ErrIncomplete
	}
	rest := buf[n : n+int(bodyLen)]
	keyLen, m := binary.Uvarint(rest)
	if m <= 0 || uint64(len(rest)-m) < keyLen {
		return Record{}, 0, fmt.Errorf("corrupt key field")
	}
	rest = rest[m:]
	key := string(rest[:int(keyLen)])
	rest = rest[int(keyLen):]
	src, m := binary.Uvarint(rest)
	if m <= 0 {
		return Record{}, 0, fmt.Errorf("corrupt source field")
	}
	rest = rest[m:]
	seq, m := binary.Uvarint(rest)
	if m <= 0 {
		return Record{}, 0, fmt.Errorf("corrupt sequence field")
	}
	rest = rest[m:]
	plLen, m := binary.Uvarint(rest)
	if m <= 0 || uint64(len(rest)-m) < plLen {
		return Record{}, 0, fmt.Errorf("corrupt payload field")
	}
	rest = rest[m:]
	payload := append([]byte(nil), rest[:int(plLen)]...)
	rest = rest[int(plLen):]
	if len(rest) != 0 {
		return Record{}, 0, fmt.Errorf("trailing bytes in record body")
	}
	return Record{Key: key, Source: uint32(src), Seq: seq, Payload: payload}, n + int(bodyLen), nil
}
