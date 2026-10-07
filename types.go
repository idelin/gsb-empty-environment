// Package csvq parses CSV sources whose headers may contain duplicate
// column names. Column identity is positional: the i-th field of every
// record belongs to the i-th header column regardless of its name.
//
// Records with fewer fields than the header are padded with missing
// fields (distinct from present-but-empty fields). Records with more
// fields than the header, or with malformed quoting, are quarantined
// whole; quarantining never shifts the column positions of later
// records. Newlines inside quoted fields never trigger row splitting
// or repair.
package csvq

// Field is a single parsed field.
type Field struct {
	Value   string
	Present bool // false only for fields padded onto short records
	Empty   bool // present but empty (,, or "")
	Quoted  bool // field was quoted in the source
}

// Record is a valid data record, padded to the header width.
type Record struct {
	Seq       int // sequence among data records, quarantined rows included
	Offset    int64
	EndOffset int64
	Fields    []Field
}

// QuarantinedRow is an isolated error row kept with its raw source span.
type QuarantinedRow struct {
	Seq       int
	Offset    int64
	EndOffset int64
	Raw       []byte
	Fields    []string // fields parsed before isolation
	Reason    string
}

// Column is a header column. Names may repeat; Index is the identity.
type Column struct {
	Index int
	Name  string
}

// Header is the parsed header record.
type Header struct {
	Columns []Column
}

// Len returns the positional width of the table.
func (h Header) Len() int { return len(h.Columns) }

// Name returns the name of the i-th column.
func (h Header) Name(i int) string { return h.Columns[i].Name }

// Item is a valid record or a quarantined row in source order.
type Item struct {
	Seq        int
	Offset     int64
	EndOffset  int64
	Record     *Record
	Quarantine *QuarantinedRow
}

// Table is a parsed CSV source.
type Table struct {
	Header     Header
	Records    []Record
	Quarantine []QuarantinedRow
	Items      []Item // Records and Quarantine interleaved by Seq
	DataOffset int64  // offset where the first data record starts
}
