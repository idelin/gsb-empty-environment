package objectindex

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// collectPages drains a listing and returns all items.
func collectPages(t *testing.T, s *Store, prefix string, limit int) []Item {
	t.Helper()
	var all []Item
	cur := Cursor{}
	for i := 0; i < 100000; i++ {
		page, err := s.List(prefix, cur, limit)
		if err != nil {
			t.Fatalf("List(%q) page %d: %v", prefix, i, err)
		}
		all = append(all, page.Items...)
		cur = page.Cursor
		if cur.Done {
			return all
		}
	}
	t.Fatalf("List(%q) did not terminate", prefix)
	return nil
}

// frozenReference is a sorted-map snapshot of the model used as the
// ground truth for list results.
func frozenReference(model map[string][]byte, prefix string) []Item {
	var keys []string
	for k := range model {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	items := make([]Item, 0, len(keys))
	for _, k := range keys {
		items = append(items, Item{Key: k, Value: model[k]})
	}
	return items
}

func itemsMatch(t *testing.T, got, want []Item) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("item count mismatch: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i].Key != want[i].Key || !reflect.DeepEqual(got[i].Value, want[i].Value) {
			t.Fatalf("item %d mismatch: got {%q %v}, want {%q %v}", i, got[i].Key, got[i].Value, want[i].Key, want[i].Value)
		}
	}
}

func TestPutGetDelete(t *testing.T) {
	s := NewStore(Options{})
	id1 := s.Put("a", []byte("1"))
	if it, ok := s.Get("a"); !ok || it.ID != id1 || string(it.Value) != "1" {
		t.Fatalf("Get(a) = %+v, %v", it, ok)
	}
	if !s.Delete("a") {
		t.Fatal("Delete(a) should report a live object")
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("Get(a) after delete should miss")
	}
	if s.Delete("a") {
		t.Fatal("second Delete(a) should report no live object")
	}
	if s.Delete("never-existed") {
		t.Fatal("Delete of unknown key should report false")
	}
	if id := s.Put("", []byte("x")); id != 0 {
		t.Fatal("Put of empty key should be rejected with ID 0")
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("Get of empty key should miss")
	}
}

func TestDeleteRecreateIsNewObject(t *testing.T) {
	s := NewStore(Options{})
	id1 := s.Put("k", []byte("v1"))

	// Pin a snapshot while the first incarnation is live.
	pinned := Cursor{Version: s.Version()}

	s.Delete("k")
	id2 := s.Put("k", []byte("v2"))
	if id1 == id2 {
		t.Fatal("recreated key must get a fresh object ID")
	}

	// The pinned snapshot still sees the original incarnation.
	page, err := s.List("", pinned, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != id1 || string(page.Items[0].Value) != "v1" {
		t.Fatalf("pinned snapshot = %+v, want original incarnation", page.Items)
	}

	// A fresh snapshot sees only the new incarnation.
	items := collectPages(t, s, "", 10)
	if len(items) != 1 || items[0].ID != id2 || string(items[0].Value) != "v2" {
		t.Fatalf("fresh listing = %+v, want recreated incarnation", items)
	}
}

func TestEmptyPrefixListsEverything(t *testing.T) {
	s := NewStore(Options{})
	model := map[string][]byte{}
	for _, k := range []string{"a", "b/1", "b/2", "z", "\xff"} {
		model[k] = []byte("v-" + k)
		s.Put(k, model[k])
	}
	items := collectPages(t, s, "", 2)
	itemsMatch(t, items, frozenReference(model, ""))
}

func TestPrefixFiltering(t *testing.T) {
	s := NewStore(Options{})
	model := map[string][]byte{}
	for _, k := range []string{"obj/a", "obj/b", "obj/c/1", "other/x", "obj"} {
		model[k] = []byte("v")
		s.Put(k, model[k])
	}
	items := collectPages(t, s, "obj/", 1)
	itemsMatch(t, items, frozenReference(model, "obj/"))
}

func TestEmptyPagesStillAdvanceCursor(t *testing.T) {
	// ScanFactor 1 and limit 3 give a scan budget of 3 keys per call.
	s := NewStore(Options{ScanFactor: 1})
	for i := 0; i < 10; i++ {
		s.Put(fmt.Sprintf("a%d", i), []byte("v"))
	}
	// Prefix "z" matches nothing; every page is empty but the cursor
	// must advance by the scan budget each call.
	cur := Cursor{}
	var lastKeys []string
	for {
		page, err := s.List("z", cur, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("expected empty page, got %v", page.Items)
		}
		cur = page.Cursor
		if len(lastKeys) > 0 && cur.LastKey <= lastKeys[len(lastKeys)-1] {
			t.Fatalf("cursor did not advance: %q after %q", cur.LastKey, lastKeys)
		}
		lastKeys = append(lastKeys, cur.LastKey)
		if cur.Done {
			break
		}
	}
	// 10 keys, budget 3 per page: 4 pages to finish.
	want := []string{"a2", "a5", "a8", "a9"}
	if !reflect.DeepEqual(lastKeys, want) {
		t.Fatalf("advancing LastKeys = %v, want %v", lastKeys, want)
	}
}

func TestLimitCountsReturnedItemsOnly(t *testing.T) {
	s := NewStore(Options{ScanFactor: 100})
	// Interleave matching and non-matching keys.
	model := map[string][]byte{}
	for i := 0; i < 20; i++ {
		k := fmt.Sprintf("keep/%02d", i)
		model[k] = []byte(k)
		s.Put(k, model[k])
		s.Put(fmt.Sprintf("skip/%02d", i), []byte("x"))
	}
	cur := Cursor{}
	var all []Item
	for {
		page, err := s.List("keep/", cur, 4)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 4 {
			t.Fatalf("page returned %d items, limit 4", len(page.Items))
		}
		all = append(all, page.Items...)
		cur = page.Cursor
		if cur.Done {
			break
		}
	}
	itemsMatch(t, all, frozenReference(model, "keep/"))
}

func TestMaxKeyAndBoundary(t *testing.T) {
	s := NewStore(Options{})
	keys := []string{"a", "\xff", "\xff\xff", "z"}
	model := map[string][]byte{}
	for _, k := range keys {
		model[k] = []byte("v")
		s.Put(k, model[k])
	}
	// Empty prefix covers everything including the max keys.
	items := collectPages(t, s, "", 1)
	itemsMatch(t, items, frozenReference(model, ""))

	// Prefix of pure 0xff has no upper bound; must still terminate.
	items = collectPages(t, s, "\xff", 1)
	itemsMatch(t, items, frozenReference(model, "\xff"))

	// Cursor positioned past the maximum key finishes immediately.
	page, err := s.List("", Cursor{Version: s.Version(), LastKey: "\xff\xff"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Cursor.Done || len(page.Items) != 0 {
		t.Fatalf("past-max cursor = %+v, want done empty page", page)
	}
}

func TestSnapshotExpiry(t *testing.T) {
	s := NewStore(Options{Retention: 4})
	s.Put("a", []byte("1"))
	page, err := s.List("", Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	cur := page.Cursor

	// Still within the retention window.
	s.Put("b", []byte("1"))
	if _, err := s.List("", cur, 10); err != nil {
		t.Fatalf("cursor within retention should work: %v", err)
	}

	// Push the snapshot out of the retention window.
	for i := 0; i < 10; i++ {
		s.Put(fmt.Sprintf("k%d", i), []byte("v"))
	}
	if _, err := s.List("", cur, 10); err != ErrSnapshotExpired {
		t.Fatalf("expired cursor err = %v, want ErrSnapshotExpired", err)
	}

	// A version the store never produced is a different error.
	if _, err := s.List("", Cursor{Version: s.Version() + 100}, 10); err != ErrCursorVersion {
		t.Fatalf("future cursor err = %v, want ErrCursorVersion", err)
	}
}

func TestConcurrentUpdatesMatchFrozenReference(t *testing.T) {
	s := NewStore(Options{Retention: 100000})
	model := map[string][]byte{}
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("obj/%04d", i)
		model[k] = []byte(fmt.Sprintf("v%d", i))
		s.Put(k, model[k])
	}
	// Record the IDs of the frozen snapshot so we can verify the
	// listing returns the frozen incarnations, not recreated ones.
	frozenIDs := map[string]uint64{}
	for k := range model {
		it, _ := s.Get(k)
		frozenIDs[k] = it.ID
	}

	// Pin the snapshot, then freeze the reference model.
	first, err := s.List("obj/", Cursor{}, 7)
	if err != nil {
		t.Fatal(err)
	}
	cur := first.Cursor
	reference := frozenReference(model, "obj/")

	// Concurrently churn the store: delete and recreate keys, add new
	// ones. None of this may leak into the pinned listing.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			k := fmt.Sprintf("obj/%04d", i%200)
			s.Delete(k)
			s.Put(k, []byte("churn"))
			s.Put(fmt.Sprintf("obj/new%04d", i), []byte("n"))
		}
	}()

	all := append([]Item{}, first.Items...)
	for !cur.Done {
		page, err := s.List("obj/", cur, 7)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		cur = page.Cursor
	}
	close(stop)
	wg.Wait()

	itemsMatch(t, all, reference)
	for _, it := range all {
		if it.ID != frozenIDs[it.Key] {
			t.Fatalf("key %q listed with ID %d, want frozen ID %d", it.Key, it.ID, frozenIDs[it.Key])
		}
	}
}

func TestRandomizedAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewStore(Options{Retention: 100000, ScanFactor: 3})
	model := map[string][]byte{}
	prefixes := []string{"", "a", "b", "k/", "zz", "\xff"}
	keyspace := make([]string, 0, 60)
	for _, p := range []string{"a", "b", "k/", "zz", "\xff", "m"} {
		for i := 0; i < 10; i++ {
			keyspace = append(keyspace, fmt.Sprintf("%s%02d", p, i))
		}
	}

	for op := 0; op < 400; op++ {
		k := keyspace[rng.Intn(len(keyspace))]
		if rng.Intn(3) == 0 {
			if s.Delete(k) {
				delete(model, k)
			}
		} else {
			v := []byte(fmt.Sprintf("op%d", op))
			s.Put(k, v)
			model[k] = v
		}

		if op%17 == 0 {
			prefix := prefixes[rng.Intn(len(prefixes))]
			limit := 1 + rng.Intn(7)
			items := collectPages(t, s, prefix, limit)
			itemsMatch(t, items, frozenReference(model, prefix))
		}
	}
}
