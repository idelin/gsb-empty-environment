package parquetcol

import (
	"encoding/binary"
	"math"
)

// Minimal Thrift compact protocol implementation, just enough to serialize the
// parquet.thrift metadata structs. Field type constants follow the compact
// protocol specification.
const (
	ctStop   byte = 0x00
	ctTrue   byte = 0x01
	ctFalse  byte = 0x02
	ctByte   byte = 0x03
	ctI16    byte = 0x04
	ctI32    byte = 0x05
	ctI64    byte = 0x06
	ctDouble byte = 0x07
	ctBinary byte = 0x08
	ctList   byte = 0x09
	ctStruct byte = 0x0c
)

type compactWriter struct {
	buf     []byte
	lastFid []int16
}

func newCompactWriter() *compactWriter {
	return &compactWriter{}
}

func (w *compactWriter) writeFieldBegin(fid int16, typ byte) {
	last := int16(0)
	if len(w.lastFid) > 0 {
		last = w.lastFid[len(w.lastFid)-1]
	}
	delta := fid - last
	if delta > 0 && delta <= 15 {
		w.buf = append(w.buf, byte(delta)<<4|typ)
	} else {
		w.buf = append(w.buf, typ)
		w.buf = appendI16(w.buf, fid)
	}
	if len(w.lastFid) > 0 {
		w.lastFid[len(w.lastFid)-1] = fid
	}
}

func (w *compactWriter) writeStructBegin() { w.lastFid = append(w.lastFid, 0) }

func (w *compactWriter) writeStructEnd() {
	w.lastFid = w.lastFid[:len(w.lastFid)-1]
}

func (w *compactWriter) writeStop() { w.buf = append(w.buf, ctStop) }

func (w *compactWriter) writeBoolField(fid int16, v bool) {
	last := int16(0)
	if len(w.lastFid) > 0 {
		last = w.lastFid[len(w.lastFid)-1]
	}
	typ := ctFalse
	if v {
		typ = ctTrue
	}
	delta := fid - last
	if delta > 0 && delta <= 15 {
		w.buf = append(w.buf, byte(delta)<<4|typ)
	} else {
		w.buf = append(w.buf, typ)
		w.buf = appendI16(w.buf, fid)
	}
	w.setLast(fid)
}

func (w *compactWriter) setLast(fid int16) {
	w.lastFid[len(w.lastFid)-1] = fid
}

func (w *compactWriter) writeI32Field(fid int16, v int32) {
	w.writeFieldBegin(fid, ctI32)
	w.setLast(fid)
	w.buf = appendUvarint(w.buf, zigzag32(v))
}

func (w *compactWriter) writeI64Field(fid int16, v int64) {
	w.writeFieldBegin(fid, ctI64)
	w.setLast(fid)
	w.buf = appendUvarint(w.buf, zigzag64(v))
}

func (w *compactWriter) writeBinaryField(fid int16, v []byte) {
	w.writeFieldBegin(fid, ctBinary)
	w.setLast(fid)
	w.buf = appendUvarint(w.buf, uint64(len(v)))
	w.buf = append(w.buf, v...)
}

func (w *compactWriter) writeStringField(fid int16, v string) {
	w.writeBinaryField(fid, []byte(v))
}

func (w *compactWriter) writeListBegin(fid int16, elemType byte, n int) {
	w.writeFieldBegin(fid, ctList)
	w.setLast(fid)
	if n <= 14 {
		w.buf = append(w.buf, byte(n)<<4|elemType)
	} else {
		w.buf = append(w.buf, 0xf0|elemType)
		w.buf = appendUvarint(w.buf, uint64(n))
	}
}

func (w *compactWriter) writeStructField(fid int16, marshal func(*compactWriter)) {
	w.writeFieldBegin(fid, ctStruct)
	w.setLast(fid)
	marshal(w)
}

func zigzag32(v int32) uint64   { return uint64(uint32(v)<<1) ^ uint64(uint32(v>>31)) }
func zigzag64(v int64) uint64   { return uint64(v<<1) ^ uint64(v>>63) }
func unzigzag64(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// compact protocol stores i16/i32/i64 zigzag-encoded.
func appendI16(buf []byte, v int16) []byte { return appendUvarint(buf, zigzag32(int32(v))) }

func appendDouble(buf []byte, v float64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
	return append(buf, b[:]...)
}

type compactReader struct {
	data    []byte
	pos     int
	lastID  int16
	idStack []int16
}

func newCompactReader(data []byte) *compactReader { return &compactReader{data: data} }

func (r *compactReader) readByte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errCorrupt("thrift: unexpected end")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *compactReader) readVarint() (uint64, error) {
	v, n := consumeUvarint(r.data[r.pos:])
	if n == 0 {
		return 0, errCorrupt("thrift: bad varint")
	}
	r.pos += n
	return v, nil
}

func (r *compactReader) readI16() (int16, error) {
	v, err := r.readVarint()
	return int16(unzigzag64(v)), err
}

func (r *compactReader) readI32() (int32, error) {
	v, err := r.readVarint()
	if err != nil {
		return 0, err
	}
	return int32(unzigzag64(v)), nil
}

func (r *compactReader) readI64() (int64, error) {
	v, err := r.readVarint()
	return unzigzag64(v), err
}

func (r *compactReader) readBinary() ([]byte, error) {
	n, err := r.readVarint()
	if err != nil {
		return nil, err
	}
	if r.pos+int(n) > len(r.data) {
		return nil, errCorrupt("thrift: binary out of bounds")
	}
	b := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

func (r *compactReader) readDouble() (float64, error) {
	if r.pos+8 > len(r.data) {
		return 0, errCorrupt("thrift: double out of bounds")
	}
	v := math.Float64frombits(binary.LittleEndian.Uint64(r.data[r.pos:]))
	r.pos += 8
	return v, nil
}

// fieldHeader describes one struct field.
type fieldHeader struct {
	fid int16
	typ byte
}

// beginStruct resets field-id delta tracking for an incoming nested struct.
func (r *compactReader) beginStruct() {
	r.idStack = append(r.idStack, r.lastID)
	r.lastID = 0
}

// endStruct restores the parent struct's last field id.
func (r *compactReader) endStruct() {
	if len(r.idStack) == 0 {
		r.lastID = 0
		return
	}
	r.lastID = r.idStack[len(r.idStack)-1]
	r.idStack = r.idStack[:len(r.idStack)-1]
}

// readFieldHeader returns the next field or typ == ctStop at end of struct.
func (r *compactReader) readFieldHeader() (fieldHeader, error) {
	b, err := r.readByte()
	if err != nil {
		return fieldHeader{}, err
	}
	if b == ctStop {
		return fieldHeader{typ: ctStop}, nil
	}
	typ := b & 0x0f
	delta := int16(b >> 4)
	var fid int16
	if delta == 0 {
		fid, err = r.readI16()
		if err != nil {
			return fieldHeader{}, err
		}
	} else {
		fid = r.lastID + delta
	}
	r.lastID = fid
	return fieldHeader{fid: fid, typ: typ}, nil
}

func (r *compactReader) readListHeader() (byte, int, error) {
	b, err := r.readByte()
	if err != nil {
		return 0, 0, err
	}
	size := int(b >> 4)
	elemType := b & 0x0f
	if size == 15 {
		n, err := r.readVarint()
		if err != nil {
			return 0, 0, err
		}
		size = int(n)
	}
	return elemType, size, nil
}

// skipValue skips a value of the given compact type, including nested structs
// and lists, so readers can ignore unknown fields safely.
func (r *compactReader) skipValue(typ byte) error {
	switch typ {
	case ctTrue, ctFalse:
		return nil
	case ctByte:
		_, err := r.readByte()
		return err
	case ctI16, ctI32, ctI64:
		_, err := r.readVarint()
		return err
	case ctDouble:
		_, err := r.readDouble()
		return err
	case ctBinary:
		_, err := r.readBinary()
		return err
	case ctStruct:
		r.beginStruct()
		for {
			fh, err := r.readFieldHeader()
			if err != nil {
				return err
			}
			if fh.typ == ctStop {
				break
			}
			if err := r.skipValue(fh.typ); err != nil {
				return err
			}
		}
		r.endStruct()
		return nil
	case ctList:
		elemType, n, err := r.readListHeader()
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if err := r.skipValue(elemType); err != nil {
				return err
			}
		}
		return nil
	default:
		return errCorrupt("thrift: unknown compact type")
	}
}
