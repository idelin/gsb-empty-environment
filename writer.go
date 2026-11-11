package parquetcol

import (
	"io"
)

// Default tuning parameters.
const (
	DefaultPageSize      = 1 << 20 // 1 MiB uncompressed target per data page
	DefaultMaxDictionary = 1 << 20 // 1 MiB of distinct PLAIN values per chunk
	parquetWriterVersion = 1
)

// Options configures a Writer.
type Options struct {
	// PageSize is the target uncompressed data page size in bytes.
	PageSize int
	// MaxDictionaryBytes falls a column chunk back to PLAIN once its
	// dictionary exceeds this size.
	MaxDictionaryBytes int
	// CreatedBy is recorded in the file metadata.
	CreatedBy string
}

// Writer writes Parquet rows. Rows are grouped into row groups; call Flush to
// finish the current row group, or rely on Close to flush a final group.
type Writer struct {
	schema *Schema
	fw     *fileWriter
	opts   Options

	buffered  []Value
	rowGroups []thriftRowGroup
	totalRows int64
}

// NewWriter creates a Parquet writer over w.
func NewWriter(w io.Writer, schema *Schema, opts *Options) (*Writer, error) {
	if schema == nil {
		return nil, errCorrupt("nil schema")
	}
	o := Options{
		PageSize:           DefaultPageSize,
		MaxDictionaryBytes: DefaultMaxDictionary,
		CreatedBy:          "parquetcol",
	}
	if opts != nil {
		if opts.PageSize > 0 {
			o.PageSize = opts.PageSize
		}
		if opts.MaxDictionaryBytes > 0 {
			o.MaxDictionaryBytes = opts.MaxDictionaryBytes
		}
		if opts.CreatedBy != "" {
			o.CreatedBy = opts.CreatedBy
		}
	}
	pw := &Writer{
		schema: schema,
		fw:     newFileWriter(w),
		opts:   o,
	}
	if _, err := pw.fw.write(parquetMagic); err != nil {
		return nil, err
	}
	return pw, nil
}

// WriteRow buffers one record.
func (pw *Writer) WriteRow(record Value) error {
	cols := pw.freshColumns()
	if err := shredRecord(pw.schema, record, cols); err != nil {
		return err
	}
	pw.buffered = append(pw.buffered, record)
	return nil
}

func (pw *Writer) freshColumns() map[*Node]*columnData {
	cols := make(map[*Node]*columnData, len(pw.schema.leaves))
	for _, leaf := range pw.schema.leaves {
		cols[leaf] = newColumnData(leaf)
	}
	return cols
}

// RowsBuffered reports how many rows await the next Flush.
func (pw *Writer) RowsBuffered() int { return len(pw.buffered) }

// Flush writes all buffered rows as one row group.
func (pw *Writer) Flush() error {
	if len(pw.buffered) == 0 {
		return nil
	}
	numRows := len(pw.buffered)
	cols := make(map[*Node]*columnData, len(pw.schema.leaves))
	for _, leaf := range pw.schema.leaves {
		cols[leaf] = newColumnData(leaf)
	}
	for _, record := range pw.buffered {
		if err := shredRecord(pw.schema, record, cols); err != nil {
			return err
		}
	}

	chunks := make([]thriftColumnChunk, 0, len(pw.schema.leaves))
	var groupBytes int64
	for _, leaf := range pw.schema.leaves {
		cw := newColumnChunkWriter(leaf, cols[leaf], pw.opts.PageSize, pw.opts.MaxDictionaryBytes)
		chunk, err := cw.writeTo(pw.fw)
		if err != nil {
			return err
		}
		groupBytes += chunk.metaData.totalCompressed
		chunks = append(chunks, *chunk)
	}

	pw.rowGroups = append(pw.rowGroups, thriftRowGroup{
		columns:         chunks,
		totalByteSize:   groupBytes,
		totalCompressed: groupBytes,
		numRows:         int64(numRows),
	})
	pw.totalRows += int64(numRows)
	pw.buffered = pw.buffered[:0]
	return nil
}

// Close flushes any buffered rows and writes the Parquet footer.
func (pw *Writer) Close() error {
	if err := pw.Flush(); err != nil {
		return err
	}
	meta := &thriftFileMetaData{
		version:   parquetWriterVersion,
		schema:    schemaElements(pw.schema.Root),
		numRows:   pw.totalRows,
		rowGroups: pw.rowGroups,
		createdBy: pw.opts.CreatedBy,
	}
	for range pw.schema.leaves {
		meta.columnOrders = append(meta.columnOrders, thriftColumnOrder{})
	}
	cw := newCompactWriter()
	meta.write(cw)
	if _, err := pw.fw.write(cw.buf); err != nil {
		return err
	}
	if err := pw.fw.writeUint32(uint32(len(cw.buf))); err != nil {
		return err
	}
	_, err := pw.fw.write(parquetMagic)
	return err
}
