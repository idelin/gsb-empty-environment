package parquetcol

import (
	"bytes"
	"io"
)

// Reader reads back rows written by Writer.
type Reader struct {
	data   []byte
	schema *Schema
	meta   *thriftFileMetaData
}

// NewReader parses the Parquet footer and prepares row reads.
func NewReader(r io.ReaderAt, size int64) (*Reader, error) {
	if size < 12 {
		return nil, errCorrupt("file too small")
	}
	data := make([]byte, size)
	if _, err := r.ReadAt(data, 0); err != nil && err != io.EOF {
		return nil, err
	}
	return NewReaderFromBytes(data)
}

// NewReaderFromBytes reads a Parquet file held entirely in memory.
func NewReaderFromBytes(data []byte) (*Reader, error) {
	if len(data) < 12 || !bytes.Equal(data[:4], parquetMagic) ||
		!bytes.Equal(data[len(data)-4:], parquetMagic) {
		return nil, errCorrupt("bad magic bytes")
	}
	footerLen := int(u32FromBytes(data[len(data)-8:]))
	footerStart := len(data) - 8 - footerLen
	if footerStart < 4 {
		return nil, errCorrupt("footer length out of bounds")
	}
	cr := newCompactReader(data[footerStart : footerStart+footerLen])
	meta, err := readFileMetaData(cr)
	if err != nil {
		return nil, err
	}
	root, err := schemaFromElements(meta.schema)
	if err != nil {
		return nil, err
	}
	schema, err := Compile(root)
	if err != nil {
		return nil, err
	}
	return &Reader{data: data, schema: schema, meta: meta}, nil
}

func u32FromBytes(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// Schema returns the reconstructed schema.
func (pr *Reader) Schema() *Schema { return pr.schema }

// NumRowGroups reports the number of row groups.
func (pr *Reader) NumRowGroups() int { return len(pr.meta.rowGroups) }

// NumRows reports the total row count.
func (pr *Reader) NumRows() int64 { return pr.meta.numRows }

// ReadAll reads and assembles every row in the file.
func (pr *Reader) ReadAll() ([]Value, error) {
	var out []Value
	for gi := range pr.meta.rowGroups {
		rows, err := pr.ReadRowGroup(gi)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// ReadRowGroup reads and assembles one row group.
func (pr *Reader) ReadRowGroup(group int) ([]Value, error) {
	if group < 0 || group >= len(pr.meta.rowGroups) {
		return nil, errCorrupt("row group index out of range")
	}
	rg := pr.meta.rowGroups[group]
	if len(rg.columns) != len(pr.schema.leaves) {
		return nil, errCorrupt("column count does not match schema")
	}
	cols := make(map[*Node]*columnData, len(pr.schema.leaves))
	for i, leaf := range pr.schema.leaves {
		chunkEnd := int64(len(pr.data))
		if i+1 < len(rg.columns) {
			next := rg.columns[i+1].fileOffset
			if next > 0 {
				chunkEnd = next
			}
		} else {
			footerLen := int(u32FromBytes(pr.data[len(pr.data)-8:]))
			chunkEnd = int64(len(pr.data) - 8 - footerLen)
		}
		col, err := pr.readColumn(leaf, rg.columns[i].metaData, chunkEnd)
		if err != nil {
			return nil, err
		}
		cols[leaf] = col
	}
	return assemble(pr.schema, cols, int(rg.numRows))
}

func (pr *Reader) readColumn(node *Node, meta *thriftColumnMetaData, chunkEnd int64) (*columnData, error) {
	if meta == nil {
		return nil, errCorrupt("column chunk without metadata")
	}
	if meta.codec != codecUncompressed {
		return nil, errCorrupt("compressed columns are not supported")
	}
	col := newColumnData(node)
	var dictionary []Value

	pos := meta.dataPageOffset
	if meta.dictionaryOffset >= 0 {
		pos = meta.dictionaryOffset
	}
	// Pages run until the next column chunk starts; derive the end lazily by
	// parsing headers, which carry their own payload lengths.
	for {
		if pos >= chunkEnd {
			break
		}
		header, hdrLen, err := pr.readPageHeaderAt(pos)
		if err != nil {
			return nil, err
		}
		payloadStart := pos + int64(hdrLen)
		if payloadStart+int64(header.compressedSize) > chunkEnd {
			return nil, errCorrupt("page payload exceeds column chunk boundary")
		}
		payload := pr.data[payloadStart : payloadStart+int64(header.compressedSize)]
		switch header.type_ {
		case pageDictionaryPage:
			dictionary, err = plainDecode(node.Physical, payload, int(header.dictionaryPage.numValues), node.IsString)
			if err != nil {
				return nil, err
			}
		case pageDataPage:
			if err := pr.readDataPage(node, header.dataPage, payload, dictionary, col); err != nil {
				return nil, err
			}
		default:
			return nil, errCorrupt("unsupported page type")
		}
		pos = payloadStart + int64(header.compressedSize)
		if int64(len(col.defs)) >= meta.numValues {
			break
		}
	}
	if int64(len(col.defs)) != meta.numValues {
		return nil, errCorrupt("column entry count mismatch")
	}
	return col, nil
}

func (pr *Reader) readPageHeaderAt(offset int64) (*thriftPageHeader, int, error) {
	// Thrift structs have no explicit length; compact structs end in the stop
	// byte, but readPageHeader consumes exactly one top-level struct when given
	// a reader starting at the struct begin. Compact protocol has no begin
	// marker, so the reader starts directly at the first field header.
	cr := newCompactReader(pr.data[offset:])
	header, err := readPageHeader(cr)
	if err != nil {
		return nil, 0, err
	}
	return header, cr.pos, nil
}

func (pr *Reader) readDataPage(node *Node, hdr *thriftDataPageHeader, payload []byte, dictionary []Value, col *columnData) error {
	if hdr == nil {
		return errCorrupt("data page without header")
	}
	numValues := int(hdr.numValues)
	pos := 0

	var reps []int
	if node.maxRep > 0 {
		n := int(u32FromBytes(payload[pos:]))
		pos += 4
		repW := bitWidth(node.maxRep)
		dec := newHybridDecoder(payload[pos:pos+n], repW)
		var err error
		reps, err = dec.decodeAll(numValues)
		if err != nil {
			return err
		}
		pos += n
	} else {
		reps = make([]int, numValues)
	}

	var defs []int
	if node.maxDef > 0 {
		n := int(u32FromBytes(payload[pos:]))
		pos += 4
		defW := bitWidth(node.maxDef)
		dec := newHybridDecoder(payload[pos:pos+n], defW)
		var err error
		defs, err = dec.decodeAll(numValues)
		if err != nil {
			return err
		}
		pos += n
	} else {
		defs = make([]int, numValues)
	}

	definedCount := 0
	for _, d := range defs {
		if d >= node.maxDef {
			definedCount++
		}
	}

	var vals []Value
	var err error
	switch hdr.encoding {
	case encPlain:
		vals, err = plainDecode(node.Physical, payload[pos:], definedCount, node.IsString)
	case encRLEDictionary, encPlainDictionary:
		if dictionary == nil {
			return errCorrupt("dictionary-encoded page without dictionary")
		}
		width := bitWidth(len(dictionary) - 1)
		dec := newHybridDecoder(payload[pos:], width)
		indices, err := dec.decodeAll(definedCount)
		if err != nil {
			return err
		}
		vals = make([]Value, 0, definedCount)
		for _, idx := range indices {
			if idx < 0 || idx >= len(dictionary) {
				return errCorrupt("dictionary index out of range")
			}
			vals = append(vals, dictionary[idx])
		}
	default:
		return errCorrupt("unsupported data page encoding")
	}
	if err != nil {
		return err
	}
	if len(vals) != definedCount {
		return errCorrupt("defined value count mismatch")
	}
	col.reps = append(col.reps, reps...)
	col.defs = append(col.defs, defs...)
	col.vals = append(col.vals, vals...)
	return nil
}
