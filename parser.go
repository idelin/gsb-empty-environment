// Package csvq parses quoted, comma-separated files whose header column
// names may repeat. Column identity is positional: the i-th field of a
// record always belongs to the i-th header column, regardless of names.
//
// Row repair policy:
//   - records with fewer fields than the header are padded with Missing
//     fields (distinct from empty fields, which are present but have an
//     empty value);
//   - records with more fields than the header are quarantined whole,
//     keeping their raw byte range, and never shift the column positions
//     of subsequent records;
//   - newlines inside quoted fields never split a record and therefore
//     never trigger row repair.
package csvq

import (
	"fmt"
	"strings"
)

// Field is a single parsed field.
type Field struct {
	Value   string
	Quoted  bool // field was enclosed in quotes in the source
	Missing bool // padded by row repair; absent from the source record
}

// Record is a parsed data record with its original byte range.
type Record struct {
	Index  int // ordinal among all data rows, including quarantined ones
	Offset int64
	Length int64 // includes the line terminator
	Fields []Field
}

// Values returns the field values, including padded missing ones as "".
func (r Record) Values() []string {
	out := make([]string, len(r.Fields))
	for i, f := range r.Fields {
		out[i] = f.Value
	}
	return out
}

// RowError describes a quarantined row.
type RowError struct {
	Index  int // ordinal among all data rows
	Offset int64
	Length int64
	Raw    string
	Reason string
}

// File is a fully parsed source.
type File struct {
	Header     []string // by position; names may repeat
	Records    []Record
	Quarantine []RowError
}

// ColumnIndex returns the position of the first column with the given
// name, or -1. Because names may repeat, position is the only reliable
// identity.
func (f *File) ColumnIndex(name string) int {
	for i, h := range f.Header {
		if h == name {
			return i
		}
	}
	return -1
}

// Width returns the number of header columns.
func (f *File) Width() int { return len(f.Header) }

// scanRecord scans one record starting at pos. It returns the fields, the
// position just past the record terminator, and false only at EOF.
func scanRecord(src []byte, pos int) ([]Field, int, bool) {
	if pos >= len(src) {
		return nil, pos, false
	}
	var fields []Field
	for {
		var f Field
		if pos < len(src) && src[pos] == '"' {
			f.Quoted = true
			pos++
			var sb strings.Builder
			for pos < len(src) {
				c := src[pos]
				if c == '"' {
					if pos+1 < len(src) && src[pos+1] == '"' {
						sb.WriteByte('"')
						pos += 2
						continue
					}
					pos++
					break
				}
				// Embedded newlines are consumed here as ordinary
				// bytes, so they never end the record.
				sb.WriteByte(c)
				pos++
			}
			f.Value = sb.String()
			// Leniently skip anything between the closing quote and
			// the next delimiter or terminator.
			for pos < len(src) && src[pos] != ',' && src[pos] != '\n' && src[pos] != '\r' {
				pos++
			}
		} else {
			start := pos
			for pos < len(src) && src[pos] != ',' && src[pos] != '\n' && src[pos] != '\r' {
				pos++
			}
			f.Value = string(src[start:pos])
		}
		fields = append(fields, f)
		if pos >= len(src) {
			return fields, pos, true
		}
		if src[pos] == ',' {
			pos++
			continue
		}
		if src[pos] == '\r' {
			pos++
			if pos < len(src) && src[pos] == '\n' {
				pos++
			}
		} else {
			pos++
		}
		return fields, pos, true
	}
}

// Parse parses src into a File, applying the row repair policy.
func Parse(src []byte) *File {
	f := &File{}
	pos := 0
	hfields, npos, ok := scanRecord(src, pos)
	if !ok {
		return f
	}
	pos = npos
	for _, hf := range hfields {
		f.Header = append(f.Header, hf.Value)
	}
	idx := 0
	for {
		fields, npos, ok := scanRecord(src, pos)
		if !ok {
			break
		}
		start := pos
		pos = npos
		if len(fields) == 1 && !fields[0].Quoted && fields[0].Value == "" {
			continue // blank line
		}
		rec := Record{Index: idx, Offset: int64(start), Length: int64(pos - start)}
		switch {
		case len(fields) > len(f.Header):
			f.Quarantine = append(f.Quarantine, RowError{
				Index:  idx,
				Offset: rec.Offset,
				Length: rec.Length,
				Raw:    string(src[start:pos]),
				Reason: fmt.Sprintf("too many fields: got %d, want %d", len(fields), len(f.Header)),
			})
		case len(fields) < len(f.Header):
			for len(fields) < len(f.Header) {
				fields = append(fields, Field{Missing: true})
			}
			rec.Fields = fields
			f.Records = append(f.Records, rec)
		default:
			rec.Fields = fields
			f.Records = append(f.Records, rec)
		}
		idx++
	}
	return f
}

// ParseRecordAt independently re-parses exactly one record from the raw
// source at the given byte offset. It is used to verify recorded byte
// ranges and to sample records for recovery checks. Fields are returned
// unpadded, exactly as found in the source.
func ParseRecordAt(src []byte, offset int64) (Record, error) {
	if offset < 0 || offset >= int64(len(src)) {
		return Record{}, fmt.Errorf("offset %d out of range", offset)
	}
	fields, npos, ok := scanRecord(src, int(offset))
	if !ok {
		return Record{}, fmt.Errorf("no record at offset %d", offset)
	}
	return Record{Offset: offset, Length: int64(npos) - offset, Fields: fields}, nil
}

// Chunk is an enumerated, contiguous block of good records.
type Chunk struct {
	Index       int
	FirstRecord int // index into File.Records, inclusive
	LastRecord  int // exclusive
	ByteStart   int64
	ByteEnd     int64
}

// Chunks enumerates the good records of f into blocks of at most size
// records, tracking the byte range each block spans in the source.
func Chunks(f *File, size int) []Chunk {
	if size <= 0 {
		size = 1
	}
	var out []Chunk
	for i := 0; i < len(f.Records); i += size {
		end := i + size
		if end > len(f.Records) {
			end = len(f.Records)
		}
		out = append(out, Chunk{
			Index:       len(out),
			FirstRecord: i,
			LastRecord:  end,
			ByteStart:   f.Records[i].Offset,
			ByteEnd:     f.Records[end-1].Offset + f.Records[end-1].Length,
		})
	}
	return out
}
