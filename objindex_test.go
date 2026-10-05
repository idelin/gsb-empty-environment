package objindex

import (
	"fmt"
	"testing"
)

func collectAll(t *testing.T, ix *Index, prefix string, limit int) []Object {
	t.Helper()
	var out []Object
	var cur Cursor
	for {
		page, err := ix.List(prefix, cur, limit)
		if err != nil {
			t.Fatalf("List(%q): %v", prefix, err)
		}
		out = append(out, page.Objects...)
		cur = page.Cursor
		if page.Done {
			return out
		}
	}
}

func keysOf(objs []Object) []string {
	keys := make([]string, len(objs))
	for i, o := range objs {
		keys[i] = o.Key
	}
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAddAndListInOrder(t *testing.T) {
	ix := New()
	ix.Add("b", []byte("2"))
	ix.Add("a", []byte("1"))
	ix.Add("c", []byte("3"))

	got := collectAll(t, ix, "", 10)
	want := []string{"a", "b", "c"}
	if !equalStrings(keysOf(got), want) {
		t.Fatalf("got %v, want %v", keysOf(got), want)
	}
	if string(got[0].Value) != "1" || got[0].Generation != 1 {
		t.Fatalf("unexpected first object: %+v", got[0])
	}
}

func TestDeleteTombstone(t *testing.T) {
	ix := New()
	ix.Add("k", []byte("v"))
	if !ix.Delete("k") {
		t.Fatal("first Delete should report removal")
	}
	if ix.Delete("k") {
		t.Fatal("second Delete should report no live object")
	}
	if ix.Delete("absent") {
		t.Fatal("Delete of unknown key should report false")
	}
	if got := collectAll(t, ix, "", 10); len(got) != 0 {
		t.Fatalf("tombstoned key listed: %v", keysOf(got))
	}
}

func TestDeleteRecreateIsDistinctObject(t *testing.T) {
	ix := New()
	g1 := ix.Add("k", []byte("old"))
	snap, _ := ix.Freeze()
	if !ix.Delete("k") {
		t.Fatal("Delete failed")
	}
	g2 := ix.Add("k", []byte("new"))
	if g2 <= g1 {
		t.Fatalf("recreated generation %d not after %d", g2, g1)
	}

	// Old snapshot still sees the original object.
	page, err := ix.List("", Cursor{Snapshot: snap}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Generation != g1 || string(page.Objects[0].Value) != "old" {
		t.Fatalf("old snapshot: %+v", page.Objects)
	}

	// Current snapshot sees only the recreated object.
	got := collectAll(t, ix, "", 10)
	if len(got) != 1 || got[0].Generation != g2 || string(got[0].Value) != "new" {
		t.Fatalf("current snapshot: %+v", got)
	}

	// A cursor positioned at the old generation must not skip the new one.
	page, err = ix.List("", Cursor{Snapshot: ix.Version(), LastKey: "k", LastGen: g1 - 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 1 || page.Objects[0].Generation != g2 {
		t.Fatalf("resume before recreation: %+v", page.Objects)
	}
}

func TestLimitCountsReturnedItemsOnly(t *testing.T) {
	ix := New(WithMaxScan(1000))
	for i := 0; i < 5; i++ {
		ix.Add(fmt.Sprintf("k%d", i), []byte{byte('0' + i)})
	}
	ix.Delete("k1")
	ix.Delete("k3")

	page, err := ix.List("", Cursor{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page.Objects); !equalStrings(got, []string{"k0", "k2"}) {
		t.Fatalf("page 1: %v", got)
	}
	if page.Done {
		t.Fatal("page 1 should not be done")
	}
	page, err = ix.List("", page.Cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(page.Objects); !equalStrings(got, []string{"k4"}) {
		t.Fatalf("page 2: %v", got)
	}
	if !page.Done {
		t.Fatal("page 2 should be done")
	}
}

func TestEmptyPageStillAdvancesCursor(t *testing.T) {
	// maxScan 2 with a run of tombstones forces empty pages.
	ix := New(WithMaxScan(2))
	ix.Add("live", []byte("x"))
	for i := 0; i < 5; i++ {
		k := fmt.Sprintf("t%d", i)
		ix.Add(k, []byte("x"))
		ix.Delete(k)
	}

	var cur Cursor
	seenEmpty := false
	var got []Object
	for i := 0; i < 20; i++ {
		page, err := ix.List("", cur, 1)
		if err != nil {
			t.Fatal(err)
		}
		prev := cur
		cur = page.Cursor
		if len(page.Objects) == 0 && !page.Done {
			seenEmpty = true
			if cur.LastKey == prev.LastKey && cur.LastGen == prev.LastGen {
				t.Fatal("empty page did not advance cursor")
			}
		}
		got = append(got, page.Objects...)
		if page.Done {
			if !seenEmpty {
				t.Fatal("expected at least one filtered empty page")
			}
			if !equalStrings(keysOf(got), []string{"live"}) {
				t.Fatalf("got %v", keysOf(got))
			}
			return
		}
	}
	t.Fatal("listing did not terminate")
}

func TestSnapshotExpiry(t *testing.T) {
	ix := New(WithRetainVersions(2))
	ix.Add("a", []byte("1"))
	snap, _ := ix.Freeze()
	ix.Add("b", []byte("2"))
	ix.Add("c", []byte("3"))
	ix.Add("d", []byte("4"))

	if _, err := ix.List("", Cursor{Snapshot: snap}, 10); err != ErrSnapshotExpired {
		t.Fatalf("got %v, want ErrSnapshotExpired", err)
	}
	if _, err := ix.List("", Cursor{Snapshot: ix.Version()}, 10); err != nil {
		t.Fatalf("current snapshot should be valid: %v", err)
	}
	if _, err := ix.List("", Cursor{Snapshot: ix.Version() + 1}, 10); err != ErrSnapshotFuture {
		t.Fatalf("got %v, want ErrSnapshotFuture", err)
	}
	if _, err := ix.List("", Cursor{}, 0); err != ErrInvalidLimit {
		t.Fatalf("got %v, want ErrInvalidLimit", err)
	}
}

func TestPrefixFiltering(t *testing.T) {
	ix := New()
	for _, k := range []string{"app/a", "app/b", "apple/c", "banana"} {
		ix.Add(k, []byte(k))
	}
	got := collectAll(t, ix, "app", 2)
	if !equalStrings(keysOf(got), []string{"app/a", "app/b", "apple/c"}) {
		t.Fatalf("prefix app: %v", keysOf(got))
	}
	got = collectAll(t, ix, "app/", 10)
	if !equalStrings(keysOf(got), []string{"app/a", "app/b"}) {
		t.Fatalf("prefix app/: %v", keysOf(got))
	}
}

func TestEmptyPrefixNoMatch(t *testing.T) {
	ix := New()
	ix.Add("a", []byte("1"))
	page, err := ix.List("zzz", Cursor{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 0 || !page.Done {
		t.Fatalf("no-match prefix: %+v", page)
	}
	// Empty index, empty prefix.
	empty := New()
	page, err = empty.List("", Cursor{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 0 || !page.Done {
		t.Fatalf("empty index: %+v", page)
	}
}

func TestMaxKeyBoundary(t *testing.T) {
	ix := New()
	keys := []string{"a", "ab", "abc", "\xff", "\xff\xff"}
	for _, k := range keys {
		ix.Add(k, []byte(k))
	}
	// Exact-fit limit: last page ends at the max key and must report Done.
	var cur Cursor
	var got []Object
	for {
		page, err := ix.List("", cur, len(keys))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Objects...)
		if !page.Done {
			t.Fatal("single full page should be done")
		}
		if !equalStrings(keysOf(got), keys) {
			t.Fatalf("got %q, want %q", keysOf(got), keys)
		}
		cur = page.Cursor
		break
	}
	// Resuming past the max key yields an empty, done page.
	page, err := ix.List("", cur, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 0 || !page.Done {
		t.Fatalf("after max key: %+v", page)
	}
	// Paginating one by one reaches the max key exactly.
	got = collectAll(t, ix, "", 1)
	if !equalStrings(keysOf(got), keys) {
		t.Fatalf("page-by-page: got %q, want %q", keysOf(got), keys)
	}
}

func TestCursorEncodeDecode(t *testing.T) {
	ix := New()
	for i := 0; i < 3; i++ {
		ix.Add(fmt.Sprintf("k%d", i), []byte("v"))
	}
	page, err := ix.List("", Cursor{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	token := page.Cursor.Encode()
	cur, err := DecodeCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	if cur != page.Cursor {
		t.Fatalf("round trip: %+v != %+v", cur, page.Cursor)
	}
	rest, err := ix.List("", cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(keysOf(rest.Objects), []string{"k2"}) {
		t.Fatalf("resume from decoded cursor: %v", keysOf(rest.Objects))
	}
	if _, err := DecodeCursor("not-a-cursor"); err == nil {
		t.Fatal("invalid token should fail")
	}
}

func TestCompactionPreservesVisibleSnapshots(t *testing.T) {
	ix := New(WithRetainVersions(3))
	frozenBySnap := make(map[uint64]map[string]Object)
	for i := 0; i < 10; i++ {
		k := fmt.Sprintf("k%d", i%3)
		ix.Add(k, []byte{byte('0' + i)})
		if i%2 == 1 {
			ix.Delete(k)
		}
		snap, frozen := ix.Freeze()
		frozenBySnap[snap] = frozen
	}
	// The oldest retained snapshot must still list consistently with the
	// frozen reference captured at that version, despite compaction.
	minValid := ix.Version() - 2
	cur := Cursor{Snapshot: minValid}
	var listed []Object
	for {
		page, err := ix.List("", cur, 2)
		if err != nil {
			t.Fatalf("oldest retained snapshot: %v", err)
		}
		listed = append(listed, page.Objects...)
		cur = page.Cursor
		if page.Done {
			break
		}
	}
	want := refListing(frozenBySnap[minValid], "")
	if !equalObjects(listed, want) {
		t.Fatalf("oldest retained snapshot: got %v, want %v", keysOf(listed), keysOf(want))
	}
	if _, err := ix.List("", Cursor{Snapshot: minValid - 1}, 100); err != ErrSnapshotExpired {
		t.Fatalf("got %v, want ErrSnapshotExpired", err)
	}
}
