package memtx

import (
	"encoding/json"
	"sort"
)

// KV is a single key-value pair returned by scans.
type KV struct {
	Key   string
	Value string
}

// wsEntry is a buffered write. A nil value pointer denotes a delete.
type wsEntry struct {
	val *string
}

// Cursor marks a resumption point inside a transaction's merged
// (snapshot + write-set) key space. It is bound to the write-set revision
// at issue time; any later Put or Delete in the same transaction revokes
// it (Scan returns ErrStaleCursor).
type Cursor struct {
	// LastKey is the last key returned by the page that issued this cursor.
	LastKey string
	// Revision is the write-set revision at issue time.
	Revision uint64
}

// Encode serializes the cursor into an opaque token for clients.
func (c *Cursor) Encode() string {
	b, _ := json.Marshal(c)
	return string(b)
}

// DecodeCursor restores a cursor previously produced by Encode.
func DecodeCursor(token string) (*Cursor, error) {
	var c Cursor
	if err := json.Unmarshal([]byte(token), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ScanOptions parameterizes a Scan call. The range is [Start, End); an
// empty End scans to the end of the key space. Limit <= 0 means no limit.
type ScanOptions struct {
	Start  string
	End    string
	Limit  int
	Cursor *Cursor
}

// Txn is a single transaction. It is not safe for concurrent use.
type Txn struct {
	store *Store
	snap  uint64

	ws     map[string]wsEntry
	wsRev  uint64 // bumped on every Put/Delete
	closed bool
}

// Get returns the value for key, observing the transaction's own writes.
func (t *Txn) Get(key string) (string, bool, error) {
	if t.closed {
		return "", false, ErrClosed
	}
	if e, ok := t.ws[key]; ok {
		if e.val == nil {
			return "", false, nil
		}
		return *e.val, true, nil
	}
	v, ok := t.store.snapshotGet(t.snap, key)
	return v, ok, nil
}

// Put buffers a write of value at key.
func (t *Txn) Put(key, value string) error {
	if t.closed {
		return ErrClosed
	}
	v := value
	t.ws[key] = wsEntry{val: &v}
	t.wsRev++
	return nil
}

// Delete buffers a delete of key.
func (t *Txn) Delete(key string) error {
	if t.closed {
		return ErrClosed
	}
	t.ws[key] = wsEntry{val: nil}
	t.wsRev++
	return nil
}

// WriteSetRevision exposes the current write-set revision (mainly for
// tests and cursor diagnostics).
func (t *Txn) WriteSetRevision() uint64 { return t.wsRev }

// Scan returns up to Limit pairs from the merged view of the fixed
// snapshot and the write set, ordered by key. Write-set inserts override
// or extend snapshot data; write-set deletes suppress snapshot keys.
//
// The merge is a streaming k-way merge of the two sorted sources, so it
// stops as soon as Limit pairs have been produced. If more pairs may
// remain, a non-nil cursor is returned; pass it back via ScanOptions.Cursor
// to fetch the next page. The cursor is valid only until the next Put or
// Delete on this transaction.
func (t *Txn) Scan(opts ScanOptions) ([]KV, *Cursor, error) {
	if t.closed {
		return nil, nil, ErrClosed
	}
	if opts.Cursor != nil && opts.Cursor.Revision != t.wsRev {
		return nil, nil, ErrStaleCursor
	}

	// Effective lower bound.
	lo := opts.Start
	loExclusive := false
	if opts.Cursor != nil {
		lo = opts.Cursor.LastKey
		loExclusive = true
	}
	hi := opts.End

	// Source 1: snapshot entries within the caller's range.
	snap := t.store.snapshotEntries(t.snap, opts.Start, hi)

	// Source 2: write-set entries within the caller's range, sorted.
	wsKeys := make([]string, 0, len(t.ws))
	for k := range t.ws {
		if k >= opts.Start && (hi == "" || k < hi) {
			wsKeys = append(wsKeys, k)
		}
	}
	sort.Strings(wsKeys)

	capHint := 64
	if opts.Limit > 0 && opts.Limit < capHint {
		capHint = opts.Limit
	}
	out := make([]KV, 0, capHint)

	// Streaming merge-join; write set wins on key collision. nextKV yields
	// only entries that are actually visible (skipping tombstones and keys
	// at or below the cursor position).
	i, j := 0, 0
	nextKV := func() (KV, bool) {
		for i < len(snap) || j < len(wsKeys) {
			var kv KV
			switch {
			case j >= len(wsKeys) || (i < len(snap) && snap[i].Key < wsKeys[j]):
				kv = snap[i]
				i++
			case i >= len(snap) || wsKeys[j] < snap[i].Key:
				key := wsKeys[j]
				j++
				e := t.ws[key]
				if e.val == nil {
					continue
				}
				kv = KV{Key: key, Value: *e.val}
			default: // same key in both: write set decides
				key := wsKeys[j]
				i++
				j++
				e := t.ws[key]
				if e.val == nil {
					continue
				}
				kv = KV{Key: key, Value: *e.val}
			}
			if kv.Key < lo || (loExclusive && kv.Key == lo) {
				continue
			}
			return kv, true
		}
		return KV{}, false
	}

	for opts.Limit <= 0 || len(out) < opts.Limit {
		kv, ok := nextKV()
		if !ok {
			return out, nil, nil
		}
		out = append(out, kv)
	}

	// Page filled: a cursor is issued only if at least one more visible
	// entry remains.
	if _, ok := nextKV(); ok {
		return out, &Cursor{LastKey: out[len(out)-1].Key, Revision: t.wsRev}, nil
	}
	return out, nil, nil
}

// Commit validates the write set against commits that landed after this
// transaction's snapshot (first-committer-wins) and, on success, applies
// it atomically as a single new commit sequence.
func (t *Txn) Commit() error {
	if t.closed {
		return ErrClosed
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for key := range t.ws {
		chain := t.store.data[key]
		if n := len(chain); n > 0 && chain[n-1].seq > t.snap {
			return ErrConflict
		}
	}
	t.store.seq++
	seq := t.store.seq
	for key, e := range t.ws {
		if e.val == nil {
			t.store.data[key] = append(t.store.data[key], cell{seq: seq, tomb: true})
		} else {
			t.store.data[key] = append(t.store.data[key], cell{seq: seq, val: *e.val})
		}
	}
	t.closed = true
	return nil
}

// Rollback discards the write set and closes the transaction.
func (t *Txn) Rollback() {
	t.closed = true
	t.ws = nil
}

func sortKVs(kvs []KV) {
	sort.Slice(kvs, func(a, b int) bool { return kvs[a].Key < kvs[b].Key })
}
