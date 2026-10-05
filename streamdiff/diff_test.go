package streamdiff

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
)

// memSource is an in-memory Source that can be told to fail once after a
// fixed number of Next calls, simulating a connection reset.
type memSource struct {
	version   string
	recs      []Record
	idx       int
	calls     int
	failAfter int // fail once before serving the (failAfter+1)-th record; <0 disables
	failed    bool
}

func openMem(version string, recs []Record, after string, failAfter int) *memSource {
	start := sort.Search(len(recs), func(i int) bool { return recs[i].Key > after })
	return &memSource{version: version, recs: recs, idx: start, failAfter: failAfter}
}

func (s *memSource) Version() string { return s.version }

func (s *memSource) Next(ctx context.Context) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if s.failAfter >= 0 && !s.failed && s.calls >= s.failAfter {
		s.failed = true
		return Record{}, errors.New("connection reset by peer")
	}
	if s.idx >= len(s.recs) {
		return Record{}, io.EOF
	}
	s.calls++
	rec := s.recs[s.idx]
	s.idx++
	return rec, nil
}

type collectSink struct{ changes []Change }

func (s *collectSink) Emit(ctx context.Context, c Change) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.changes = append(s.changes, c)
	return nil
}

func key(i int) string { return fmt.Sprintf("k%06d", i) }

// buildSet creates records k[0..n) where value(i) derives the content.
func buildSet(n int, value func(i int) []byte) []Record {
	recs := make([]Record, n)
	for i := range recs {
		recs[i] = Record{Key: key(i), Value: NewBytesValue(value(i), 4096)}
	}
	return recs
}

func changeMap(changes []Change) map[string]Kind {
	m := make(map[string]Kind, len(changes))
	for _, c := range changes {
		m[c.Key] = c.Kind
	}
	return m
}

func TestLongCommonPrefix(t *testing.T) {
	const n = 20000
	oldV := func(i int) []byte { return []byte(fmt.Sprintf("value-%d", i)) }
	left := buildSet(n, oldV)
	right := buildSet(n, oldV)
	// Diverge only near the tail: modify one, remove one, add one.
	right[n-3] = Record{Key: key(n - 3), Value: NewBytesValue([]byte("changed"), 4)}
	right = right[:n-1] // drop last key -> removed
	right = append(right, Record{Key: key(n), Value: NewBytesValue([]byte("brand new"), 4)})

	sink := &collectSink{}
	cur, err := Diff(context.Background(),
		openMem("v1", left, "", -1), openMem("v1", right, "", -1), Cursor{}, sink)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if cur.LastKey != key(n) {
		t.Fatalf("cursor = %q, want %q", cur.LastKey, key(n))
	}
	got := changeMap(sink.changes)
	want := map[string]Kind{key(n - 3): Modified, key(n - 1): Removed, key(n): Added}
	if len(got) != len(want) {
		t.Fatalf("got %d changes %v, want %v", len(got), got, want)
	}
	for k, kind := range want {
		if got[k] != kind {
			t.Fatalf("key %s: got %v, want %v", k, got[k], kind)
		}
	}
}

func TestDifferenceOnlyAtEnd(t *testing.T) {
	const n = 5000
	v := func(i int) []byte { return []byte(fmt.Sprintf("payload-%d", i)) }
	left := buildSet(n, v)
	right := buildSet(n, v)
	// Only the very last record differs, deep inside a large value.
	big := make([]byte, 1<<20)
	copy(big, []byte("identical-prefix"))
	right[n-1] = Record{Key: key(n - 1), Value: NewBytesValue(big, 1<<16)}

	sink := &collectSink{}
	_, err := Diff(context.Background(),
		openMem("v1", left, "", -1), openMem("v1", right, "", -1), Cursor{}, sink)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(sink.changes) != 1 || sink.changes[0].Kind != Modified || sink.changes[0].Key != key(n-1) {
		t.Fatalf("changes = %v, want single Modified at %s", sink.changes, key(n-1))
	}
}

func TestDisconnectAndResumeNoDuplicates(t *testing.T) {
	const n = 1000
	oldV := func(i int) []byte { return []byte(fmt.Sprintf("old-%d", i)) }
	left := buildSet(n, oldV)
	right := buildSet(n, func(i int) []byte {
		if i%7 == 0 { // every 7th record modified
			return []byte(fmt.Sprintf("new-%d", i))
		}
		return oldV(i)
	})
	// Remove every 11th key from the right, add fresh keys interleaved.
	filtered := right[:0]
	for i, r := range right {
		if i%11 != 0 {
			filtered = append(filtered, r)
		}
	}
	right = filtered
	for i := 0; i < n; i += 100 {
		right = append(right, Record{Key: fmt.Sprintf("k%06d-x", i), Value: NewBytesValue([]byte("added"), 2)})
	}
	sort.Slice(right, func(i, j int) bool { return right[i].Key < right[j].Key })

	// Reference run without failures.
	ref := &collectSink{}
	if _, err := Diff(context.Background(),
		openMem("v1", left, "", -1), openMem("v1", right, "", -1), Cursor{}, ref); err != nil {
		t.Fatalf("reference Diff: %v", err)
	}

	// Run with repeated mid-stream disconnects on both sides, resuming from
	// the returned cursor until completion.
	sink := &collectSink{}
	var cur Cursor
	attempts := 0
	for {
		attempts++
		if attempts > 50 {
			t.Fatal("too many resume attempts")
		}
		// Each attempt fails once, a few records in, alternating sides.
		lFail, rFail := -1, -1
		if attempts%2 == 1 {
			rFail = 5 + attempts
		} else {
			lFail = 3 + attempts
		}
		var err error
		cur, err = Diff(context.Background(),
			openMem("v1", left, cur.LastKey, lFail),
			openMem("v1", right, cur.LastKey, rFail),
			cur, sink)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "connection reset") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if len(sink.changes) != len(ref.changes) {
		t.Fatalf("resumed run emitted %d changes, reference %d", len(sink.changes), len(ref.changes))
	}
	seen := make(map[string]int)
	for i, c := range sink.changes {
		seen[c.Key]++
		if seen[c.Key] > 1 {
			t.Fatalf("duplicate change for key %s", c.Key)
		}
		if c.Kind != ref.changes[i].Kind || c.Key != ref.changes[i].Key {
			t.Fatalf("change %d = %v %s, reference %v %s", i, c.Kind, c.Key, ref.changes[i].Kind, ref.changes[i].Key)
		}
	}
}

func TestCursorVersionBinding(t *testing.T) {
	left := buildSet(3, func(i int) []byte { return []byte("x") })
	right := buildSet(3, func(i int) []byte { return []byte("x") })
	cur := Cursor{LeftVersion: "v1", RightVersion: "v1", LastKey: key(0)}
	_, err := Diff(context.Background(),
		openMem("v2", left, cur.LastKey, -1), openMem("v1", right, cur.LastKey, -1), cur, &collectSink{})
	if err == nil || !strings.Contains(err.Error(), "cursor bound") {
		t.Fatalf("expected version mismatch error, got %v", err)
	}
}

func TestCancellation(t *testing.T) {
	left := buildSet(100, func(i int) []byte { return []byte("a") })
	right := buildSet(100, func(i int) []byte { return []byte("b") }) // all modified
	ctx, cancel := context.WithCancel(context.Background())
	sink := &collectSink{}
	wrapped := SinkFunc(func(ctx context.Context, c Change) error {
		if err := sink.Emit(ctx, c); err != nil {
			return err
		}
		cancel() // cancel after first delivery
		return nil
	})
	cur, err := Diff(ctx, openMem("v1", left, "", -1), openMem("v1", right, "", -1), Cursor{}, wrapped)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(sink.changes) != 1 {
		t.Fatalf("emitted %d changes before cancel, want 1", len(sink.changes))
	}
	if cur.LastKey != key(0) {
		t.Fatalf("cursor = %q, want %q", cur.LastKey, key(0))
	}
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(ctx context.Context, c Change) error

func (f SinkFunc) Emit(ctx context.Context, c Change) error { return f(ctx, c) }

// noHashValue hides the strong digest to force the byte-wise path.
type noHashValue struct{ inner *BytesValue }

func (v noHashValue) Open() ChunkIter                   { return v.inner.Open() }
func (v noHashValue) Strong() ([sha256.Size]byte, bool) { return [sha256.Size]byte{}, false }

func TestValueComparisonPaths(t *testing.T) {
	content := make([]byte, 300000)
	for i := range content {
		content[i] = byte(i * 31)
	}
	cases := []struct {
		name string
		a, b Value
		want bool
	}{
		{"same bytes different chunking (hash path)", NewBytesValue(content, 1000), NewBytesValue(content, 65536), true},
		{"same bytes different chunking (byte path)", noHashValue{NewBytesValue(content, 1000)}, noHashValue{NewBytesValue(content, 65536)}, true},
		{"one side hashed one not", NewBytesValue(content, 1000), noHashValue{NewBytesValue(content, 65536)}, true},
		{"differ at last byte", NewBytesValue(append(append([]byte{}, content[:len(content)-1]...), 0xFF), 4096), NewBytesValue(content, 4096), content[len(content)-1] == 0xFF},
		{"different lengths common prefix", NewBytesValue(content[:100], 7), NewBytesValue(content[:101], 7), false},
		{"both empty", NewBytesValue(nil, 1), NewBytesValue(nil, 1), true},
		{"empty vs non-empty", NewBytesValue(nil, 1), NewBytesValue([]byte("z"), 1), false},
	}
	for _, tc := range cases {
		got, err := equalValues(tc.a, tc.b)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBackpressureBlockingSink(t *testing.T) {
	left := buildSet(10, func(i int) []byte { return []byte("a") })
	right := buildSet(10, func(i int) []byte { return []byte("b") })
	gate := make(chan struct{})
	sink := SinkFunc(func(ctx context.Context, c Change) error {
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Diff(ctx, openMem("v1", left, "", -1), openMem("v1", right, "", -1), Cursor{}, sink)
		done <- err
	}()
	// The diff must be parked in the first Emit: nothing completes.
	select {
	case err := <-done:
		t.Fatalf("diff finished despite blocked sink: %v", err)
	default:
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatalf("Diff: %v", err)
	}
	cancel()
}
