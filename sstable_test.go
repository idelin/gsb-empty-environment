package stcs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTableRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.sst")

	w, err := NewTableWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 1000
	for i := 0; i < n; i++ {
		e := Entry{
			Key:   []byte(fmt.Sprintf("key%06d", i)),
			Value: []byte(fmt.Sprintf("val-%d", i)),
			Seq:   uint64(i + 1),
		}
		if err := w.Add(e); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	meta, err := w.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if meta.Count != n {
		t.Fatalf("count = %d, want %d", meta.Count, n)
	}
	if string(meta.FirstKey) != "key000000" || string(meta.LastKey) != "key000999" {
		t.Fatalf("key range = %q..%q", meta.FirstKey, meta.LastKey)
	}
	if meta.MinSeq != 1 || meta.MaxSeq != n {
		t.Fatalf("seq range = %d..%d", meta.MinSeq, meta.MaxSeq)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != st.Size() {
		t.Fatalf("reported size %d != stat size %d", meta.Size, st.Size())
	}

	rt, err := OpenTable(path)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Count != meta.Count || rt.Size != meta.Size ||
		string(rt.FirstKey) != string(meta.FirstKey) ||
		string(rt.LastKey) != string(meta.LastKey) ||
		rt.MinSeq != meta.MinSeq || rt.MaxSeq != meta.MaxSeq {
		t.Fatalf("reopened meta mismatch: %+v vs %+v", rt, meta)
	}

	it, err := rt.Iterator()
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	i := 0
	for it.Next() {
		e := it.Entry()
		wantKey := fmt.Sprintf("key%06d", i)
		wantVal := fmt.Sprintf("val-%d", i)
		if string(e.Key) != wantKey || string(e.Value) != wantVal || e.Seq != uint64(i+1) {
			t.Fatalf("entry %d = {%q %q %d}, want {%q %q %d}",
				i, e.Key, e.Value, e.Seq, wantKey, wantVal, i+1)
		}
		i++
	}
	if err := it.Err(); err != nil {
		t.Fatal(err)
	}
	if i != n {
		t.Fatalf("iterated %d entries, want %d", i, n)
	}
}

func TestTableWriterRejectsUnsortedKeys(t *testing.T) {
	dir := t.TempDir()
	w, err := NewTableWriter(filepath.Join(dir, "t.sst"))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(Entry{Key: []byte("b"), Value: []byte("1"), Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Add(Entry{Key: []byte("a"), Value: []byte("1"), Seq: 2}); err == nil {
		t.Fatal("expected error for out-of-order key")
	}
	if err := w.Add(Entry{Key: []byte("b"), Value: []byte("1"), Seq: 3}); err == nil {
		t.Fatal("expected error for duplicate key")
	}
}
