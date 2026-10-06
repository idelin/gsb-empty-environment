package memtx

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// modelTxn is the reference model: a plain copy of the committed state at
// begin, plus a write set. Scans fully materialize the merged map and sort
// it — the simplest possible correct implementation, checked against the
// streaming merge in Txn.Scan.
type modelTxn struct {
	snap map[string]string
	ws   map[string]*string // nil = delete
	rev  uint64
}

func newModelTxn(committed map[string]string) *modelTxn {
	snap := make(map[string]string, len(committed))
	for k, v := range committed {
		snap[k] = v
	}
	return &modelTxn{snap: snap, ws: make(map[string]*string)}
}

func (m *modelTxn) get(key string) (string, bool) {
	if v, ok := m.ws[key]; ok {
		if v == nil {
			return "", false
		}
		return *v, true
	}
	v, ok := m.snap[key]
	return v, ok
}

func (m *modelTxn) put(key, val string) {
	v := val
	m.ws[key] = &v
	m.rev++
}

func (m *modelTxn) del(key string) {
	m.ws[key] = nil
	m.rev++
}

func (m *modelTxn) scan(opts ScanOptions) ([]KV, *Cursor, error) {
	if opts.Cursor != nil && opts.Cursor.Revision != m.rev {
		return nil, nil, ErrStaleCursor
	}
	merged := make(map[string]string, len(m.snap))
	for k, v := range m.snap {
		merged[k] = v
	}
	for k, v := range m.ws {
		if v == nil {
			delete(merged, k)
		} else {
			merged[k] = *v
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		if k < opts.Start || (opts.End != "" && k >= opts.End) {
			continue
		}
		if opts.Cursor != nil && k <= opts.Cursor.LastKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KV, 0, len(keys))
	var next *Cursor
	for i, k := range keys {
		if opts.Limit > 0 && len(out) == opts.Limit {
			next = &Cursor{LastKey: out[len(out)-1].Key, Revision: m.rev}
			_ = i
			break
		}
		out = append(out, KV{Key: k, Value: merged[k]})
	}
	return out, next, nil
}

// TestModelCrossCheck drives random operation sequences through both the
// real transaction and the model and requires identical observable
// behavior: Get results, every page of every Scan (with random page sizes
// and ranges), cursor issuance, and stale-cursor rejection.
func TestModelCrossCheck(t *testing.T) {
	for _, seed := range []int64{20261007, 1, 42, 987654321} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runModelCrossCheck(t, seed)
		})
	}
}

func runModelCrossCheck(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	store := NewStore()
	committed := map[string]string{}

	key := func() string { return fmt.Sprintf("k%03d", rng.Intn(40)) }
	val := func() string { return fmt.Sprintf("v%d", rng.Intn(1000)) }

	for round := 0; round < 300; round++ {
		tx := store.Begin()
		mt := newModelTxn(committed)

		ops := 1 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			switch rng.Intn(5) {
			case 0, 1: // Put
				k, v := key(), val()
				if err := tx.Put(k, v); err != nil {
					t.Fatalf("round %d: Put: %v", round, err)
				}
				mt.put(k, v)
			case 2: // Delete
				k := key()
				if err := tx.Delete(k); err != nil {
					t.Fatalf("round %d: Delete: %v", round, err)
				}
				mt.del(k)
			case 3: // Get
				k := key()
				gv, gok, err := tx.Get(k)
				if err != nil {
					t.Fatalf("round %d: Get: %v", round, err)
				}
				mv, mok := mt.get(k)
				if gok != mok || gv != mv {
					t.Fatalf("round %d: Get(%q) = (%q,%v), model = (%q,%v)",
						round, k, gv, gok, mv, mok)
				}
			case 4: // Paginated scan of a random range with random page size.
				start := fmt.Sprintf("k%03d", rng.Intn(40))
				end := ""
				if rng.Intn(2) == 0 {
					end = fmt.Sprintf("k%03d", rng.Intn(40))
					if end <= start {
						end = ""
					}
				}
				limit := 1 + rng.Intn(7)
				var cursors []*Cursor
				var cur *Cursor
				for page := 0; ; page++ {
					if page > 50 {
						t.Fatalf("round %d: pagination runaway", round)
					}
					opts := ScanOptions{Start: start, End: end, Limit: limit, Cursor: cur}
					gkvs, gcur, gerr := tx.Scan(opts)
					mkvs, mcur, merr := mt.scan(opts)
					if gerr != merr {
						t.Fatalf("round %d: scan err = %v, model = %v", round, gerr, merr)
					}
					if !reflect.DeepEqual(gkvs, mkvs) {
						t.Fatalf("round %d: page %d = %v, model = %v", round, page, gkvs, mkvs)
					}
					if (gcur == nil) != (mcur == nil) {
						t.Fatalf("round %d: cursor = %v, model = %v", round, gcur, mcur)
					}
					if gcur != nil && *gcur != *mcur {
						t.Fatalf("round %d: cursor = %+v, model = %+v", round, *gcur, *mcur)
					}
					cur = gcur
					if cur != nil {
						cursors = append(cursors, cur)
					}
					if cur == nil {
						break
					}
				}
				// Mutate the write set: every previously issued cursor for
				// this transaction must now be rejected by both sides.
				if len(cursors) > 0 && rng.Intn(2) == 0 {
					k := key()
					if rng.Intn(2) == 0 {
						v := val()
						_ = tx.Put(k, v)
						mt.put(k, v)
					} else {
						_ = tx.Delete(k)
						mt.del(k)
					}
					stale := cursors[rng.Intn(len(cursors))]
					opts := ScanOptions{Start: start, End: end, Limit: limit, Cursor: stale}
					if _, _, err := tx.Scan(opts); err != ErrStaleCursor {
						t.Fatalf("round %d: stale cursor err = %v", round, err)
					}
					if _, _, err := mt.scan(opts); err != ErrStaleCursor {
						t.Fatalf("round %d: model stale cursor err = %v", round, err)
					}
				}
			}
		}

		// Single-threaded: commits always succeed. Apply to committed state.
		if err := tx.Commit(); err != nil {
			t.Fatalf("round %d: commit: %v", round, err)
		}
		for k, v := range mt.ws {
			if v == nil {
				delete(committed, k)
			} else {
				committed[k] = *v
			}
		}
	}

	// Final consistency check: a fresh transaction sees exactly the
	// committed map, fully merged scan included.
	tx := store.Begin()
	got, _, err := tx.Scan(ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]KV, 0, len(committed))
	ks := make([]string, 0, len(committed))
	for k := range committed {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		want = append(want, KV{Key: k, Value: committed[k]})
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("final state mismatch:\n got %v\nwant %v", got, want)
	}
}
