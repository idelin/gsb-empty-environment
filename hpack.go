package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// Config controls limits and header policy for Decoder. A zero value
// selects the default. Negative limits disable the corresponding limit.
type Config struct {
	// MaxDynamicTableSize is the upper bound accepted by dynamic table size
	// updates. Zero means 4096.
	MaxDynamicTableSize uint32

	// MaxCompressedBlockBytes bounds the sum of all HEADERS and CONTINUATION
	// fragments in one header block. Zero means 1 MiB; negative is unlimited.
	MaxCompressedBlockBytes int

	// MaxDecompressedBytes bounds name+value bytes across one header block.
	// Zero means 1 MiB; negative is unlimited.
	MaxDecompressedBytes int

	// MaxHeaderListSize bounds the RFC 7540 header list size (name+value+32
	// per field) across one block. Zero means 1 MiB; negative is unlimited.
	MaxHeaderListSize int

	// MaxHeaderCount bounds fields in one block. Zero means 1000; negative
	// is unlimited.
	MaxHeaderCount int

	// MaxStringLength bounds each decoded name or value. Zero means 64 KiB;
	// negative is unlimited.
	MaxStringLength int

	// SensitiveHeaders must not be inserted into the dynamic table and are
	// marked Sensitive in results. A nil slice selects the default set.
	SensitiveHeaders []string

	// SingletonHeaders may appear at most once in a block. A nil slice
	// selects a conservative default set.
	SingletonHeaders []string

	// AllowUppercaseNames disables the lowercase-name requirement. It
	// defaults to false because HTTP/2 field names are lowercase.
	AllowUppercaseNames bool

	// CommitHistory is the number of successful TableCommit records retained
	// by the decoder. Zero means 128; negative retains all records.
	CommitHistory int
}

const (
	defaultDynamicTableSize  = 4096
	defaultCompressedBytes   = 1 << 20
	defaultDecompressedBytes = 1 << 20
	defaultHeaderListSize    = 1 << 20
	defaultHeaderCount       = 1000
	defaultStringLength      = 64 << 10
	defaultCommitHistory     = 128
)

func defaultSensitiveHeaders() []string {
	return []string{"authorization", "proxy-authorization", "cookie", "set-cookie"}
}

func defaultSingletonHeaders() []string {
	return []string{"content-length", "host", "authorization", "proxy-authorization"}
}

func normalizedConfig(c Config) Config {
	if c.MaxDynamicTableSize == 0 {
		c.MaxDynamicTableSize = defaultDynamicTableSize
	}
	if c.MaxCompressedBlockBytes == 0 {
		c.MaxCompressedBlockBytes = defaultCompressedBytes
	}
	if c.MaxDecompressedBytes == 0 {
		c.MaxDecompressedBytes = defaultDecompressedBytes
	}
	if c.MaxHeaderListSize == 0 {
		c.MaxHeaderListSize = defaultHeaderListSize
	}
	if c.MaxHeaderCount == 0 {
		c.MaxHeaderCount = defaultHeaderCount
	}
	if c.MaxStringLength == 0 {
		c.MaxStringLength = defaultStringLength
	}
	if c.SensitiveHeaders == nil {
		c.SensitiveHeaders = defaultSensitiveHeaders()
	} else {
		c.SensitiveHeaders = append([]string(nil), c.SensitiveHeaders...)
	}
	if c.SingletonHeaders == nil {
		c.SingletonHeaders = defaultSingletonHeaders()
	} else {
		c.SingletonHeaders = append([]string(nil), c.SingletonHeaders...)
	}
	if c.CommitHistory == 0 {
		c.CommitHistory = defaultCommitHistory
	}
	return c
}

func stringSet(v []string) map[string]struct{} {
	m := make(map[string]struct{}, len(v))
	for _, s := range v {
		m[strings.ToLower(s)] = struct{}{}
	}
	return m
}

var pseudoHeaderNames = map[string]struct{}{
	":authority": {},
	":method":    {},
	":path":      {},
	":protocol":  {},
	":scheme":    {},
	":status":    {},
}

type hpackParser struct {
	cfg        Config
	sensitive  map[string]struct{}
	singletons map[string]struct{}
	streamID   uint32
	trailers   bool

	table      *DynamicTable
	fields     []HeaderField
	ops        []TableOp
	evicted    []HeaderField
	seen       map[string]struct{}
	sawField   bool
	sawRegular bool

	decompressed int
	headerList   int
}

func newHPACKParser(cfg Config, table *DynamicTable, streamID uint32, trailers bool) *hpackParser {
	return &hpackParser{
		cfg:        cfg,
		sensitive:  stringSet(cfg.SensitiveHeaders),
		singletons: stringSet(cfg.SingletonHeaders),
		streamID:   streamID,
		trailers:   trailers,
		table:      table,
		seen:       make(map[string]struct{}),
	}
}

var errNeedMore = errors.New("netsec: need more data")
var errVarintOverflow = fmt.Errorf("%w: integer overflow", ErrCompression)

func (p *hpackParser) parse(data []byte) error {
	off := 0
	for off < len(data) {
		b := data[off]
		var err error
		switch {
		case b&0x80 != 0:
			off, err = p.parseIndexed(data, off)
		case b&0xc0 == 0x40:
			off, err = p.parseLiteral(data, off, 6, true, false)
		case b&0xf0 == 0x00:
			off, err = p.parseLiteral(data, off, 4, false, false)
		case b&0xf0 == 0x10:
			off, err = p.parseLiteral(data, off, 4, false, true)
		case b&0xe0 == 0x20:
			off, err = p.parseTableSizeUpdate(data, off)
		default:
			return compressionErr("invalid HPACK representation", ErrInvalidEncoding)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (p *hpackParser) parseIndexed(data []byte, off int) (int, error) {
	idx, next, err := readVarInt(7, data, off)
	if err != nil {
		return off, hpackReadError(err)
	}
	if idx == 0 {
		return off, compressionErr("index 0 is invalid", ErrInvalidIndex)
	}
	field, ok := p.lookup(idx)
	if !ok {
		return off, compressionErr(fmt.Sprintf("index %d is outside the header table", idx), ErrInvalidIndex)
	}
	if err := p.addField(field, false, false); err != nil {
		return off, err
	}
	return next, nil
}

func (p *hpackParser) parseLiteral(data []byte, off int, prefix uint8, incremental, neverIndexed bool) (int, error) {
	nameIdx, next, err := readVarInt(prefix, data, off)
	if err != nil {
		return off, hpackReadError(err)
	}
	var name string
	if nameIdx > 0 {
		field, ok := p.lookup(nameIdx)
		if !ok {
			return off, compressionErr(fmt.Sprintf("index %d is outside the header table", nameIdx), ErrInvalidIndex)
		}
		name = field.Name
	} else {
		name, next, err = p.readString(data, next, "header name")
		if err != nil {
			return off, err
		}
	}
	value, next, err := p.readString(data, next, "header value")
	if err != nil {
		return off, err
	}
	if err := p.addField(HeaderField{Name: name, Value: value, Sensitive: neverIndexed}, incremental, neverIndexed); err != nil {
		return off, err
	}
	return next, nil
}

func (p *hpackParser) parseTableSizeUpdate(data []byte, off int) (int, error) {
	if p.sawField {
		return off, compressionErr("dynamic table size update is not at the beginning of the block", ErrDynamicTableSize)
	}
	size, next, err := readVarInt(5, data, off)
	if err != nil {
		return off, hpackReadError(err)
	}
	if size > uint64(p.table.allowedMaxSize) {
		return off, compressionErr(fmt.Sprintf("dynamic table size %d exceeds allowed %d", size, p.table.allowedMaxSize), ErrDynamicTableSize)
	}
	evicted := p.table.setMaxSize(uint32(size))
	p.ops = append(p.ops, TableOp{Kind: TableOpSetMaxSize, MaxSize: uint32(size)})
	p.evicted = append(p.evicted, evicted...)
	return next, nil
}

func (p *hpackParser) lookup(idx uint64) (HeaderField, bool) {
	if idx <= staticTableLen {
		return tableAt(idx)
	}
	return p.table.at(idx - staticTableLen)
}

func (p *hpackParser) readString(data []byte, off int, what string) (string, int, error) {
	if off >= len(data) {
		return "", off, compressionErr("truncated "+what, ErrTruncatedBlock)
	}
	huffman := data[off]&0x80 != 0
	n, next, err := readVarInt(7, data, off)
	if err != nil {
		return "", off, hpackReadError(err)
	}
	if n > uint64(len(data)-next) {
		return "", off, compressionErr("truncated "+what, ErrTruncatedBlock)
	}
	raw := data[next : next+int(n)]
	next += int(n)
	if huffman {
		out, err := huffmanDecode(raw, p.cfg.MaxStringLength)
		if err != nil {
			if errors.Is(err, ErrLimitExceeded) {
				return "", off, streamErr(p.streamID, CodeEnhanceYourCalm, "Huffman string exceeds configured maximum", err)
			}
			return "", off, compressionErr("invalid Huffman string", ErrInvalidHuffman)
		}
		return out, next, nil
	}
	if p.cfg.MaxStringLength > 0 && len(raw) > p.cfg.MaxStringLength {
		return "", off, streamErr(p.streamID, CodeEnhanceYourCalm, "string exceeds configured maximum", ErrLimitExceeded)
	}
	return string(raw), next, nil
}

func (p *hpackParser) addField(f HeaderField, incremental, neverIndexed bool) error {
	if f.Name == "" {
		return streamErr(p.streamID, CodeProtocol, "empty header name", ErrInvalidHeaderName)
	}
	if !p.cfg.AllowUppercaseNames && hasUppercase(f.Name) {
		return streamErr(p.streamID, CodeProtocol, "header name contains uppercase", ErrUppercaseHeader)
	}
	if !validHeaderName(f.Name) {
		return streamErr(p.streamID, CodeProtocol, "header name contains an invalid character", ErrInvalidHeaderName)
	}
	nameKey := f.Name
	if !f.IsPseudo() {
		nameKey = strings.ToLower(f.Name)
	}
	if f.IsPseudo() {
		if p.trailers {
			return streamErr(p.streamID, CodeProtocol, "pseudo-header in trailers", ErrPseudoHeaderOrder)
		}
		if p.sawRegular {
			return streamErr(p.streamID, CodeProtocol, "pseudo-header follows a regular header", ErrPseudoHeaderOrder)
		}
		if _, ok := pseudoHeaderNames[f.Name]; !ok {
			return streamErr(p.streamID, CodeProtocol, "unknown pseudo-header", ErrInvalidHeaderName)
		}
		if _, ok := p.seen[nameKey]; ok {
			return streamErr(p.streamID, CodeProtocol, "duplicate pseudo-header", ErrDuplicateHeader)
		}
	} else {
		p.sawRegular = true
		if _, singleton := p.singletons[nameKey]; singleton {
			if _, ok := p.seen[nameKey]; ok {
				return streamErr(p.streamID, CodeProtocol, "duplicate singleton header", ErrDuplicateHeader)
			}
		}
	}
	if _, ok := p.seen[nameKey]; ok {
		// Regular headers may legally repeat. Only pseudo and configured
		// singleton names reach this branch as an error above.
	} else {
		p.seen[nameKey] = struct{}{}
	}
	p.sawField = true

	isSensitive := neverIndexed || p.isSensitive(f.Name)
	if isSensitive {
		if incremental {
			name := f.Name
			if len(name) > 64 {
				name = name[:64]
			}
			return streamErr(p.streamID, CodeProtocol, "sensitive header was offered for incremental indexing", fmt.Errorf("%w: %s", ErrSensitiveHeaderIndexed, name))
		}
		f.Sensitive = true
	}

	p.decompressed += len(f.Name) + len(f.Value)
	p.headerList += f.HeaderListSize()
	if p.cfg.MaxDecompressedBytes > 0 && p.decompressed > p.cfg.MaxDecompressedBytes {
		return streamErr(p.streamID, CodeEnhanceYourCalm, "decompressed field budget exceeded", ErrLimitExceeded)
	}
	if p.cfg.MaxHeaderListSize > 0 && p.headerList > p.cfg.MaxHeaderListSize {
		return streamErr(p.streamID, CodeEnhanceYourCalm, "header list size budget exceeded", ErrLimitExceeded)
	}
	if p.cfg.MaxHeaderCount > 0 && len(p.fields)+1 > p.cfg.MaxHeaderCount {
		return streamErr(p.streamID, CodeEnhanceYourCalm, "header count budget exceeded", ErrLimitExceeded)
	}

	p.fields = append(p.fields, f)
	if incremental {
		evicted := p.table.Add(f)
		p.ops = append(p.ops, TableOp{Kind: TableOpInsert, Field: f})
		p.evicted = append(p.evicted, evicted...)
	}
	return nil
}

func (p *hpackParser) isSensitive(name string) bool {
	_, ok := p.sensitive[strings.ToLower(name)]
	return ok
}

func hasUppercase(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == ':' {
		if len(name) == 1 {
			return false
		}
		name = name[1:]
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func hpackReadError(err error) error {
	if errors.Is(err, errNeedMore) {
		return compressionErr("truncated header block", ErrTruncatedBlock)
	}
	if errors.Is(err, errVarintOverflow) {
		return compressionErr("HPACK integer overflow", errVarintOverflow)
	}
	return compressionErr("invalid HPACK integer", err)
}

func readVarInt(n uint8, data []byte, off int) (uint64, int, error) {
	if n < 1 || n > 8 {
		return 0, off, errVarintOverflow
	}
	if off >= len(data) {
		return 0, off, errNeedMore
	}
	value := uint64(data[off]) & ((1 << n) - 1)
	off++
	if value < (1<<n)-1 {
		return value, off, nil
	}
	var shift uint
	for {
		if off >= len(data) {
			return 0, off, errNeedMore
		}
		b := data[off]
		off++
		if shift >= 56 {
			return 0, off, errVarintOverflow
		}
		value += uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, off, nil
		}
		shift += 7
	}
}

// decodeBlock validates and decodes a complete block against a clone of the
// current dynamic table. The live table is only replaced after every field
// has passed validation.
func (d *Decoder) decodeBlock(streamID uint32, fragments [][]byte, prefix SecurityContext, blockID uint64, trailers bool) ([]HeaderField, *TableCommit, error) {
	before := d.table.Clone()
	work := d.table.Clone()
	parser := newHPACKParser(d.cfg, work, streamID, trailers)

	total := 0
	for _, f := range fragments {
		total += len(f)
		if d.cfg.MaxCompressedBlockBytes > 0 && total > d.cfg.MaxCompressedBlockBytes {
			return nil, nil, streamErr(streamID, CodeEnhanceYourCalm, "compressed header block budget exceeded", ErrLimitExceeded)
		}
	}
	data := make([]byte, 0, total)
	for _, f := range fragments {
		data = append(data, f...)
	}
	if err := parser.parse(data); err != nil {
		return nil, nil, err
	}

	// Validation and decoding both completed. Publish the cloned table and
	// produce an audit record whose operations can be replayed.
	d.table = work
	auditFields := make([]HeaderField, len(parser.fields))
	for i, f := range parser.fields {
		if f.Sensitive {
			f.Value = "<redacted>"
		}
		auditFields[i] = f
	}
	commit := TableCommit{
		Sequence:           d.commitSequence + 1,
		BlockID:            blockID,
		StreamID:           streamID,
		PeerID:             prefix.PeerConnectionID,
		TenantID:           prefix.TenantID,
		SettingsGeneration: prefix.SettingsGeneration,
		CompressedBytes:    total,
		DecompressedBytes:  parser.decompressed,
		FieldCount:         len(parser.fields),
		Digest:             digestBlock(fragments),
		InitialMaxSize:     before.maxSize,
		AllowedMaxSize:     work.allowedMaxSize,
		MaxSize:            work.maxSize,
		TableSizeBefore:    before.size,
		TableSizeAfter:     work.size,
		TableBefore:        before.Entries(),
		Operations:         append([]TableOp(nil), parser.ops...),
		Evicted:            append([]HeaderField(nil), parser.evicted...),
		Fields:             auditFields,
	}
	d.commitSequence++
	d.recordCommit(commit)
	resultCommit := cloneCommit(commit)
	resultFields := make([]HeaderField, len(parser.fields))
	copy(resultFields, parser.fields)
	return resultFields, &resultCommit, nil
}
