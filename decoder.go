package netsec

import (
	"errors"
	"fmt"
	"math"
)

const defaultMaxDynamicTableSize uint32 = 4096

var (
	// ErrTruncatedHeaderBlock means that a representation or string ended
	// before the header block was complete.
	ErrTruncatedHeaderBlock = errors.New("hpack: truncated header block")

	// ErrIntegerOverflow means that an HPACK integer does not fit in uint64.
	ErrIntegerOverflow = errors.New("hpack: integer overflow")

	// ErrIndexOutOfRange means that an indexed reference does not identify
	// either a static table entry or a live dynamic table entry.
	ErrIndexOutOfRange = errors.New("hpack: table index out of range")

	// ErrSizeUpdateNotAtStart means that a dynamic table size update was
	// found after the first header field representation in a header block.
	ErrSizeUpdateNotAtStart = errors.New("hpack: dynamic table size update is not at the beginning of the header block")

	// ErrSizeUpdateTooLarge means that a dynamic table size update exceeded
	// the maximum allowed by SETTINGS_HEADER_TABLE_SIZE.
	ErrSizeUpdateTooLarge = errors.New("hpack: dynamic table size update exceeds the SETTINGS limit")

	// ErrInvalidHuffman means that a string was marked as Huffman encoded but
	// did not contain a valid HPACK Huffman bit sequence.
	ErrInvalidHuffman = errors.New("hpack: invalid Huffman-encoded string")
)

// HeaderField is one decoded HTTP header field. Sensitive reports that the
// field used the never-indexed literal representation.
type HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

// Header is an alias for HeaderField.
type Header = HeaderField

// IsPseudo reports whether the header name is an HTTP/2 pseudo-header.
func (h HeaderField) IsPseudo() bool {
	return len(h.Name) > 0 && h.Name[0] == ':'
}

// Size returns the HPACK dynamic table size of the field, including the
// RFC 7541 per-entry overhead of 32 octets.
func (h HeaderField) Size() uint32 {
	return uint32(uint64(len(h.Name)) + uint64(len(h.Value)) + 32)
}

// Decoder decodes complete HPACK header blocks while retaining dynamic table
// state between blocks. A Decoder is not safe for concurrent use.
type Decoder struct {
	entries     []HeaderField // newest entry first
	size        uint64
	maxSize     uint64
	maxAllowed  uint64
	initialized bool
}

// NewDecoder returns a decoder whose SETTINGS_HEADER_TABLE_SIZE limit is
// maxDynamicTableSize. If the argument is omitted, the HTTP/2 default of 4096
// octets is used.
func NewDecoder(maxDynamicTableSize ...uint32) *Decoder {
	max := defaultMaxDynamicTableSize
	if len(maxDynamicTableSize) > 0 {
		max = maxDynamicTableSize[0]
	}
	d := &Decoder{}
	d.SetMaxDynamicTableSize(max)
	return d
}

// NewDecoderWithMax is an explicit single-argument form of NewDecoder.
func NewDecoderWithMax(maxDynamicTableSize uint32) *Decoder {
	return NewDecoder(maxDynamicTableSize)
}

func (d *Decoder) ensureInitialized() {
	if d.initialized {
		return
	}
	d.maxAllowed = uint64(defaultMaxDynamicTableSize)
	d.maxSize = uint64(defaultMaxDynamicTableSize)
	d.initialized = true
}

// SetMaxDynamicTableSize applies a new SETTINGS_HEADER_TABLE_SIZE limit and
// resizes the current dynamic table to that limit. Entries that no longer fit
// are evicted.
func (d *Decoder) SetMaxDynamicTableSize(max uint32) {
	d.ensureInitialized()
	d.maxAllowed = uint64(max)
	d.maxSize = uint64(max)
	d.evict()
	d.initialized = true
}

// SetAllowedMaxDynamicTableSize sets the upper bound for encoded dynamic table
// size updates without increasing the current table capacity. If the current
// capacity exceeds the new bound, it is reduced and entries are evicted.
func (d *Decoder) SetAllowedMaxDynamicTableSize(max uint32) {
	d.ensureInitialized()
	d.maxAllowed = uint64(max)
	if d.maxSize > d.maxAllowed {
		d.maxSize = d.maxAllowed
		d.evict()
	}
}

// MaxDynamicTableSize returns the current dynamic table capacity.
func (d *Decoder) MaxDynamicTableSize() uint32 {
	d.ensureInitialized()
	return uint32(d.maxSize)
}

// AllowedMaxDynamicTableSize returns the SETTINGS_HEADER_TABLE_SIZE limit.
func (d *Decoder) AllowedMaxDynamicTableSize() uint32 {
	d.ensureInitialized()
	return uint32(d.maxAllowed)
}

// DynamicTableSize returns the number of octets currently used by the dynamic
// table, using the RFC 7541 entry-size calculation.
func (d *Decoder) DynamicTableSize() uint32 {
	d.ensureInitialized()
	return uint32(d.size)
}

// DynamicTableLen returns the number of entries in the dynamic table.
func (d *Decoder) DynamicTableLen() int {
	d.ensureInitialized()
	return len(d.entries)
}

func (d *Decoder) evict() {
	for d.size > d.maxSize && len(d.entries) > 0 {
		last := len(d.entries) - 1
		d.size -= uint64(d.entries[last].Size())
		d.entries = d.entries[:last]
	}
}

// Decode decodes one complete header block. Dynamic table changes are committed
// only if the whole block is valid; a failed block leaves the decoder unchanged.
func (d *Decoder) Decode(block []byte) ([]HeaderField, error) {
	if d == nil {
		return nil, errors.New("hpack: nil decoder")
	}
	d.ensureInitialized()

	state := decodeState{
		entries:    append([]HeaderField(nil), d.entries...),
		size:       d.size,
		maxSize:    d.maxSize,
		maxAllowed: d.maxAllowed,
	}
	fields, err := decodeBlock(block, &state)
	if err != nil {
		return nil, err
	}

	d.entries = state.entries
	d.size = state.size
	d.maxSize = state.maxSize
	return fields, nil
}

// DecodeBlock is an alias for Decode.
func (d *Decoder) DecodeBlock(block []byte) ([]HeaderField, error) {
	return d.Decode(block)
}

// DecodeFull is an alias for Decode, matching the name used by common Go
// HPACK decoder APIs.
func (d *Decoder) DecodeFull(block []byte) ([]HeaderField, error) {
	return d.Decode(block)
}

// Decode decodes one standalone header block with the supplied
// SETTINGS_HEADER_TABLE_SIZE limit.
func Decode(block []byte, maxDynamicTableSize uint32) ([]HeaderField, error) {
	return NewDecoder(maxDynamicTableSize).Decode(block)
}

// DecodeHeaderBlock is an alias for Decode.
func DecodeHeaderBlock(block []byte, maxDynamicTableSize uint32) ([]HeaderField, error) {
	return Decode(block, maxDynamicTableSize)
}

// DecodePairs decodes a block and returns name/value pairs without metadata.
func DecodePairs(block []byte, maxDynamicTableSize uint32) ([][2]string, error) {
	fields, err := Decode(block, maxDynamicTableSize)
	if err != nil {
		return nil, err
	}
	pairs := make([][2]string, len(fields))
	for i, field := range fields {
		pairs[i] = [2]string{field.Name, field.Value}
	}
	return pairs, nil
}

type decodeState struct {
	entries    []HeaderField
	size       uint64
	maxSize    uint64
	maxAllowed uint64
}

func (s *decodeState) lookup(index uint64) (HeaderField, error) {
	if index == 0 {
		return HeaderField{}, fmt.Errorf("%w: index 0", ErrIndexOutOfRange)
	}
	if index <= uint64(staticTableLen) {
		return staticTable[index-1], nil
	}
	dynamicIndex := index - uint64(staticTableLen) - 1
	if dynamicIndex >= uint64(len(s.entries)) {
		return HeaderField{}, fmt.Errorf("%w: index %d (static=%d, dynamic=%d)",
			ErrIndexOutOfRange, index, staticTableLen, len(s.entries))
	}
	return s.entries[dynamicIndex], nil
}

func (s *decodeState) setMaxSize(size uint64) {
	s.maxSize = size
	for s.size > s.maxSize && len(s.entries) > 0 {
		last := len(s.entries) - 1
		s.size -= uint64(s.entries[last].Size())
		s.entries = s.entries[:last]
	}
}

func (s *decodeState) add(field HeaderField) {
	entrySize := uint64(len(field.Name)) + uint64(len(field.Value)) + 32
	if entrySize > s.maxSize {
		// RFC 7541 Section 4.4: an entry larger than the current table
		// capacity empties the table and is not inserted.
		s.entries = nil
		s.size = 0
		return
	}
	s.entries = append(s.entries, HeaderField{})
	copy(s.entries[1:], s.entries[:len(s.entries)-1])
	s.entries[0] = field
	s.size += entrySize
	for s.size > s.maxSize && len(s.entries) > 0 {
		last := len(s.entries) - 1
		s.size -= uint64(s.entries[last].Size())
		s.entries = s.entries[:last]
	}
}

type headerBlockParser struct {
	src []byte
	off int
}

func decodeBlock(block []byte, state *decodeState) ([]HeaderField, error) {
	p := headerBlockParser{src: block}
	fields := make([]HeaderField, 0, 8)
	sawHeader := false

	for p.off < len(block) {
		first := block[p.off]
		switch {
		case first&0x80 != 0:
			index, err := p.readInt(7)
			if err != nil {
				return nil, err
			}
			field, err := state.lookup(index)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
			sawHeader = true

		case first&0x40 != 0:
			field, err := p.readLiteral(6, false, state)
			if err != nil {
				return nil, err
			}
			state.add(field)
			fields = append(fields, field)
			sawHeader = true

		case first&0x20 != 0:
			if sawHeader {
				return nil, fmt.Errorf("%w: update byte at offset %d", ErrSizeUpdateNotAtStart, p.off)
			}
			size, err := p.readInt(5)
			if err != nil {
				return nil, err
			}
			if size > state.maxAllowed {
				return nil, fmt.Errorf("%w: wanted %d, SETTINGS allows %d",
					ErrSizeUpdateTooLarge, size, state.maxAllowed)
			}
			state.setMaxSize(size)

		case first&0x10 != 0:
			field, err := p.readLiteral(4, true, state)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
			sawHeader = true

		default:
			field, err := p.readLiteral(4, false, state)
			if err != nil {
				return nil, err
			}
			fields = append(fields, field)
			sawHeader = true
		}
	}

	return fields, nil
}

func (p *headerBlockParser) readLiteral(prefix uint8, sensitive bool, state *decodeState) (HeaderField, error) {
	nameIndex, err := p.readInt(prefix)
	if err != nil {
		return HeaderField{}, err
	}

	var name string
	if nameIndex == 0 {
		name, err = p.readString()
		if err != nil {
			return HeaderField{}, err
		}
	} else {
		field, err := state.lookup(nameIndex)
		if err != nil {
			return HeaderField{}, err
		}
		name = field.Name
	}

	value, err := p.readString()
	if err != nil {
		return HeaderField{}, err
	}
	return HeaderField{Name: name, Value: value, Sensitive: sensitive}, nil
}

func (p *headerBlockParser) readString() (string, error) {
	if p.off >= len(p.src) {
		return "", ErrTruncatedHeaderBlock
	}
	huffman := p.src[p.off]&0x80 != 0
	length, err := p.readInt(7)
	if err != nil {
		return "", err
	}
	if length > uint64(len(p.src)-p.off) {
		return "", fmt.Errorf("%w: string length %d, remaining %d",
			ErrTruncatedHeaderBlock, length, len(p.src)-p.off)
	}
	end := p.off + int(length)
	raw := p.src[p.off:end]
	p.off = end
	if huffman {
		return decodeHuffman(raw)
	}
	return string(raw), nil
}

func (p *headerBlockParser) readInt(prefix uint8) (uint64, error) {
	if p.off >= len(p.src) {
		return 0, ErrTruncatedHeaderBlock
	}
	mask := uint64(1)<<prefix - 1
	value := uint64(p.src[p.off]) & mask
	p.off++
	if value < mask {
		return value, nil
	}

	var shift uint
	for {
		if p.off >= len(p.src) {
			return 0, ErrTruncatedHeaderBlock
		}
		octet := p.src[p.off]
		p.off++
		part := uint64(octet & 0x7f)
		if shift >= 64 || part > (math.MaxUint64-value)>>shift {
			return 0, ErrIntegerOverflow
		}
		value += part << shift
		if octet&0x80 == 0 {
			return value, nil
		}
		shift += 7
	}
}
