package objindex

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// refListing is the frozen sorted-map reference: it sorts the frozen map's
// keys and filters by prefix, exactly what an obviously-correct
// implementation would do.
func refListing(frozen map[string]Object, prefix string) []Object {
	keys := make([]string, 0, len(frozen))
	for k := range frozen {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]Object, 0, len(keys))
	for _, k := range keys {
		out = append(out, frozen[k])
	}
	return out
}

func equalObjects(a, b []Object) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Generation != b[i].Generation ||
			string(a[i].Value) != string(b[i].Value) {
			return false
		}
	}
	return true
}

// listAll paginates to exhaustion, asserting the cursor never goes backward
// and the listing terminates.
func listAll(t *testing.T, ix *Index, prefix string, cur Cursor, limit int) []Object {
	t.Helper()
	var out []Object
	for step := 0; ; step++ {
		if step > 100000 {
			t.Fatal("listing did not terminate")
		}
		page, err := ix.List(prefix, cur, limit)
		if err != nil {
			t.Fatalf("List(%q): %v", prefix, err)
		}
		prev := cur
		cur = page.Cursor
		if cur.LastKey < prev.LastKey ||
			(cur.LastKey == prev.LastKey && cur.LastGen < prev.LastGen) {
			t.Fatalf("cursor moved backward: %+v -> %+v", prev, cur)
		}
		out = append(out, page.Objects...)
		if page.Done {
			return out
		}
	}
}

// TestConcurrentUpdatesAgainstFrozenReference freezes the index, then hammers
// it with concurrent adds, deletes and delete-recreates while several readers
// paginate the frozen snapshot. Every reader must observe exactly the frozen
// reference, for every prefix, including the empty-match prefix and the tail
// of the key space.
func TestConcurrentUpdatesAgainstFrozenReference(t *testing.T) {
	// Large retain window: this test exercises consistency, not expiry.
	ix := New(WithMaxScan(3), WithRetainVersions(1<<40))
	seed := []string{"a/1", "a/2", "a/3", "b/1", "b/2", "m", "z/1", "z/2", "\xff\xff"}
	for i, k := range seed {
		ix.Add(k, []byte{byte(i)})
	}
	ix.Delete("b/2")
	snap, frozen := ix.Freeze()

	prefixes := []string{"", "a/", "b/", "z/", "no/such/prefix", "\xff"}
	want := make(map[string][]Object, len(prefixes))
	for _, p := range prefixes {
		want[p] = refListing(frozen, p)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Mutators: add new keys, delete and recreate existing ones.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(w) + 1))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				k := seed[r.Intn(len(seed))]
				switch r.Intn(3) {
				case 0:
					ix.Add(fmt.Sprintf("w%d/%d", w, i), []byte{byte(i)})
				case 1:
					ix.Delete(k)
					ix.Add(k, []byte{byte(i)}) // recreate same key: new object
				case 2:
					ix.Delete(k)
				}
			}
		}(w)
	}

	// Readers: paginate the frozen snapshot with small pages.
	var readers sync.WaitGroup
	errs := make(chan string, len(prefixes)*3)
	for _, p := range prefixes {
		for _, limit := range []int{1, 2, 7} {
			p, limit := p, limit
			readers.Add(1)
			go func() {
				defer readers.Done()
				var got []Object
				cur := Cursor{Snapshot: snap}
				for {
					page, err := ix.List(p, cur, limit)
					if err != nil {
						errs <- fmt.Sprintf("prefix %q: %v", p, err)
						return
					}
					got = append(got, page.Objects...)
					cur = page.Cursor
					if page.Done {
						break
					}
				}
				if !equalObjects(got, want[p]) {
					errs <- fmt.Sprintf("prefix %q limit %d: got %v, want %v",
						p, limit, keysOf(got), keysOf(want[p]))
				}
			}()
		}
	}
	readers.Wait()
	close(stop)
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// TestRandomizedAgainstFrozenReference drives random mutations and, whenever
// it lists, freezes the index and verifies the full paginated traversal
// against the frozen sorted-map reference.
func TestRandomizedAgainstFrozenReference(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	ix := New(WithRetainVersions(8), WithMaxScan(4))
	keyPool := []string{"", "a", "a/b", "a/c", "b", "b/x", "b/y", "c", "\xff", "\xff\xfe", "zz"}
	prefixes := []string{"", "a", "a/", "b", "b/", "q", "\xff"}

	for step := 0; step < 400; step++ {
		switch r.Intn(4) {
		case 0, 1:
			k := keyPool[r.Intn(len(keyPool))]
			if k == "" {
				k = fmt.Sprintf("n%d", step)
			}
			ix.Add(k, []byte{byte(step)})
		case 2:
			k := keyPool[r.Intn(len(keyPool))]
			if k != "" {
				ix.Delete(k)
			}
		case 3:
			snap, frozen := ix.Freeze()
			prefix := prefixes[r.Intn(len(prefixes))]
			limit := 1 + r.Intn(5)
			got := listAll(t, ix, prefix, Cursor{Snapshot: snap}, limit)
			if want := refListing(frozen, prefix); !equalObjects(got, want) {
				t.Fatalf("step %d prefix %q limit %d: got %v, want %v",
					step, prefix, limit, keysOf(got), keysOf(want))
			}
		}
	}
}

// TestSnapshotExpiryUnderChurn verifies that cursors bound to evicted
// snapshots fail with ErrSnapshotExpired while retained snapshots keep
// matching the frozen reference.
func TestSnapshotExpiryUnderChurn(t *testing.T) {
	ix := New(WithRetainVersions(3))
	ix.Add("a", []byte("1"))
	oldSnap, _ := ix.Freeze()
	for i := 0; i < 10; i++ {
		ix.Add(fmt.Sprintf("k%d", i), []byte{byte(i)})
		ix.Delete(fmt.Sprintf("k%d", i))
	}
	if _, err := ix.List("", Cursor{Snapshot: oldSnap}, 5); err != ErrSnapshotExpired {
		t.Fatalf("got %v, want ErrSnapshotExpired", err)
	}
	snap, frozen := ix.Freeze()
	got := listAll(t, ix, "", Cursor{Snapshot: snap}, 2)
	if want := refListing(frozen, ""); !equalObjects(got, want) {
		t.Fatalf("got %v, want %v", keysOf(got), keysOf(want))
	}
}
