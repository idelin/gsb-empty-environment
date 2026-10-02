
package lsm

import (
	"fmt"
	"io/ioutil"
	"os"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "lsm-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPutGetFlushCompact(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	e.SetFlushThreshold(100)

	// Three generations of SSTables; key space overlaps across generations.
	for gen := 0; gen < 3; gen++ {
		for i := 0; i < 100; i++ {
			key := fmt.Sprintf("key-%04d", i)
			val := fmt.Sprintf("gen%d-val-%04d", gen, i)
			if err := e.Put(key, val); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(e.SSTables()); got != 3 {
		t.Fatalf("want 3 sstables, got %d", got)
	}

	// Newest generation wins before compaction.
	v, ok, err := e.Get("key-0042")
	if err != nil || !ok {
		t.Fatalf("get failed: ok=%v err=%v", ok, err)
	}
	if v != "gen2-val-0042" {
		t.Fatalf("stale value before compaction: %q", v)
	}

	oldTables := e.SSTables()
	if err := e.Compact(); err != nil {
		t.Fatal(err)
	}

	// Compaction merged everything into one table and deleted the old files.
	if got := len(e.SSTables()); got != 1 {
		t.Fatalf("want 1 sstable after compaction, got %d", got)
	}
	for _, name := range oldTables {
		if _, err := os.Stat(dir + "/" + name); !os.IsNotExist(err) {
			t.Fatalf("old sstable %s still exists after compaction", name)
		}
	}

	// Data survives compaction with newest values.
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%04d", i)
		want := fmt.Sprintf("gen2-val-%04d", i)
		v, ok, err := e.Get(key)
		if err != nil || !ok || v != want {
			t.Fatalf("after compaction: %s = %q (ok=%v err=%v), want %q", key, v, ok, err, want)
		}
	}
}

func TestReopenRecoversSSTables(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	e, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := e.Put(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Flush(); err != nil {
		t.Fatal(err)
	}

	e2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(e2.SSTables()); got != 1 {
		t.Fatalf("reopen: want 1 sstable, got %d", got)
	}
	v, ok, err := e2.Get("k7")
	if err != nil || !ok || v != "v7" {
		t.Fatalf("reopen get: %q ok=%v err=%v", v, ok, err)
	}

	// Sequence continues past recovered files, no name collision.
	if err := e2.Put("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := len(e2.SSTables()); got != 2 {
		t.Fatalf("reopen flush: want 2 sstables, got %d", got)
	}
}

func TestSSTableRoundTrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	entries := map[string]string{"b": "2", "a": "1", "c": "3"}
	path := dir + "/sst-000001.sst"
	if err := WriteSSTable(path, entries); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSSTable(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range entries {
		if got[k] != want {
			t.Fatalf("key %s: got %q want %q", k, got[k], want)
		}
	}
}
