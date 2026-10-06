package memtx

import "sort"

// KV is a single key-value pair returned by a scan.
type KV struct {
	Key   string
	Value []byte
}

// Cursor marks a resumable position in a Scan. A cursor is bound to the
// issuing transaction: it records both the snapshot revision and the
// write-set revision at the time it was created. Reusing a cursor after
// the transaction's write set changed fails with ErrStaleCursor, so a
// paginated reader never observes a shifting view mid-iteration.
type Cursor struct {
	// LastKey is the last key of the previous page; the next page starts
	// strictly after it.
	LastKey string
	// SnapRev is the snapshot revision of the issuing transaction.
	SnapRev Revision
	// WSRev is the write-set revision of the issuing transaction at the
	// time the cursor was created.
	WSRev uint64
}

// Page is one page of scan results. Next is nil when the scan is
// exhausted; otherwise pass it back to Scan to fetch the following page.
type Page struct {
	Items []KV
	Next  *Cursor
}

// Scan returns up to limit key-value pairs in sorted key order from the
// half-open range [start, end). An empty end means no upper bound. The
// result merges the transaction's snapshot with its write set: buffered
// puts appear in their sorted position, buffered deletes mask snapshot
// entries, and snapshot entries overwritten by the write set show the
// buffered value.
//
// Pass nil cursor for the first page and page.Next for subsequent pages.
// A limit <= 0 means no limit (single page). If the write set changed
// since cursor was issued, Scan returns ErrStaleCursor.
func (tx *Txn) Scan(start, end string, limit int, cursor *Cursor) (*Page, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if err := tx.checkActive(); err != nil {
		return nil, err
	}

	lower := start
	lowerExclusive := false
	if cursor != nil {
		if cursor.SnapRev != tx.snapRev || cursor.WSRev != tx.wsRev {
			return nil, ErrStaleCursor
		}
		lower = cursor.LastKey
		lowerExclusive = true
	}

	merged := tx.mergedRange(lower, lowerExclusive, end)

	if limit > 0 && len(merged) > limit {
		page := &Page{Items: merged[:limit]}
		page.Next = &Cursor{
			LastKey: merged[limit-1].Key,
			SnapRev: tx.snapRev,
			WSRev:   tx.wsRev,
		}
		return page, nil
	}
	return &Page{Items: merged}, nil
}

// mergedRange materializes the full merged view of the snapshot and the
// write set over the given key range, in ascending key order. The caller
// must hold tx.mu.
func (tx *Txn) mergedRange(lower string, lowerExclusive bool, end string) []KV {
	inRange := func(key string) bool {
		if lowerExclusive {
			if key <= lower {
				return false
			}
		} else if key < lower {
			return false
		}
		return end == "" || key < end
	}

	// Collect write-set entries in range: puts contribute their buffered
	// value; deletes and overwritten keys suppress the snapshot entry.
	merged := make(map[string][]byte)
	for key, entry := range tx.ws {
		if !inRange(key) {
			continue
		}
		if entry.deleted {
			merged[key] = nil
		} else {
			merged[key] = entry.value
		}
	}

	// Overlay the snapshot for keys not touched by the write set.
	tx.db.mu.RLock()
	for key := range tx.db.data {
		if _, overridden := merged[key]; overridden {
			continue
		}
		if !inRange(key) {
			continue
		}
		if value, ok := tx.db.lookupLocked(key, tx.snapRev); ok {
			merged[key] = value
		}
	}
	tx.db.mu.RUnlock()

	keys := make([]string, 0, len(merged))
	for key, value := range merged {
		if value != nil {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	out := make([]KV, 0, len(keys))
	for _, key := range keys {
		out = append(out, KV{Key: key, Value: append([]byte(nil), merged[key]...)})
	}
	return out
}
