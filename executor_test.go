package sortexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"testing"
)

func makeRecords(n int, seed int64) []Record {
	recs := make([]Record, n)
	for i := range recs {
		recs[i] = Record{Key: int64(i), Payload: []byte(fmt.Sprintf("val-%d", i))}
	}
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(n, func(i, j int) { recs[i], recs[j] = recs[j], recs[i] })
	return recs
}

func checkSortedBatch(t *testing.T, batch []Record, wantStart int) {
	t.Helper()
	for i, rec := range batch {
		want := int64(wantStart + i)
		if rec.Key != want {
			t.Fatalf("batch[%d].Key = %d, want %d", i, rec.Key, want)
		}
		if got, wantP := string(rec.Payload), fmt.Sprintf("val-%d", want); got != wantP {
			t.Fatalf("batch[%d].Payload = %q, want %q", i, got, wantP)
		}
	}
}

func checkClean(t *testing.T, tr *Tracker, fs *MemFS) {
	t.Helper()
	if !tr.Balanced() {
		t.Fatalf("resources leaked: open files=%d open iterators=%d",
			tr.OpenFiles(), tr.OpenIterators())
	}
	if left := fs.Files(); len(left) != 0 {
		t.Fatalf("spill files left behind: %v", left)
	}
}

func drain(t *testing.T, c *Cursor) []Record {
	t.Helper()
	var all []Record
	for {
		batch, err := c.Next(context.Background())
		if err == io.EOF {
			return all
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		all = append(all, batch...)
	}
}

func TestRunSortsAndDelivers(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ex := NewExecutor(fs, WithRunSize(64), WithBatchSize(100), WithTracker(tr))

	cur, err := ex.Run(context.Background(), makeRecords(1000, 42))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	all := drain(t, cur)
	if len(all) != 1000 {
		t.Fatalf("got %d records, want 1000", len(all))
	}
	checkSortedBatch(t, all, 0)
	if err := cur.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkClean(t, tr, fs)
}

func TestEmptyInput(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ex := NewExecutor(fs, WithTracker(tr))

	cur, err := ex.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := cur.Next(context.Background()); err != io.EOF {
		t.Fatalf("Next = %v, want io.EOF", err)
	}
	checkClean(t, tr, fs)
}

func TestCancelDuringProduce(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ctx, cancel := context.WithCancel(context.Background())
	barriers := Barriers{
		Step: func(p Phase, n int64) {
			if p == PhaseProduce && n == 7 {
				cancel()
			}
		},
	}
	ex := NewExecutor(fs, WithRunSize(4), WithTracker(tr), WithBarriers(barriers))

	_, err := ex.Run(ctx, makeRecords(100, 1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	checkClean(t, tr, fs)
}

func TestCancelDuringMerge(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ctx, cancel := context.WithCancel(context.Background())
	barriers := Barriers{
		Step: func(p Phase, n int64) {
			if p == PhaseMerge && n == 5 {
				cancel()
			}
		},
	}
	ex := NewExecutor(fs, WithRunSize(4), WithTracker(tr), WithBarriers(barriers))

	_, err := ex.Run(ctx, makeRecords(100, 2))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if opened := tr.IteratorsOpened(); opened == 0 {
		t.Fatal("expected merge iterators to have been opened")
	}
	checkClean(t, tr, fs)
}

func TestCancelDuringDeliverKeepsDeliveredBatch(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ex := NewExecutor(fs, WithRunSize(10), WithBatchSize(10), WithTracker(tr))

	ctx, cancel := context.WithCancel(context.Background())
	cur, err := ex.Run(ctx, makeRecords(50, 3))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	batch, err := cur.Next(ctx)
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	cancel()
	if _, err := cur.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next after cancel = %v, want context.Canceled", err)
	}
	// Sticky error on subsequent calls.
	if _, err := cur.Next(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("sticky Next = %v, want context.Canceled", err)
	}
	// The batch delivered before cancellation stays valid and complete.
	if len(batch) != 10 {
		t.Fatalf("delivered batch has %d records, want 10", len(batch))
	}
	checkSortedBatch(t, batch, 0)
	if err := cur.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	checkClean(t, tr, fs)
}

func TestCloseBeforeExhaustion(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ex := NewExecutor(fs, WithRunSize(10), WithBatchSize(10), WithTracker(tr))

	cur, err := ex.Run(context.Background(), makeRecords(50, 4))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	batch, err := cur.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	checkSortedBatch(t, batch, 0)
	// Close is idempotent; Next after close reports ErrClosed.
	for i := 0; i < 3; i++ {
		if err := cur.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
	}
	if _, err := cur.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %v, want ErrClosed", err)
	}
	// Delivered batch is untouched by Close.
	checkSortedBatch(t, batch, 0)
	checkClean(t, tr, fs)
}

// 50 records, runSize 10, batchSize 10, payloads always present:
//   - produce: 5 run files (creates 1-5), 2 writes/record -> writes 1-100
//   - merge:   opens 1-5 (run files), create 6 (sorted), writes 101-200,
//     reads 1-100 (run contents)
//   - deliver: open 6 (sorted), reads 101-200
func faultExecutor(inner *MemFS, tr *Tracker) (*Executor, *FaultFS) {
	ffs := &FaultFS{Inner: inner}
	ex := NewExecutor(ffs, WithRunSize(10), WithBatchSize(10), WithTracker(tr))
	return ex, ffs
}

func TestFaultInjection(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*FaultFS)
	}{
		{"create in produce", func(f *FaultFS) { f.CreateAt = 1 }},
		{"create in produce later run", func(f *FaultFS) { f.CreateAt = 4 }},
		{"write in produce", func(f *FaultFS) { f.WriteAt = 30 }},
		{"open in merge", func(f *FaultFS) { f.OpenAt = 3 }},
		{"read in merge", func(f *FaultFS) { f.ReadAt = 50 }},
		{"create sorted in merge", func(f *FaultFS) { f.CreateAt = 6 }},
		{"write in merge", func(f *FaultFS) { f.WriteAt = 150 }},
		{"open in deliver", func(f *FaultFS) { f.OpenAt = 6 }},
		{"read in deliver", func(f *FaultFS) { f.ReadAt = 150 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := NewMemFS()
			tr := &Tracker{}
			ex, ffs := faultExecutor(fs, tr)
			tc.apply(ffs)

			cur, err := ex.Run(context.Background(), makeRecords(50, 5))
			if err == nil {
				// Failure lands in the deliver phase.
				for {
					_, err = cur.Next(context.Background())
					if err != nil {
						break
					}
				}
			}
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("err = %v, want ErrInjected", err)
			}
			if cur != nil {
				if err := cur.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			}
			checkClean(t, tr, fs)
		})
	}
}

func TestIteratorClosedExactlyOnceOnMergeFailure(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	ex, ffs := faultExecutor(fs, tr)
	ffs.ReadAt = 10 // fail mid-merge while reading run files

	_, err := ex.Run(context.Background(), makeRecords(50, 6))
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("Run = %v, want ErrInjected", err)
	}
	if opened := tr.IteratorsOpened(); opened != 5 {
		t.Fatalf("iterators opened = %d, want 5", opened)
	}
	if got := tr.OpenIterators(); got != 0 {
		t.Fatalf("open iterators = %d, want 0 (each closed exactly once)", got)
	}
	checkClean(t, tr, fs)
}

func TestBarrierEnterFiresPerPhase(t *testing.T) {
	fs := NewMemFS()
	tr := &Tracker{}
	var entered []Phase
	barriers := Barriers{
		Enter: func(p Phase) { entered = append(entered, p) },
	}
	ex := NewExecutor(fs, WithRunSize(10), WithBatchSize(10),
		WithTracker(tr), WithBarriers(barriers))

	cur, err := ex.Run(context.Background(), makeRecords(30, 7))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	drain(t, cur)
	want := []Phase{PhaseProduce, PhaseMerge, PhaseDeliver}
	if len(entered) != len(want) {
		t.Fatalf("entered phases = %v, want %v", entered, want)
	}
	for i := range want {
		if entered[i] != want[i] {
			t.Fatalf("entered phases = %v, want %v", entered, want)
		}
	}
	checkClean(t, tr, fs)
}
