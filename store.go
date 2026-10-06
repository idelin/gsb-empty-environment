package csvq

import (
	"fmt"
	"strconv"
	"sync"
)

// Cell is a stored value. Raw always holds the original source text, so
// no representation (including leading zeros) is ever lost.
type Cell struct {
	Raw     string
	Missing bool
}

// Row is a stored record.
type Row struct {
	RecordIndex int
	Cells       []Cell
}

// Batch is an atomically committed set of rows, bound to the schema
// version under which it was committed.
type Batch struct {
	SchemaVersion int
	Rows          []Row
}

type schemaSnapshot struct {
	Version int
	Types   []Type
}

// Store holds committed batches and the schema version history.
type Store struct {
	mu      sync.Mutex
	width   int
	schemas []schemaSnapshot
	batches []Batch
}

// NewStore creates an empty store for files of the given column width.
func NewStore(width int) *Store { return &Store{width: width} }

// SchemaVersion returns the latest committed schema version, or 0 if no
// batch has been committed yet.
func (s *Store) SchemaVersion() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.schemas) == 0 {
		return 0
	}
	return s.schemas[len(s.schemas)-1].Version
}

// Types returns the column types of the given schema version.
func (s *Store) Types(version int) ([]Type, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sn := range s.schemas {
		if sn.Version == version {
			return append([]Type(nil), sn.Types...), nil
		}
	}
	return nil, fmt.Errorf("unknown schema version %d", version)
}

// RowCount returns the total number of committed rows.
func (s *Store) RowCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b.Rows)
	}
	return n
}

// Batches returns a copy of the committed batches.
func (s *Store) Batches() []Batch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Batch(nil), s.batches...)
}

// Commit atomically writes one batch of records under the given inferred
// column types. If any record is invalid, nothing is committed. If the
// merged types widen the current schema (e.g. an integer column turns
// out to be text), a new schema version is created and the batch is
// bound to it; previously committed batches keep their original version.
func (s *Store) Commit(recs []Record, types []Type) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(types) != s.width {
		return fmt.Errorf("type count %d does not match width %d", len(types), s.width)
	}
	// Validate everything before mutating anything.
	for _, r := range recs {
		if len(r.Fields) != s.width {
			return fmt.Errorf("record %d has %d fields, want %d", r.Index, len(r.Fields), s.width)
		}
	}
	version := 1
	if len(s.schemas) > 0 {
		cur := s.schemas[len(s.schemas)-1]
		merged := make([]Type, s.width)
		changed := false
		for c := 0; c < s.width; c++ {
			merged[c] = widen(cur.Types[c], types[c])
			if merged[c] != cur.Types[c] {
				changed = true
			}
		}
		version = cur.Version
		if changed {
			version++
			s.schemas = append(s.schemas, schemaSnapshot{Version: version, Types: merged})
		}
	} else {
		s.schemas = append(s.schemas, schemaSnapshot{Version: 1, Types: append([]Type(nil), types...)})
	}
	batch := Batch{SchemaVersion: version}
	for _, r := range recs {
		row := Row{RecordIndex: r.Index}
		for _, f := range r.Fields {
			row.Cells = append(row.Cells, Cell{Raw: f.Value, Missing: f.Missing})
		}
		batch.Rows = append(batch.Rows, row)
	}
	s.batches = append(s.batches, batch)
	return nil
}

// ViewRow is a row rendered under a specific schema version.
type ViewRow struct {
	RecordIndex int
	Values      []interface{} // int64, float64, string, or nil for missing
}

func renderRow(row Row, types []Type) ViewRow {
	vr := ViewRow{RecordIndex: row.RecordIndex, Values: make([]interface{}, len(row.Cells))}
	for i, c := range row.Cells {
		if c.Missing {
			vr.Values[i] = nil
			continue
		}
		switch types[i] {
		case TInt:
			if n, err := strconv.ParseInt(c.Raw, 10, 64); err == nil {
				vr.Values[i] = n
			} else {
				vr.Values[i] = c.Raw
			}
		case TFloat:
			if n, err := strconv.ParseFloat(c.Raw, 64); err == nil {
				vr.Values[i] = n
			} else {
				vr.Values[i] = c.Raw
			}
		default:
			vr.Values[i] = c.Raw
		}
	}
	return vr
}

// View renders only the batches committed under the given schema
// version, using that version's types. Old batches therefore keep their
// original typed values even after later batches widen a column.
func (s *Store) View(version int) ([]ViewRow, error) {
	types, err := s.Types(version)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ViewRow
	for _, b := range s.batches {
		if b.SchemaVersion != version {
			continue
		}
		for _, row := range b.Rows {
			out = append(out, renderRow(row, types))
		}
	}
	return out, nil
}

// ViewAll renders every committed row under the latest schema version.
// Because cells keep their raw source text, widening a column to text
// never alters previously committed values.
func (s *Store) ViewAll() []ViewRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.schemas) == 0 {
		return nil
	}
	types := s.schemas[len(s.schemas)-1].Types
	var out []ViewRow
	for _, b := range s.batches {
		for _, row := range b.Rows {
			out = append(out, renderRow(row, types))
		}
	}
	return out
}

// Importer infers types per batch and commits batches atomically.
type Importer struct {
	store *Store
	width int
}

// NewImporter creates an Importer writing into store.
func NewImporter(store *Store, width int) *Importer {
	return &Importer{store: store, width: width}
}

// ImportBatch infers column types from recs and commits them atomically.
func (imp *Importer) ImportBatch(recs []Record) error {
	return imp.store.Commit(recs, inferColumnTypes(recs, imp.width))
}
