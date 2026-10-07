package csvq

import (
	"fmt"
	"strconv"
)

// ColType is the inferred type of a column.
type ColType int

const (
	// Int columns hold values that all parse as base-10 integers.
	Int ColType = iota
	// Text columns hold raw text. Int widens to Text, never the reverse.
	Text
)

func (t ColType) String() string {
	if t == Int {
		return "int"
	}
	return "text"
}

// Cell is one stored value. Raw always keeps the original source text,
// so widening a column never loses information such as leading zeros.
type Cell struct {
	Raw     string
	Present bool // false for missing (padded) fields
	IsInt   bool // Raw parses as a base-10 integer
	Int     int64
}

// Row is one stored record. Cells are positional: Cells[i] belongs to
// header column i.
type Row struct {
	Batch int
	Seq   int // source record sequence
	Cells []Cell
}

// ColumnSchema is the type of one positional column at a schema version.
type ColumnSchema struct {
	Name string
	Type ColType
}

// Schema is an immutable schema version. Version numbers start at 1.
type Schema struct {
	Version int
	Columns []ColumnSchema
}

// BatchMeta describes one atomically committed batch.
type BatchMeta struct {
	ID          int
	Schema      int // schema version the batch committed under
	SeqLo       int // first source sequence covered by the batch
	SeqHi       int // one past the last source sequence covered
	Rows        int
	Quarantined []int // sequences quarantined in this batch
}

// QuarantineRecord is a quarantined row as committed by a batch.
type QuarantineRecord struct {
	Batch     int
	Seq       int
	Offset    int64
	EndOffset int64
	Raw       []byte
	Reason    string
}

// Store holds committed batches and the schema version history.
type Store struct {
	width      int
	schemas    []Schema
	rows       []Row
	batches    []BatchMeta
	quarantine []QuarantineRecord
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{} }

// Import commits the whole table as a single atomic batch.
func (s *Store) Import(t *Table) (BatchMeta, error) {
	return s.ImportBatch(t.Header, t.Records, t.Quarantine)
}

// ImportBatch commits one batch atomically: on any error the store is
// left untouched. Column types are inferred from the batch; when a
// previously integer column sees non-integer data the column widens to
// text and a new schema version is appended. Quarantined rows are
// recorded with their source sequences and never shift the positional
// cells of other rows.
func (s *Store) ImportBatch(h Header, recs []Record, qs []QuarantinedRow) (BatchMeta, error) {
	width := h.Len()
	if width == 0 {
		return BatchMeta{}, fmt.Errorf("csvq: empty header")
	}
	if len(recs) == 0 && len(qs) == 0 {
		return BatchMeta{}, fmt.Errorf("csvq: empty batch")
	}
	if s.width != 0 && width != s.width {
		return BatchMeta{}, fmt.Errorf("csvq: header width %d does not match store width %d", width, s.width)
	}
	for _, r := range recs {
		if len(r.Fields) != width {
			return BatchMeta{}, fmt.Errorf("csvq: record %d has %d fields, want %d", r.Seq, len(r.Fields), width)
		}
	}

	candidate := inferTypes(recs, width)

	// Schema evolution is staged locally and committed only on success.
	var staged []Schema
	if s.width == 0 {
		cols := make([]ColumnSchema, width)
		for i, c := range h.Columns {
			cols[i] = ColumnSchema{Name: c.Name, Type: candidate[i]}
		}
		staged = append(staged, Schema{Version: 1, Columns: cols})
	} else {
		cur := s.schemas[len(s.schemas)-1]
		cols := make([]ColumnSchema, width)
		copy(cols, cur.Columns)
		widened := false
		for i := range cols {
			if cols[i].Type == Int && candidate[i] == Text {
				cols[i].Type = Text
				widened = true
			}
		}
		if widened {
			staged = append(staged, Schema{Version: cur.Version + 1, Columns: cols})
		}
	}

	batchID := len(s.batches)
	rows := make([]Row, 0, len(recs))
	for _, r := range recs {
		cells := make([]Cell, width)
		for i, f := range r.Fields {
			c := Cell{Raw: f.Value, Present: f.Present}
			if f.Present && !f.Empty {
				if v, err := strconv.ParseInt(f.Value, 10, 64); err == nil {
					c.IsInt = true
					c.Int = v
				}
			}
			cells[i] = c
		}
		rows = append(rows, Row{Batch: batchID, Seq: r.Seq, Cells: cells})
	}

	lo, hi := seqRange(recs, qs)

	// Commit.
	s.schemas = append(s.schemas, staged...)
	if s.width == 0 {
		s.width = width
	}
	s.rows = append(s.rows, rows...)
	var qseqs []int
	for _, q := range qs {
		s.quarantine = append(s.quarantine, QuarantineRecord{
			Batch:     batchID,
			Seq:       q.Seq,
			Offset:    q.Offset,
			EndOffset: q.EndOffset,
			Raw:       append([]byte(nil), q.Raw...),
			Reason:    q.Reason,
		})
		qseqs = append(qseqs, q.Seq)
	}
	meta := BatchMeta{
		ID:          batchID,
		Schema:      s.schemas[len(s.schemas)-1].Version,
		SeqLo:       lo,
		SeqHi:       hi,
		Rows:        len(recs),
		Quarantined: qseqs,
	}
	s.batches = append(s.batches, meta)
	return meta, nil
}

// inferTypes infers the narrowest type per column from a batch. Missing
// and empty fields carry no evidence; the default is Int.
func inferTypes(recs []Record, width int) []ColType {
	types := make([]ColType, width)
	for _, r := range recs {
		for i := 0; i < width && i < len(r.Fields); i++ {
			f := r.Fields[i]
			if !f.Present || f.Empty {
				continue
			}
			if _, err := strconv.ParseInt(f.Value, 10, 64); err != nil {
				types[i] = Text
			}
		}
	}
	return types
}

func seqRange(recs []Record, qs []QuarantinedRow) (int, int) {
	lo, hi := 0, -1
	see := func(seq int) {
		if hi < lo {
			lo, hi = seq, seq+1
			return
		}
		if seq < lo {
			lo = seq
		}
		if seq+1 > hi {
			hi = seq + 1
		}
	}
	for _, r := range recs {
		see(r.Seq)
	}
	for _, q := range qs {
		see(q.Seq)
	}
	return lo, hi
}

// Width returns the positional column count, or 0 before the first batch.
func (s *Store) Width() int { return s.width }

// Rows returns the number of committed records.
func (s *Store) Rows() int { return len(s.rows) }

// Schemas returns the schema version history.
func (s *Store) Schemas() []Schema { return append([]Schema(nil), s.schemas...) }

// Batches returns the committed batch metadata.
func (s *Store) Batches() []BatchMeta { return append([]BatchMeta(nil), s.batches...) }

// Quarantine returns the committed quarantine records.
func (s *Store) Quarantine() []QuarantineRecord {
	return append([]QuarantineRecord(nil), s.quarantine...)
}

// Columns returns the positional column names of the first schema.
func (s *Store) Columns() []string {
	if len(s.schemas) == 0 {
		return nil
	}
	names := make([]string, len(s.schemas[0].Columns))
	for i, c := range s.schemas[0].Columns {
		names[i] = c.Name
	}
	return names
}

// Value is a cell rendered under a schema version.
type Value struct {
	Present bool
	IsInt   bool // rendered as an integer under this version
	Int     int64
	Text    string // always the original raw text
}

// ViewRow is a stored row rendered under a schema version.
type ViewRow struct {
	Batch  int
	Seq    int
	Values []Value
}

// View renders every committed row under one schema version. Old batches
// keep their original values: an integer column later widened to text
// still renders integers under the old version, and renders the original
// raw text (leading zeros included) under the widened version.
type View struct {
	Schema Schema
	Rows   []ViewRow
}

// View renders the store under the given schema version.
func (s *Store) View(version int) (*View, error) {
	if version < 1 || version > len(s.schemas) {
		return nil, fmt.Errorf("csvq: unknown schema version %d", version)
	}
	schema := s.schemas[version-1]
	v := &View{Schema: schema}
	for _, r := range s.rows {
		vr := ViewRow{Batch: r.Batch, Seq: r.Seq, Values: make([]Value, len(r.Cells))}
		for i, c := range r.Cells {
			val := Value{Present: c.Present, Text: c.Raw}
			if c.Present && schema.Columns[i].Type == Int && c.IsInt {
				val.IsInt = true
				val.Int = c.Int
			}
			vr.Values[i] = val
		}
		v.Rows = append(v.Rows, vr)
	}
	return v, nil
}
