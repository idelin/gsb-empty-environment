package netsec

import "errors"

// DefaultMaxHeaderListSize mirrors the default header list size used by common
// HTTP/2 stacks (16 MiB).
const DefaultMaxHeaderListSize = 16 << 20

// HPACK errors. All of these are treated as COMPRESSION_ERROR stream/connection
// failures by the converter.
var (
	errHPACKIndex          = errors.New("netsec/hpack: invalid indexed representation")
	errHPACKInteger        = errors.New("netsec/hpack: truncated integer representation")
	errHPACKString         = errors.New("netsec/hpack: truncated string literal")
	errHPACKHuffman        = errors.New("netsec/hpack: invalid Huffman encoding")
	errHPACKTableSize      = errors.New("netsec/hpack: dynamic table size update exceeds limit")
	errHPACKHeaderListSize = errors.New("netsec/hpack: header list too large")
)

// hpackHeaderField is one decoded HPACK header entry.
type hpackHeaderField struct {
	Name  string
	Value string
}

// hpackStaticTable is the static table from RFC 7541 Appendix A.
var hpackStaticTable = []hpackHeaderField{
	{":authority", ""},                   // 1
	{":method", "GET"},                   // 2
	{":method", "POST"},                  // 3
	{":path", "/"},                       // 4
	{":path", "/index.html"},             // 5
	{":scheme", "http"},                  // 6
	{":scheme", "https"},                 // 7
	{":status", "200"},                   // 8
	{":status", "204"},                   // 9
	{":status", "206"},                   // 10
	{":status", "304"},                   // 11
	{":status", "400"},                   // 12
	{":status", "404"},                   // 13
	{":status", "500"},                   // 14
	{"accept-charset", ""},               // 15
	{"accept-encoding", "gzip, deflate"}, // 16
	{"accept-language", ""},              // 17
	{"accept-ranges", ""},                // 18
	{"accept", ""},                       // 19
	{"access-control-allow-origin", ""},  // 20
	{"age", ""},                          // 21
	{"allow", ""},                        // 22
	{"authorization", ""},                // 23
	{"cache-control", ""},                // 24
	{"content-disposition", ""},          // 25
	{"content-encoding", ""},             // 26
	{"content-language", ""},             // 27
	{"content-length", ""},               // 28
	{"content-location", ""},             // 29
	{"content-range", ""},                // 30
	{"content-type", ""},                 // 31
	{"cookie", ""},                       // 32
	{"date", ""},                         // 33
	{"etag", ""},                         // 34
	{"expect", ""},                       // 35
	{"expires", ""},                      // 36
	{"from", ""},                         // 37
	{"host", ""},                         // 38
	{"if-match", ""},                     // 39
	{"if-modified-since", ""},            // 40
	{"if-none-match", ""},                // 41
	{"if-range", ""},                     // 42
	{"if-unmodified-since", ""},          // 43
	{"last-modified", ""},                // 44
	{"link", ""},                         // 45
	{"location", ""},                     // 46
	{"max-forwards", ""},                 // 47
	{"proxy-authenticate", ""},           // 48
	{"proxy-authorization", ""},          // 49
	{"range", ""},                        // 50
	{"referer", ""},                      // 51
	{"refresh", ""},                      // 52
	{"retry-after", ""},                  // 53
	{"server", ""},                       // 54
	{"set-cookie", ""},                   // 55
	{"strict-transport-security", ""},    // 56
	{"transfer-encoding", ""},            // 57
	{"user-agent", ""},                   // 58
	{"vary", ""},                         // 59
	{"via", ""},                          // 60
	{"www-authenticate", ""},             // 61
}

// hpackDecoder implements RFC 7541 header block decoding. A decoder instance
// owns one dynamic table and must be used by a single goroutine.
type hpackDecoder struct {
	dynamic        []hpackHeaderField // newest entry last
	dynamicSize    int
	maxTableSize   uint32
	headerListSize int
	maxHeaderList  int
}

func newHPACKDecoder(maxTableSize, maxHeaderList int) *hpackDecoder {
	if maxTableSize < 0 {
		maxTableSize = 0
	}
	if maxHeaderList <= 0 {
		maxHeaderList = DefaultMaxHeaderListSize
	}
	return &hpackDecoder{
		maxTableSize:  uint32(maxTableSize),
		maxHeaderList: maxHeaderList,
	}
}

// Decode expands one complete header block (a HEADERS frame plus any
// CONTINUATION fragments) into a flat header field list.
func (d *hpackDecoder) Decode(block []byte) ([]hpackHeaderField, error) {
	fields := make([]hpackHeaderField, 0, 16)
	p := block
	for len(p) > 0 {
		b := p[0]
		switch {
		case b&0x80 != 0:
			// Section 6.1: indexed header field representation.
			idx, rest, err := readHPACKInt(p, 7)
			if err != nil {
				return nil, err
			}
			hf, err := d.at(idx)
			if err != nil {
				return nil, err
			}
			p = rest
			fields = append(fields, hf)
			if err := d.addSize(hf); err != nil {
				return nil, err
			}
		case b&0x40 != 0:
			// Section 6.2.1: literal with incremental indexing.
			idx, rest, err := readHPACKInt(p, 6)
			if err != nil {
				return nil, err
			}
			hf, rest, err := d.readLiteral(rest, idx)
			if err != nil {
				return nil, err
			}
			p = rest
			d.addEntry(hf)
			fields = append(fields, hf)
			if err := d.addSize(hf); err != nil {
				return nil, err
			}
		case b&0xe0 == 0x20:
			// Section 6.3: dynamic table size update.
			size, rest, err := readHPACKInt(p, 5)
			if err != nil {
				return nil, err
			}
			if size > uint64(d.maxTableSize) {
				return nil, errHPACKTableSize
			}
			d.setMaxTableSize(size)
			p = rest
		default:
			// Section 6.2.2/6.2.3: literal without indexing / never indexed.
			idx, rest, err := readHPACKInt(p, 4)
			if err != nil {
				return nil, err
			}
			hf, rest, err := d.readLiteral(rest, idx)
			if err != nil {
				return nil, err
			}
			p = rest
			fields = append(fields, hf)
			if err := d.addSize(hf); err != nil {
				return nil, err
			}
		}
	}
	return fields, nil
}

func (d *hpackDecoder) addSize(hf hpackHeaderField) error {
	// RFC 7541 section 4.1: each entry costs name + value + 32 octets.
	d.headerListSize += len(hf.Name) + len(hf.Value) + 32
	if d.headerListSize > d.maxHeaderList {
		return errHPACKHeaderListSize
	}
	return nil
}

func (d *hpackDecoder) at(index uint64) (hpackHeaderField, error) {
	if index == 0 {
		return hpackHeaderField{}, errHPACKIndex
	}
	if int(index) <= len(hpackStaticTable) {
		return hpackStaticTable[index-1], nil
	}
	dynIdx := int(index) - len(hpackStaticTable) - 1
	if dynIdx >= len(d.dynamic) {
		return hpackHeaderField{}, errHPACKIndex
	}
	// Dynamic table is stored newest-last; HPACK indices address newest-first.
	return d.dynamic[len(d.dynamic)-1-dynIdx], nil
}

func (d *hpackDecoder) readLiteral(p []byte, nameIndex uint64) (hpackHeaderField, []byte, error) {
	var hf hpackHeaderField
	if nameIndex != 0 {
		ref, err := d.at(nameIndex)
		if err != nil {
			return hf, nil, err
		}
		hf.Name = ref.Name
	} else {
		name, rest, err := readHPACKString(p)
		if err != nil {
			return hf, nil, err
		}
		hf.Name = name
		p = rest
	}
	value, rest, err := readHPACKString(p)
	if err != nil {
		return hf, nil, err
	}
	hf.Value = value
	return hf, rest, nil
}

func (d *hpackDecoder) addEntry(hf hpackHeaderField) {
	entrySize := len(hf.Name) + len(hf.Value) + 32
	for uint32(d.dynamicSize+entrySize) > d.maxTableSize && len(d.dynamic) > 0 {
		d.evict()
	}
	// An entry larger than the table size is not inserted (RFC 7541 4.4).
	if uint32(entrySize) <= d.maxTableSize {
		d.dynamic = append(d.dynamic, hf)
		d.dynamicSize += entrySize
	}
}

func (d *hpackDecoder) evict() {
	if len(d.dynamic) == 0 {
		return
	}
	oldest := d.dynamic[0]
	d.dynamicSize -= len(oldest.Name) + len(oldest.Value) + 32
	d.dynamic = d.dynamic[1:]
}

func (d *hpackDecoder) setMaxTableSize(size uint64) {
	d.maxTableSize = uint32(size)
	for len(d.dynamic) > 0 && uint32(d.dynamicSize) > d.maxTableSize {
		d.evict()
	}
}

// readHPACKInt decodes an HPACK integer (RFC 7541 section 5.1). prefixBits is
// the number of payload bits in the first byte (7, 6, 5 or 4).
func readHPACKInt(p []byte, prefixBits uint) (uint64, []byte, error) {
	if len(p) == 0 {
		return 0, nil, errHPACKInteger
	}
	mask := uint64(1<<prefixBits) - 1
	value := uint64(p[0]) & mask
	if value < mask {
		return value, p[1:], nil
	}
	var shift uint
	for i := 1; i < len(p); i++ {
		b := p[i]
		if shift >= 64-7 {
			return 0, nil, errHPACKInteger
		}
		value += uint64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			return value, p[i+1:], nil
		}
	}
	return 0, nil, errHPACKInteger
}

func readHPACKString(p []byte) (string, []byte, error) {
	if len(p) == 0 {
		return "", nil, errHPACKString
	}
	huffman := p[0]&0x80 != 0
	length, rest, err := readHPACKInt(p, 7)
	if err != nil {
		return "", nil, err
	}
	if length > uint64(len(rest)) {
		return "", nil, errHPACKString
	}
	raw := rest[:length]
	rest = rest[length:]
	if !huffman {
		return string(raw), rest, nil
	}
	decoded, err := huffmanDecode(raw)
	if err != nil {
		return "", nil, err
	}
	return string(decoded), rest, nil
}
