package stcs

import (
	"path/filepath"
	"testing"
)

func writeTable(t *testing.T, dir, name string, entries []Entry) *Table {
	t.Helper()
	w, err := NewTableWriter(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := w.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	meta, err := w.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestMergeKeepsOnlyLatestVersion(t *testing.T) {
	dir := t.TempDir()
	// Three tables with overlapping keys; newer writes carry higher seqs.
	t1 := writeTable(t, dir, "a.sst", []Entry{
		{Key: []byte("apple"), Value: []byte("v1"), Seq: 1},
		{Key: []byte("banana"), Value: []byte("v1"), Seq: 2},
		{Key: []byte("cherry"), Value: []byte("v1"), Seq: 3},
	})
	t2 := writeTable(t, dir, "b.sst", []Entry{
		{Key: []byte("banana"), Value: []byte("v2"), Seq: 4},
		{Key: []byte("date"), Value: []byte("v2"), Seq: 5},
	})
	t3 := writeTable(t, dir, "c.sst", []Entry{
		{Key: []byte("apple"), Value: []byte("v3"), Seq: 6},
		{Key: []byte("banana"), Value: []byte("v3"), Seq: 7},
	})

	out := filepath.Join(dir, "out.sst")
	meta, deduped, err := MergeTables([]*Table{t1, t2, t3}, out)
	if err != nil {
		t.Fatal(err)
	}
	if deduped != 3 { // apple x1, banana x2 dropped
		t.Fatalf("deduped = %d, want 3", deduped)
	}
	if meta.Count != 4 {
		t.Fatalf("output count = %d, want 4", meta.Count)
	}

	want := []Entry{
		{Key: []byte("apple"), Value: []byte("v3"), Seq: 6},
		{Key: []byte("banana"), Value: []byte("v3"), Seq: 7},
		{Key: []byte("cherry"), Value: []byte("v1"), Seq: 3},
		{Key: []byte("date"), Value: []byte("v2"), Seq: 5},
	}
	it, err := meta.Iterator()
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	i := 0
	for it.Next() {
		if i >= len(want) {
			t.Fatal("too many entries")
		}
		e := it.Entry()
		w := want[i]
		if string(e.Key) != string(w.Key) || string(e.Value) != string(w.Value) || e.Seq != w.Seq {
			t.Fatalf("entry %d = {%q %q %d}, want {%q %q %d}",
				i, e.Key, e.Value, e.Seq, w.Key, w.Value, w.Seq)
		}
		i++
	}
	if i != len(want) {
		t.Fatalf("got %d entries, want %d", i, len(want))
	}
}

func TestMergeEmptyInputs(t *testing.T) {
	dir := t.TempDir()
	empty := writeTable(t, dir, "empty.sst", nil)
	full := writeTable(t, dir, "full.sst", []Entry{
		{Key: []byte("k"), Value: []byte("v"), Seq: 1},
	})
	meta, deduped, err := MergeTables([]*Table{empty, full}, filepath.Join(dir, "out.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if deduped != 0 || meta.Count != 1 {
		t.Fatalf("count=%d deduped=%d, want 1/0", meta.Count, deduped)
	}
}
