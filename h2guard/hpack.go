package h2guard

// Simplified HPACK (RFC 7541) decoder.
//
// Scope: indexed header field representation (6.1), literal with incremental
// indexing (6.2.1), literal without indexing (6.2.2) and literal never
// indexed (6.2.3), dynamic table size updates (6.3), integer representation
// (5.1) and string literal representation (5.2) without Huffman encoding.
// Huffman-encoded strings are rejected with a COMPRESSION_ERROR: this keeps
// the decoder small enough to audit while still accepting every plain
// (H=0) header block, including those produced by real clients that disable
// Huffman and all blocks built by this package's test helpers.

// HeaderField is one decoded name/value pair, preserving wire order.
type HeaderField struct {
	Name  string
	Value string
}

// hpackEntry is one dynamic table row.
type hpackEntry struct {
	name  string
	value string
}

// hpackDecoder is the per-connection HPACK state. A new instance is created by
// the parser; callers never touch it directly.
type hpackDecoder struct {
	dynamic []hpackEntry // index 1 maps to the last element
	size    uint32       // current dynamic table size per RFC 7541 4.1
	maxSize uint32       // table size acknowledged via SETTINGS
}

func newHPACKDecoder(maxTableSize uint32) *hpackDecoder {
	if maxTableSize == 0 {
		maxTableSize = 4096
	}
	return &hpackDecoder{maxSize: maxTableSize}
}

// hpackStaticTable is the static table from RFC 7541 Appendix A.
var hpackStaticTable = []hpackEntry{
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

// lookup returns the entry for a 1-based HPACK index.
func (d *hpackDecoder) lookup(index uint32) (hpackEntry, bool) {
	if index == 0 {
		return hpackEntry{}, false
	}
	if index <= uint32(len(hpackStaticTable)) {
		return hpackStaticTable[index-1], true
	}
	dyn := index - uint32(len(hpackStaticTable)) - 1
	if dyn >= uint32(len(d.dynamic)) {
		return hpackEntry{}, false
	}
	return d.dynamic[uint32(len(d.dynamic))-1-dyn], true
}

// entrySize implements RFC 7541 section 4.1.
func entrySize(name, value string) uint32 {
	return uint32(len(name) + len(value) + 32)
}

// add inserts an entry at the front of the dynamic table, evicting as required.
func (d *hpackDecoder) add(name, value string) {
	need := entrySize(name, value)
	for d.size+need > d.maxSize && len(d.dynamic) > 0 {
		last := d.dynamic[0]
		d.size -= entrySize(last.name, last.value)
		d.dynamic = d.dynamic[1:]
	}
	// An entry larger than the table size alone is simply not inserted.
	if need <= d.maxSize {
		d.dynamic = append(d.dynamic, hpackEntry{})
		copy(d.dynamic[1:], d.dynamic)
		d.dynamic[0] = hpackEntry{name, value}
		d.size += need
	}
}

// setMaxSize applies a SETTINGS_HEADER_TABLE_SIZE change or an in-block
// dynamic table size update (RFC 7541 4.2/4.3).
func (d *hpackDecoder) setMaxSize(newSize uint32) {
	if newSize > d.maxSize {
		newSize = d.maxSize
	}
	for d.size > newSize && len(d.dynamic) > 0 {
		last := d.dynamic[0]
		d.size -= entrySize(last.name, last.value)
		d.dynamic = d.dynamic[1:]
	}
}

// decodeInteger implements RFC 7541 section 5.1. prefixBits is 5, 6 or 7.
func decodeInteger(buf []byte, pos int, prefixBits uint) (value uint32, next int, err error) {
	if pos >= len(buf) {
		return 0, pos, connError(ErrCodeCompression, "hpack: truncated integer")
	}
	mask := uint32(1<<prefixBits) - 1
	value = uint32(buf[pos]) & mask
	next = pos + 1
	if value < mask {
		return value, next, nil
	}
	var shift uint
	for {
		if next >= len(buf) {
			return 0, next, connError(ErrCodeCompression, "hpack: truncated integer continuation")
		}
		b := buf[next]
		next++
		if shift >= 32 || (uint32(b&0x7f)<<shift)>>(shift) != uint32(b&0x7f) {
			return 0, next, connError(ErrCodeCompression, "hpack: integer overflow")
		}
		value += uint32(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, next, nil
		}
		shift += 7
	}
}

// decodeString implements RFC 7541 section 5.2. Huffman (H=1) is rejected.
func (d *hpackDecoder) decodeString(buf []byte, pos int) (string, int, error) {
	if pos >= len(buf) {
		return "", pos, connError(ErrCodeCompression, "hpack: truncated string header")
	}
	huffman := buf[pos]&0x80 != 0
	length, next, err := decodeInteger(buf, pos, 7)
	if err != nil {
		return "", next, err
	}
	if huffman {
		return "", next, connError(ErrCodeCompression, "hpack: huffman-encoded strings are not supported")
	}
	if uint32(next)+length > uint32(len(buf)) {
		return "", next, connError(ErrCodeCompression, "hpack: string length exceeds block")
	}
	s := string(buf[next : next+int(length)])
	next += int(length)
	return s, next, nil
}

// Decode parses a complete, reassembled header block into an ordered field
// list. It mutates the decoder's dynamic table as required by the encoding.
func (d *hpackDecoder) Decode(block []byte) ([]HeaderField, error) {
	var fields []HeaderField
	pos := 0
	for pos < len(block) {
		b := block[pos]
		switch {
		case b&0x80 != 0:
			// 6.1 indexed header field.
			index, next, err := decodeInteger(block, pos, 7)
			if err != nil {
				return nil, err
			}
			entry, ok := d.lookup(index)
			if !ok {
				return nil, connError(ErrCodeCompression, "hpack: invalid indexed reference %d", index)
			}
			fields = append(fields, HeaderField{entry.name, entry.value})
			pos = next
		case b&0xe0 == 0x20:
			// 6.3 dynamic table size update.
			newSize, next, err := decodeInteger(block, pos, 5)
			if err != nil {
				return nil, err
			}
			if newSize > d.maxSize {
				return nil, connError(ErrCodeCompression, "hpack: table size update above SETTINGS limit")
			}
			d.setMaxSize(newSize)
			pos = next
		case b&0xc0 == 0x40 || b&0xe0 == 0x00 || b&0xf0 == 0x00:
			// 6.2.1 literal with incremental indexing (01......),
			// 6.2.2 literal without indexing (0000....),
			// 6.2.3 literal never indexed (0001....).
			indexing := b&0xc0 == 0x40
			prefixBits := uint(4)
			if indexing {
				prefixBits = 6
			}
			index, next, err := decodeInteger(block, pos, prefixBits)
			if err != nil {
				return nil, err
			}
			var name string
			if index != 0 {
				entry, ok := d.lookup(index)
				if !ok {
					return nil, connError(ErrCodeCompression, "hpack: invalid name reference %d", index)
				}
				name = entry.name
			} else {
				name, next, err = d.decodeString(block, next)
				if err != nil {
					return nil, err
				}
			}
			var value string
			value, next, err = d.decodeString(block, next)
			if err != nil {
				return nil, err
			}
			fields = append(fields, HeaderField{name, value})
			if indexing {
				d.add(name, value)
			}
			pos = next
		default:
			return nil, connError(ErrCodeCompression, "hpack: unknown representation byte 0x%02x", b)
		}
	}
	return fields, nil
}
