package recordio

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// On-disk layout:
//
//	file     = magic record*
//	record   = bodyLen u32 LE ; bytes following, = schemaVer(2)+fieldCount(2)+fields
//	field    = fieldID u16 LE ; fieldLen u32 LE ; payload fieldLen
//
// bodyLen is the record's self-declared size. A reader that does not know a
// fieldID still steps over the field using fieldLen, so records produced by a
// newer schema version stay positionally readable.
const (
	Magic = "RCD1"

	HeaderSize   = 4 // file magic
	lenSize      = 4 // bodyLen
	recMetaSize  = 4 // schema version (u16) + field count (u16)
	fieldHdrSize = 6 // field id (u16) + field length (u32)

	// DefaultMaxRecordSize caps a single record body. A declared length above
	// the cap is bad data, never "need more data".
	DefaultMaxRecordSize = 1 << 20

	maxIndexEntries = 1 << 28
)

var (
	// ErrNeedMoreData means the input so far is valid but a frame cannot be
	// completed until more bytes arrive.
	ErrNeedMoreData = errors.New("recordio: need more data")
	// ErrBadData means the bytes already present violate the format. The
	// parser becomes permanently failed and never resynchronizes.
	ErrBadData = errors.New("recordio: bad data")
)

// Field is one typed, ID-tagged TLV value inside a record.
type Field struct {
	ID   uint16
	Data []byte
}

// Record carries the schema version it was written under plus all its fields.
type Record struct {
	SchemaVersion uint16
	Fields        []Field
}

// FieldByID returns the first field carrying id. Unknown IDs are simply not
// requested, which is how cross-version field skipping works.
func (r *Record) FieldByID(id uint16) ([]byte, bool) {
	for i := range r.Fields {
		if r.Fields[i].ID == id {
			return r.Fields[i].Data, true
		}
	}
	return nil, false
}

// EncodeRecord serializes one record. Empty field lists are valid and produce
// a minimal, non-empty frame.
func EncodeRecord(schemaVersion uint16, fields []Field) []byte {
	bodyLen := recMetaSize
	for i := range fields {
		bodyLen += fieldHdrSize + len(fields[i].Data)
	}
	out := make([]byte, lenSize+bodyLen)
	binary.LittleEndian.PutUint32(out[0:], uint32(bodyLen))
	binary.LittleEndian.PutUint16(out[lenSize:], schemaVersion)
	binary.LittleEndian.PutUint16(out[lenSize+2:], uint16(len(fields)))
	pos := lenSize + recMetaSize
	for i := range fields {
		binary.LittleEndian.PutUint16(out[pos:], fields[i].ID)
		binary.LittleEndian.PutUint32(out[pos+2:], uint32(len(fields[i].Data)))
		copy(out[pos+fieldHdrSize:], fields[i].Data)
		pos += fieldHdrSize + len(fields[i].Data)
	}
	return out
}

// parseBody parses exactly bodyLen bytes. It enforces that the declared field
// count and field lengths consume the body precisely; any shortfall or
// overrun is bad data, so a corrupt length can never be turned into a
// successful (misaligned) parse. Field payloads are allocated only after the
// declared length is proven to fit inside the body.
func parseBody(body []byte) (*Record, error) {
	if len(body) < recMetaSize {
		return nil, fmt.Errorf("%w: record body is %d bytes, minimum is %d",
			ErrBadData, len(body), recMetaSize)
	}
	rec := &Record{
		SchemaVersion: binary.LittleEndian.Uint16(body[0:2]),
	}
	count := int(binary.LittleEndian.Uint16(body[2:4]))

	fields := make([]Field, 0, count)
	pos := recMetaSize
	for i := 0; i < count; i++ {
		if len(body)-pos < fieldHdrSize {
			return nil, fmt.Errorf("%w: truncated header for field %d/%d",
				ErrBadData, i+1, count)
		}
		id := binary.LittleEndian.Uint16(body[pos:])
		fieldLen := int(binary.LittleEndian.Uint32(body[pos+2:]))
		pos += fieldHdrSize

		remaining := len(body) - pos
		if fieldLen > remaining || fieldLen < 0 {
			return nil, fmt.Errorf("%w: field %d declares %d bytes but only %d remain",
				ErrBadData, i+1, fieldLen, remaining)
		}
		data := make([]byte, fieldLen)
		copy(data, body[pos:pos+fieldLen])
		pos += fieldLen
		fields = append(fields, Field{ID: id, Data: data})
	}
	if pos != len(body) {
		return nil, fmt.Errorf("%w: %d trailing byte(s) after %d field(s); record boundary broken",
			ErrBadData, len(body)-pos, count)
	}
	rec.Fields = fields
	return rec, nil
}
