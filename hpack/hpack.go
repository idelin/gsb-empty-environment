// Package hpack implements a minimal HPACK (RFC 7541) encoder and decoder
// sufficient for an embeddable HTTP/2 reverse proxy. The encoder never uses
// Huffman coding; the decoder rejects Huffman-coded strings.
package hpack

import (
	"errors"
	"fmt"
)

// HeaderField is a single name/value header pair.
type HeaderField struct {
	Name  string
	Value string
}

// ErrHuffmanUnsupported is returned by the Decoder when it encounters a
// Huffman-coded string literal.
var ErrHuffmanUnsupported = errors.New("hpack: huffman-coded strings are not supported")

// staticTable is the HPACK static table (RFC 7541 Appendix A), index 1..61.
var staticTable = []HeaderField{
	{":authority", ""},
	{":method", "GET"},
	{":method", "POST"},
	{":path", "/"},
	{":path", "/index.html"},
	{":scheme", "http"},
	{":scheme", "https"},
	{":status", "200"},
	{":status", "204"},
	{":status", "206"},
	{":status", "304"},
	{":status", "400"},
	{":status", "404"},
	{":status", "500"},
	{"accept-charset", ""},
	{"accept-encoding", "gzip, deflate"},
	{"accept-language", ""},
	{"accept-ranges", ""},
	{"accept", ""},
	{"access-control-allow-origin", ""},
	{"age", ""},
	{"allow", ""},
	{"authorization", ""},
	{"cache-control", ""},
	{"content-disposition", ""},
	{"content-encoding", ""},
	{"content-language", ""},
	{"content-length", ""},
	{"content-location", ""},
	{"content-range", ""},
	{"content-type", ""},
	{"cookie", ""},
	{"date", ""},
	{"etag", ""},
	{"expect", ""},
	{"expires", ""},
	{"from", ""},
	{"host", ""},
	{"if-match", ""},
	{"if-modified-since", ""},
	{"if-none-match", ""},
	{"if-range", ""},
	{"if-unmodified-since", ""},
	{"last-modified", ""},
	{"link", ""},
	{"location", ""},
	{"max-forwards", ""},
	{"proxy-authenticate", ""},
	{"proxy-authorization", ""},
	{"range", ""},
	{"referer", ""},
	{"refresh", ""},
	{"retry-after", ""},
	{"server", ""},
	{"set-cookie", ""},
	{"strict-transport-security", ""},
	{"transfer-encoding", ""},
	{"user-agent", ""},
	{"vary", ""},
	{"via", ""},
	{"www-authenticate", ""},
}

const staticTableLen = 61

// dynamicTable is the HPACK dynamic table. Entries are stored newest-first.
type dynamicTable struct {
	entries []HeaderField
	size    uint64
	maxSize uint64
}

func entrySize(f HeaderField) uint64 {
	return uint64(len(f.Name)) + uint64(len(f.Value)) + 32
}

func (t *dynamicTable) setMaxSize(max uint64) {
	t.maxSize = max
	t.evict()
}

func (t *dynamicTable) add(f HeaderField) {
	t.entries = append([]HeaderField{f}, t.entries...)
	t.size += entrySize(f)
	t.evict()
}

func (t *dynamicTable) evict() {
	for t.size > t.maxSize && len(t.entries) > 0 {
		last := t.entries[len(t.entries)-1]
		t.entries = t.entries[:len(t.entries)-1]
		t.size -= entrySize(last)
	}
}

func (t *dynamicTable) clear() {
	t.entries = nil
	t.size = 0
}

// at returns the dynamic-table entry for a 1-based dynamic index.
func (t *dynamicTable) at(idx uint64) (HeaderField, bool) {
	if idx == 0 || idx > uint64(len(t.entries)) {
		return HeaderField{}, false
	}
	return t.entries[idx-1], true
}

// appendInt encodes n using the HPACK integer representation with the given
// prefix size; the prefix bits of the first byte are ORed with mask.
func appendInt(dst []byte, n uint64, prefixBits uint, mask byte) []byte {
	max := uint64(1<<prefixBits) - 1
	if n < max {
		return append(dst, mask|byte(n))
	}
	dst = append(dst, mask|byte(max))
	n -= max
	for n >= 128 {
		dst = append(dst, byte(n&0x7f)|0x80)
		n >>= 7
	}
	return append(dst, byte(n))
}

// readInt decodes an HPACK integer from buf. The prefix bits of buf[0] hold
// the low bits of the value.
func readInt(buf []byte, prefixBits uint) (uint64, []byte, error) {
	if len(buf) == 0 {
		return 0, nil, errors.New("hpack: truncated integer")
	}
	max := uint64(1<<prefixBits) - 1
	n := uint64(buf[0]) & max
	if n < max {
		return n, buf[1:], nil
	}
	shift := uint(0)
	i := 1
	for {
		if i >= len(buf) {
			return 0, nil, errors.New("hpack: truncated integer continuation")
		}
		b := buf[i]
		i++
		if shift > 57 {
			return 0, nil, errors.New("hpack: integer overflow")
		}
		n += uint64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			return n, buf[i:], nil
		}
	}
}

// appendString encodes a string literal without Huffman coding.
func appendString(dst []byte, s string) []byte {
	dst = appendInt(dst, uint64(len(s)), 7, 0)
	return append(dst, s...)
}

// readString decodes a string literal.
func readString(buf []byte) (string, []byte, error) {
	if len(buf) == 0 {
		return "", nil, errors.New("hpack: truncated string")
	}
	if buf[0]&0x80 != 0 {
		return "", nil, ErrHuffmanUnsupported
	}
	n, rest, err := readInt(buf, 7)
	if err != nil {
		return "", nil, err
	}
	if uint64(len(rest)) < n {
		return "", nil, errors.New("hpack: truncated string body")
	}
	return string(rest[:n]), rest[n:], nil
}

// Encoder is an HPACK encoder with a dynamic table. It is not safe for
// concurrent use; callers must serialize Encode calls.
type Encoder struct {
	dt      dynamicTable
	maxSize uint64
	pending []uint64 // pending dynamic table size updates, emitted at next block start
}

// NewEncoder returns an Encoder whose dynamic table capacity is maxSize.
func NewEncoder(maxSize uint32) *Encoder {
	e := &Encoder{maxSize: uint64(maxSize)}
	e.dt.setMaxSize(uint64(maxSize))
	return e
}

// MaxDynamicTableSize returns the current dynamic table capacity.
func (e *Encoder) MaxDynamicTableSize() uint32 {
	return uint32(e.maxSize)
}

// SetMaxDynamicTableSize changes the dynamic table capacity. A table size
// update is emitted at the start of the next encoded header block, as
// required by RFC 7541 section 4.2.
func (e *Encoder) SetMaxDynamicTableSize(n uint32) {
	e.maxSize = uint64(n)
	e.dt.setMaxSize(uint64(n))
	e.pending = append(e.pending, uint64(n))
}

// ClearTable evicts every dynamic table entry and schedules a table size
// update of 0 followed by a restore to the current capacity at the start of
// the next header block. A shared connection uses this when switching
// tenants so that no subsequent header block can reference another tenant's
// entries.
func (e *Encoder) ClearTable() {
	e.dt.clear()
	e.pending = append(e.pending, 0, e.maxSize)
}

// Encode appends the header block for fields to dst and returns the result.
// Pending table size updates are emitted first.
func (e *Encoder) Encode(dst []byte, fields []HeaderField) []byte {
	for _, n := range e.pending {
		dst = appendInt(dst, n, 5, 0x20)
	}
	e.pending = e.pending[:0]
	for _, f := range fields {
		dst = e.encodeField(dst, f)
	}
	return dst
}

func (e *Encoder) encodeField(dst []byte, f HeaderField) []byte {
	if idx, ok := e.findExact(f); ok {
		return appendInt(dst, idx, 7, 0x80)
	}
	nameIdx, hasName := e.findName(f.Name)
	var enc []byte
	if hasName {
		enc = appendInt(dst, nameIdx, 6, 0x40)
	} else {
		enc = appendInt(dst, 0, 6, 0x40)
		enc = appendString(enc, f.Name)
	}
	enc = appendString(enc, f.Value)
	e.dt.add(f)
	return enc
}

// findExact returns the full index of an exact name+value match.
func (e *Encoder) findExact(f HeaderField) (uint64, bool) {
	for i, s := range staticTable {
		if s.Name == f.Name && s.Value == f.Value {
			return uint64(i) + 1, true
		}
	}
	for i, d := range e.dt.entries {
		if d.Name == f.Name && d.Value == f.Value {
			return staticTableLen + 1 + uint64(i), true
		}
	}
	return 0, false
}

// findName returns the index of a name-only match.
func (e *Encoder) findName(name string) (uint64, bool) {
	for i, s := range staticTable {
		if s.Name == name {
			return uint64(i) + 1, true
		}
	}
	for i, d := range e.dt.entries {
		if d.Name == name {
			return staticTableLen + 1 + uint64(i), true
		}
	}
	return 0, false
}

// Decoder is an HPACK decoder with a dynamic table. It is not safe for
// concurrent use.
type Decoder struct {
	dt         dynamicTable
	maxAllowed uint64
}

// NewDecoder returns a Decoder that rejects dynamic table size updates
// larger than maxAllowed.
func NewDecoder(maxAllowed uint32) *Decoder {
	d := &Decoder{maxAllowed: uint64(maxAllowed)}
	d.dt.setMaxSize(uint64(maxAllowed))
	return d
}

// Decode decodes a complete header block and returns its header fields.
func (d *Decoder) Decode(block []byte) ([]HeaderField, error) {
	var fields []HeaderField
	buf := block
	for len(buf) > 0 {
		b := buf[0]
		switch {
		case b&0x80 != 0: // indexed header field
			idx, rest, err := readInt(buf, 7)
			if err != nil {
				return nil, err
			}
			f, err := d.lookup(idx)
			if err != nil {
				return nil, err
			}
			fields = append(fields, f)
			buf = rest
		case b&0xc0 == 0x40: // literal with incremental indexing
			f, rest, err := d.readLiteral(buf, 6)
			if err != nil {
				return nil, err
			}
			d.dt.add(f)
			fields = append(fields, f)
			buf = rest
		case b&0xe0 == 0x20: // dynamic table size update
			n, rest, err := readInt(buf, 5)
			if err != nil {
				return nil, err
			}
			if n > d.maxAllowed {
				return nil, fmt.Errorf("hpack: table size update %d exceeds limit %d", n, d.maxAllowed)
			}
			d.dt.setMaxSize(n)
			buf = rest
		default: // literal without indexing / never indexed
			f, rest, err := d.readLiteral(buf, 4)
			if err != nil {
				return nil, err
			}
			fields = append(fields, f)
			buf = rest
		}
	}
	return fields, nil
}

func (d *Decoder) lookup(idx uint64) (HeaderField, error) {
	if idx == 0 {
		return HeaderField{}, errors.New("hpack: invalid index 0")
	}
	if idx <= staticTableLen {
		return staticTable[idx-1], nil
	}
	if f, ok := d.dt.at(idx - staticTableLen); ok {
		return f, nil
	}
	return HeaderField{}, fmt.Errorf("hpack: index %d out of range", idx)
}

func (d *Decoder) readLiteral(buf []byte, prefixBits uint) (HeaderField, []byte, error) {
	idx, rest, err := readInt(buf, prefixBits)
	if err != nil {
		return HeaderField{}, nil, err
	}
	var f HeaderField
	if idx == 0 {
		name, r, err := readString(rest)
		if err != nil {
			return HeaderField{}, nil, err
		}
		f.Name = name
		rest = r
	} else {
		base, err := d.lookup(idx)
		if err != nil {
			return HeaderField{}, nil, err
		}
		f.Name = base.Name
	}
	value, r, err := readString(rest)
	if err != nil {
		return HeaderField{}, nil, err
	}
	f.Value = value
	return f, r, nil
}
