package main

import (
	"encoding/binary"
	"fmt"
)

const (
	tagDeletion = 0
	tagValue    = 1
)

const batchHeaderSize = 12 // 8-byte sequence + 4-byte count

type BatchOp struct {
	Seq   uint64
	Del   bool
	Key   []byte
	Value []byte
}

// Batch is a decoded WriteBatch: one atomic commit.
type Batch struct {
	Seq   uint64
	Count uint32
	Ops   []BatchOp
}

func decodeVarint32(b []byte) (uint32, int, bool) {
	v, n := binary.Uvarint(b)
	if n <= 0 || v > 0xffffffff {
		return 0, 0, false
	}
	return uint32(v), n, true
}

// parseBatch decodes a WriteBatch. It is strict: the record count in the
// header must match the number of entries exactly, with no trailing bytes,
// so a torn or spliced record can never become a fake commit.
func parseBatch(data []byte) (*Batch, error) {
	if len(data) < batchHeaderSize {
		return nil, fmt.Errorf("batch too short: %d bytes", len(data))
	}
	seq := binary.LittleEndian.Uint64(data[:8])
	count := binary.LittleEndian.Uint32(data[8:12])
	rest := data[batchHeaderSize:]
	b := &Batch{Seq: seq, Count: count}
	for len(rest) > 0 {
		tag := rest[0]
		rest = rest[1:]
		switch tag {
		case tagDeletion, tagValue:
		default:
			return nil, fmt.Errorf("unknown entry tag %d", tag)
		}
		klen, n, ok := decodeVarint32(rest)
		if !ok || int(klen) > len(rest)-n {
			return nil, fmt.Errorf("bad key length")
		}
		rest = rest[n:]
		key := rest[:klen]
		rest = rest[klen:]
		op := BatchOp{Del: tag == tagDeletion, Key: append([]byte(nil), key...)}
		if tag == tagValue {
			vlen, n, ok := decodeVarint32(rest)
			if !ok || int(vlen) > len(rest)-n {
				return nil, fmt.Errorf("bad value length")
			}
			rest = rest[n:]
			op.Value = append([]byte(nil), rest[:vlen]...)
			rest = rest[vlen:]
		}
		b.Ops = append(b.Ops, op)
	}
	if uint32(len(b.Ops)) != count {
		return nil, fmt.Errorf("entry count mismatch: header says %d, decoded %d", count, len(b.Ops))
	}
	// Assign the sequence range: entry i of the batch gets seq+i.
	for i := range b.Ops {
		b.Ops[i].Seq = seq + uint64(i)
	}
	return b, nil
}
