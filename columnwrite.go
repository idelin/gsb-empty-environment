package parquetcol

// columnChunkWriter builds one Parquet v1 column chunk from shredded data.
type columnChunkWriter struct {
	node     *Node
	col      *columnData
	stats    *stats
	pageSize int
	maxDict  int

	entryPos    int // next entry to buffer
	pageStart   int // first entry of the in-progress page
	definedPos  int // defined-value cursor for entries before entryPos
	pageEncoded int // estimated payload bytes of the in-progress page

	dict          *dictionaryBuilder
	useDictionary bool
	dictWritten   bool

	startOffset   int64
	dictOffset    int64
	firstDataPage int64
	totalSize     int64
}

func newColumnChunkWriter(node *Node, col *columnData, pageSize, maxDict int) *columnChunkWriter {
	cw := &columnChunkWriter{
		node:          node,
		col:           col,
		stats:         newStats(node.Physical),
		pageSize:      pageSize,
		maxDict:       maxDict,
		dictOffset:    -1,
		useDictionary: node.Physical != TypeBoolean,
	}
	if cw.useDictionary {
		cw.dict = newDictionaryBuilder(node.Physical)
	}
	return cw
}

func (cw *columnChunkWriter) writeTo(fw *fileWriter) (*thriftColumnChunk, error) {
	cw.startOffset = fw.offset()
	n := len(cw.col.defs)
	if n == 0 {
		return nil, errCorrupt("column chunk with zero entries")
	}
	if cw.useDictionary && cw.dictionaryExceedsCap() {
		cw.useDictionary = false
		cw.dict = nil
	} else if cw.useDictionary {
		// Build the complete dictionary up front so every data page can safely
		// reference indices that are all present in the dictionary page.
		for _, v := range cw.pageDefinedValues(0, n) {
			cw.dict.add(v)
		}
	}
	for cw.entryPos < n {
		if err := cw.bufferEntry(); err != nil {
			return nil, err
		}
		if cw.pageEncoded >= cw.pageSize && cw.entryPos < n {
			if err := cw.flushPage(fw); err != nil {
				return nil, err
			}
		}
	}
	if cw.entryPos > cw.pageStart {
		if err := cw.flushPage(fw); err != nil {
			return nil, err
		}
	}

	encodings := []int32{encPlain, encRLE, encBitPacked}
	if cw.dictWritten {
		encodings = []int32{encPlain, encPlainDictionary, encRLE, encRLEDictionary, encBitPacked}
	}
	meta := &thriftColumnMetaData{
		type_:             cw.node.Physical.thriftType(),
		encodings:         encodings,
		pathInSchema:      nodePath(cw.node),
		codec:             codecUncompressed,
		numValues:         int64(n),
		totalUncompressed: cw.totalSize,
		totalCompressed:   cw.totalSize,
		dataPageOffset:    cw.firstDataPage,
		dictionaryOffset:  cw.dictOffset,
		stats: &thriftStatistics{
			max:         cw.stats.max,
			min:         cw.stats.min,
			nullCount:   cw.stats.nullCount,
			distinctCnt: int64(len(cw.stats.distinct)),
		},
	}
	return &thriftColumnChunk{fileOffset: cw.chunkFileOffset(), metaData: meta}, nil
}

func (cw *columnChunkWriter) chunkFileOffset() int64 {
	if cw.dictOffset >= 0 {
		return cw.dictOffset
	}
	return cw.firstDataPage
}

func (cw *columnChunkWriter) bufferEntry() error {
	idx := cw.entryPos
	def := cw.col.defs[idx]
	if def >= cw.node.maxDef {
		val := cw.col.vals[cw.definedPos]
		cw.stats.addValue(val)
		cw.pageEncoded += len(plainEncode(cw.node.Physical, []Value{val}))
		if cw.useDictionary {
			cw.dict.add(val)
		}
		cw.definedPos++
	} else {
		cw.stats.addNull()
	}
	cw.pageEncoded += levelEntryBytes(cw.node)
	cw.entryPos++
	return nil
}

func levelEntryBytes(node *Node) int {
	repW := bitWidth(node.maxRep)
	defW := bitWidth(node.maxDef)
	return (repW+7)/8 + (defW+7)/8
}

// dictionaryExceedsCap reports whether the dictionary over all defined values
// exceeds the configured byte budget. The decision is made before any page is
// written so a chunk never mixes dictionary and PLAIN pages.
func (cw *columnChunkWriter) dictionaryExceedsCap() bool {
	builder := newDictionaryBuilder(cw.node.Physical)
	totalBytes := 0
	defined := cw.pageDefinedValues(0, len(cw.col.defs))
	for _, v := range defined {
		builder.add(v)
		totalBytes += len(plainEncode(cw.node.Physical, []Value{v}))
		if builder.size() > cw.maxDict || totalBytes > cw.maxDict {
			return true
		}
	}
	return false
}

func (cw *columnChunkWriter) flushPage(fw *fileWriter) error {
	if cw.dictWritten == false && cw.useDictionary && cw.dict != nil && cw.dict.size() > 0 {
		if err := cw.writeDictionaryPage(fw); err != nil {
			return err
		}
	}

	start, end := cw.pageStart, cw.entryPos
	reps := cw.col.reps[start:end]
	defs := cw.col.defs[start:end]
	repW := bitWidth(cw.node.maxRep)
	defW := bitWidth(cw.node.maxDef)

	var payload []byte
	if repW > 0 {
		repBytes := encodeRLEBitPackedHybrid(reps, repW)
		payload = append(payload, u32LE(len(repBytes))...)
		payload = append(payload, repBytes...)
	}
	if defW > 0 {
		defBytes := encodeRLEBitPackedHybrid(defs, defW)
		payload = append(payload, u32LE(len(defBytes))...)
		payload = append(payload, defBytes...)
	}

	definedVals := cw.pageDefinedValues(start, end)
	encoding := int32(encPlain)
	if cw.dictWritten {
		encoding = encRLEDictionary
		indices := make([]int, len(definedVals))
		for i, v := range definedVals {
			key := string(plainEncode(cw.node.Physical, []Value{v}))
			indices[i] = cw.dict.lookup[key]
		}
		width := bitWidth(cw.dict.size() - 1)
		payload = append(payload, encodeRLEBitPackedHybrid(indices, width)...)
	} else {
		payload = append(payload, plainEncode(cw.node.Physical, definedVals)...)
	}

	header := &thriftPageHeader{
		type_:            pageDataPage,
		uncompressedSize: int32(len(payload)),
		compressedSize:   int32(len(payload)),
		dataPage: &thriftDataPageHeader{
			numValues:               int32(end - start),
			encoding:                encoding,
			definitionLevelEncoding: encRLE,
			repetitionLevelEncoding: encRLE,
		},
	}
	pageStart := fw.offset()
	if cw.firstDataPage == 0 {
		cw.firstDataPage = pageStart
	}
	if err := cw.writePage(fw, header, payload); err != nil {
		return err
	}
	cw.pageStart = end
	cw.pageEncoded = 0
	return nil
}

func (cw *columnChunkWriter) writeDictionaryPage(fw *fileWriter) error {
	dictBytes := plainEncode(cw.node.Physical, cw.dict.dict)
	header := &thriftPageHeader{
		type_:            pageDictionaryPage,
		uncompressedSize: int32(len(dictBytes)),
		compressedSize:   int32(len(dictBytes)),
		dictionaryPage: &thriftDictionaryPageHeader{
			numValues: int32(cw.dict.size()),
			encoding:  encPlain,
		},
	}
	cw.dictOffset = fw.offset()
	if err := cw.writePage(fw, header, dictBytes); err != nil {
		return err
	}
	cw.dictWritten = true
	return nil
}

func (cw *columnChunkWriter) writePage(fw *fileWriter, header *thriftPageHeader, payload []byte) error {
	hw := newCompactWriter()
	header.write(hw)
	before := fw.offset()
	if _, err := fw.write(hw.buf); err != nil {
		return err
	}
	if _, err := fw.write(payload); err != nil {
		return err
	}
	cw.totalSize += fw.offset() - before
	return nil
}

// pageDefinedValues returns the defined values for entries [start,end) using
// the fact that defined values appear in column order.
func (cw *columnChunkWriter) pageDefinedValues(start, end int) []Value {
	skipBefore := 0
	for i := 0; i < start; i++ {
		if cw.col.defs[i] >= cw.node.maxDef {
			skipBefore++
		}
	}
	count := 0
	for i := start; i < end; i++ {
		if cw.col.defs[i] >= cw.node.maxDef {
			count++
		}
	}
	return cw.col.vals[skipBefore : skipBefore+count]
}

func u32LE(n int) []byte {
	b := make([]byte, 4)
	b[0] = byte(n)
	b[1] = byte(n >> 8)
	b[2] = byte(n >> 16)
	b[3] = byte(n >> 24)
	return b
}
