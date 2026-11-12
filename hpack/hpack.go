// Package hpack implements the minimal RFC 7541 HPACK decoder needed to
// turn a reassembled HTTP/2 header block into concrete header fields.
//
// It supports indexed header fields, all three literal representations,
// dynamic table size updates, Huffman-encoded strings and a bounded dynamic
// table. The implementation is intentionally allocation-light and returns
// explicit errors on any malformed representation.
package hpack

import "errors"

// Field is one decoded header field.
type Field struct {
	Name      string
	Value     string
	Sensitive bool
}

// Size returns the HPACK dynamic-table size of the entry:
// name length + value length + 32 (RFC 7541 section 4.1).
func (f Field) Size() uint32 {
	return uint32(len(f.Name) + len(f.Value) + 32)
}

var (
	// ErrIndexedOutOfRange is returned for an index that does not point at
	// a static or dynamic table entry.
	ErrIndexedOutOfRange = errors.New("hpack: indexed representation out of range")
	// ErrInvalidRepresentation is returned for a malformed header block.
	ErrInvalidRepresentation = errors.New("hpack: invalid header field representation")
	// ErrDynamicTableTooLarge is returned when a size update exceeds the
	// table size the peer is allowed to request.
	ErrDynamicTableTooLarge = errors.New("hpack: dynamic table size update exceeds allowed maximum")
)

// Decoder is a per-HTTP/2-connection HPACK decoder. Its dynamic table is
// shared by every header block on the connection, as required by RFC 7541.
//
// DecodeFull is safe only from a single goroutine, matching the frame
// delivery order guaranteed by an HTTP/2 connection.
type Decoder struct {
	dyn        []Field // newest entry first (HPACK dynamic index 1)
	dynSize    uint32
	maxSize    uint32
	allowedMax uint32
}

// NewDecoder creates a decoder whose dynamic table may grow to at most
// initialMax bytes and never beyond allowedMax bytes.
func NewDecoder(initialMax, allowedMax uint32) *Decoder {
	if allowedMax < initialMax {
		allowedMax = initialMax
	}
	return &Decoder{maxSize: initialMax, allowedMax: allowedMax}
}

// SetMaxSize applies a SETTINGS_HEADER_TABLE_SIZE change from the local
// peer. Pending entries are evicted to satisfy the new limit.
func (d *Decoder) SetMaxSize(max uint32) {
	if max > d.allowedMax {
		max = d.allowedMax
	}
	d.maxSize = max
	d.evict()
}

// DynamicTableSize reports the current occupied size of the dynamic table.
func (d *Decoder) DynamicTableSize() uint32 { return d.dynSize }

// DecodeFull decodes one complete header block and returns its fields.
// The block must be the concatenation of a HEADERS frame payload and every
// following CONTINUATION payload, ending at END_HEADERS. A truncated block
// or any invalid representation returns an error and emits no fields.
func (d *Decoder) DecodeFull(block []byte) ([]Field, error) {
	var fields []Field
	p := block
	for len(p) > 0 {
		b := p[0]
		switch {
		case b&0x80 != 0:
			idx, rest, err := readVarint(p, 7)
			if err != nil {
				return nil, err
			}
			f, ok := d.at(idx)
			if !ok {
				return nil, ErrIndexedOutOfRange
			}
			fields = append(fields, f)
			p = rest
		case b&0xc0 == 0x40:
			f, rest, err := d.readLiteral(p, 6, true)
			if err != nil {
				return nil, err
			}
			fields = append(fields, f)
			p = rest
		case b&0xe0 == 0x20:
			size, rest, err := readVarint(p, 5)
			if err != nil {
				return nil, err
			}
			if size > uint64(d.allowedMax) {
				return nil, ErrDynamicTableTooLarge
			}
			d.maxSize = uint32(size)
			d.evict()
			p = rest
		case b&0xf0 == 0x10:
			f, rest, err := d.readLiteral(p, 4, false)
			if err != nil {
				return nil, err
			}
			f.Sensitive = true
			fields = append(fields, f)
			p = rest
		case b&0xf0 == 0x00:
			f, rest, err := d.readLiteral(p, 4, false)
			if err != nil {
				return nil, err
			}
			fields = append(fields, f)
			p = rest
		default:
			return nil, ErrInvalidRepresentation
		}
	}
	return fields, nil
}

// at resolves a 1-based HPACK index against the static table followed by
// the dynamic table.
func (d *Decoder) at(index uint64) (Field, bool) {
	if index == 0 {
		return Field{}, false
	}
	if index <= uint64(len(staticTable)) {
		return staticTable[index-1], true
	}
	dynIndex := int(index) - len(staticTable) - 1
	if dynIndex < 0 || dynIndex >= len(d.dyn) {
		return Field{}, false
	}
	return d.dyn[dynIndex], true
}

// readLiteral parses a literal header field representation. prefix is the
// integer prefix width (6 for incremental indexing, 4 otherwise) and
// incremental decides whether the field is added to the dynamic table.
func (d *Decoder) readLiteral(p []byte, prefix uint, incremental bool) (Field, []byte, error) {
	nameIndex, rest, err := readVarint(p, prefix)
	if err != nil {
		return Field{}, nil, err
	}
	var f Field
	if nameIndex != 0 {
		ref, ok := d.at(nameIndex)
		if !ok {
			return Field{}, nil, ErrIndexedOutOfRange
		}
		f.Name = ref.Name
	} else {
		name, r, err := readString(rest)
		if err != nil {
			return Field{}, nil, err
		}
		f.Name = string(name)
		rest = r
	}
	value, rest, err := readString(rest)
	if err != nil {
		return Field{}, nil, err
	}
	f.Value = string(value)

	if incremental {
		d.add(f)
	}
	return f, rest, nil
}

// add inserts a field at the front of the dynamic table and evicts entries
// until the table fits, per RFC 7541 section 4.4.
func (d *Decoder) add(f Field) {
	size := f.Size()
	if size > d.maxSize {
		// The entry cannot fit at all: the table is emptied.
		d.dyn = d.dyn[:0]
		d.dynSize = 0
		return
	}
	d.dyn = append(d.dyn, Field{})
	copy(d.dyn[1:], d.dyn)
	d.dyn[0] = f
	d.dynSize += size
	d.evict()
}

func (d *Decoder) evict() {
	for d.dynSize > d.maxSize && len(d.dyn) > 0 {
		oldest := d.dyn[len(d.dyn)-1]
		d.dynSize -= oldest.Size()
		d.dyn = d.dyn[:len(d.dyn)-1]
	}
}

// readVarint decodes an HPACK integer (RFC 7541 section 5.1).
func readVarint(p []byte, prefix uint) (uint64, []byte, error) {
	if len(p) == 0 {
		return 0, nil, ErrInvalidRepresentation
	}
	mask := uint64(1)<<prefix - 1
	value := uint64(p[0]) & mask
	if value < mask {
		return value, p[1:], nil
	}
	rest := p[1:]
	var shift uint
	for {
		if len(rest) == 0 {
			return 0, nil, ErrInvalidRepresentation
		}
		b := rest[0]
		rest = rest[1:]
		if shift >= 64-7 {
			return 0, nil, ErrInvalidRepresentation
		}
		value += uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, rest, nil
		}
		shift += 7
	}
}

// readString decodes an HPACK string literal (RFC 7541 section 5.2).
func readString(p []byte) ([]byte, []byte, error) {
	length, rest, err := readVarint(p, 7)
	if err != nil {
		return nil, nil, err
	}
	if length > uint64(len(rest)) {
		return nil, nil, ErrInvalidRepresentation
	}
	raw := rest[:length]
	rest = rest[length:]
	if p[0]&0x80 == 0 {
		out := make([]byte, len(raw))
		copy(out, raw)
		return out, rest, nil
	}
	decoded, err := huffmanDecode(raw)
	if err != nil {
		return nil, nil, err
	}
	return decoded, rest, nil
}
