package h2h1

import (
	"errors"
	"fmt"
)

// HeaderField is a decoded HPACK name/value pair. Sensitive is set for the
// "never indexed" representation.
type HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

// StaticTable is the RFC 7541 Appendix A static table. Index zero is unused.
var StaticTable = [...]HeaderField{
	{},
	{Name: ":authority"},
	{Name: ":method", Value: "GET"},
	{Name: ":method", Value: "POST"},
	{Name: ":path", Value: "/"},
	{Name: ":path", Value: "/index.html"},
	{Name: ":scheme", Value: "http"},
	{Name: ":scheme", Value: "https"},
	{Name: ":status", Value: "200"},
	{Name: ":status", Value: "204"},
	{Name: ":status", Value: "206"},
	{Name: ":status", Value: "304"},
	{Name: ":status", Value: "400"},
	{Name: ":status", Value: "404"},
	{Name: ":status", Value: "500"},
	{Name: "accept-charset"},
	{Name: "accept-encoding", Value: "gzip, deflate"},
	{Name: "accept-language"},
	{Name: "accept-ranges"},
	{Name: "accept"},
	{Name: "access-control-allow-origin"},
	{Name: "age"},
	{Name: "allow"},
	{Name: "authorization"},
	{Name: "cache-control"},
	{Name: "content-disposition"},
	{Name: "content-encoding"},
	{Name: "content-language"},
	{Name: "content-length"},
	{Name: "content-location"},
	{Name: "content-range"},
	{Name: "content-type"},
	{Name: "cookie"},
	{Name: "date"},
	{Name: "etag"},
	{Name: "expect"},
	{Name: "expires"},
	{Name: "from"},
	{Name: "host"},
	{Name: "if-match"},
	{Name: "if-modified-since"},
	{Name: "if-none-match"},
	{Name: "if-range"},
	{Name: "if-unmodified-since"},
	{Name: "last-modified"},
	{Name: "link"},
	{Name: "location"},
	{Name: "max-forwards"},
	{Name: "proxy-authenticate"},
	{Name: "proxy-authorization"},
	{Name: "range"},
	{Name: "referer"},
	{Name: "refresh"},
	{Name: "retry-after"},
	{Name: "server"},
	{Name: "set-cookie"},
	{Name: "strict-transport-security"},
	{Name: "transfer-encoding"},
	{Name: "user-agent"},
	{Name: "vary"},
	{Name: "via"},
	{Name: "www-authenticate"},
}

const hpackStaticTableLen = uint64(len(StaticTable) - 1)

type dynamicEntry struct {
	field HeaderField
	size  uint32
}

// HPACKDecoder is a stateful RFC 7541 decoder. One decoder must be used for
// all header blocks on the same HTTP/2 connection, including blocks split
// across CONTINUATION frames.
type HPACKDecoder struct {
	allowedMax uint32
	tableMax   uint32
	tableSize  uint32
	dynamic    []dynamicEntry
}

// NewHPACKDecoder creates a decoder. maxTableSize is the largest dynamic table
// size that a header block is allowed to select through a table-size update.
// RFC 7541's default is 4096.
func NewHPACKDecoder(maxTableSize uint32) *HPACKDecoder {
	return &HPACKDecoder{
		allowedMax: maxTableSize,
		tableMax:   maxTableSize,
	}
}

// NewHpackDecoder is a spelling-compatible alias.
func NewHpackDecoder(maxTableSize uint32) *HPACKDecoder {
	return NewHPACKDecoder(maxTableSize)
}

// HpackDecoder is a Go spelling-compatible alias.
type HpackDecoder = HPACKDecoder

// SetAllowedTableSize changes the SETTINGS_HEADER_TABLE_SIZE limit. If the
// current table is larger than the new limit, it is reduced immediately.
func (d *HPACKDecoder) SetAllowedTableSize(max uint32) error {
	if d == nil {
		return errors.New("h2h1: nil HPACK decoder")
	}
	d.allowedMax = max
	if d.tableMax > max {
		d.tableMax = max
		d.evictTo(max)
	}
	return nil
}

func (d *HPACKDecoder) AllowedTableSize() uint32 {
	if d == nil {
		return 0
	}
	return d.allowedMax
}

func (d *HPACKDecoder) TableSize() uint32 {
	if d == nil {
		return 0
	}
	return d.tableSize
}

func (d *HPACKDecoder) DynamicTableEntries() int {
	if d == nil {
		return 0
	}
	return len(d.dynamic)
}

// ResetDynamicTable drops all dynamic entries and restores the table size to
// the allowed SETTINGS limit.
func (d *HPACKDecoder) ResetDynamicTable() {
	if d == nil {
		return
	}
	d.dynamic = nil
	d.tableSize = 0
	d.tableMax = d.allowedMax
}

// Decode decodes one complete header block. Table-size updates are accepted
// only before the first indexed or literal header representation.
func (d *HPACKDecoder) Decode(block []byte) ([]HeaderField, error) {
	var fields []HeaderField
	if err := d.DecodeInto(block, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// DecodeInto appends decoded fields to dst.
func (d *HPACKDecoder) DecodeInto(block []byte, dst *[]HeaderField) error {
	if d == nil {
		return errors.New("h2h1: nil HPACK decoder")
	}
	if dst == nil {
		return errors.New("h2h1: nil HPACK destination")
	}
	decoded := make([]HeaderField, 0, len(block)/8+1)
	pos := 0
	sawHeader := false
	for pos < len(block) {
		b := block[pos]
		switch {
		case b&0x80 != 0: // Indexed header field.
			sawHeader = true
			index, err := readHPACKInt(block, &pos, 7)
			if err != nil {
				return err
			}
			if index == 0 {
				return errors.New("h2h1: HPACK indexed representation has index 0")
			}
			f, err := d.lookup(index)
			if err != nil {
				return err
			}
			decoded = append(decoded, f)

		case b&0xc0 == 0x40: // Literal with incremental indexing.
			sawHeader = true
			name, value, err := d.decodeLiteral(block, &pos, 6)
			if err != nil {
				return err
			}
			f := HeaderField{Name: name, Value: value}
			decoded = append(decoded, f)
			d.add(f)

		case b&0xe0 == 0x20: // Dynamic table size update.
			if sawHeader {
				return errors.New("h2h1: HPACK dynamic table size update after a header field")
			}
			update, err := readHPACKInt(block, &pos, 5)
			if err != nil {
				return err
			}
			if update > uint64(d.allowedMax) {
				return fmt.Errorf("h2h1: HPACK table size update %d exceeds SETTINGS_HEADER_TABLE_SIZE %d",
					update, d.allowedMax)
			}
			d.tableMax = uint32(update)
			d.evictTo(d.tableMax)

		case b&0xf0 == 0x10: // Literal never indexed.
			sawHeader = true
			name, value, err := d.decodeLiteral(block, &pos, 4)
			if err != nil {
				return err
			}
			decoded = append(decoded, HeaderField{Name: name, Value: value, Sensitive: true})

		default: // 0000: Literal without indexing.
			sawHeader = true
			name, value, err := d.decodeLiteral(block, &pos, 4)
			if err != nil {
				return err
			}
			decoded = append(decoded, HeaderField{Name: name, Value: value})
		}
	}
	*dst = append(*dst, decoded...)
	return nil
}

func (d *HPACKDecoder) decodeLiteral(block []byte, pos *int, namePrefix uint8) (string, string, error) {
	nameIndex, err := readHPACKInt(block, pos, namePrefix)
	if err != nil {
		return "", "", err
	}
	var name string
	if nameIndex == 0 {
		name, err = readHPACKString(block, pos)
		if err != nil {
			return "", "", err
		}
	} else {
		f, lookupErr := d.lookup(nameIndex)
		if lookupErr != nil {
			return "", "", lookupErr
		}
		name = f.Name
	}
	value, err := readHPACKString(block, pos)
	if err != nil {
		return "", "", err
	}
	return name, value, nil
}

func (d *HPACKDecoder) lookup(index uint64) (HeaderField, error) {
	if index == 0 {
		return HeaderField{}, errors.New("h2h1: HPACK index 0 is invalid")
	}
	if index <= hpackStaticTableLen {
		return StaticTable[index], nil
	}
	dynamicIndex := index - hpackStaticTableLen - 1
	if dynamicIndex >= uint64(len(d.dynamic)) {
		return HeaderField{}, fmt.Errorf("h2h1: HPACK index %d is outside static and dynamic tables", index)
	}
	// HPACK numbers the newest dynamic entry 62.
	return d.dynamic[len(d.dynamic)-1-int(dynamicIndex)].field, nil
}

func (d *HPACKDecoder) add(field HeaderField) {
	size := uint32(len(field.Name)) + uint32(len(field.Value)) + 32
	d.evictToForEntry(d.tableMax, size)
	if size > d.tableMax {
		return
	}
	d.dynamic = append(d.dynamic, dynamicEntry{field: field, size: size})
	d.tableSize += size
}

func (d *HPACKDecoder) evictTo(limit uint32) {
	for len(d.dynamic) > 0 && d.tableSize > limit {
		d.evictOldest()
	}
}

func (d *HPACKDecoder) evictToForEntry(limit, incoming uint32) {
	for len(d.dynamic) > 0 && d.tableSize+incoming > limit {
		d.evictOldest()
	}
}

func (d *HPACKDecoder) evictOldest() {
	e := d.dynamic[0]
	d.dynamic = d.dynamic[1:]
	d.tableSize -= e.size
}

func readHPACKInt(data []byte, pos *int, prefix uint8) (uint64, error) {
	if *pos >= len(data) {
		return 0, errors.New("h2h1: truncated HPACK integer")
	}
	mask := uint64((1 << prefix) - 1)
	value := uint64(data[*pos]) & mask
	(*pos)++
	if value < mask {
		return value, nil
	}
	var shift uint
	for {
		if *pos >= len(data) {
			return 0, errors.New("h2h1: truncated HPACK integer continuation")
		}
		b := data[*pos]
		(*pos)++
		if shift >= 63 || (shift == 56 && b&0xfe != 0) {
			return 0, errors.New("h2h1: HPACK integer overflow")
		}
		value += uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, nil
		}
		shift += 7
		if shift > 63 {
			return 0, errors.New("h2h1: HPACK integer overflow")
		}
	}
}

func readHPACKString(data []byte, pos *int) (string, error) {
	if *pos >= len(data) {
		return "", errors.New("h2h1: truncated HPACK string")
	}
	huffman := data[*pos]&0x80 != 0
	length, err := readHPACKInt(data, pos, 7)
	if err != nil {
		return "", err
	}
	if length > uint64(len(data)-*pos) {
		return "", fmt.Errorf("h2h1: HPACK string length %d exceeds remaining block", length)
	}
	n := int(length)
	raw := data[*pos : *pos+n]
	*pos += n
	if huffman {
		decoded, decodeErr := HuffmanDecode(raw)
		if decodeErr != nil {
			return "", decodeErr
		}
		return string(decoded), nil
	}
	return string(raw), nil
}
