package seqlog

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Log, string) {
	dir, err := ioutil.TempDir("", "seqlog")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Open(filepath.Join(dir, "test.log"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return l, dir
}

func payload(tag string, n int) []byte {
	p := bytes.Repeat([]byte(tag), n/len(tag)+1)
	return p[:n]
}

// TestOutOfOrderCompletion verifies that completing reservations out of
// order never exposes a gap: the visible prefix advances only over
// contiguous completed segments.
func TestOutOfOrderCompletion(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	a, _ := l.Reserve(10)
	b, _ := l.Reserve(20)
	c, _ := l.Reserve(30)

	if a.Offset() != 0 {
		t.Fatalf("a offset = %d, want 0", a.Offset())
	}
	if b.Offset() != a.Size() || c.Offset() != a.Size()+b.Size() {
		t.Fatalf("offsets not contiguous: a=%d b=%d c=%d", a.Offset(), b.Offset(), c.Offset())
	}

	a.Write(payload("a", 10))
	b.Write(payload("b", 20))
	c.Write(payload("c", 30))

	// Complete in reverse order.
	if err := c.Commit(); err != nil {
		t.Fatal(err)
	}
	if v := l.VisibleOffset(); v != 0 {
		t.Fatalf("visible = %d after committing c, want 0", v)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if v := l.VisibleOffset(); v != 0 {
		t.Fatalf("visible = %d after committing b, want 0", v)
	}
	if err := a.Commit(); err != nil {
		t.Fatal(err)
	}
	want := a.Size() + b.Size() + c.Size()
	if v := l.VisibleOffset(); v != want {
		t.Fatalf("visible = %d, want %d", v, want)
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}

	// Records decode in offset order with intact payloads.
	it := l.NewIterator()
	expect := []struct {
		tag string
		n   int
	}{{"a", 10}, {"b", 20}, {"c", 30}}
	for _, e := range expect {
		rec, err := it.Next()
		if err != nil {
			t.Fatal(err)
		}
		if rec.IsHole() {
			t.Fatalf("unexpected hole at %d", rec.Offset)
		}
		if !bytes.Equal(rec.Payload, payload(e.tag, e.n)) {
			t.Fatalf("payload mismatch at %d", rec.Offset)
		}
	}
	if _, err := it.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

// TestFailureLeavesHole verifies that a failed reservation becomes an
// identifiable hole record and the prefix advances past it.
func TestFailureLeavesHole(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	a, _ := l.Reserve(8)
	bad, _ := l.Reserve(16)
	c, _ := l.Reserve(8)

	a.Write(payload("a", 8))
	c.Write(payload("c", 8))

	if err := bad.Fail(); err != nil {
		t.Fatal(err)
	}
	if v := l.VisibleOffset(); v != 0 {
		t.Fatalf("visible = %d, want 0 (a not committed)", v)
	}
	a.Commit()
	c.Commit()

	if v, want := l.VisibleOffset(), l.AllocatedOffset(); v != want {
		t.Fatalf("visible = %d, want %d", v, want)
	}

	it := l.NewIterator()
	r1, _ := it.Next()
	r2, _ := it.Next()
	r3, _ := it.Next()
	if r1.IsHole() || r3.IsHole() {
		t.Fatal("data records misclassified as holes")
	}
	if !r2.IsHole() {
		t.Fatal("failed reservation not visible as hole")
	}
	if r2.Offset != bad.Offset() {
		t.Fatalf("hole offset = %d, want %d", r2.Offset, bad.Offset())
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestCancelNeverReassigns verifies cancel semantics: unwritten reservations
// become holes, written ones refuse to cancel, and no offset is ever reused.
func TestCancelNeverReassigns(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	r1, _ := l.Reserve(10)
	if err := r1.Cancel(); err != nil {
		t.Fatalf("cancel before write: %v", err)
	}

	r2, _ := l.Reserve(10)
	if r2.Offset() != r1.Offset()+r1.Size() {
		t.Fatalf("offset reused or gap: r1=%d r2=%d", r1.Offset(), r2.Offset())
	}
	r2.Write(payload("x", 10))
	if err := r2.Cancel(); err != ErrCannotCancel {
		t.Fatalf("cancel after write = %v, want ErrCannotCancel", err)
	}
	// The written range must still be completed explicitly.
	if err := r2.Commit(); err != nil {
		t.Fatal(err)
	}

	r3, _ := l.Reserve(10)
	if r3.Offset() != r2.Offset()+r2.Size() {
		t.Fatalf("offset reused after commit: r2=%d r3=%d", r2.Offset(), r3.Offset())
	}
	r3.Write(payload("y", 10))
	r3.Commit()

	if v, want := l.VisibleOffset(), l.AllocatedOffset(); v != want {
		t.Fatalf("visible = %d, want %d", v, want)
	}
	it := l.NewIterator()
	rec, _ := it.Next()
	if !rec.IsHole() {
		t.Fatal("cancelled reservation not visible as hole")
	}
	rec, _ = it.Next()
	if rec.IsHole() || !bytes.Equal(rec.Payload, payload("x", 10)) {
		t.Fatal("written record lost after failed cancel")
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestBarrierInterleaved drives many producers whose reserve/write/ack
// phases are interleaved through barriers, then checks offset disjointness
// and prefix contiguity.
func TestBarrierInterleaved(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	const producers = 8
	const rounds = 25

	type outcome struct {
		offset int64
		size   int64
		kind   RecordType
		data   []byte
	}
	results := make(chan outcome, producers*rounds)

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + 1))
			for round := 0; round < rounds; round++ {
				n := 1 + rng.Intn(64)
				body := []byte(fmt.Sprintf("p%02d-r%02d-", id, round))
				data := bytes.Repeat(body, n/len(body)+1)[:n]

				res, err := l.Reserve(n)
				if err != nil {
					t.Error(err)
					return
				}
				// Interleave: sleep so other producers reserve/write in
				// between this producer's phases.
				time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond)
				switch rng.Intn(10) {
				case 0: // fail: hole record
					if err := res.Fail(); err != nil {
						t.Error(err)
						return
					}
					results <- outcome{res.Offset(), res.Size(), HoleRecord, nil}
				case 1: // cancel before write: hole record
					if err := res.Cancel(); err != nil {
						t.Error(err)
						return
					}
					results <- outcome{res.Offset(), res.Size(), HoleRecord, nil}
				default:
					if err := res.Write(data); err != nil {
						t.Error(err)
						return
					}
					time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond)
					if err := res.Commit(); err != nil {
						t.Error(err)
						return
					}
					results <- outcome{res.Offset(), res.Size(), DataRecord, data}
				}
				// Periodically wait on a barrier so in-flight work drains
				// and the visible prefix catches up mid-run.
				if round%5 == 4 {
					if _, err := l.Barrier().Wait(); err != nil {
						t.Error(err)
						return
					}
					if err := l.Validate(); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(p)
	}
	wg.Wait()
	close(results)

	final, err := l.Barrier().Wait()
	if err != nil {
		t.Fatal(err)
	}
	if final != l.AllocatedOffset() {
		t.Fatalf("visible = %d after final barrier, want %d", final, l.AllocatedOffset())
	}

	// 1. Reserved ranges are pairwise non-overlapping and tile [0, final).
	var ranges []outcome
	byOffset := make(map[int64]outcome)
	for o := range results {
		ranges = append(ranges, o)
		if _, dup := byOffset[o.offset]; dup {
			t.Fatalf("offset %d assigned twice", o.offset)
		}
		byOffset[o.offset] = o
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].offset < ranges[j].offset })
	var cursor int64
	for _, r := range ranges {
		if r.offset != cursor {
			t.Fatalf("ranges not contiguous: gap/overlap at %d, cursor %d", r.offset, cursor)
		}
		cursor += r.size
	}
	if cursor != final {
		t.Fatalf("ranges cover %d bytes, visible prefix is %d", cursor, final)
	}

	// 2. The visible prefix decodes as a contiguous record sequence whose
	//    contents match what each producer wrote.
	it := l.NewIterator()
	for _, want := range ranges {
		rec, err := it.Next()
		if err != nil {
			t.Fatal(err)
		}
		if rec.Offset != want.offset {
			t.Fatalf("record at %d, want %d", rec.Offset, want.offset)
		}
		if rec.Type != want.kind {
			t.Fatalf("record at %d type = %d, want %d", rec.Offset, rec.Type, want.kind)
		}
		if want.kind == DataRecord && !bytes.Equal(rec.Payload, want.data) {
			t.Fatalf("payload mismatch at %d", rec.Offset)
		}
	}
	if _, err := it.Next(); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
	if err := l.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestReaderBoundaries verifies readers can never see beyond the visible
// prefix, including while producers hold pending reservations.
func TestReaderBoundaries(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	a, _ := l.Reserve(4)
	a.Write(payload("a", 4))
	pending, _ := l.Reserve(4) // reserved but never completed

	buf := make([]byte, 4)
	if _, err := l.ReadAt(buf, 0); err != ErrOutOfRange {
		t.Fatalf("read before any commit = %v, want ErrOutOfRange", err)
	}
	a.Commit()
	if _, err := l.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadAt(buf, a.Size()); err != ErrOutOfRange {
		t.Fatalf("read into pending region = %v, want ErrOutOfRange", err)
	}
	if v := l.VisibleOffset(); v != a.Size() {
		t.Fatalf("visible = %d, want %d", v, a.Size())
	}
	_ = pending
}

// TestReservationMisuse covers the reservation state machine errors.
func TestReservationMisuse(t *testing.T) {
	l, dir := openTemp(t)
	defer os.RemoveAll(dir)
	defer l.Close()

	r, _ := l.Reserve(8)
	if err := r.Commit(); err != ErrNotWritten {
		t.Fatalf("commit without write = %v, want ErrNotWritten", err)
	}
	if err := r.Write([]byte("short")); err != ErrBadSize {
		t.Fatalf("short write = %v, want ErrBadSize", err)
	}
	r.Write(payload("z", 8))
	r.Commit()
	if err := r.Commit(); err != ErrAlreadyDone {
		t.Fatalf("double commit = %v, want ErrAlreadyDone", err)
	}
	if err := r.Fail(); err != ErrAlreadyDone {
		t.Fatalf("fail after commit = %v, want ErrAlreadyDone", err)
	}

	if _, err := l.Reserve(-1); err != ErrBadSize {
		t.Fatalf("negative reserve = %v, want ErrBadSize", err)
	}
	l.Close()
	if _, err := l.Reserve(1); err != ErrClosed {
		t.Fatalf("reserve after close = %v, want ErrClosed", err)
	}
}
