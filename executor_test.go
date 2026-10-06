package sortexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"
	"sync/atomic"
	"testing"
	"time"
)

var errInjected = errors.New("injected fault")

func newEnv(t *testing.T, opts Options) (*Executor, *FaultFS, string) {
	t.Helper()
	dir := t.TempDir()
	ffs := NewFaultFS(OSFS{Dir: dir})
	ffs.Err = errInjected
	return New(ffs, opts), ffs, dir
}

// assertQuiescent verifies every resource counter returned to zero, no
// handle was closed twice, and no spill file leaked on disk.
func assertQuiescent(t *testing.T, exec *Executor, ffs *FaultFS, dir string) {
	t.Helper()
	if n := exec.Tracker.Files(); n != 0 {
		t.Errorf("tracker files = %d, want 0", n)
	}
	if n := exec.Tracker.Iters(); n != 0 {
		t.Errorf("tracker iters = %d, want 0", n)
	}
	if n := ffs.OpenHandles(); n != 0 {
		t.Errorf("open handles = %d, want 0", n)
	}
	if names := ffs.OverClosed(); len(names) != 0 {
		t.Errorf("handles closed more than once: %v", names)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("spill dir not empty: %d entries left", len(entries))
	}
}

func makeRecords(n int, rnd *rand.Rand) []Record {
	recs := make([]Record, n)
	perm := rnd.Perm(n)
	for i := range recs {
		recs[i] = Record{
			Key:   fmt.Sprintf("key-%08d", perm[i]),
			Value: []byte(fmt.Sprintf("val-%d", i)),
		}
	}
	return recs
}

func sortedCopy(recs []Record) []Record {
	out := append([]Record(nil), recs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func drain(t *testing.T, it *Iterator) [][]Record {
	t.Helper()
	var batches [][]Record
	for {
		batch, err := it.Next(context.Background())
		if err == io.EOF {
			return batches
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		batches = append(batches, batch)
	}
}

func TestSortedDeliveryAndQuiescence(t *testing.T) {
	exec, ffs, dir := newEnv(t, Options{RunSize: 64, BatchSize: 100})
	rnd := rand.New(rand.NewSource(1))
	recs := makeRecords(1000, rnd)
	src := SliceSource(recs)

	it, err := exec.Sort(context.Background(), &src)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	var got []Record
	for _, batch := range drain(t, it) {
		got = append(got, batch...)
	}

	want := sortedCopy(recs)
	if len(got) != len(want) {
		t.Fatalf("delivered %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Key != want[i].Key || string(got[i].Value) != string(want[i].Value) {
			t.Fatalf("record %d = %v, want %v", i, got[i], want[i])
		}
	}
	// EOF auto-closed the iterator: everything must be back to zero.
	assertQuiescent(t, exec, ffs, dir)
	if err := it.Close(); err != nil {
		t.Fatalf("Close after EOF: %v", err)
	}
}

func TestCancelDuringProduce(t *testing.T) {
	barrier := NewBarrier()
	entered := make(chan struct{})
	exec, ffs, dir := newEnv(t, Options{
		RunSize: 10,
		Hooks: Hooks{
			AfterRun: func(ctx context.Context) error {
				close(entered)
				return barrier.Wait(ctx)
			},
		},
	})
	recs := makeRecords(100, rand.New(rand.NewSource(2)))
	src := SliceSource(recs)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := exec.Sort(ctx, &src)
		errCh <- err
	}()
	<-entered
	cancel()
	barrier.Release()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Sort error = %v, want context.Canceled", err)
	}
	assertQuiescent(t, exec, ffs, dir)
}

func TestCancelDuringMerge(t *testing.T) {
	barrier := NewBarrier()
	entered := make(chan struct{})
	exec, ffs, dir := newEnv(t, Options{
		RunSize: 10,
		Hooks: Hooks{
			BeforeMerge: func(ctx context.Context) error {
				close(entered)
				return barrier.Wait(ctx)
			},
		},
	})
	recs := makeRecords(100, rand.New(rand.NewSource(3)))
	src := SliceSource(recs)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := exec.Sort(ctx, &src)
		errCh <- err
	}()
	<-entered
	cancel()
	barrier.Release()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Sort error = %v, want context.Canceled", err)
	}
	assertQuiescent(t, exec, ffs, dir)
}

func TestCancelDuringDeliveryKeepsDeliveredBatches(t *testing.T) {
	barrier := NewBarrier()
	entered := make(chan struct{})
	var calls atomic.Int64
	exec, ffs, dir := newEnv(t, Options{
		RunSize:   10,
		BatchSize: 10,
		Hooks: Hooks{
			BeforeBatch: func(ctx context.Context) error {
				if calls.Add(1) == 2 {
					close(entered)
					return barrier.Wait(ctx)
				}
				return nil
			},
		},
	})
	recs := makeRecords(50, rand.New(rand.NewSource(4)))
	want := sortedCopy(recs)
	src := SliceSource(recs)

	ctx, cancel := context.WithCancel(context.Background())
	it, err := exec.Sort(ctx, &src)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}

	first, err := it.Next(ctx)
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := it.Next(ctx)
		errCh <- err
	}()
	<-entered
	cancel()
	barrier.Release()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Next error = %v, want context.Canceled", err)
	}

	// The batch delivered before cancellation stays valid and intact.
	if len(first) != 10 {
		t.Fatalf("first batch len = %d, want 10", len(first))
	}
	for i := range first {
		if first[i].Key != want[i].Key || string(first[i].Value) != string(want[i].Value) {
			t.Fatalf("delivered batch mutated at %d: got %v, want %v", i, first[i], want[i])
		}
	}
	// The failed Next auto-closed the iterator.
	if _, err := it.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after close = %v, want ErrClosed", err)
	}
	assertQuiescent(t, exec, ffs, dir)
}

func TestCloseIdempotentExactlyOnce(t *testing.T) {
	exec, ffs, dir := newEnv(t, Options{RunSize: 10, BatchSize: 10})
	recs := makeRecords(50, rand.New(rand.NewSource(5)))
	src := SliceSource(recs)

	it, err := exec.Sort(context.Background(), &src)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if _, err := it.Next(context.Background()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := it.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := it.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	assertQuiescent(t, exec, ffs, dir)
}

func TestFaultPropagation(t *testing.T) {
	cases := []struct {
		name  string
		arm   func(ffs *FaultFS)
		dirty bool // fault strikes mid-delivery rather than inside Sort
	}{
		{"create", func(f *FaultFS) { f.FailCreateAt = 2 }, false},
		{"write", func(f *FaultFS) { f.FailWriteAt = 1 }, false},
		{"open", func(f *FaultFS) { f.FailOpenAt = 1 }, false},
		{"read", func(f *FaultFS) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec, ffs, dir := newEnv(t, Options{RunSize: 10, BatchSize: 10})
			tc.arm(ffs)
			n := 50
			if tc.dirty {
				// Large values force bufio refills during delivery so
				// the armed read fault actually strikes mid-stream.
				n = 200
			}
			recs := makeRecords(n, rand.New(rand.NewSource(6)))
			if tc.dirty {
				for i := range recs {
					recs[i].Value = make([]byte, 512)
				}
			}
			src := SliceSource(recs)

			it, err := exec.Sort(context.Background(), &src)
			if !tc.dirty {
				if !errors.Is(err, errInjected) {
					t.Fatalf("Sort error = %v, want injected fault", err)
				}
				if it != nil {
					t.Fatalf("Sort returned iterator on failure")
				}
				assertQuiescent(t, exec, ffs, dir)
				return
			}

			// Read fault armed after the merge phase so it strikes
			// while a batch is being delivered.
			if err != nil {
				t.Fatalf("Sort: %v", err)
			}
			first, err := it.Next(context.Background())
			if err != nil {
				t.Fatalf("first Next: %v", err)
			}
			ffs.FailReadAt = ffs.ReadCalls() + 1
			var nextErr error
			for nextErr == nil {
				_, nextErr = it.Next(context.Background())
			}
			if !errors.Is(nextErr, errInjected) {
				t.Fatalf("Next error = %v, want injected fault", nextErr)
			}
			// The delivered batch is untouched by the failure.
			if len(first) != 10 {
				t.Fatalf("delivered batch len = %d, want 10", len(first))
			}
			assertQuiescent(t, exec, ffs, dir)
		})
	}
}

func TestEmptySource(t *testing.T) {
	exec, ffs, dir := newEnv(t, Options{RunSize: 10})
	src := SliceSource(nil)
	it, err := exec.Sort(context.Background(), &src)
	if err != nil {
		t.Fatalf("Sort: %v", err)
	}
	if _, err := it.Next(context.Background()); err != io.EOF {
		t.Fatalf("Next = %v, want io.EOF", err)
	}
	assertQuiescent(t, exec, ffs, dir)
}

func TestBarrierReleaseUnblocks(t *testing.T) {
	barrier := NewBarrier()
	done := make(chan error, 1)
	go func() { done <- barrier.Wait(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	barrier.Release()
	barrier.Release() // idempotent
	if err := <-done; err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
}
