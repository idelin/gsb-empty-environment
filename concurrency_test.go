package symbolcache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSharedDownloadSingleflight(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mirror := &fakeMirror{payload: pdb, started: started, release: release}
	cache, _ := newTestCache(t, mirror)

	const waiters = 8
	var wg sync.WaitGroup
	errs := make([]error, waiters)
	handles := make([]*Handle, waiters)
	wg.Add(waiters)
	for i := 0; i < waiters; i++ {
		go func(i int) {
			defer wg.Done()
			h, err := cache.Get(context.Background(), key)
			errs[i] = err
			handles[i] = h
		}(i)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("download never started")
	}
	// Let every waiter attach before releasing the single fetch.
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	if mirror.callCount() != 1 {
		t.Fatalf("mirror calls = %d, want a single shared download", mirror.callCount())
	}
	for i, h := range handles {
		if h == nil {
			t.Fatalf("waiter %d got nil handle", i)
		}
		_ = h.Close()
	}
}

func TestIndependentCancellation(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	mirror := &fakeMirror{payload: pdb, started: started, release: release}
	cache, _ := newTestCache(t, mirror)

	// Leader keeps the shared download alive.
	leaderDone := make(chan struct{})
	go func() {
		h, err := cache.Get(context.Background(), key)
		if err == nil {
			_ = h.Close()
		}
		close(leaderDone)
	}()
	<-started

	// A second caller cancels; it must fail without disturbing the leader.
	ctx, cancel := context.WithCancel(context.Background())
	cancelErr := make(chan error, 1)
	go func() {
		h, err := cache.Get(ctx, key)
		if h != nil {
			_ = h.Close()
		}
		cancelErr <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := <-cancelErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller err = %v, want context.Canceled", err)
	}

	close(release)
	select {
	case <-leaderDone:
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not finish after the shared download completed")
	}
	if mirror.callCount() != 1 {
		t.Fatalf("mirror calls = %d, want 1", mirror.callCount())
	}

	// The validated object is now cached and serves a fresh caller immediately.
	h, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("post-cancel Get: %v", err)
	}
	_ = h.Close()
}

func TestAllWaitersCancelStopsDownload(t *testing.T) {
	key := testKey(testGUID, 7)
	pdb := buildPDB(testGUID, 7, 512, nil)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	mirror := &fakeMirror{payload: pdb, started: started, release: release}
	cache, dir := newTestCache(t, mirror)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		h, err := cache.Get(ctx, key)
		if h != nil {
			_ = h.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// Once the only waiter is gone nothing may be published and temp is clean.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(dir, "tmp"))
		if countObjects(t, dir) == 0 && len(entries) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := countObjects(t, dir); n != 0 {
		t.Fatal("canceled download published an object")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(entries) != 0 {
		t.Fatalf("temp dir not clean after cancel: %v", entries)
	}
}

func TestDifferentKeysDoNotShare(t *testing.T) {
	pdbA := buildPDB(testGUID, 7, 512, nil)
	otherGUID := testGUID
	otherGUID[0] ^= 0xff
	pdbB := buildPDB(otherGUID, 9, 512, nil)

	ma := &fakeMirror{name: "a", payload: pdbA}
	mb := &fakeMirror{name: "b", payload: pdbB}
	cache, dir := newTestCache(t, ma, mb)

	hA, err := cache.Get(context.Background(), testKey(testGUID, 7))
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	hB, err := cache.Get(context.Background(), testKey(otherGUID, 9))
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if hA.RelativePath() == hB.RelativePath() {
		t.Fatal("different keys resolved to the same object path")
	}
	_ = hA.Close()
	_ = hB.Close()
	if n := countObjects(t, dir); n != 2 {
		t.Fatalf("objects = %d, want 2", n)
	}
}
