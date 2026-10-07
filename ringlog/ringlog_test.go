package ringlog

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// rec encodes a record as its absolute sequence number for easy checking.
func rec(abs uint64) []byte { return []byte(fmt.Sprintf("rec-%d", abs)) }

func recsFrom(lo, hi uint64) [][]byte {
	out := make([][]byte, 0, hi-lo)
	for a := lo; a < hi; a++ {
		out = append(out, rec(a))
	}
	return out
}

func mustAbs(t *testing.T, l *Log, c Cursor) uint64 {
	t.Helper()
	if c.Seq >= l.Capacity() {
		t.Fatalf("cursor %v slot out of range", c)
	}
	return c.Gen*l.Capacity() + c.Seq
}

func checkData(t *testing.T, base uint64, got [][]byte) {
	t.Helper()
	for i, d := range got {
		want := rec(base + uint64(i))
		if string(d) != string(want) {
			t.Fatalf("record %d: got %q want %q", base+uint64(i), d, want)
		}
	}
}

// TestWraparound fills the ring several times over and verifies that only
// the last Capacity records are readable and stale cursors get the exact
// lost range.
func TestWraparound(t *testing.T) {
	const cap = 8
	l := New(cap)
	const total = 29 // wraps 3 full generations plus 5
	for a := uint64(0); a < total; a++ {
		l.Append([][]byte{rec(a)})
	}
	head := uint64(total - cap)

	// Stale cursor: exact missing range [0, head).
	_, cur, err := l.Read(context.Background(), Cursor{}, 100)
	le, ok := IsLagged(err)
	if !ok {
		t.Fatalf("expected LaggedError, got %v", err)
	}
	if got := mustAbs(t, l, le.From); got != 0 {
		t.Fatalf("missing range starts at %d, want 0", got)
	}
	if got := mustAbs(t, l, le.To); got != head {
		t.Fatalf("missing range ends at %d, want %d", got, head)
	}
	if cur != (Cursor{}) {
		t.Fatalf("lagged read consumed cursor: %v", cur)
	}

	// Resume at the oldest retained record and drain the ring.
	got, next, err := l.Read(context.Background(), le.To, 100)
	if err != nil {
		t.Fatal(err)
	}
	checkData(t, head, got)
	if abs := mustAbs(t, l, next); abs != total {
		t.Fatalf("next cursor = %d, want %d", abs, total)
	}

	// Reading at the tail yields nothing and a stable cursor.
	got, next, err = l.Read(context.Background(), next, 100)
	if err != nil || len(got) != 0 {
		t.Fatalf("read at tail: got %d records, err %v", len(got), err)
	}
	if abs := mustAbs(t, l, next); abs != total {
		t.Fatalf("cursor moved at tail: %d", abs)
	}
}

// TestBatchCrossingTail appends a batch that wraps around the end of the
// ring and verifies the retained records come back in append order.
func TestBatchCrossingTail(t *testing.T) {
	const cap = 8
	l := New(cap)
	l.Append(recsFrom(0, 6))  // slots 0..5 used, tail at slot 6
	l.Append(recsFrom(6, 13)) // batch of 7 crosses the ring end
	head := uint64(13 - cap)  // = 5

	got, next, err := l.Read(context.Background(), l.Oldest(), 100)
	if err != nil {
		t.Fatal(err)
	}
	checkData(t, head, got)
	if abs := mustAbs(t, l, next); abs != 13 {
		t.Fatalf("next cursor = %d, want 13", abs)
	}

	// A cursor into the overwritten prefix of the batch must report loss,
	// not return the record that now occupies its slot.
	_, _, err = l.Read(context.Background(), Cursor{Gen: 0, Seq: 3}, 1)
	le, ok := IsLagged(err)
	if !ok {
		t.Fatalf("expected LaggedError, got %v", err)
	}
	if got := mustAbs(t, l, le.To); got != head {
		t.Fatalf("missing range ends at %d, want %d", got, head)
	}
}

// TestBatchAtomicPublish hammers the log with one multi-record batch while
// concurrent readers observe it: every read must see either none of the
// batch or a prefix of the published tail, never a half-published batch.
func TestBatchAtomicPublish(t *testing.T) {
	const cap = 64
	l := New(cap)
	batch := recsFrom(0, 32)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, _, err := l.Read(context.Background(), Cursor{}, cap)
				if err != nil {
					if _, ok := IsLagged(err); ok {
						continue
					}
					t.Errorf("unexpected error: %v", err)
					return
				}
				// Records must be a contiguous run of the global sequence.
				for i, d := range got {
					if string(d) != string(rec(uint64(i))) {
						t.Errorf("torn batch: record %d is %q", i, d)
						return
					}
				}
			}
		}()
	}
	l.Append(batch)
	close(stop)
	wg.Wait()

	got, _, err := l.Read(context.Background(), Cursor{}, cap)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(batch) {
		t.Fatalf("after publish: got %d records, want %d", len(got), len(batch))
	}
	checkData(t, 0, got)
}

// TestCancelDoesNotConsume verifies a canceled read returns the input
// cursor unchanged and the records remain readable afterwards.
func TestCancelDoesNotConsume(t *testing.T) {
	l := New(4)
	l.Append(recsFrom(0, 4))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cur := Cursor{Gen: 0, Seq: 1}
	_, next, err := l.Read(ctx, cur, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if next != cur {
		t.Fatalf("canceled read consumed cursor: %v -> %v", cur, next)
	}

	got, next, err := l.Read(context.Background(), cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	checkData(t, 1, got)
	if abs := mustAbs(t, l, next); abs != 4 {
		t.Fatalf("next cursor = %d, want 4", abs)
	}
}

// TestOverwrittenSlotNotServedAsOldRecord overwrites every slot and checks
// that a stale cursor gets a LaggedError rather than the record that now
// occupies its old slot.
func TestOverwrittenSlotNotServedAsOldRecord(t *testing.T) {
	const cap = 4
	l := New(cap)
	l.Append(recsFrom(0, cap))
	l.Append(recsFrom(cap, 2*cap)) // slots now hold seqs 4..7

	_, cur, err := l.Read(context.Background(), Cursor{Gen: 0, Seq: 0}, 1)
	le, ok := IsLagged(err)
	if !ok {
		t.Fatalf("expected LaggedError, got %v", err)
	}
	if le.From != (Cursor{Gen: 0, Seq: 0}) || le.To != (Cursor{Gen: 1, Seq: 0}) {
		t.Fatalf("missing range = [%v, %v), want [0:0, 1:0)", le.From, le.To)
	}
	if cur != (Cursor{Gen: 0, Seq: 0}) {
		t.Fatalf("cursor consumed: %v", cur)
	}
}

// TestFutureAndInvalidCursors covers cursors past the tail and out of
// range slot indices.
func TestFutureAndInvalidCursors(t *testing.T) {
	l := New(4)
	l.Append(recsFrom(0, 2))

	if _, _, err := l.Read(context.Background(), Cursor{Gen: 1, Seq: 0}, 1); !errors.Is(err, ErrFutureCursor) {
		t.Fatalf("future cursor: got %v", err)
	}
	if _, _, err := l.Read(context.Background(), Cursor{Gen: 0, Seq: 4}, 1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid cursor: got %v", err)
	}
}

// model is the unbounded-history reference: it keeps every record ever
// appended and derives expected read results by truncating to capacity.
type model struct {
	history [][]byte
	cap     uint64
}

func (m *model) append(batch [][]byte) { m.history = append(m.history, batch...) }

func (m *model) head() uint64 {
	if uint64(len(m.history)) > m.cap {
		return uint64(len(m.history)) - m.cap
	}
	return 0
}

// read mirrors Log.Read semantics against the full history.
func (m *model) read(abs uint64, max int) (got [][]byte, next uint64, lagFrom, lagTo uint64, lagged, future bool) {
	tail := uint64(len(m.history))
	head := m.head()
	switch {
	case abs < head:
		return nil, abs, abs, head, true, false
	case abs > tail:
		return nil, abs, 0, 0, false, true
	}
	limit := abs + uint64(max)
	if limit > tail {
		limit = tail
	}
	return m.history[abs:limit], limit, 0, 0, false, false
}

// TestAgainstUnboundedModel runs randomized appends and reads, checking
// every result against the truncated unbounded history.
func TestAgainstUnboundedModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const capacity = 16
	l := New(capacity)
	m := &model{cap: capacity}
	var nextVal uint64

	appendRandom := func() {
		n := 1 + rng.Intn(3*capacity) // batches often exceed capacity
		batch := recsFrom(nextVal, nextVal+uint64(n))
		nextVal += uint64(n)
		l.Append(batch)
		m.append(batch)
	}

	readAndCheck := func(abs uint64) {
		max := 1 + rng.Intn(2*capacity)
		cur := Cursor{Gen: abs / capacity, Seq: abs % capacity}
		got, nextCur, err := l.Read(context.Background(), cur, max)
		want, wantNext, lagFrom, lagTo, lagged, future := m.read(abs, max)

		switch {
		case lagged:
			le, ok := IsLagged(err)
			if !ok {
				t.Fatalf("abs=%d: expected LaggedError, got %v", abs, err)
			}
			if got := le.From.Gen*capacity + le.From.Seq; got != lagFrom {
				t.Fatalf("abs=%d: lost range from %d, want %d", abs, got, lagFrom)
			}
			if got := le.To.Gen*capacity + le.To.Seq; got != lagTo {
				t.Fatalf("abs=%d: lost range to %d, want %d", abs, got, lagTo)
			}
			if nextCur != cur {
				t.Fatalf("abs=%d: lagged read consumed cursor", abs)
			}
		case future:
			if !errors.Is(err, ErrFutureCursor) {
				t.Fatalf("abs=%d: expected ErrFutureCursor, got %v", abs, err)
			}
		default:
			if err != nil {
				t.Fatalf("abs=%d: unexpected error %v", abs, err)
			}
			if len(got) != len(want) {
				t.Fatalf("abs=%d: got %d records, want %d", abs, len(got), len(want))
			}
			for i := range want {
				if string(got[i]) != string(want[i]) {
					t.Fatalf("abs=%d: record %d = %q, want %q", abs, i, got[i], want[i])
				}
			}
			if got := nextCur.Gen*capacity + nextCur.Seq; got != wantNext {
				t.Fatalf("abs=%d: next cursor %d, want %d", abs, got, wantNext)
			}
		}
	}

	for step := 0; step < 3000; step++ {
		if rng.Intn(2) == 0 || nextVal == 0 {
			appendRandom()
			continue
		}
		// Mix of retained, stale, and future cursors.
		var abs uint64
		switch rng.Intn(3) {
		case 0: // somewhere in retained history
			abs = m.head() + uint64(rng.Intn(int(nextVal-m.head())+1))
		case 1: // stale
			abs = uint64(rng.Intn(int(m.head()) + 1))
		default: // possibly future
			abs = nextVal + uint64(rng.Intn(4))
		}
		readAndCheck(abs)
	}
}

// TestConcurrentReadersAgainstModel appends batches while readers follow
// from random cursors; every returned run must be contiguous in the global
// sequence and consistent with the truncated unbounded history.
func TestConcurrentReadersAgainstModel(t *testing.T) {
	const capacity = 32
	l := New(capacity)
	var mu sync.Mutex
	var history [][]byte // unbounded model, appended under mu

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				mu.Lock()
				total := uint64(len(history))
				mu.Unlock()
				abs := uint64(0)
				if total > 0 {
					abs = uint64(rng.Intn(int(total) + 1))
				}
				cur := Cursor{Gen: abs / capacity, Seq: abs % capacity}
				got, next, err := l.Read(context.Background(), cur, 1+rng.Intn(capacity))
				if err != nil {
					if le, ok := IsLagged(err); ok {
						// Lost range must be exact: From is our cursor,
						// To is a position still retained (or the tail).
						if le.From != cur {
							t.Errorf("lost range from %v, want %v", le.From, cur)
							return
						}
						if next != cur {
							t.Errorf("lagged read consumed cursor")
							return
						}
						continue
					}
					t.Errorf("unexpected error: %v", err)
					return
				}
				// Contiguous run of the global sequence starting at abs.
				for i, d := range got {
					if string(d) != string(rec(abs+uint64(i))) {
						t.Errorf("abs=%d: record %d = %q, want %q", abs, i, d, rec(abs+uint64(i)))
						return
					}
				}
				if want := abs + uint64(len(got)); next.Gen*capacity+next.Seq != want {
					t.Errorf("next cursor = %v, want abs %d", next, want)
					return
				}
			}
		}(int64(r))
	}

	for i := 0; i < 200; i++ {
		n := 1 + i%7
		base := uint64(0)
		mu.Lock()
		base = uint64(len(history))
		batch := recsFrom(base, base+uint64(n))
		history = append(history, batch...)
		mu.Unlock()
		l.Append(batch)
	}
	close(stop)
	wg.Wait()
}
