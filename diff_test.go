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

// --- test doubles ---------------------------------------------------------

var errBoom = errors.New("source disconnected")

type memValue struct {
	data    []byte
	off     int
	chunk   int
	strong  []byte
	weak    uint64
	hasWeak bool
	readErr error
}

func (v *memValue) Read(p []byte) (int, error) {
	if v.readErr != nil {
		return 0, v.readErr
	}
	if v.off >= len(v.data) {
		return 0, io.EOF
	}
	n := len(v.data) - v.off
	if v.chunk > 0 && n > v.chunk {
		n = v.chunk
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, v.data[v.off:v.off+n])
	v.off += n
	return n, nil
}

func (v *memValue) StrongHash() ([]byte, bool) {
	if v.strong == nil {
		return nil, false
	}
	return v.strong, true
}

func (v *memValue) WeakHash() (uint64, bool) { return v.weak, v.hasWeak }

type memSource struct {
	version   uint64
	recs      []Record
	i         int
	failAfter int // fail the (failAfter+1)-th Next call; -1 disables
	calls     int
	err       error
}

func newMemSource(version uint64, recs []Record) *memSource {
	return &memSource{version: version, recs: recs, failAfter: -1, err: errBoom}
}

func (s *memSource) Version() uint64 { return s.version }

func (s *memSource) Next(ctx context.Context) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if s.failAfter >= 0 && s.calls >= s.failAfter {
		return Record{}, s.err
	}
	s.calls++
	if s.i >= len(s.recs) {
		return Record{}, io.EOF
	}
	r := s.recs[s.i]
	s.i++
	// Hand out a fresh value view per read, mirroring a real source whose
	// values are re-readable across separate opens of the snapshot.
	if mv, ok := r.Value.(*memValue); ok {
		cp := *mv
		cp.off = 0
		r.Value = &cp
	}
	return r, nil
}

func (s *memSource) Seek(afterKey string) error {
	s.i = sort.Search(len(s.recs), func(i int) bool { return s.recs[i].Key > afterKey })
	return nil
}

// --- helpers --------------------------------------------------------------

func rec(key, val string, chunk int) Record {
	return Record{Key: key, Value: &memValue{data: []byte(val), chunk: chunk}}
}

func keySeq(prefix string, n int, val string, chunk int) []Record {
	out := make([]Record, n)
	for i := range out {
		out[i] = rec(fmt.Sprintf("%s%06d", prefix, i), val, chunk)
	}
	return out
}

func format(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type.String() + ":" + e.Key
	}
	return out
}

func collect(evs <-chan Event, errs <-chan error) ([]Event, error) {
	var out []Event
	for e := range evs {
		out = append(out, e)
	}
	return out, <-errs
}

func mustEqual(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events mismatch\n got: %v\nwant: %v", got, want)
	}
}

// --- tests ----------------------------------------------------------------

func TestLongCommonPrefix(t *testing.T) {
	const common = 50000
	oldRecs := keySeq("k", common, "shared", 4096)
	newRecs := keySeq("k", common, "shared", 1024) // different chunking, same bytes

	oldRecs = append(oldRecs, rec("t0", "old-only", 8), rec("t2", "before", 8))
	newRecs = append(newRecs, rec("t1", "new-only", 8), rec("t2", "after", 8))

	evs, errs := Diff(context.Background(),
		newMemSource(1, oldRecs), newMemSource(2, newRecs), Options{})
	got, err := collect(evs, errs)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	mustEqual(t, format(got), []string{"deleted:t0", "added:t1", "changed:t2"})
}

func TestDifferenceOnlyAtEnd(t *testing.T) {
	const n = 20000
	oldRecs := keySeq("r", n, "same", 8192)
	newRecs := keySeq("r", n, "same", 8192)
	last := fmt.Sprintf("r%06d", n-1)
	newRecs[n-1] = rec(last, "same-but-not-quite", 8192)

	evs, errs := Diff(context.Background(),
		newMemSource(7, oldRecs), newMemSource(7, newRecs), Options{})
	got, err := collect(evs, errs)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	mustEqual(t, format(got), []string{"changed:" + last})
}

func TestDisconnectAndResumeNoDuplicates(t *testing.T) {
	oldRecs := keySeq("a", 1000, "x", 64)
	newRecs := keySeq("a", 1000, "x", 64)
	// Mutations: changes, deletions, additions.
	newRecs[300] = rec("a000300", "changed-1", 64)
	newRecs[700] = rec("a000700", "changed-2", 64)
	newRecs = append(newRecs[:400], newRecs[410:]...) // delete a000400..a000409
	newRecs = append(newRecs, rec("n000001", "new", 64), rec("n000002", "new", 64))
	sort.Slice(newRecs, func(i, j int) bool { return newRecs[i].Key < newRecs[j].Key })

	// Baseline: uninterrupted run.
	baseEvs, baseErrs := Diff(context.Background(),
		newMemSource(11, oldRecs), newMemSource(12, newRecs), Options{})
	base, err := collect(baseEvs, baseErrs)
	if err != nil {
		t.Fatalf("baseline diff failed: %v", err)
	}
	if len(base) == 0 {
		t.Fatal("baseline produced no events")
	}

	// Flaky run: the new source disconnects mid-stream.
	flaky := newMemSource(12, newRecs)
	flaky.failAfter = 137
	evs1, errs1 := Diff(context.Background(), newMemSource(11, oldRecs), flaky, Options{})
	first, err := collect(evs1, errs1)
	failure, ok := err.(*Failure)
	if !ok {
		t.Fatalf("expected *Failure, got %v (%T)", err, err)
	}
	cur := failure.Cursor
	if cur.OldVersion != 11 || cur.NewVersion != 12 {
		t.Fatalf("cursor not bound to source versions: %+v", cur)
	}

	// Resume from the cursor against fresh sources.
	oldResume, newResume, err := Resume(cur, newMemSource(11, oldRecs), newMemSource(12, newRecs))
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	evs2, errs2 := Diff(context.Background(), oldResume, newResume, Options{})
	second, err := collect(evs2, errs2)
	if err != nil {
		t.Fatalf("resumed diff failed: %v", err)
	}

	combined := append(append([]Event{}, first...), second...)
	mustEqual(t, format(combined), format(base))

	seen := make(map[string]bool)
	for _, e := range combined {
		if seen[e.Key] {
			t.Fatalf("duplicate event for key %q after resume", e.Key)
		}
		seen[e.Key] = true
	}
}

func TestResumeRejectsVersionMismatch(t *testing.T) {
	cur := Cursor{AfterKey: "a000010", OldVersion: 11, NewVersion: 12}
	_, _, err := Resume(cur, newMemSource(11, nil), newMemSource(99, nil))
	if err == nil {
		t.Fatal("expected version mismatch error")
	}
}

func TestWeakHashIsNotFinal(t *testing.T) {
	// Equal weak digests, different bytes: must still report Changed.
	mk := func(data string, weak uint64) *memValue {
		return &memValue{data: []byte(data), chunk: 16, weak: weak, hasWeak: true}
	}
	oldRecs := []Record{{Key: "k1", Value: mk("alpha", 42)}, {Key: "k2", Value: mk("same", 7)}}
	newRecs := []Record{{Key: "k1", Value: mk("omega", 42)}, {Key: "k2", Value: mk("same", 7)}}

	evs, errs := Diff(context.Background(),
		newMemSource(1, oldRecs), newMemSource(1, newRecs), Options{})
	got, err := collect(evs, errs)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	mustEqual(t, format(got), []string{"changed:k1"})
}

func TestStrongHashTrustModel(t *testing.T) {
	sum := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	// readErr proves the byte stream is never touched under the trust model.
	mk := func(data string) *memValue {
		return &memValue{data: []byte(data), strong: sum(data), readErr: errors.New("must not read bytes")}
	}

	t.Run("equal digests skip byte comparison", func(t *testing.T) {
		oldRecs := []Record{{Key: "k", Value: mk("payload")}}
		newRecs := []Record{{Key: "k", Value: mk("payload")}}
		evs, errs := Diff(context.Background(),
			newMemSource(1, oldRecs), newMemSource(1, newRecs),
			Options{TrustStrongHash: true})
		got, err := collect(evs, errs)
		if err != nil {
			t.Fatalf("diff failed: %v", err)
		}
		mustEqual(t, format(got), nil)
	})

	t.Run("differing digests conclude changed", func(t *testing.T) {
		oldRecs := []Record{{Key: "k", Value: mk("payload-v1")}}
		newRecs := []Record{{Key: "k", Value: mk("payload-v2")}}
		evs, errs := Diff(context.Background(),
			newMemSource(1, oldRecs), newMemSource(1, newRecs),
			Options{TrustStrongHash: true})
		got, err := collect(evs, errs)
		if err != nil {
			t.Fatalf("diff failed: %v", err)
		}
		mustEqual(t, format(got), []string{"changed:k"})
	})

	t.Run("without trust bytes are compared", func(t *testing.T) {
		oldRecs := []Record{{Key: "k", Value: mk("payload")}}
		newRecs := []Record{{Key: "k", Value: mk("payload")}}
		evs, errs := Diff(context.Background(),
			newMemSource(1, oldRecs), newMemSource(1, newRecs),
			Options{TrustStrongHash: false})
		if _, err := collect(evs, errs); err == nil {
			t.Fatal("expected byte comparison to run and fail")
		}
	})
}

func TestBackpressureAndCancellation(t *testing.T) {
	// Unbuffered event channel: the differ cannot run ahead of the consumer.
	oldRecs := keySeq("d", 1000, "gone", 16)
	ctx, cancel := context.WithCancel(context.Background())
	evs, errs := Diff(ctx, newMemSource(1, oldRecs), newMemSource(1, nil), Options{EmitBuffer: 0})

	first := <-evs
	if first.Type != Deleted {
		t.Fatalf("expected deleted event, got %v", first.Type)
	}
	cancel()
	for range evs { // drain until the differ observes cancellation
	}
	if err := <-errs; err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestEmptyAndOneSidedStreams(t *testing.T) {
	evs, errs := Diff(context.Background(), newMemSource(1, nil), newMemSource(1, nil), Options{})
	got, err := collect(evs, errs)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty streams: got %v events, err %v", len(got), err)
	}

	evs, errs = Diff(context.Background(),
		newMemSource(1, nil), newMemSource(1, keySeq("z", 3, "v", 4)), Options{})
	got, err = collect(evs, errs)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	mustEqual(t, format(got), []string{"added:z000000", "added:z000001", "added:z000002"})
}

func TestCursorCarriedByEvents(t *testing.T) {
	oldRecs := []Record{rec("k1", "a", 4), rec("k2", "b", 4)}
	newRecs := []Record{rec("k2", "b", 4), rec("k3", "c", 4)}
	evs, errs := Diff(context.Background(),
		newMemSource(5, oldRecs), newMemSource(6, newRecs), Options{})
	got, err := collect(evs, errs)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}
	mustEqual(t, format(got), []string{"deleted:k1", "added:k3"})
	if got[0].Cursor.AfterKey != "k1" || got[1].Cursor.AfterKey != "k3" {
		t.Fatalf("events carry wrong cursors: %+v %+v", got[0].Cursor, got[1].Cursor)
	}
	for _, e := range got {
		if e.Cursor.OldVersion != 5 || e.Cursor.NewVersion != 6 {
			t.Fatalf("cursor not bound to versions: %+v", e.Cursor)
		}
	}
}
