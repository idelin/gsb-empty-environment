package btree_test

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"

	"btree"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "btree-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

type kv struct{ k, v string }

func scanAll(t *testing.T, snap *btree.Snapshot) []kv {
	t.Helper()
	var out []kv
	err := snap.Scan(nil, nil, func(k, v []byte) bool {
		out = append(out, kv{string(k), string(v)})
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustSnapshot(t *testing.T, db *btree.DB) *btree.Snapshot {
	t.Helper()
	snap, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// model is the reference implementation: a sorted unique key set.
type model struct {
	m map[string]string
}

func newModel() *model { return &model{m: make(map[string]string)} }

func (md *model) put(k, v string) { md.m[k] = v }

func (md *model) sorted() []kv {
	keys := make([]string, 0, len(md.m))
	for k := range md.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{k, md.m[k]})
	}
	return out
}

func (md *model) rangeSlice(start, end string) []kv {
	var out []kv
	for _, e := range md.sorted() {
		if start != "" && e.k < start {
			continue
		}
		if end != "" && e.k >= end {
			break
		}
		out = append(out, e)
	}
	return out
}

func equalKVs(a, b []kv) bool {
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

func checkModel(t *testing.T, db *btree.DB, md *model) {
	t.Helper()
	if err := db.Check(); err != nil {
		t.Fatalf("structure check: %v", err)
	}
	got := scanAll(t, mustSnapshot(t, db))
	want := md.sorted()
	if !equalKVs(got, want) {
		t.Fatalf("full scan mismatch: got %d entries, want %d\nfirst got: %+v\nfirst want: %+v",
			len(got), len(want), first3(got), first3(want))
	}
}

func first3(s []kv) []kv {
	if len(s) > 3 {
		return s[:3]
	}
	return s
}

func TestBasicInsertGetScan(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 512})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	md := newModel()
	rng := rand.New(rand.NewSource(42))
	keys := rng.Perm(200)
	for _, i := range keys {
		k := fmt.Sprintf("key-%04d", i)
		v := fmt.Sprintf("val-%d", i*7)
		if err := db.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
		md.put(k, v)
	}
	checkModel(t, db, md)

	// Point lookups: existing and missing.
	for _, i := range []int{0, 57, 199} {
		k := fmt.Sprintf("key-%04d", i)
		v, found, err := db.Get([]byte(k))
		if err != nil || !found {
			t.Fatalf("Get(%s): found=%v err=%v", k, found, err)
		}
		if string(v) != md.m[k] {
			t.Fatalf("Get(%s) = %s, want %s", k, v, md.m[k])
		}
	}
	if _, found, _ := db.Get([]byte("key-9999")); found {
		t.Fatal("Get returned found for missing key")
	}

	// Range scans against the model.
	for _, r := range [][2]string{
		{"key-0000", "key-0010"},
		{"key-0050", "key-0051"},
		{"key-0190", ""},         // open end
		{"", "key-0003"},         // open start
		{"key-0100", "key-0100"}, // empty
	} {
		var got []kv
		var start, end []byte
		if r[0] != "" {
			start = []byte(r[0])
		}
		if r[1] != "" {
			end = []byte(r[1])
		}
		err := db.Scan(start, end, func(k, v []byte) bool {
			got = append(got, kv{string(k), string(v)})
			return true
		})
		if err != nil {
			t.Fatal(err)
		}
		want := md.rangeSlice(r[0], r[1])
		if !equalKVs(got, want) {
			t.Fatalf("range [%q,%q): got %+v, want %+v", r[0], r[1], first3(got), first3(want))
		}
	}
}

// TestSmallPageFrequentSplits hammers a tiny-page tree so that leaf and
// internal splits (including root splits) happen constantly, verifying the
// parent/child pointers and the leaf chain after every batch.
func TestSmallPageFrequentSplits(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	md := newModel()
	rng := rand.New(rand.NewSource(7))
	total := 0
	// Sequential ascending, sequential descending, then random: different
	// split patterns.
	for round := 0; round < 3; round++ {
		for i := 0; i < 300; i++ {
			var k string
			switch round {
			case 0:
				k = fmt.Sprintf("a-%05d", i)
			case 1:
				k = fmt.Sprintf("b-%05d", 299-i)
			default:
				k = fmt.Sprintf("c-%05d", rng.Intn(100000))
			}
			v := fmt.Sprintf("v-%d", i)
			if err := db.Insert([]byte(k), []byte(v)); err != nil {
				t.Fatal(err)
			}
			md.put(k, v)
			total++
			if total%50 == 0 {
				checkModel(t, db, md)
			}
		}
	}
	checkModel(t, db, md)

	h, err := mustSnapshot(t, db).Height()
	if err != nil {
		t.Fatal(err)
	}
	if h < 3 {
		t.Fatalf("expected height >= 3 after %d inserts into 128-byte pages, got %d", total, h)
	}
	t.Logf("height=%d after %d inserts (pageSize=128)", h, total)
}

func TestDuplicateKeyOverwrite(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	md := newModel()
	for round := 0; round < 5; round++ {
		for i := 0; i < 100; i++ {
			k := fmt.Sprintf("dup-%03d", i)
			v := fmt.Sprintf("round-%d", round)
			if err := db.Insert([]byte(k), []byte(v)); err != nil {
				t.Fatal(err)
			}
			md.put(k, v)
		}
		if err := db.Check(); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	got := scanAll(t, mustSnapshot(t, db))
	if len(got) != 100 {
		t.Fatalf("duplicate inserts leaked entries: got %d, want 100", len(got))
	}
	if !equalKVs(got, md.sorted()) {
		t.Fatal("overwrite produced wrong values")
	}
	// Overwrite with a much larger value forces re-splits of full leaves.
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("dup-%03d", i)
		v := fmt.Sprintf("large-%03d-xxxxxxxxxxxxxxxxxxxx", i)
		if err := db.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
		md.put(k, v)
	}
	checkModel(t, db, md)
}

func TestReopenPersistence(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 256})
	if err != nil {
		t.Fatal(err)
	}
	md := newModel()
	for i := 0; i < 150; i++ {
		k := fmt.Sprintf("persist-%04d", i)
		v := fmt.Sprintf("val-%d", i)
		if err := db.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
		md.put(k, v)
	}
	gen := db.Gen()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := btree.Open(dir, nil) // no options: page size comes from disk
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if db2.Gen() != gen {
		t.Fatalf("generation lost across reopen: %d != %d", db2.Gen(), gen)
	}
	if db2.PageSize() != 256 {
		t.Fatalf("page size lost across reopen: %d", db2.PageSize())
	}
	checkModel(t, db2, md)
}

func TestScanBoundaries(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 50; i++ {
		if err := db.Insert([]byte(fmt.Sprintf("k%02d", i)), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	// End is exclusive: [k10, k20) has exactly k10..k19.
	var got []string
	err = db.Scan([]byte("k10"), []byte("k20"), func(k, v []byte) bool {
		got = append(got, string(k))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || got[0] != "k10" || got[9] != "k19" {
		t.Fatalf("bad range result: %v", got)
	}
	// Early stop.
	count := 0
	err = db.Scan(nil, nil, func(k, v []byte) bool {
		count++
		return count < 5
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("early stop failed: %d", count)
	}
	// Range on an empty tree.
	dir2 := tempDir(t)
	db2, err := btree.Open(dir2, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	n := 0
	if err := db2.Scan(nil, nil, func(k, v []byte) bool { n++; return true }); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("empty tree scan returned %d entries", n)
	}
	if err := db2.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestKeyValueTooLarge(t *testing.T) {
	dir := tempDir(t)
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	big := make([]byte, db.MaxKeySize()+1)
	if err := db.Insert(big, []byte("v")); err != btree.ErrKeyTooLarge {
		t.Fatalf("want ErrKeyTooLarge, got %v", err)
	}
	bigV := make([]byte, db.MaxValueSize(1)+1)
	if err := db.Insert([]byte("k"), bigV); err != btree.ErrValueTooLarge {
		t.Fatalf("want ErrValueTooLarge, got %v", err)
	}
	// Max-size key and value still fit.
	k := make([]byte, db.MaxKeySize())
	v := make([]byte, db.MaxValueSize(len(k)))
	if err := db.Insert(k, v); err != nil {
		t.Fatalf("max-size insert failed: %v", err)
	}
	if err := db.Check(); err != nil {
		t.Fatal(err)
	}
}
