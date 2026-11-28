package cowfs

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// TestFreeInvalidatesARCAndReuseIsFresh drives the full storage-layer loop:
// read a block through the ARC, force it onto a deadlist, delete the retaining
// snapshot (which frees the physical id and invalidates the ARC), then reuse
// the id with new content and prove no stale or ghost-induced read occurs.
func TestFreeInvalidatesARCAndReuseIsFresh(t *testing.T) {
	e := NewEngine(WithARCCapacity(16))

	if err := e.Write("doc", []byte("hello-cow")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot("S1"); err != nil {
		t.Fatal(err)
	}

	// Capture the physical id of the original block and warm it in the ARC.
	originalID := e.head["doc"][0].ID
	if data, err := e.Read("doc"); err != nil || !bytes.Equal(data, []byte("hello-cow")) {
		t.Fatalf("initial read data=%q err=%v", data, err)
	}
	if t1, t2, _, _ := e.ARC().Contains(originalID); !t1 && !t2 {
		t.Fatal("original block should be resident in the ARC")
	}

	// Overwrite after the snapshot: the old block moves to the active deadlist
	// but stays physically live and ARC-cached while S1 retains it.
	if err := e.Write("doc", []byte("second-version")); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Store().Read(originalID); !ok {
		t.Fatal("deadlisted block must remain physically allocated")
	}

	// Snapshot the new state, then delete S1. The original block has no other
	// observer, so it is freed and the ARC entry (resident or ghost) is dropped.
	if _, err := e.Snapshot("S2"); err != nil {
		t.Fatal(err)
	}
	freed, err := e.DeleteSnapshot("S1")
	if err != nil {
		t.Fatal(err)
	}
	if freed != len("hello-cow") {
		t.Fatalf("freed %d bytes, want %d", freed, len("hello-cow"))
	}
	if _, ok := e.Store().Read(originalID); ok {
		t.Fatal("physical block should have been freed")
	}
	if t1, t2, b1, b2 := e.ARC().Contains(originalID); t1 || t2 || b1 || b2 {
		t.Fatal("ARC entry survived block free; dirty-read guard broken")
	}

	// Allocate until the freed id is handed out again with brand-new content.
	var reused bool
	for i := 0; i < 64 && !reused; i++ {
		payload := []byte(fmt.Sprintf("reuse-%02d", i))
		if err := e.Write(fmt.Sprintf("fill%d", i), payload); err != nil {
			t.Fatal(err)
		}
		if e.head[fmt.Sprintf("fill%d", i)][0].ID == originalID {
			reused = true
			// Read through the ARC: the reused id must expose new bytes.
			got, err := e.Read(fmt.Sprintf("fill%d", i))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("dirty read on reused id: got %q want %q", got, payload)
			}
			if bytes.Contains(got, []byte("hello-cow")) {
				t.Fatalf("stale content returned for reused id: %q", got)
			}
		}
	}
	if !reused {
		t.Fatal("freed physical id was never reused in the test window")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentWritesSnapshotsReads exercises the locking contract under a
// mixed parallel workload and validates accounting at the end.
func TestConcurrentWritesSnapshotsReads(t *testing.T) {
	e := NewEngine(WithARCCapacity(32))
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				file := fmt.Sprintf("f%d", (worker+i)%6)
				if err := e.Write(file, mkBlock(1+(i%12), byte(worker))); err != nil {
					t.Error(err)
					return
				}
				if _, err := e.Read(file); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			name := fmt.Sprintf("snap-%d", i)
			if _, err := e.Snapshot(name); err != nil {
				t.Error(err)
				return
			}
			if i%2 == 0 {
				if _, err := e.DeleteSnapshot(name); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()

	wg.Wait()
	if err := e.Validate(); err != nil {
		t.Fatalf("post-concurrency validation: %v", err)
	}
	if err := e.ARC().CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// TestMultiBlockFileDeadlist verifies slot-level CoW and deadlist handling for
// files composed of several blocks.
func TestMultiBlockFileDeadlist(t *testing.T) {
	e := NewEngine()

	if err := e.WriteBlock("m", 0, []byte("aaaa")); err != nil {
		t.Fatal(err)
	}
	if err := e.WriteBlock("m", 1, []byte("bbbb")); err != nil {
		t.Fatal(err)
	}
	if err := e.WriteBlock("m", 2, []byte("cccc")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Snapshot("S1"); err != nil {
		t.Fatal(err)
	}

	firstBlockID := e.head["m"][0].ID
	if err := e.WriteBlock("m", 0, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}

	got, err := e.Read("m")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "AAAAbbbbcccc" {
		t.Fatalf("live content = %q", got)
	}
	old, err := e.ReadAtSnapshot("S1", "m")
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "aaaabbbbcccc" {
		t.Fatalf("snapshot content = %q", old)
	}

	// The displaced first block is retained on S2's future deadlist boundary.
	if _, ok := e.Store().Read(firstBlockID); !ok {
		t.Fatal("displaced block must stay live while the snapshot retains it")
	}

	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	if err := e.WriteBlock("m", 9, nil); err == nil {
		t.Fatal("gap slot write must be rejected")
	}
}
