package latchcrabbing

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSequentialInsertAndContains(t *testing.T) {
	tree := New(3)
	const total = 1000

	for i := 0; i < total; i++ {
		key := (i * 37) % total
		if !tree.Insert(key) {
			t.Fatalf("first insert of %d reported duplicate", key)
		}
	}
	for i := 0; i < total; i++ {
		if tree.Insert(i) {
			t.Fatalf("duplicate insert of %d reported success", i)
		}
	}
	if got := tree.Len(); got != total {
		t.Fatalf("Len() = %d, want %d", got, total)
	}
	for i := 0; i < total; i++ {
		if !tree.Contains(i) {
			t.Fatalf("Contains(%d) = false", i)
		}
	}
	if tree.Contains(total + 1) {
		t.Fatalf("Contains(%d) = true for absent key", total+1)
	}
	if got := tree.Height(); got < 3 {
		t.Fatalf("Height() = %d, want at least 3", got)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	tree := New(4)
	const (
		preloaded        = 512
		readerCount      = 10
		writerCount      = 5
		readsPerReader   = 1000
		insertsPerWriter = 200
	)

	for i := 0; i < preloaded; i++ {
		tree.Insert(i)
	}

	var wg sync.WaitGroup
	var readErrors int32
	start := make(chan struct{})

	for reader := 0; reader < readerCount; reader++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for i := 0; i < readsPerReader; i++ {
				key := (id*131 + i) % preloaded
				if !tree.Contains(key) {
					atomic.AddInt32(&readErrors, 1)
					return
				}
			}
		}(reader)
	}

	for writer := 0; writer < writerCount; writer++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			base := preloaded + id*insertsPerWriter
			for i := 0; i < insertsPerWriter; i++ {
				tree.Insert(base + i)
			}
		}(writer)
	}

	close(start)
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent readers and writers did not finish; possible deadlock")
	}

	if got := atomic.LoadInt32(&readErrors); got != 0 {
		t.Fatalf("readers observed %d missing preloaded keys", got)
	}

	wantLen := preloaded + writerCount*insertsPerWriter
	if got := tree.Len(); got != wantLen {
		t.Fatalf("Len() = %d, want %d", got, wantLen)
	}
	for i := preloaded; i < wantLen; i++ {
		if !tree.Contains(i) {
			t.Fatalf("inserted key %d is not visible", i)
		}
	}
	if got := tree.Stats().EarlyWriteReleases; got == 0 {
		t.Fatal("writers never released a safe ancestor early")
	}
}

func TestLowLevelWriterDoesNotBlockHighLevelReader(t *testing.T) {
	tree := New(4)
	for i := 0; i < 256; i++ {
		tree.Insert(i)
	}
	if got := tree.Height(); got < 3 {
		t.Fatalf("test requires height at least 3, got %d", got)
	}

	leftLeaf := leftmostLeaf(tree.header.children[0])
	rightLeaf := rightmostLeaf(tree.header.children[0])
	if leftLeaf == rightLeaf {
		t.Fatal("test requires at least two leaves")
	}

	rightLeaf.mu.Lock()
	defer rightLeaf.mu.Unlock()

	result := make(chan bool, 1)
	go func() {
		result <- tree.Contains(0)
	}()

	select {
	case found := <-result:
		if !found {
			t.Fatal("reader did not find preloaded key 0")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("reader blocked behind a write latch on an unrelated leaf")
	}
}

func TestSafeChildReleasesAncestorsEarly(t *testing.T) {
	tree := New(4)
	for i := 0; i < 128; i++ {
		tree.Insert(i)
	}

	before := tree.Stats().EarlyWriteReleases
	tree.Insert(1000)
	after := tree.Stats().EarlyWriteReleases
	if after <= before {
		t.Fatalf("EarlyWriteReleases did not increase: before=%d after=%d", before, after)
	}
}

func leftmostLeaf(n *node) *node {
	for !n.leaf {
		n = n.children[0]
	}
	return n
}

func rightmostLeaf(n *node) *node {
	for !n.leaf {
		n = n.children[len(n.children)-1]
	}
	return n
}
