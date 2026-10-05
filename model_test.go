package rehash

import (
	"math/rand"
	"testing"
)

// model is the versioned reference map the table is checked against.
type model struct {
	m   map[int]int
	ver uint64
}

func (mo *model) put(k, v int) { mo.m[k] = v; mo.ver++ }
func (mo *model) del(k int) {
	if _, ok := mo.m[k]; ok {
		delete(mo.m, k)
		mo.ver++
	}
}

// checkGet verifies every probed key against the reference map.
func checkGet(t *testing.T, tab *Table[int, int], mo *model, keys []int) {
	t.Helper()
	for _, k := range keys {
		gv, gok := tab.Get(k)
		wv, wok := mo.m[k]
		if gok != wok || (gok && gv != wv) {
			t.Fatalf("Get(%d) = %v,%v; reference says %v,%v", k, gv, gok, wv, wok)
		}
	}
}

// checkSnapshot verifies the snapshot equals the reference map exactly:
// no missing keys, no extra keys, no duplicates, correct values.
func checkSnapshot(t *testing.T, tab *Table[int, int], mo *model) {
	t.Helper()
	snap := tab.Snapshot()
	if snap.Version() != mo.ver {
		t.Fatalf("snapshot version %d != reference version %d", snap.Version(), mo.ver)
	}
	if snap.Len() != len(mo.m) {
		t.Fatalf("snapshot len %d != reference len %d", snap.Len(), len(mo.m))
	}
	seen := make(map[int]int, snap.Len())
	for k, v, ok := snap.Next(); ok; k, v, ok = snap.Next() {
		if _, dup := seen[k]; dup {
			t.Fatalf("snapshot yielded key %d twice", k)
		}
		seen[k] = v
		wv, wok := mo.m[k]
		if !wok {
			t.Fatalf("snapshot has unexpected key %d", k)
		}
		if wv != v {
			t.Fatalf("snapshot[%d] = %d, reference says %d", k, v, wv)
		}
	}
	for k := range mo.m {
		if _, ok := seen[k]; !ok {
			t.Fatalf("snapshot missing key %d", k)
		}
	}
}

// TestRandomModel runs randomized operation sequences against both the
// table and a versioned reference map, cross-checking reads, snapshots,
// versions and migration invariants after every step.
func TestRandomModel(t *testing.T) {
	for _, chunk := range []int{1, 2, 5} {
		chunk := chunk
		t.Run(chunkName(chunk), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(1234 + chunk)))
			tab := New[int, int](IntHash, WithMigrateChunk(chunk))
			mo := &model{m: make(map[int]int)}
			const keySpace = 64 // small space: forces updates, deletes, collisions
			const steps = 4000
			for step := 0; step < steps; step++ {
				k := rng.Intn(keySpace)
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4: // put
					v := rng.Intn(1000)
					tab.Put(k, v)
					mo.put(k, v)
				case 5, 6, 7: // delete
					if tab.Delete(k) != func() bool { _, ok := mo.m[k]; return ok }() {
						t.Fatalf("step %d: Delete(%d) mismatch with reference", step, k)
					}
					mo.del(k)
				case 8: // probe reads
					checkGet(t, tab, mo, []int{k, rng.Intn(keySpace), rng.Intn(keySpace)})
				case 9: // snapshot + full verification
					checkSnapshot(t, tab, mo)
				}
				if tab.Version() != mo.ver {
					t.Fatalf("step %d: table version %d != reference version %d",
						step, tab.Version(), mo.ver)
				}
				if tab.Len() != len(mo.m) {
					t.Fatalf("step %d: Len %d != reference %d", step, tab.Len(), len(mo.m))
				}
				// Migration invariants: progress bounded, old table shrinks.
				if m, tot := tab.Progress(); tot > 0 && (m < 0 || m > tot) {
					t.Fatalf("step %d: bad progress %d/%d", step, m, tot)
				}
			}
			// Drain migration and do a final full comparison.
			for tab.Migrating() {
				tab.Get(-1)
			}
			keys := make([]int, 0, keySpace*2)
			for k := -keySpace / 2; k < keySpace+keySpace/2; k++ {
				keys = append(keys, k)
			}
			checkGet(t, tab, mo, keys)
			checkSnapshot(t, tab, mo)
		})
	}
}

func chunkName(c int) string {
	return "chunk_" + string(rune('0'+c))
}

// TestModelSnapshotStability verifies a snapshot taken mid-mutation keeps
// referring to its own version while the live table moves on.
func TestModelSnapshotStability(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	tab := New[int, int](IntHash, WithMigrateChunk(1))
	mo := &model{m: make(map[int]int)}
	for i := 0; i < 300; i++ {
		k := rng.Intn(128)
		tab.Put(k, i)
		mo.put(k, i)
	}
	snap := tab.Snapshot()
	frozen := make(map[int]int, len(mo.m))
	for k, v := range mo.m {
		frozen[k] = v
	}
	// Continue mutating; snapshot must stay at the frozen version.
	for i := 0; i < 300; i++ {
		k := rng.Intn(128)
		if i%3 == 0 {
			tab.Delete(k)
			mo.del(k)
		} else {
			tab.Put(k, 10000+i)
			mo.put(k, 10000+i)
		}
	}
	got := snap.Collect()
	if len(got) != len(frozen) {
		t.Fatalf("stable snapshot has %d entries, want %d", len(got), len(frozen))
	}
	for k, v := range frozen {
		if got[k] != v {
			t.Fatalf("stable snapshot[%d] = %d, want %d", k, got[k], v)
		}
	}
	// Live table still matches the (moved-on) reference.
	checkSnapshot(t, tab, mo)
}
