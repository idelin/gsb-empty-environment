package memtx

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file cross-checks the library against an independent reference
// model. The model implements the specified semantics directly: a
// transaction's read view is the FULL MERGE of (1) the committed snapshot
// taken at begin time and (2) the transaction's own write set, with
// write-set deletes masking snapshot entries and write-set inserts
// merged into the sorted order. Cursors bind to (snapshot rev, write-set
// rev) and are rejected after either changes.

// --- reference model ---

type mVersion struct {
	rev   int
	value string
}

type modelDB struct {
	rev  int
	data map[string][]mVersion // versions ascending by rev
}

func newModelDB() *modelDB {
	return &modelDB{data: make(map[string][]mVersion)}
}

type modelTxn struct {
	db      *modelDB
	snapRev int
	ws      map[string]*string // nil = delete
	wsRev   int
	closed  bool
}

func (m *modelDB) begin() *modelTxn {
	return &modelTxn{db: m, snapRev: m.rev, ws: make(map[string]*string)}
}

// view materializes the full merged read view of the transaction.
func (t *modelTxn) view() map[string]string {
	view := make(map[string]string)
	for key, versions := range t.db.data {
		for i := len(versions) - 1; i >= 0; i-- {
			if versions[i].rev <= t.snapRev {
				if versions[i].value != "" {
					view[key] = versions[i].value
				}
				break
			}
		}
	}
	for key, value := range t.ws {
		if value == nil {
			delete(view, key)
		} else {
			view[key] = *value
		}
	}
	return view
}

func (t *modelTxn) get(key string) (string, bool) {
	v, ok := t.view()[key]
	return v, ok
}

func (t *modelTxn) put(key, value string) {
	t.ws[key] = &value
	t.wsRev++
}

func (t *modelTxn) del(key string) {
	t.ws[key] = nil
	t.wsRev++
}

func (t *modelTxn) commit() error {
	for key := range t.ws {
		versions := t.db.data[key]
		if len(versions) > 0 && versions[len(versions)-1].rev > t.snapRev {
			t.closed = true
			return ErrConflict
		}
	}
	t.db.rev++
	for key, value := range t.ws {
		v := ""
		if value != nil {
			v = *value
		}
		t.db.data[key] = append(t.db.data[key], mVersion{rev: t.db.rev, value: v})
	}
	t.closed = true
	return nil
}

type mCursor struct {
	lastKey string
	snapRev int
	wsRev   int
}

// scan mirrors Txn.Scan: sorted full merge, half-open [start, end),
// limit <= 0 means unlimited, stale cursors rejected.
func (t *modelTxn) scan(start, end string, limit int, cursor *mCursor) ([]KV, *mCursor, error) {
	lower := start
	lowerExclusive := false
	if cursor != nil {
		if cursor.snapRev != t.snapRev || cursor.wsRev != t.wsRev {
			return nil, nil, ErrStaleCursor
		}
		lower = cursor.lastKey
		lowerExclusive = true
	}
	var keys []string
	for key := range t.view() {
		if lowerExclusive {
			if key <= lower {
				continue
			}
		} else if key < lower {
			continue
		}
		if end != "" && key >= end {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	view := t.view()
	items := make([]KV, 0, len(keys))
	for _, key := range keys {
		items = append(items, KV{Key: key, Value: []byte(view[key])})
	}
	if limit > 0 && len(items) > limit {
		next := &mCursor{lastKey: items[limit-1].Key, snapRev: t.snapRev, wsRev: t.wsRev}
		return items[:limit], next, nil
	}
	return items, nil, nil
}

// --- lockstep driver ---

func TestModelLockstep(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	db := Open()
	mdb := newModelDB()

	type pair struct {
		tx *Txn
		mt *modelTxn
	}
	var txns []pair

	key := func() string { return fmt.Sprintf("k%02d", rng.Intn(24)) }
	value := func() string { return fmt.Sprintf("v%d", rng.Intn(1000)) }

	for step := 0; step < 4000; step++ {
		op := rng.Intn(100)
		switch {
		case op < 15 || len(txns) == 0: // begin
			txns = append(txns, pair{db.Begin(), mdb.begin()})

		case op < 45: // put
			p := txns[rng.Intn(len(txns))]
			if p.mt.closed {
				continue
			}
			k, v := key(), value()
			if err := p.tx.Put(k, []byte(v)); err != nil {
				t.Fatalf("step %d: Put: %v", step, err)
			}
			p.mt.put(k, v)

		case op < 55: // delete
			p := txns[rng.Intn(len(txns))]
			if p.mt.closed {
				continue
			}
			k := key()
			if err := p.tx.Delete(k); err != nil {
				t.Fatalf("step %d: Delete: %v", step, err)
			}
			p.mt.del(k)

		case op < 70: // get + compare
			p := txns[rng.Intn(len(txns))]
			if p.mt.closed {
				continue
			}
			k := key()
			got, err := p.tx.Get(k)
			want, ok := p.mt.get(k)
			if ok {
				if err != nil || string(got) != want {
					t.Fatalf("step %d: Get(%q) = %q, %v; model wants %q", step, k, got, err, want)
				}
			} else if err != ErrKeyNotFound {
				t.Fatalf("step %d: Get(%q) err = %v; model wants not found", step, k, err)
			}

		case op < 85: // paginated full scan + compare
			p := txns[rng.Intn(len(txns))]
			if p.mt.closed {
				continue
			}
			limit := 1 + rng.Intn(7)
			var got, want []KV
			var cursor *Cursor
			var mcursor *mCursor
			for page := 0; ; page++ {
				gp, err := p.tx.Scan("", "", limit, cursor)
				if err != nil {
					t.Fatalf("step %d page %d: Scan: %v", step, page, err)
				}
				wp, mnext, err := p.mt.scan("", "", limit, mcursor)
				if err != nil {
					t.Fatalf("step %d page %d: model scan: %v", step, page, err)
				}
				if !reflect.DeepEqual(gp.Items, wp) {
					t.Fatalf("step %d page %d: page mismatch\ngot  %v\nwant %v", step, page, gp.Items, wp)
				}
				if (gp.Next == nil) != (mnext == nil) {
					t.Fatalf("step %d page %d: continuation mismatch: lib=%v model=%v", step, page, gp.Next, mnext)
				}
				got = append(got, gp.Items...)
				want = append(want, wp...)
				if gp.Next == nil {
					break
				}
				cursor, mcursor = gp.Next, mnext
			}
			_ = got
			_ = want

		case op < 90: // stale cursor check: cursor, mutate, reuse
			p := txns[rng.Intn(len(txns))]
			if p.mt.closed {
				continue
			}
			gp, err := p.tx.Scan("", "", 1, nil)
			if err != nil {
				t.Fatalf("step %d: Scan: %v", step, err)
			}
			_, mcursor, err := p.mt.scan("", "", 1, nil)
			if err != nil {
				t.Fatalf("step %d: model scan: %v", step, err)
			}
			if gp.Next == nil || mcursor == nil {
				continue // single page: no cursor to invalidate
			}
			k, v := key(), value()
			if err := p.tx.Put(k, []byte(v)); err != nil {
				t.Fatalf("step %d: Put: %v", step, err)
			}
			p.mt.put(k, v)
			if _, err := p.tx.Scan("", "", 1, gp.Next); err != ErrStaleCursor {
				t.Fatalf("step %d: stale cursor err = %v, want ErrStaleCursor", step, err)
			}
			if _, _, err := p.mt.scan("", "", 1, mcursor); err != ErrStaleCursor {
				t.Fatalf("step %d: model stale cursor err = %v, want ErrStaleCursor", step, err)
			}

		case op < 97: // commit + compare outcome
			idx := rng.Intn(len(txns))
			p := txns[idx]
			if p.mt.closed {
				continue
			}
			gotErr := p.tx.Commit()
			wantErr := p.mt.commit()
			if (gotErr == ErrConflict) != (wantErr == ErrConflict) {
				t.Fatalf("step %d: commit outcome mismatch: lib=%v model=%v", step, gotErr, wantErr)
			}
			txns = append(txns[:idx], txns[idx+1:]...)

		default: // rollback
			idx := rng.Intn(len(txns))
			p := txns[idx]
			if p.mt.closed {
				continue
			}
			p.tx.Rollback()
			p.mt.closed = true
			txns = append(txns[:idx], txns[idx+1:]...)
		}
	}

	// Final reconciliation: a fresh reader sees exactly the model's
	// committed state.
	final := db.Begin()
	defer final.Rollback()
	page, err := final.Scan("", "", 0, nil)
	if err != nil {
		t.Fatalf("final scan: %v", err)
	}
	mfinal := mdb.begin()
	mitems, _, err := mfinal.scan("", "", 0, nil)
	if err != nil {
		t.Fatalf("final model scan: %v", err)
	}
	if !reflect.DeepEqual(page.Items, mitems) {
		t.Fatalf("final committed state mismatch\ngot  %v\nwant %v", page.Items, mitems)
	}
}
