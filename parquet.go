package parquetcol

// Parquet enum values (from parquet.thrift).
const (
	encPlain           int32 = 0
	encPlainDictionary int32 = 2
	encRLE             int32 = 3
	encBitPacked       int32 = 4
	encRLEDictionary   int32 = 8

	pageDataPage       int32 = 0
	pageDictionaryPage int32 = 2

	codecUncompressed int32 = 0

	convUTF8 int32 = 0
	convList int32 = 3
	convEnum int32 = 4
)

var parquetMagic = []byte("PAR1")

// thriftStatistics is the parquet.thrift Statistics struct.
type thriftStatistics struct {
	max         []byte
	min         []byte
	nullCount   int64
	distinctCnt int64
}

func (s *thriftStatistics) write(w *compactWriter) {
	w.writeStructBegin()
	if s.max != nil {
		w.writeBinaryField(1, s.max)
	}
	if s.min != nil {
		w.writeBinaryField(2, s.min)
	}
	if s.nullCount >= 0 {
		w.writeI64Field(3, s.nullCount)
	}
	if s.distinctCnt >= 0 {
		w.writeI64Field(4, s.distinctCnt)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readStatistics(r *compactReader) (*thriftStatistics, error) {
	s := &thriftStatistics{nullCount: -1, distinctCnt: -1}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return s, nil
		}
		switch fh.fid {
		case 1:
			s.max, err = r.readBinary()
		case 2:
			s.min, err = r.readBinary()
		case 3:
			s.nullCount, err = r.readI64()
		case 4:
			s.distinctCnt, err = r.readI64()
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftSchemaElement mirrors parquet.thrift SchemaElement.
type thriftSchemaElement struct {
	typePtr        *int32
	typeLength     int32
	repetitionType *int32
	name           string
	numChildren    int32
	convertedType  *int32
}

func (e *thriftSchemaElement) write(w *compactWriter) {
	w.writeStructBegin()
	if e.typePtr != nil {
		w.writeI32Field(1, *e.typePtr)
	}
	if e.repetitionType != nil {
		w.writeI32Field(3, *e.repetitionType)
	}
	w.writeStringField(4, e.name)
	if e.numChildren != 0 {
		w.writeI32Field(5, e.numChildren)
	}
	if e.convertedType != nil {
		w.writeI32Field(6, *e.convertedType)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readSchemaElement(r *compactReader) (*thriftSchemaElement, error) {
	e := &thriftSchemaElement{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return e, nil
		}
		switch fh.fid {
		case 1:
			v, err := r.readI32()
			if err != nil {
				return nil, err
			}
			e.typePtr = &v
		case 2:
			e.typeLength, err = r.readI32()
		case 3:
			v, err := r.readI32()
			if err != nil {
				return nil, err
			}
			e.repetitionType = &v
		case 4:
			b, err := r.readBinary()
			if err != nil {
				return nil, err
			}
			e.name = string(b)
		case 5:
			e.numChildren, err = r.readI32()
		case 6:
			v, err := r.readI32()
			if err != nil {
				return nil, err
			}
			e.convertedType = &v
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftDataPageHeader mirrors parquet.thrift DataPageHeader.
type thriftDataPageHeader struct {
	numValues               int32
	encoding                int32
	definitionLevelEncoding int32
	repetitionLevelEncoding int32
	statistics              *thriftStatistics
}

func (h *thriftDataPageHeader) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI32Field(1, h.numValues)
	w.writeI32Field(2, h.encoding)
	w.writeI32Field(3, h.definitionLevelEncoding)
	w.writeI32Field(4, h.repetitionLevelEncoding)
	if h.statistics != nil {
		w.writeStructField(5, h.statistics.write)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readDataPageHeader(r *compactReader) (*thriftDataPageHeader, error) {
	h := &thriftDataPageHeader{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return h, nil
		}
		switch fh.fid {
		case 1:
			h.numValues, err = r.readI32()
		case 2:
			h.encoding, err = r.readI32()
		case 3:
			h.definitionLevelEncoding, err = r.readI32()
		case 4:
			h.repetitionLevelEncoding, err = r.readI32()
		case 5:
			h.statistics, err = readStatistics(r)
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftDictionaryPageHeader mirrors parquet.thrift DictionaryPageHeader.
type thriftDictionaryPageHeader struct {
	numValues int32
	encoding  int32
	isSorted  bool
}

func (h *thriftDictionaryPageHeader) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI32Field(1, h.numValues)
	w.writeI32Field(2, h.encoding)
	if h.isSorted {
		w.writeBoolField(3, true)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readDictionaryPageHeader(r *compactReader) (*thriftDictionaryPageHeader, error) {
	h := &thriftDictionaryPageHeader{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return h, nil
		}
		switch fh.fid {
		case 1:
			h.numValues, err = r.readI32()
		case 2:
			h.encoding, err = r.readI32()
		case 3:
			b, err := r.readByte()
			if err != nil {
				return nil, err
			}
			h.isSorted = b == ctTrue
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftPageHeader mirrors parquet.thrift PageHeader.
type thriftPageHeader struct {
	type_            int32
	uncompressedSize int32
	compressedSize   int32
	dataPage         *thriftDataPageHeader
	dictionaryPage   *thriftDictionaryPageHeader
}

func (h *thriftPageHeader) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI32Field(1, h.type_)
	w.writeI32Field(2, h.uncompressedSize)
	w.writeI32Field(3, h.compressedSize)
	if h.dataPage != nil {
		w.writeStructField(5, h.dataPage.write)
	}
	if h.dictionaryPage != nil {
		w.writeStructField(7, h.dictionaryPage.write)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readPageHeader(r *compactReader) (*thriftPageHeader, error) {
	h := &thriftPageHeader{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return h, nil
		}
		switch fh.fid {
		case 1:
			h.type_, err = r.readI32()
		case 2:
			h.uncompressedSize, err = r.readI32()
		case 3:
			h.compressedSize, err = r.readI32()
		case 5:
			h.dataPage, err = readDataPageHeader(r)
		case 6:
			err = r.skipValue(fh.typ)
		case 7:
			h.dictionaryPage, err = readDictionaryPageHeader(r)
		case 8:
			err = r.skipValue(fh.typ)
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftKeyValue mirrors parquet.thrift KeyValue.
type thriftKeyValue struct {
	key   string
	value *string
}

func (kv *thriftKeyValue) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeStringField(1, kv.key)
	if kv.value != nil {
		w.writeStringField(2, *kv.value)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readKeyValue(r *compactReader) (*thriftKeyValue, error) {
	kv := &thriftKeyValue{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return kv, nil
		}
		switch fh.fid {
		case 1:
			b, err := r.readBinary()
			if err != nil {
				return nil, err
			}
			kv.key = string(b)
		case 2:
			b, err := r.readBinary()
			if err != nil {
				return nil, err
			}
			s := string(b)
			kv.value = &s
		default:
			if err := r.skipValue(fh.typ); err != nil {
				return nil, err
			}
		}
	}
}

// thriftColumnMetaData mirrors parquet.thrift ColumnMetaData.
type thriftColumnMetaData struct {
	type_             int32
	encodings         []int32
	pathInSchema      []string
	codec             int32
	numValues         int64
	totalUncompressed int64
	totalCompressed   int64
	keyValueMetadata  []thriftKeyValue
	stats             *thriftStatistics
	dataPageOffset    int64
	dictionaryOffset  int64
}

func (m *thriftColumnMetaData) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI32Field(1, m.type_)
	w.writeListBegin(2, ctI32, len(m.encodings))
	for _, e := range m.encodings {
		w.buf = appendUvarint(w.buf, zigzag32(e))
	}
	w.writeListBegin(3, ctBinary, len(m.pathInSchema))
	for _, p := range m.pathInSchema {
		w.buf = appendUvarint(w.buf, uint64(len(p)))
		w.buf = append(w.buf, p...)
	}
	w.writeI32Field(4, m.codec)
	w.writeI64Field(5, m.numValues)
	w.writeI64Field(6, m.totalUncompressed)
	w.writeI64Field(7, m.totalCompressed)
	if len(m.keyValueMetadata) > 0 {
		w.writeListBegin(8, ctStruct, len(m.keyValueMetadata))
		for i := range m.keyValueMetadata {
			m.keyValueMetadata[i].write(w)
		}
	}
	if m.stats != nil {
		w.writeStructField(9, m.stats.write)
	}
	w.writeI64Field(12, m.dataPageOffset)
	if m.dictionaryOffset >= 0 {
		w.writeI64Field(13, m.dictionaryOffset)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readColumnMetaData(r *compactReader) (*thriftColumnMetaData, error) {
	m := &thriftColumnMetaData{dictionaryOffset: -1}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return m, nil
		}
		switch fh.fid {
		case 1:
			m.type_, err = r.readI32()
		case 2:
			elemType, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				v, err := r.readI32()
				if err != nil {
					return nil, err
				}
				m.encodings = append(m.encodings, v)
			}
			_ = elemType
		case 3:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				b, err := r.readBinary()
				if err != nil {
					return nil, err
				}
				m.pathInSchema = append(m.pathInSchema, string(b))
			}
		case 4:
			m.codec, err = r.readI32()
		case 5:
			m.numValues, err = r.readI64()
		case 6:
			m.totalUncompressed, err = r.readI64()
		case 7:
			m.totalCompressed, err = r.readI64()
		case 8:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				kv, err := readKeyValue(r)
				if err != nil {
					return nil, err
				}
				m.keyValueMetadata = append(m.keyValueMetadata, *kv)
			}
		case 9:
			m.stats, err = readStatistics(r)
		case 12:
			m.dataPageOffset, err = r.readI64()
		case 13:
			m.dictionaryOffset, err = r.readI64()
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftColumnChunk mirrors parquet.thrift ColumnChunk.
type thriftColumnChunk struct {
	fileOffset int64
	metaData   *thriftColumnMetaData
}

func (c *thriftColumnChunk) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI64Field(2, c.fileOffset)
	if c.metaData != nil {
		w.writeStructField(3, c.metaData.write)
	}
	w.writeStop()
	w.writeStructEnd()
}

func readColumnChunk(r *compactReader) (*thriftColumnChunk, error) {
	c := &thriftColumnChunk{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return c, nil
		}
		switch fh.fid {
		case 2:
			c.fileOffset, err = r.readI64()
		case 3:
			c.metaData, err = readColumnMetaData(r)
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftRowGroup mirrors parquet.thrift RowGroup.
type thriftRowGroup struct {
	columns         []thriftColumnChunk
	totalByteSize   int64
	numRows         int64
	totalCompressed int64
}

func (g *thriftRowGroup) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeListBegin(1, ctStruct, len(g.columns))
	for i := range g.columns {
		g.columns[i].write(w)
	}
	w.writeI64Field(2, g.totalByteSize)
	w.writeI64Field(3, g.numRows)
	w.writeI64Field(7, g.totalCompressed)
	w.writeStop()
	w.writeStructEnd()
}

func readRowGroup(r *compactReader) (*thriftRowGroup, error) {
	g := &thriftRowGroup{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return g, nil
		}
		switch fh.fid {
		case 1:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				c, err := readColumnChunk(r)
				if err != nil {
					return nil, err
				}
				g.columns = append(g.columns, *c)
			}
		case 2:
			g.totalByteSize, err = r.readI64()
		case 3:
			g.numRows, err = r.readI64()
		case 7:
			g.totalCompressed, err = r.readI64()
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftFileMetaData mirrors parquet.thrift FileMetaData.
type thriftFileMetaData struct {
	version      int32
	schema       []thriftSchemaElement
	numRows      int64
	rowGroups    []thriftRowGroup
	keyValue     []thriftKeyValue
	createdBy    string
	columnOrders []thriftColumnOrder
}

func (m *thriftFileMetaData) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeI32Field(1, m.version)
	w.writeListBegin(2, ctStruct, len(m.schema))
	for i := range m.schema {
		m.schema[i].write(w)
	}
	w.writeI64Field(3, m.numRows)
	w.writeListBegin(4, ctStruct, len(m.rowGroups))
	for i := range m.rowGroups {
		m.rowGroups[i].write(w)
	}
	if len(m.keyValue) > 0 {
		w.writeListBegin(5, ctStruct, len(m.keyValue))
		for i := range m.keyValue {
			m.keyValue[i].write(w)
		}
	}
	if m.createdBy != "" {
		w.writeStringField(6, m.createdBy)
	}
	if len(m.columnOrders) > 0 {
		w.writeListBegin(7, ctStruct, len(m.columnOrders))
		for i := range m.columnOrders {
			m.columnOrders[i].write(w)
		}
	}
	w.writeStop()
	w.writeStructEnd()
}

func readFileMetaData(r *compactReader) (*thriftFileMetaData, error) {
	m := &thriftFileMetaData{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return m, nil
		}
		switch fh.fid {
		case 1:
			m.version, err = r.readI32()
		case 2:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				e, err := readSchemaElement(r)
				if err != nil {
					return nil, err
				}
				m.schema = append(m.schema, *e)
			}
		case 3:
			m.numRows, err = r.readI64()
		case 4:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				g, err := readRowGroup(r)
				if err != nil {
					return nil, err
				}
				m.rowGroups = append(m.rowGroups, *g)
			}
		case 5:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				kv, err := readKeyValue(r)
				if err != nil {
					return nil, err
				}
				m.keyValue = append(m.keyValue, *kv)
			}
		case 6:
			b, err := r.readBinary()
			if err != nil {
				return nil, err
			}
			m.createdBy = string(b)
		case 7:
			_, n, err := r.readListHeader()
			if err != nil {
				return nil, err
			}
			for i := 0; i < n; i++ {
				order, err := readColumnOrder(r)
				if err != nil {
					return nil, err
				}
				m.columnOrders = append(m.columnOrders, *order)
			}
		default:
			err = r.skipValue(fh.typ)
		}
		if err != nil {
			return nil, err
		}
	}
}

// thriftColumnOrder wraps the ColumnOrder union. Only TYPE_ORDER is defined;
// its inner TypeOrder struct currently has no fields.
type thriftColumnOrder struct{}

func (o *thriftColumnOrder) write(w *compactWriter) {
	w.writeStructBegin()
	w.writeStructField(1, func(inner *compactWriter) {
		inner.buf = append(inner.buf, ctStop)
	})
	w.writeStop()
	w.writeStructEnd()
}

func readColumnOrder(r *compactReader) (*thriftColumnOrder, error) {
	o := &thriftColumnOrder{}
	r.beginStruct()
	for {
		fh, err := r.readFieldHeader()
		if err != nil {
			return nil, err
		}
		if fh.typ == ctStop {
			r.endStruct()
			return o, nil
		}
		if err := r.skipValue(fh.typ); err != nil {
			return nil, err
		}
	}
}

// schemaElements flattens the schema tree into parquet.thrift SchemaElements in
// pre-order.
func schemaElements(n *Node) []thriftSchemaElement {
	var out []thriftSchemaElement
	var walk func(node *Node)
	walk = func(node *Node) {
		e := thriftSchemaElement{name: node.Name}
		if node.parent != nil {
			rep := node.Repetition.thriftRep()
			e.repetitionType = &rep
		}
		if node.IsLeaf {
			t := node.Physical.thriftType()
			e.typePtr = &t
			if node.IsString {
				ct := convUTF8
				e.convertedType = &ct
			}
		} else {
			e.numChildren = int32(len(node.Children))
			if isListWrapper(node) {
				ct := convList
				e.convertedType = &ct
			}
		}
		out = append(out, e)
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

func isListWrapper(n *Node) bool {
	return n.Repetition == Optional && len(n.Children) == 1 &&
		n.Children[0].Name == "list" && n.Children[0].Repetition == Repeated
}

// schemaFromElements reconstructs a schema tree from flattened elements.
func schemaFromElements(elements []thriftSchemaElement) (*Node, error) {
	if len(elements) == 0 {
		return nil, errCorrupt("empty schema")
	}
	idx := 0
	var build func(parent *Node) (*Node, error)
	build = func(parent *Node) (*Node, error) {
		if idx >= len(elements) {
			return nil, errCorrupt("schema elements truncated")
		}
		e := elements[idx]
		idx++
		node := &Node{Name: e.name, parent: parent}
		if e.repetitionType != nil {
			rep, err := repetitionFromThrift(*e.repetitionType)
			if err != nil {
				return nil, err
			}
			node.Repetition = rep
		}
		if e.typePtr != nil {
			t, err := physicalFromThrift(*e.typePtr)
			if err != nil {
				return nil, err
			}
			node.IsLeaf = true
			node.Physical = t
			if node.Name == emptySentinelName {
				node.synthetic = true
			}
			if e.convertedType != nil && *e.convertedType == convUTF8 {
				node.IsString = true
			}
			return node, nil
		}
		for i := int32(0); i < e.numChildren; i++ {
			child, err := build(node)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, child)
		}
		return node, nil
	}
	root, err := build(nil)
	if err != nil {
		return nil, err
	}
	if idx != len(elements) {
		return nil, errCorrupt("trailing schema elements")
	}
	return root, nil
}
