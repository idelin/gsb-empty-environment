package seqstore

import (
	"context"
	"fmt"
	"sort"
)

// Store is the query library: it serves consistent cross-table reads
// from replicas that may lag the primary log.
type Store struct {
	primary  *Primary
	replicas map[string]*Replica
}

func NewStore(primary *Primary, tables ...string) *Store {
	s := &Store{primary: primary, replicas: make(map[string]*Replica)}
	for _, t := range tables {
		s.replicas[t] = NewReplica(t)
	}
	return s
}

func (s *Store) Primary() *Primary { return s.primary }

func (s *Store) Replica(table string) *Replica { return s.replicas[table] }

// WaitError reports that a table replica has not yet applied the log
// up to the sequence number a query requires. The caller should wait
// for Want (or retry later) instead of accepting a partial result.
type WaitError struct {
	Table string
	Have  uint64
	Want  uint64
}

func (e *WaitError) Error() string {
	return fmt.Sprintf("table %q only applied seq %d, query requires seq %d", e.Table, e.Have, e.Want)
}

// Snapshot returns the highest sequence number that every listed table
// has fully applied: the safe point for a consistent cross-table read.
func (s *Store) Snapshot(tables ...string) uint64 {
	snap := s.primary.Head()
	for _, t := range tables {
		if w := s.replicas[t].Watermark(); w < snap {
			snap = w
		}
	}
	return snap
}

// ReadAt reads keys from multiple tables as of the unified sequence
// number seq. If any involved table has not applied the log up to seq,
// it returns a *WaitError naming the lagging table and the sequence
// number to wait for — never a partial mix of one transaction.
func (s *Store) ReadAt(seq uint64, keys map[string][]string) (map[string]map[string]int64, error) {
	tables := make([]string, 0, len(keys))
	for table := range keys {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		r := s.replicas[table]
		if r == nil {
			return nil, fmt.Errorf("unknown table %q", table)
		}
		if w := r.Watermark(); w < seq {
			return nil, &WaitError{Table: table, Have: w, Want: seq}
		}
	}
	out := make(map[string]map[string]int64, len(keys))
	for table, ks := range keys {
		row := make(map[string]int64, len(ks))
		for _, k := range ks {
			if v, ok := s.replicas[table].readAt(k, seq); ok {
				row[k] = v
			}
		}
		out[table] = row
	}
	return out, nil
}

// Read serves a read at the current consistent snapshot across the
// involved tables. It never blocks and never mixes transactions; the
// returned sequence number identifies the snapshot that was read.
func (s *Store) Read(keys map[string][]string) (map[string]map[string]int64, uint64) {
	tables := make([]string, 0, len(keys))
	for table := range keys {
		tables = append(tables, table)
	}
	snap := s.Snapshot(tables...)
	res, err := s.ReadAt(snap, keys)
	if err != nil {
		panic(fmt.Sprintf("seqstore: snapshot %d unreadable: %v", snap, err))
	}
	return res, snap
}

// WaitFor blocks until every listed table has applied the log up to
// seq, or ctx is done.
func (s *Store) WaitFor(ctx context.Context, tables []string, seq uint64) error {
	for _, t := range tables {
		r := s.replicas[t]
		if r == nil {
			return fmt.Errorf("unknown table %q", t)
		}
		if err := r.WaitFor(ctx, seq); err != nil {
			return err
		}
	}
	return nil
}
