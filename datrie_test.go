package datrie

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// checkInvariants verifies the structural invariants of the double
// arrays: every occupied slot is reachable from its parent recorded in
// check, and every child of a used state points back at that state.
func checkInvariants(t *testing.T, tr *Trie) {
	t.Helper()
	for i := 1; i < len(tr.check); i++ {
		p := tr.check[i]
		if p == freeCheck {
			continue
		}
		found := false
		for c := 0; c < alphabet; c++ {
			if int(tr.base[p])+c == i {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("slot %d: check=%d but no transition from parent reaches it", i, p)
		}
	}
}

func TestPutGetPrefixWords(t *testing.T) {
	tr := New()
	words := map[string]int64{
		"a": 1, "ab": 2, "abc": 3, "abd": 4,
		"b": 5, "bc": 6,
		"he": 7, "hello": 8, "hell": 9,
	}
	for k, v := range words {
		tr.Put(k, v)
	}
	if tr.Len() != len(words) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(words))
	}
	for k, want := range words {
		got, ok := tr.Get(k)
		if !ok || got != want {
			t.Fatalf("Get(%q) = %d,%v want %d,true", k, got, ok, want)
		}
	}
	for _, absent := range []string{"", "x", "ac", "abcd", "hel", "hi"} {
		if _, ok := tr.Get(absent); ok {
			t.Fatalf("Get(%q) unexpectedly found", absent)
		}
	}
	checkInvariants(t, tr)
}

func TestDuplicatePutUpdatesInPlace(t *testing.T) {
	tr := New()
	tr.Put("go", 1)
	tr.Put("gopher", 2)
	before := tr.Stats()
	tr.Put("go", 42)   // update existing key
	tr.Put("gopher", 7) // update existing key
	after := tr.Stats()
	if tr.Len() != 2 {
		t.Fatalf("Len = %d, want 2", tr.Len())
	}
	if after.UsedSlots != before.UsedSlots || after.Slots != before.Slots {
		t.Fatalf("duplicate insert allocated slots: before %+v after %+v", before, after)
	}
	if v, _ := tr.Get("go"); v != 42 {
		t.Fatalf("Get(go) = %d, want 42", v)
	}
	if v, _ := tr.Get("gopher"); v != 7 {
		t.Fatalf("Get(gopher) = %d, want 7", v)
	}
}

func TestRelocationKeepsChildren(t *testing.T) {
	tr := New()
	// Force many conflicts: single-byte keys all branch off the root,
	// and deep keys create long relocation chains.
	keys := make(map[string]int64)
	for c := 0; c < 128; c++ {
		k := string([]byte{byte(c)})
		keys[k] = int64(c)
		tr.Put(k, int64(c))
	}
	deep := []string{"apple", "application", "apply", "apt", "apex",
		"banana", "band", "bandana", "can", "canary", "canoe"}
	for i, k := range deep {
		keys[k] = int64(1000 + i)
		tr.Put(k, int64(1000+i))
	}
	for k, want := range keys {
		got, ok := tr.Get(k)
		if !ok || got != want {
			t.Fatalf("after relocations Get(%q) = %d,%v want %d,true", k, got, ok, want)
		}
	}
	checkInvariants(t, tr)
}

func TestDelete(t *testing.T) {
	tr := New()
	for i, k := range []string{"he", "hell", "hello", "help", "hero"} {
		tr.Put(k, int64(i+1))
	}
	if !tr.Delete("hello") {
		t.Fatal("Delete(hello) = false, want true")
	}
	if _, ok := tr.Get("hello"); ok {
		t.Fatal("hello still present after delete")
	}
	// Prefix words of the deleted key must survive.
	for _, k := range []string{"he", "hell", "help", "hero"} {
		if _, ok := tr.Get(k); !ok {
			t.Fatalf("%q lost after deleting hello", k)
		}
	}
	if tr.Delete("hello") {
		t.Fatal("second Delete(hello) = true, want false")
	}
	if tr.Delete("nope") {
		t.Fatal("Delete(nope) = true, want false")
	}
	if tr.Len() != 4 {
		t.Fatalf("Len = %d, want 4", tr.Len())
	}

	// Deleting a whole branch frees its slots for reuse.
	before := tr.Stats().UsedSlots
	tr.Delete("help")
	tr.Delete("hero")
	after := tr.Stats().UsedSlots
	if after >= before {
		t.Fatalf("expected slots to be freed: before %d after %d", before, after)
	}
	checkInvariants(t, tr)

	// The freed slots are reusable.
	tr.Put("hex", 99)
	if v, ok := tr.Get("hex"); !ok || v != 99 {
		t.Fatalf("Get(hex) = %d,%v after reuse", v, ok)
	}
	checkInvariants(t, tr)
}

func TestCollectPrefix(t *testing.T) {
	tr := New()
	words := []string{"app", "apple", "application", "apply", "banana", "band"}
	for i, w := range words {
		tr.Put(w, int64(i))
	}
	got := tr.Collect("app")
	want := []Entry{
		{Key: "app", Value: 0},
		{Key: "apple", Value: 1},
		{Key: "application", Value: 2},
		{Key: "apply", Value: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect(app) = %v, want %v", got, want)
	}
	if all := tr.Collect(""); len(all) != len(words) {
		t.Fatalf("Collect(\"\") returned %d entries, want %d", len(all), len(words))
	}
	if got := tr.Collect("zzz"); got != nil {
		t.Fatalf("Collect(zzz) = %v, want nil", got)
	}
}

func TestFuzzAgainstMap(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tr := New()
	ref := make(map[string]int64)
	randKey := func() string {
		n := 1 + rng.Intn(8)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(6))
		}
		return string(b)
	}
	for i := 0; i < 5000; i++ {
		k := randKey()
		v := int64(rng.Intn(1000))
		tr.Put(k, v)
		ref[k] = v
	}
	if tr.Len() != len(ref) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(ref))
	}
	for k, want := range ref {
		got, ok := tr.Get(k)
		if !ok || got != want {
			t.Fatalf("Get(%q) = %d,%v want %d,true", k, got, ok, want)
		}
	}
	// Delete half of the keys.
	i := 0
	for k := range ref {
		if i%2 == 0 {
			if !tr.Delete(k) {
				t.Fatalf("Delete(%q) failed", k)
			}
			delete(ref, k)
		}
		i++
	}
	if tr.Len() != len(ref) {
		t.Fatalf("after deletes Len = %d, want %d", tr.Len(), len(ref))
	}
	// Collect("") must match the reference map exactly.
	var gotKeys []string
	for _, e := range tr.Collect("") {
		want, ok := ref[e.Key]
		if !ok || want != e.Value {
			t.Fatalf("Collect returned stale entry %q=%d", e.Key, e.Value)
		}
		gotKeys = append(gotKeys, e.Key)
	}
	if len(gotKeys) != len(ref) {
		t.Fatalf("Collect returned %d keys, want %d", len(gotKeys), len(ref))
	}
	if !sort.StringsAreSorted(gotKeys) {
		t.Fatal("Collect output is not lexicographically sorted")
	}
	checkInvariants(t, tr)
}

func TestStatsLayout(t *testing.T) {
	tr := New()
	for i := 0; i < 100; i++ {
		tr.Put(fmt.Sprintf("key-%03d", i), int64(i))
	}
	st := tr.Stats()
	if st.Keys != 100 {
		t.Fatalf("Keys = %d, want 100", st.Keys)
	}
	if st.TotalBytes != st.BaseBytes+st.CheckBytes+st.ValueBytes+st.FlagBytes {
		t.Fatalf("inconsistent stats: %+v", st)
	}
	if st.UsedSlots > st.Slots || st.UsedSlots == 0 {
		t.Fatalf("bad slot counts: %+v", st)
	}
}
