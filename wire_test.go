package pgoutput

import (
	"encoding/binary"
	"time"
)

// wireBuilder mirrors the server-side pgoutput encoding so tests can feed
// realistic, constructed message sequences through Parse and the Assembler.
type wireBuilder struct {
	buf []byte
}

func (b *wireBuilder) bytes(v []byte) *wireBuilder {
	b.buf = append(b.buf, v...)
	return b
}

func (b *wireBuilder) u8(v byte) *wireBuilder {
	b.buf = append(b.buf, v)
	return b
}

func (b *wireBuilder) u16(v uint16) *wireBuilder {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
	return b
}

func (b *wireBuilder) u32(v uint32) *wireBuilder {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
	return b
}

func (b *wireBuilder) i32(v int32) *wireBuilder {
	return b.u32(uint32(v))
}

func (b *wireBuilder) u64(v uint64) *wireBuilder {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	b.buf = append(b.buf, tmp[:]...)
	return b
}

func (b *wireBuilder) lsn(v LSN) *wireBuilder { return b.u64(uint64(v)) }

func (b *wireBuilder) cstr(s string) *wireBuilder {
	b.buf = append(b.buf, s...)
	b.buf = append(b.buf, 0)
	return b
}

func (b *wireBuilder) text16(v []byte) *wireBuilder {
	return b.u16(uint16(len(v))).bytes(v)
}

func (b *wireBuilder) build(typ byte) []byte {
	out := make([]byte, 0, 1+len(b.buf))
	out = append(out, typ)
	out = append(out, b.buf...)
	return out
}

// copyData wraps an inner pgoutput payload in a CopyData ('w') frame.
func copyData(inner []byte) []byte {
	out := []byte{'w'}
	var lenBuf [4]byte
	// Per the streaming protocol the length covers itself and the payload,
	// i.e. everything after the message type byte.
	binary.BigEndian.PutUint32(lenBuf[:], uint32(4+len(inner)))
	out = append(out, lenBuf[:]...)
	out = append(out, inner...)
	return out
}

func timeToMicros(t time.Time) int64 {
	return t.Sub(pgEpoch).Microseconds()
}

// ---- message constructors -------------------------------------------------

func wireRelation(id uint32, namespace, name string, replicaIdentity byte, cols ...RelationColumn) []byte {
	b := &wireBuilder{}
	b.u32(id).cstr(namespace).cstr(name).u8(replicaIdentity).u16(uint16(len(cols)))
	for _, c := range cols {
		var flags byte
		if c.Key {
			flags |= colFlagKey
		}
		if c.Binary {
			flags |= colFlagBinary
		}
		b.u8(flags).cstr(c.Name).u32(c.DataType).i32(c.TypeModifier)
	}
	return b.build('R')
}

func wireType(id uint32, namespace, name string) []byte {
	return (&wireBuilder{}).u32(id).cstr(namespace).cstr(name).build('Y')
}

func wireTuple(cols []TupleColumn) *wireBuilder {
	b := &wireBuilder{}
	b.u16(uint16(len(cols)))
	for _, c := range cols {
		b.u8(c.Flag)
		if c.Flag == tupleText || c.Flag == tupleBinary {
			b.text16(c.Value)
		}
	}
	return b
}

func wireInsert(relID uint32, row []TupleColumn) []byte {
	b := &wireBuilder{}
	b.u32(relID)
	b.buf = append(b.buf, wireTuple(row).buf...)
	return b.build('I')
}

func wireUpdate(relID uint32, oldMarker byte, old, new []TupleColumn) []byte {
	b := &wireBuilder{}
	b.u32(relID).u8(oldMarker)
	if oldMarker != 'N' {
		b.buf = append(b.buf, wireTuple(old).buf...)
	}
	b.u8('N')
	b.buf = append(b.buf, wireTuple(new).buf...)
	return b.build('U')
}

func wireDelete(relID uint32, marker byte, old []TupleColumn) []byte {
	b := &wireBuilder{}
	b.u32(relID).u8(marker)
	b.buf = append(b.buf, wireTuple(old).buf...)
	return b.build('D')
}

func wireTruncate(options uint8, ids ...uint32) []byte {
	b := &wireBuilder{}
	b.u32(uint32(len(ids))).u8(options)
	for _, id := range ids {
		b.u32(id)
	}
	return b.build('T')
}

func wireBegin(xid uint32, finalLSN LSN, t time.Time) []byte {
	return (&wireBuilder{}).lsn(finalLSN).u64(uint64(timeToMicros(t))).u32(xid).build('B')
}

func wireCommit(commitLSN, endLSN LSN, t time.Time) []byte {
	return (&wireBuilder{}).u8(0).lsn(commitLSN).lsn(endLSN).u64(uint64(timeToMicros(t))).build('C')
}

func wireStreamStart(xid uint32, first bool, subXID uint32) []byte {
	b := &wireBuilder{}
	var flags byte
	if first {
		flags = 1
	}
	b.u8(flags).u32(xid)
	if subXID != 0 {
		b.u32(subXID)
	}
	return b.build('S')
}

func wireStreamStop() []byte { return []byte{'E'} }

func wireStreamCommit(xid uint32, commitLSN, endLSN LSN, t time.Time) []byte {
	return (&wireBuilder{}).u32(xid).u8(0).lsn(commitLSN).lsn(endLSN).
		u64(uint64(timeToMicros(t))).build('c')
}

func wireStreamAbort(xid, subXID uint32, lsn, endLSN LSN, t time.Time) []byte {
	return (&wireBuilder{}).u32(xid).u32(subXID).lsn(lsn).lsn(endLSN).
		u64(uint64(timeToMicros(t))).build('A')
}

func wireKeepalive(lsn LSN, t time.Time, reply bool) []byte {
	var flags byte
	if reply {
		flags = 1
	}
	b := &wireBuilder{}
	return b.lsn(lsn).u64(uint64(timeToMicros(t))).u8(flags).build('k')
}

func wireLogicalMessage(transactional bool, lsn LSN, prefix string, content []byte) []byte {
	typ := byte('m')
	if transactional {
		typ = 'M'
	}
	b := &wireBuilder{}
	b.lsn(lsn).cstr(prefix).u32(uint32(len(content))).bytes(content)
	return b.build(typ)
}

func textCol(v string) TupleColumn   { return TupleColumn{Flag: tupleText, Value: []byte(v)} }
func nullCol() TupleColumn           { return TupleColumn{Flag: tupleNull} }
func unchangedCol() TupleColumn      { return TupleColumn{Flag: tupleUnchanged, Unchanged: true} }
func binaryCol(v []byte) TupleColumn { return TupleColumn{Flag: tupleBinary, Value: v} }
