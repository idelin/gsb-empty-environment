package blink

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentInsertionsAndScans(t *testing.T) {
	tree := NewWithOptions(Options{MaxKeys: 8, MaxChildren: 8})

	const (
		stableKeys   = 400
		insertedKeys = 2400
		writers      = 8
		scanners     = 4
	)
	for i := 0; i < stableKeys; i++ {
		tree.Insert(keyOf(i), valueOf(i))
	}

	start := make(chan struct{})
	stopScans := make(chan struct{})
	var scanWG sync.WaitGroup
	scanErr := make(chan error, 1)

	for s := 0; s < scanners; s++ {
		scanWG.Add(1)
		go func() {
			defer scanWG.Done()
			<-start
			expect := make(map[string]bool, stableKeys)
			for i := 0; i < stableKeys; i++ {
				expect[string(keyOf(i))] = true
			}
			for {
				select {
				case <-stopScans:
					return
				default:
				}
				entries := tree.Scan(nil, nil)
				seen := make(map[string]bool, len(entries))
				var prev []byte
				for i, entry := range entries {
					if i > 0 && bytes.Compare(prev, entry.Key) >= 0 {
						select {
						case scanErr <- fmt.Errorf("scan is not strictly sorted at %d", i):
						default:
						}
						return
					}
					if seen[string(entry.Key)] {
						select {
						case scanErr <- fmt.Errorf("duplicate key %q", entry.Key):
						default:
						}
						return
					}
					seen[string(entry.Key)] = true
					prev = entry.Key
				}
				for key := range expect {
					if !seen[key] {
						select {
						case scanErr <- fmt.Errorf("scan missed stable key %q", key):
						default:
						}
						return
					}
				}
			}
		}()
	}

	var writeWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writeWG.Add(1)
		go func(worker int) {
			defer writeWG.Done()
			<-start
			for i := 0; i < insertedKeys/writers; i++ {
				n := stableKeys + worker*(insertedKeys/writers) + i
				tree.Insert(keyOf(n), valueOf(n))
				if i%7 == 0 {
					if got, ok := tree.Get(keyOf(n)); !ok || !bytes.Equal(got, valueOf(n)) {
						select {
						case scanErr <- fmt.Errorf("Get(%d) failed during insert", n):
						default:
						}
						return
					}
				}
			}
		}(w)
	}

	close(start)
	writeWG.Wait()
	close(stopScans)
	scanWG.Wait()
	select {
	case err := <-scanErr:
		t.Fatal(err)
	default:
	}

	for i := 0; i < stableKeys+insertedKeys; i++ {
		if got, ok := tree.Get(keyOf(i)); !ok || !bytes.Equal(got, valueOf(i)) {
			t.Fatalf("final Get(%d) = %q, %v", i, got, ok)
		}
	}
	if got, want := tree.Len(), stableKeys+insertedKeys; got != want {
		t.Fatalf("Len = %d, want %d", got, want)
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := tree.Stats().RootSplits; got == 0 {
		t.Fatal("expected at least one root split")
	}
	if got := tree.Height(); got < 3 {
		t.Fatalf("height = %d, want a multi-level tree", got)
	}
}

func TestConcurrentMixedReadWriteDelete(t *testing.T) {
	tree := NewWithOptions(Options{MaxKeys: 6, MaxChildren: 6})
	const (
		workers = 12
		perWork = 350
		total   = workers * perWork
	)

	var wg sync.WaitGroup
	errs := make(chan error, workers*4)
	start := make(chan struct{})

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			base := worker * perWork
			for i := 0; i < perWork; i++ {
				n := base + i
				tree.Insert(keyOf(n), valueOf(n))
				if i%9 == 0 {
					if got, ok := tree.Get(keyOf(n)); !ok || !bytes.Equal(got, valueOf(n)) {
						errs <- fmt.Errorf("worker %d missed its insert %d", worker, n)
						return
					}
				}
				if i%31 == 0 {
					entries := tree.Scan(keyOf(base), keyOf(base+i))
					if len(entries) == 0 {
						errs <- fmt.Errorf("worker %d empty local scan", worker)
						return
					}
					prev := entries[0].Key
					for _, entry := range entries[1:] {
						if bytes.Compare(prev, entry.Key) >= 0 {
							errs <- fmt.Errorf("worker %d local scan out of order", worker)
							return
						}
						prev = entry.Key
					}
				}
			}
		}(w)
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if tree.Len() != total {
		t.Fatalf("Len = %d, want %d", tree.Len(), total)
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after inserts: %v", err)
	}

	start = make(chan struct{})
	deleteErrs := make(chan error, workers*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			base := worker * perWork
			for i := 0; i < perWork; i++ {
				n := base + i
				if n%2 == 0 {
					if _, ok := tree.Delete(keyOf(n)); !ok {
						deleteErrs <- fmt.Errorf("worker %d failed to delete %d", worker, n)
						return
					}
				} else if got, ok := tree.Get(keyOf(n)); !ok || !bytes.Equal(got, valueOf(n)) {
					deleteErrs <- fmt.Errorf("worker %d false negative for %d", worker, n)
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(deleteErrs)
	for err := range deleteErrs {
		t.Fatal(err)
	}

	tree.ReclaimEmpty()
	want := total / 2
	if tree.Len() != want {
		t.Fatalf("Len after deletes = %d, want %d", tree.Len(), want)
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after deletes: %v", err)
	}
	entries := tree.Scan(nil, nil)
	if len(entries) != want {
		t.Fatalf("scan after deletes = %d, want %d", len(entries), want)
	}
	for i, entry := range entries {
		if i > 0 && bytes.Compare(entries[i-1].Key, entry.Key) >= 0 {
			t.Fatalf("post-delete scan is not sorted at %d", i)
		}
	}
}

func TestConcurrentDeleteAndScan(t *testing.T) {
	tree := NewWithOptions(Options{MaxKeys: 8, MaxChildren: 8})
	const (
		keys    = 2400
		readers = 6
	)
	for i := 0; i < keys; i++ {
		tree.Insert(keyOf(i), valueOf(i))
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate before deletes: %v", err)
	}

	start := make(chan struct{})
	stop := make(chan struct{})
	var readersWG sync.WaitGroup
	firstErr := make(chan error, 1)

	for r := 0; r < readers; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				entries := tree.Scan(nil, nil)
				seen := make(map[string]bool, len(entries))
				for i, entry := range entries {
					if i > 0 && bytes.Compare(entries[i-1].Key, entry.Key) >= 0 {
						select {
						case firstErr <- fmt.Errorf("scan is not strictly sorted at %d", i):
						default:
						}
						return
					}
					if seen[string(entry.Key)] {
						select {
						case firstErr <- fmt.Errorf("scan returned duplicate key %q", entry.Key):
						default:
						}
						return
					}
					seen[string(entry.Key)] = true
				}
			}
		}()
	}

	const deleters = 8
	deleteErr := make(chan error, 1)
	var deleteWG sync.WaitGroup
	for d := 0; d < deleters; d++ {
		deleteWG.Add(1)
		go func(worker int) {
			defer deleteWG.Done()
			<-start
			for n := worker * 2; n < keys; n += deleters * 2 {
				if _, ok := tree.Delete(keyOf(n)); !ok {
					select {
					case deleteErr <- fmt.Errorf("delete missed key %d", n):
					default:
					}
					return
				}
			}
		}(d)
	}

	close(start)
	deleteWG.Wait()
	close(stop)
	readersWG.Wait()

	select {
	case err := <-firstErr:
		t.Fatal(err)
	default:
	}
	select {
	case err := <-deleteErr:
		t.Fatal(err)
	default:
	}
	tree.ReclaimEmpty()
	if got, want := tree.Len(), keys-keys/2; got != want {
		t.Fatalf("Len after deletes = %d, want %d", got, want)
	}
	for n := 0; n < keys; n++ {
		_, ok := tree.Get(keyOf(n))
		if n%2 == 0 && ok {
			t.Fatalf("deleted key %d is still present", n)
		}
		if n%2 == 1 && !ok {
			t.Fatalf("live key %d is missing", n)
		}
	}
	if err := tree.Validate(); err != nil {
		t.Fatalf("Validate after concurrent deletes: %v", err)
	}
}
