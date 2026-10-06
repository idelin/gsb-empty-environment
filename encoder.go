package nbf

import "encoding/binary"

// Encode serializes f using the given byte order and writes the matching
// header magic. Only fields whose bit is set in Record.Fields are emitted.
func Encode(f *File, order binary.ByteOrder) []byte {
	w := &writer{order: order}
	if order == binary.BigEndian {
		w.bytes([]byte(MagicBigEndian))
	} else {
		w.bytes([]byte(MagicLittleEndian))
	}
	w.u16(f.Version)
	w.u16(uint16(len(f.Records)))
	for i := range f.Records {
		w.record(&f.Records[i])
	}
	return w.buf
}

type writer struct {
	buf   []byte
	order binary.ByteOrder
}

func (w *writer) bytes(b []byte) { w.buf = append(w.buf, b...) }

func (w *writer) u8(v byte) { w.buf = append(w.buf, v) }

func (w *writer) u16(v uint16) {
	var b [2]byte
	w.order.PutUint16(b[:], v)
	w.bytes(b[:])
}

func (w *writer) u32(v uint32) {
	var b [4]byte
	w.order.PutUint32(b[:], v)
	w.bytes(b[:])
}

func (w *writer) u64(v uint64) {
	var b [8]byte
	w.order.PutUint64(b[:], v)
	w.bytes(b[:])
}

// record writes bitmap, body length, then the body.
func (w *writer) record(rec *Record) {
	w.u16(rec.Fields & knownMask)
	body := encodeBody(rec, w.order)
	w.u32(uint32(len(body)))
	w.bytes(body)
}

func encodeBody(rec *Record, order binary.ByteOrder) []byte {
	w := &writer{order: order}
	if rec.Has(tagID) {
		w.u8(tagID)
		w.u16(4)
		w.u32(rec.ID)
	}
	if rec.Has(tagTimestamp) {
		w.u8(tagTimestamp)
		w.u16(8)
		w.u64(rec.Timestamp)
	}
	if rec.Has(tagFlags) {
		w.u8(tagFlags)
		w.u16(2)
		w.u16(rec.Flags)
	}
	if rec.Has(tagName) {
		w.u8(tagName)
		w.u16(uint16(len(rec.Name)))
		w.bytes([]byte(rec.Name))
	}
	if rec.Has(tagChildren) {
		nested := encodeNested(rec.Children, order)
		w.u8(tagChildren)
		w.u16(uint16(len(nested)))
		w.bytes(nested)
	}
	return w.buf
}

// encodeNested writes a nested record list: count followed by records,
// each length-prefixed exactly as at the top level.
func encodeNested(children []Record, order binary.ByteOrder) []byte {
	w := &writer{order: order}
	w.u16(uint16(len(children)))
	for i := range children {
		w.record(&children[i])
	}
	return w.buf
}
