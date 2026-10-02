package stcs

import (
	"fmt"
	"os"
	"testing"
)

func TestBucketTables(t *testing.T) {
	mk := func(size int64) *Table { return &Table{Size: size} }
	tables := []*Table{
		mk(10 << 20), mk(11 << 20), mk(9 << 20), mk(10 << 20), // one tier
		mk(40 << 20), // bigger tier
		mk(41 << 20),
	}
	buckets := bucketTables(tables, 1.5)
	if len(buckets) != 2 {
		t.Fatalf("got %d buckets, want 2", len(buckets))
	}
	var small, big int
	for _, b := range buckets {
		switch len(b) {
		case 4:
			small++
		case 2:
			big++
		default:
			t.Fatalf("unexpected bucket size %d", len(b))
		}
	}
	if small != 1 || big != 1 {
		t.Fatalf("buckets = %v (want one of 4 and one of 2)", buckets)
	}
}

func TestCompactorTriggersAtThreshold(t *testing.T) {
	dir := t.TempDir()
	m := &Metrics{}
	c := NewCompactor(dir, 4, 1.5, m)

	var events []CompactEvent
	c.OnCompact = func(ev CompactEvent) { events = append(events, ev) }

	// Flush 3 tables: below threshold, nothing happens.
	for i := 0; i < 3; i++ {
		tab := writeTable(t, dir, nameFor(i), []Entry{
			{Key: []byte{byte('a' + i)}, Value: []byte("v"), Seq: uint64(i + 1)},
		})
		if err := c.AddTable(tab); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.ReadAmplification(); got != 3 {
		t.Fatalf("live tables = %d, want 3 (no compaction yet)", got)
	}
	if m.Compactions != 0 {
		t.Fatalf("compactions = %d, want 0", m.Compactions)
	}

	// The 4th similar-sized table triggers the merge.
	tab := writeTable(t, dir, nameFor(3), []Entry{
		{Key: []byte("d"), Value: []byte("v"), Seq: 4},
	})
	if err := c.AddTable(tab); err != nil {
		t.Fatal(err)
	}
	if got := c.ReadAmplification(); got != 1 {
		t.Fatalf("live tables = %d, want 1 after compaction", got)
	}
	if m.Compactions != 1 {
		t.Fatalf("compactions = %d, want 1", m.Compactions)
	}
	if len(events) != 1 || len(events[0].Inputs) != 4 || events[0].LiveAfter != 1 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if m.CompactReadBytes == 0 || m.CompactWriteBytes == 0 {
		t.Fatal("compaction I/O counters not updated")
	}

	// Input files must be gone; only the merged output remains.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir holds %v, want a single merged table", names)
	}

	// The merged table holds all four entries in order.
	merged := c.Tables()[0]
	if merged.Count != 4 {
		t.Fatalf("merged count = %d, want 4", merged.Count)
	}
	it, err := merged.Iterator()
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	want := "abcd"
	i := 0
	for it.Next() {
		if string(it.Entry().Key) != string(want[i]) {
			t.Fatalf("entry %d key = %q, want %q", i, it.Entry().Key, want[i])
		}
		i++
	}
}

func nameFor(i int) string {
	return fmt.Sprintf("t%d.sst", i)
}
