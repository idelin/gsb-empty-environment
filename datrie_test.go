package datrie

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// verify checks the structural invariants of the double arrays:
//   - the free list contains exactly the slots with check == freeCheck;
//   - every used non-root slot i satisfies i == base[check[i]] + c for some
//     byte c, i.e. the parent really points back at it;
//   - every used slot is reachable from the root (no orphans);
//   - the used counter matches reality.
func verify(t *testing.T, tr *Trie) {
	t.Helper()

	inFree := make(map[int]bool)
	for cur := tr.freeHead; cur != freeNil; cur = tr.base[cur] {
		if inFree[int(cur)] {
			t.Fatalf("free list cycle at slot %d", cur)
		}
		inFree[int(cur)] = true
	}

	for i := 1; i < len(tr.check); i++ {
		if tr.check[i] == freeCheck {
			if !inFree[i] {
				t.Fatalf("slot %d free but not in free list", i)
			}
			continue
		}
		if inFree[i] {
			t.Fatalf("slot %d used but in free list", i)
		}
		p := int(tr.check[i])
		if p < 0 || p >= len(tr.check) || tr.check[p] == freeCheck {
			t.Fatalf("slot %d has invalid parent %d", i, p)
		}
		if tr.base[p] == 0 {
			t.Fatalf("slot %d: parent %d has no base", i, p)
		}
		d := i - int(tr.base[p])
		if d < 0 || d > 255 {
			t.Fatalf("slot %d not a child slot of parent %d (base=%d)", i, p, tr.base[p])
		}
	}

	// Reachability from root: catches orphan slots left by relocation.
	seen := map[int]bool{0: true}
	queue := []int{0}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if tr.base[n] == 0 {
			continue
		}
		b := int(tr.base[n])
		for c := 0; c < 256; c++ {
			idx := b + c
			if idx < len(tr.check) && tr.check[idx] == int32(n) {
				if seen[idx] {
					t.Fatalf("slot %d reachable twice", idx)
				}
				seen[idx] = true
				queue = append(queue, idx)
			}
		}
	}
	if len(seen) != tr.used {
		t.Fatalf("reachable slots %d != used %d (orphans present)", len(seen), tr.used)
	}
	if tr.used != len(tr.check)-len(inFree) {
		t.Fatalf("used=%d but slots-free=%d", tr.used, len(tr.check)-len(inFree))
	}
}

func TestInsertAndSearch(t *testing.T) {
	tr := New()
	words := map[string]int64{"he": 1, "hear": 2, "hello": 3, "help": 4, "world": 5}
	for k, v := range words {
		tr.Insert(k, v)
	}
	verify(t, tr)
	for k, want := range words {
		if got, ok := tr.Search(k); !ok || got != want {
			t.Fatalf("Search(%q) = %v,%v want %v,true", k, got, ok, want)
		}
	}
	for _, k := range []string{"h", "hell", "helloo", "word", ""} {
		if _, ok := tr.Search(k); ok {
			t.Fatalf("Search(%q) unexpectedly found", k)
		}
	}
	if tr.Len() != len(words) {
		t.Fatalf("Len() = %d, want %d", tr.Len(), len(words))
	}
}

func TestPrefixOfWordIsWord(t *testing.T) {
	tr := New()
	tr.Insert("hello", 5)
	tr.Insert("he", 2)
	tr.Insert("h", 1)
	verify(t, tr)
	for k, want := range map[string]int64{"h": 1, "he": 2, "hello": 5} {
		if got, ok := tr.Search(k); !ok || got != want {
			t.Fatalf("Search(%q) = %v,%v want %v,true", k, got, ok, want)
		}
	}
	// Prefix that is not a word must not match.
	if _, ok := tr.Search("hel"); ok {
		t.Fatal("Search(\"hel\") should miss")
	}
}

func TestDuplicateInsertUpdatesValueNoOrphans(t *testing.T) {
	tr := New()
	tr.Insert("hello", 1)
	tr.Insert("help", 2)
	before := tr.Stats()
	tr.Insert("hello", 42)
	tr.Insert("hello", 43)
	after := tr.Stats()
	verify(t, tr)
	if got, _ := tr.Search("hello"); got != 43 {
		t.Fatalf("value = %d, want 43", got)
	}
	if after.Used != before.Used || after.Slots != before.Slots {
		t.Fatalf("duplicate insert changed slots: %+v -> %+v", before, after)
	}
	if tr.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", tr.Len())
	}
}

func TestRelocationKeepsChildren(t *testing.T) {
	tr := New()
	// Many keys sharing prefixes force base conflicts and relocations.
	keys := []string{
		"a", "ab", "abc", "abcd", "b", "ba", "bab", "c", "ca", "cab",
		"apple", "apply", "apt", "bat", "batch", "bath", "cat", "catch",
	}
	for i, k := range keys {
		tr.Insert(k, int64(i+1))
		verify(t, tr)
	}
	for i, k := range keys {
		if got, ok := tr.Search(k); !ok || got != int64(i+1) {
			t.Fatalf("after relocations Search(%q) = %v,%v", k, got, ok)
		}
	}
}

func TestDelete(t *testing.T) {
	tr := New()
	tr.Insert("he", 1)
	tr.Insert("hello", 2)
	tr.Insert("help", 3)

	if !tr.Delete("hello") {
		t.Fatal("Delete(hello) = false")
	}
	if _, ok := tr.Search("hello"); ok {
		t.Fatal("hello still found after delete")
	}
	// Sibling and prefix words survive.
	if got, ok := tr.Search("help"); !ok || got != 3 {
		t.Fatal("help lost after deleting hello")
	}
	if got, ok := tr.Search("he"); !ok || got != 1 {
		t.Fatal("he lost after deleting hello")
	}
	if tr.Delete("hello") {
		t.Fatal("double delete reported success")
	}
	if tr.Delete("nope") {
		t.Fatal("delete of missing key reported success")
	}
	verify(t, tr)
}

func TestDeletePrunesAndRecyclesSlots(t *testing.T) {
	tr := New()
	tr.Insert("abc", 1)
	tr.Insert("abd", 2)
	full := tr.Stats()

	tr.Delete("abd")
	verify(t, tr)
	pruned := tr.Stats()
	if pruned.Used != full.Used-1 || pruned.Free != full.Free+1 {
		t.Fatalf("delete did not prune: %+v -> %+v", full, pruned)
	}

	// Re-inserting the same arc claims the recycled slot instead of growing.
	tr.Insert("abd", 3)
	verify(t, tr)
	reused := tr.Stats()
	if reused.Slots != pruned.Slots || reused.Free != full.Free {
		t.Fatalf("free slot not recycled: %+v -> %+v", pruned, reused)
	}
	if got, ok := tr.Search("abd"); !ok || got != 3 {
		t.Fatal("abd not found")
	}
	if got, ok := tr.Search("abc"); !ok || got != 1 {
		t.Fatal("abc not found")
	}
}

func TestPrefixSearch(t *testing.T) {
	tr := New()
	words := map[string]int64{
		"he": 1, "hear": 2, "heard": 3, "hello": 4, "help": 5, "world": 6,
	}
	for k, v := range words {
		tr.Insert(k, v)
	}
	got := tr.PrefixSearch("he")
	want := []Entry{
		{"he", 1}, {"hear", 2}, {"heard", 3}, {"hello", 4}, {"help", 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PrefixSearch(he) = %v, want %v", got, want)
	}
	if got := tr.PrefixSearch("w"); len(got) != 1 || got[0].Key != "world" {
		t.Fatalf("PrefixSearch(w) = %v", got)
	}
	if got := tr.PrefixSearch("xyz"); got != nil {
		t.Fatalf("PrefixSearch(xyz) = %v, want nil", got)
	}
	if got := tr.PrefixSearch(""); len(got) != len(words) {
		t.Fatalf("PrefixSearch(\"\") returned %d entries, want %d", len(got), len(words))
	}
}

func TestEmptyKey(t *testing.T) {
	tr := New()
	tr.Insert("", 7)
	tr.Insert("a", 1)
	if got, ok := tr.Search(""); !ok || got != 7 {
		t.Fatal("empty key not found")
	}
	if !tr.Delete("") {
		t.Fatal("Delete(\"\") = false")
	}
	if _, ok := tr.Search(""); ok {
		t.Fatal("empty key still found")
	}
	verify(t, tr)
}

func TestUTF8Keys(t *testing.T) {
	tr := New()
	tr.Insert("中", 1)
	tr.Insert("中文", 2)
	tr.Insert("中国", 3)
	if got, ok := tr.Search("中文"); !ok || got != 2 {
		t.Fatal("中文 not found")
	}
	got := tr.PrefixSearch("中")
	if len(got) != 3 || got[0].Key != "中" || got[1].Key != "中国" || got[2].Key != "中文" {
		t.Fatalf("PrefixSearch(中) = %v", got)
	}
	verify(t, tr)
}

// TestRandomAgainstMap fuzzes insert/delete against a reference map and
// re-checks all structural invariants after every operation, so relocation
// bugs (stale check values, orphan slots) cannot slip through.
func TestRandomAgainstMap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tr := New()
	ref := make(map[string]int64)

	randKey := func() string {
		n := rng.Intn(6) + 1
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(4)) // tiny alphabet => many collisions
		}
		return string(b)
	}

	for op := 0; op < 5000; op++ {
		k := randKey()
		if rng.Intn(3) == 0 {
			if tr.Delete(k) {
				delete(ref, k)
			} else if _, ok := ref[k]; ok {
				t.Fatalf("op %d: Delete(%q) missed but ref has it", op, k)
			}
		} else {
			v := rng.Int63()
			tr.Insert(k, v)
			ref[k] = v
		}
		verify(t, tr)
	}

	if tr.Len() != len(ref) {
		t.Fatalf("Len() = %d, want %d", tr.Len(), len(ref))
	}
	for k, want := range ref {
		if got, ok := tr.Search(k); !ok || got != want {
			t.Fatalf("Search(%q) = %v,%v want %v,true", k, got, ok, want)
		}
	}

	// Full traversal must equal the sorted reference set.
	keys := make([]string, 0, len(ref))
	for k := range ref {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	all := tr.PrefixSearch("")
	if len(all) != len(keys) {
		t.Fatalf("PrefixSearch(\"\") = %d entries, want %d", len(all), len(keys))
	}
	for i, e := range all {
		if e.Key != keys[i] || e.Value != ref[e.Key] {
			t.Fatalf("entry %d = %v, want key %q", i, e, keys[i])
		}
	}
	fmt.Printf("random test: %d words, %+v\n", len(ref), tr.Stats())
}
