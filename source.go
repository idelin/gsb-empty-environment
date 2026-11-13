package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// Schema describes a source table. Protected columns are tenant/RLS columns
// whose data must never flow into an OwnerSnapshot.
type Schema struct {
	Name      string
	Columns   []string
	Protected []string
	// KeyColumn names the primary-key column used to identify tuples in
	// change events. When empty, a content digest is used.
	KeyColumn string
}

func (s Schema) columns() map[string]bool {
	m := make(map[string]bool, len(s.Columns))
	for _, c := range s.Columns {
		m[c] = true
	}
	return m
}

// Row is one source tuple keyed by column name.
type Row map[string]any

// Source is a versioned base table. Rebuilding a table with the same name
// mints a fresh object ID, so caches bound to the previous incarnation can
// never be confused with the new table.
type Source struct {
	name       string
	schema     Schema
	id         string
	version    int64
	rows       map[string]Row
	superseded bool
}

// ID returns the stable object identity of this incarnation.
func (s *Source) ID() string { return s.id }

// Version returns the current data version, advanced on every applied change.
func (s *Source) Version() int64 { return s.version }

// Name returns the table name (not unique across rebuilds).
func (s *Source) Name() string { return s.name }

// EventKind classifies an incremental source change.
type EventKind string

const (
	EventInsert EventKind = "insert"
	EventUpdate EventKind = "update"
	EventDelete EventKind = "delete"
)

// RowEvent is one change against a source table. Key identifies the tuple
// (primary-key column value rendered as a string).
type RowEvent struct {
	Kind EventKind
	Key  string
	// Old is present for update/delete and is used to retract the previous
	// contribution, including visibility transitions.
	Old Row
	// New is present for insert/update.
	New Row
}

// digest renders a stable SHA-256 hex digest of the supplied fragments.
func digest(fragments ...string) string {
	h := sha256.New()
	for _, f := range fragments {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func shortDigest(fragments ...string) string {
	full := digest(fragments...)
	return full[:16]
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
