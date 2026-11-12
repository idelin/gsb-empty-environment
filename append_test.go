package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

var errInjected = errors.New("injected write fault")

// memStore is a fault-injecting Store. It deliberately allows WriteAt to
// short-write or fail after a configured number of bytes, so the append
// handle cannot assume an all-or-nothing underlying write.
type memStore struct {
	mu sync.Mutex
	// data is treated as an append-only volume. WriteAt may extend it but
	// never truncates it.
	data []byte

	shortMax int
	failAt   int64
	failed   bool
	failErr  error

	syncErr    error
	syncCount  int
	writeCalls int
}

func newMemStore() *memStore {
	return &memStore{failAt: -1}
}

func (m *memStore) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off < 0 {
		return 0, errors.New("negative write offset")
	}
	m.writeCalls++
	n := len(p)
	if m.shortMax > 0 && n > m.shortMax {
		n = m.shortMax
	}
	var injected error
	if m.failAt >= 0 && !m.failed && off+int64(n) > m.failAt {
		allowed := m.failAt - off
		if allowed < 0 {
			allowed = 0
		}
		if allowed < int64(n) {
			n = int(allowed)
			injected = m.failErr
			if injected == nil {
				injected = errInjected
			}
		}
		m.failed = true
	}
	if n > 0 {
		need := off + int64(n)
		if need > int64(len(m.data)) {
			m.data = append(m.data, make([]byte, int(need-int64(len(m.data))))...)
		}
		copy(m.data[off:need], p[:n])
	}
	return n, injected
}

func (m *memStore) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off < 0 {
		return 0, errors.New("negative read offset")
	}
	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memStore) Size() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.data)), nil
}

func (m *memStore) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncCount++
	if m.syncErr != nil {
		err := m.syncErr
		m.syncErr = nil
		return err
	}
	return nil
}

func (m *memStore) Close() error { return nil }

func (m *memStore) bytesAt(off, n int64) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if off < 0 || n < 0 || off+n > int64(len(m.data)) {
		return nil
	}
	return append([]byte(nil), m.data[off:off+n]...)
}

func (m *memStore) size() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.data))
}

func appendBytes(t *testing.T, l *Log, segments ...[]byte) Record {
	t.Helper()
	rec, err := l.Append(context.Background(), segments...)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return rec
}

func snapshot(t *testing.T, l *Log) []Record {
	t.Helper()
	recs, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return recs
}

func TestHandleExposesNoCallerChosenPosition(t *testing.T) {
	if _, ok := any((*Log)(nil)).(io.Seeker); ok {
		t.Fatal("Log unexpectedly implements io.Seeker")
	}
	if _, ok := any((*Log)(nil)).(io.WriterAt); ok {
		t.Fatal("Log unexpectedly implements io.WriterAt")
	}
	if _, ok := any((*Reservation)(nil)).(io.Seeker); ok {
		t.Fatal("Reservation unexpectedly implements io.Seeker")
	}
	if _, ok := any((*Reservation)(nil)).(io.WriterAt); ok {
		t.Fatal("Reservation unexpectedly implements io.WriterAt")
	}
}

func TestAppendAndSnapshot(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	first := appendBytes(t, l, []byte("hello"), []byte(" world"))
	if first.Offset != 0 {
		t.Fatalf("first offset = %d, want 0", first.Offset)
	}
	if first.DataOffset != headerSize {
		t.Fatalf("first data offset = %d, want %d", first.DataOffset, headerSize)
	}
	if first.Length != 11 || first.Kind != KindData {
		t.Fatalf("first record = %+v", first)
	}
	if got, want := l.VisibleEnd(), int64(recordOverhead+11); got != want {
		t.Fatalf("visible end = %d, want %d", got, want)
	}

	second := appendBytes(t, l, []byte("!!!"))
	if second.Offset != l.VisibleEnd()-int64(recordOverhead+3) {
		t.Fatalf("second offset = %d, visible end = %d", second.Offset, l.VisibleEnd())
	}
	if second.Offset != first.Offset+int64(recordOverhead)+first.Length {
		t.Fatalf("second offset %d overlaps first record", second.Offset)
	}

	recs := snapshot(t, l)
	if len(recs) != 2 {
		t.Fatalf("snapshot has %d records, want 2", len(recs))
	}
	if string(recs[0].Data) != "hello world" || string(recs[1].Data) != "!!!" {
		t.Fatalf("snapshot data = %q, %q", recs[0].Data, recs[1].Data)
	}
	if l.AllocatedEnd() != l.VisibleEnd() {
		t.Fatalf("allocated end %d != visible end %d", l.AllocatedEnd(), l.VisibleEnd())
	}
}

func TestCancelSealsExplicitHoleAndNeverReusesIt(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	first := appendBytes(t, l, []byte("committed-before"))
	original := store.bytesAt(first.DataOffset, first.Length)

	r, err := l.Reserve(40)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if n, err := r.Write([]byte("partial")); err != nil || n != len("partial") {
		t.Fatalf("partial Write = (%d, %v)", n, err)
	}
	if err := r.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	hole := r.Record()
	if hole.Kind != KindHole || hole.Length != 40 {
		t.Fatalf("hole record = %+v", hole)
	}
	if hole.Offset != first.Offset+int64(recordOverhead)+first.Length {
		t.Fatalf("hole offset = %d", hole.Offset)
	}
	if got := store.bytesAt(hole.DataOffset, hole.Length); !bytes.Equal(got, make([]byte, 40)) {
		t.Fatalf("hole payload = %x, want zeros", got)
	}

	recs := snapshot(t, l)
	if len(recs) != 2 || recs[1].Kind != KindHole || recs[1].Length != 40 {
		t.Fatalf("snapshot = %+v", recs)
	}
	if !bytes.Equal(recs[0].Data, original) {
		t.Fatalf("cancel rewrote committed payload")
	}

	next := appendBytes(t, l, []byte("after-hole"))
	if next.Offset != r.End() {
		t.Fatalf("next offset = %d, want %d (hole end)", next.Offset, r.End())
	}
	if next.Offset < hole.Offset+hole.Length {
		t.Fatalf("next append reused hole range")
	}
	if l.VisibleEnd() != next.Offset+int64(recordOverhead)+next.Length {
		t.Fatalf("visible end did not advance past committed records")
	}
}

func TestShortWritesAreRetried(t *testing.T) {
	store := newMemStore()
	store.shortMax = 3
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("abcdefghij"), 100)
	rec := appendBytes(t, l, payload[:123], payload[123:])
	if rec.Length != int64(len(payload)) {
		t.Fatalf("record length = %d, want %d", rec.Length, len(payload))
	}
	if got := store.bytesAt(rec.DataOffset, rec.Length); !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch after short writes")
	}
	if store.writeCalls < 2 {
		t.Fatalf("expected multiple WriteAt calls, got %d", store.writeCalls)
	}
	if got := l.VisibleEnd(); got != rec.Offset+int64(recordOverhead)+rec.Length {
		t.Fatalf("visible end = %d", got)
	}
}

func TestWriteFaultTurnsWholeReservationIntoHole(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	appendBytes(t, l, []byte("safe"))
	store.mu.Lock()
	store.failAt = int64(len(store.data)) + headerSize + 2
	store.failErr = errInjected
	store.mu.Unlock()

	_, err = l.Append(context.Background(), []byte("0123456789"))
	if !errors.Is(err, errInjected) {
		t.Fatalf("Append error = %v, want injected fault", err)
	}

	recs := snapshot(t, l)
	if len(recs) != 2 {
		t.Fatalf("snapshot = %+v", recs)
	}
	if recs[1].Kind != KindHole || recs[1].Length != 10 {
		t.Fatalf("failed append = %+v", recs[1])
	}
	if got := store.bytesAt(recs[1].DataOffset, recs[1].Length); !bytes.Equal(got, make([]byte, 10)) {
		t.Fatalf("failed reservation payload = %x, want zeros", got)
	}
	if !bytes.Equal(snapshot(t, l)[0].Data, []byte("safe")) {
		t.Fatalf("previously committed record changed")
	}

	next := appendBytes(t, l, []byte("continued"))
	if next.Offset != recs[1].Offset+int64(recordOverhead)+recs[1].Length {
		t.Fatalf("next append did not start after hole")
	}
}

func TestCommitSyncFailureTurnsReservationIntoHole(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	store.mu.Lock()
	store.syncErr = errors.New("sync failed")
	store.mu.Unlock()

	if _, err := l.Append(context.Background(), []byte("not-durable")); err == nil {
		t.Fatal("Append succeeded despite sync failure")
	}
	recs := snapshot(t, l)
	if len(recs) != 1 || recs[0].Kind != KindHole || recs[0].Length != int64(len("not-durable")) {
		t.Fatalf("snapshot = %+v", recs)
	}

	next := appendBytes(t, l, []byte("durable"))
	if next.Offset != recs[0].Offset+int64(recordOverhead)+recs[0].Length {
		t.Fatalf("next append did not start after hole")
	}
}

type cancelingStore struct {
	*memStore
	at     int64
	cancel func()
}

func (s *cancelingStore) WriteAt(p []byte, off int64) (int, error) {
	n, err := s.memStore.WriteAt(p, off)
	if s.cancel != nil && off+int64(n) >= s.at {
		f := s.cancel
		s.cancel = nil
		f()
	}
	return n, err
}

func TestContextCancellationSealsReservationAsHole(t *testing.T) {
	base := newMemStore()
	l, err := OpenStore(base)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, l, []byte("before-cancel"))
	before := l.VisibleEnd()

	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelingStore{
		memStore: base,
		at:       before + headerSize + 100,
		cancel:   cancel,
	}
	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("recover canceling store: %v", err)
	}
	if l2.VisibleEnd() != before {
		t.Fatalf("recovered visible end = %d, want %d", l2.VisibleEnd(), before)
	}

	payload := bytes.Repeat([]byte("z"), 256<<10)
	if _, err := l2.Append(ctx, payload); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append error = %v, want context.Canceled", err)
	}
	recs := snapshot(t, l2)
	if len(recs) != 2 {
		t.Fatalf("snapshot = %+v", recs)
	}
	if string(recs[0].Data) != "before-cancel" {
		t.Fatalf("committed prefix changed: %q", recs[0].Data)
	}
	if recs[1].Kind != KindHole || recs[1].Length != int64(len(payload)) {
		t.Fatalf("cancelled append = %+v", recs[1])
	}
	if got := base.bytesAt(recs[1].DataOffset, recs[1].Length); !bytes.Equal(got, make([]byte, len(payload))) {
		t.Fatalf("cancelled payload was not zeroed")
	}

	// A context cancelled before Append must not even reserve, let alone
	// create a hole.
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	end := l2.VisibleEnd()
	if _, err := l2.Append(cancelled, []byte("never-written")); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled Append = %v", err)
	}
	if l2.VisibleEnd() != end || l2.AllocatedEnd() != recs[1].End() {
		t.Fatalf("pre-cancelled Append changed allocator state")
	}
}

func TestConcurrentAppendsRemainDisjointAndContiguous(t *testing.T) {
	store := newMemStore()
	store.shortMax = 7
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	const (
		workers = 24
		perFunc = 12
	)
	type expectation struct {
		rec  Record
		data []byte
	}
	results := make(chan expectation, workers*perFunc)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perFunc; i++ {
				body := []byte(fmt.Sprintf("worker-%02d-record-%02d-payload", w, i))
				rec, err := l.Append(context.Background(), []byte{byte('0' + w)}, body)
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				results <- expectation{rec: rec, data: append([]byte{byte('0' + w)}, body...)}
			}
		}(w)
	}
	wg.Wait()
	close(results)

	recs := snapshot(t, l)
	if len(recs) != workers*perFunc {
		t.Fatalf("snapshot has %d records, want %d", len(recs), workers*perFunc)
	}
	if l.VisibleEnd() != l.AllocatedEnd() {
		t.Fatalf("visible end %d != allocated end %d", l.VisibleEnd(), l.AllocatedEnd())
	}

	got := make(map[int64][]byte, len(recs))
	for _, rec := range recs {
		got[rec.Offset] = rec.Data
	}
	for exp := range results {
		data, ok := got[exp.rec.Offset]
		if !ok {
			t.Errorf("record at %d missing from committed snapshot", exp.rec.Offset)
			continue
		}
		if !bytes.Equal(data, exp.data) {
			t.Errorf("record at %d = %q, want %q", exp.rec.Offset, data, exp.data)
		}
	}

	sorted := append([]Record(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Offset < sorted[j].Offset })
	for i := 1; i < len(sorted); i++ {
		end := sorted[i-1].Offset + int64(recordOverhead) + sorted[i-1].Length
		if sorted[i].Offset != end {
			t.Fatalf("records overlap or leave a gap: %d then %d (prev end %d)", sorted[i-1].Offset, sorted[i].Offset, end)
		}
	}
}

func TestOutOfOrderCommitCannotExposePastPendingReservation(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	earlier, err := l.Reserve(10)
	if err != nil {
		t.Fatal(err)
	}
	later, err := l.Reserve(10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := later.Write([]byte("later-data")); err != nil {
		t.Fatal(err)
	}
	if err := later.Commit(); err != nil {
		t.Fatal(err)
	}

	if got := l.VisibleEnd(); got != earlier.Offset() {
		t.Fatalf("visible end = %d, want %d before earlier reservation resolves", got, earlier.Offset())
	}
	if recs := snapshot(t, l); len(recs) != 0 {
		t.Fatalf("snapshot exposed later record before earlier gap: %+v", recs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20_000_000)
	defer cancel()
	if err := l.WaitVisible(ctx, later.End()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitVisible = %v, want deadline exceeded", err)
	}

	if err := earlier.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := l.WaitVisible(context.Background(), later.End()); err != nil {
		t.Fatalf("WaitVisible after hole: %v", err)
	}
	recs := snapshot(t, l)
	if len(recs) != 2 || recs[0].Kind != KindHole || recs[1].Kind != KindData {
		t.Fatalf("snapshot after hole = %+v", recs)
	}
	if string(recs[1].Data) != "later-data" {
		t.Fatalf("later data = %q", recs[1].Data)
	}
}

func TestRecoverySealsPartialPayload(t *testing.T) {
	store := newMemStore()
	l1, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, l1, []byte("before-interruption"))

	r, err := l1.Reserve(100)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if n, err := r.Write([]byte("0123456789")); err != nil || n != 10 {
		t.Fatalf("partial Write = (%d, %v)", n, err)
	}
	// Simulate a process crash: l1 is abandoned without Commit or Cancel.
	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}

	recs := snapshot(t, l2)
	if len(recs) != 2 {
		t.Fatalf("recovered snapshot = %+v", recs)
	}
	if !bytes.Equal(recs[0].Data, []byte("before-interruption")) {
		t.Fatalf("committed prefix was damaged during recovery")
	}
	hole := recs[1]
	if hole.Kind != KindHole || hole.Length != 100 || hole.Offset != r.Offset() {
		t.Fatalf("recovered tail = %+v, want hole at %d length 100", hole, r.Offset())
	}
	if got := store.bytesAt(hole.DataOffset, hole.Length); !bytes.Equal(got, make([]byte, 100)) {
		t.Fatalf("recovered hole payload = %x, want zeros", got)
	}
	if l2.VisibleEnd() != r.End() {
		t.Fatalf("visible end = %d, want hole end %d", l2.VisibleEnd(), r.End())
	}

	next := appendBytes(t, l2, []byte("after-recovery"))
	if next.Offset != r.End() {
		t.Fatalf("append after recovery at %d, want %d", next.Offset, r.End())
	}
}

func TestRecoveryKeepsLaterCommittedRecordAfterEarlierHole(t *testing.T) {
	store := newMemStore()
	l1, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}

	// The earlier reservation never gets a trailer; the later one commits
	// first and therefore occupies a higher offset.
	earlier, err := l1.Reserve(64)
	if err != nil {
		t.Fatal(err)
	}
	later, err := l1.Reserve(9)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := later.Write([]byte("recovered")); err != nil {
		t.Fatal(err)
	}
	if err := later.Commit(); err != nil {
		t.Fatal(err)
	}
	if l1.VisibleEnd() != earlier.Offset() {
		t.Fatalf("later record became visible before earlier reservation resolved")
	}

	// Simulate a crash while the earlier reservation is still open.
	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	recs := snapshot(t, l2)
	if len(recs) != 2 {
		t.Fatalf("recovered snapshot = %+v", recs)
	}
	if recs[0].Kind != KindHole || recs[0].Offset != earlier.Offset() || recs[0].Length != 64 {
		t.Fatalf("earlier record = %+v", recs[0])
	}
	if recs[1].Kind != KindData || recs[1].Offset != later.Offset() || string(recs[1].Data) != "recovered" {
		t.Fatalf("later record = %+v", recs[1])
	}
	if l2.VisibleEnd() != later.End() {
		t.Fatalf("visible end = %d, want %d", l2.VisibleEnd(), later.End())
	}
}

func TestRecoverySealsTornHeader(t *testing.T) {
	store := newMemStore()
	l1, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, l1, []byte("committed"))
	tornAt := l1.VisibleEnd()
	if _, err := store.WriteAt([]byte("xyz"), tornAt); err != nil {
		t.Fatalf("inject torn header: %v", err)
	}

	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	recs := snapshot(t, l2)
	if len(recs) != 2 || recs[1].Kind != KindHole || recs[1].Offset != tornAt {
		t.Fatalf("recovered snapshot = %+v", recs)
	}
	if !bytes.Equal(recs[0].Data, []byte("committed")) {
		t.Fatalf("committed record changed")
	}
	if got := store.bytesAt(tornAt, 3); string(got) != "xyz" {
		t.Fatalf("recovery overwrote pre-existing torn bytes: %q", got)
	}
	if got, want := l2.VisibleEnd(), tornAt+recordOverhead; got != want {
		t.Fatalf("visible end = %d, want %d", got, want)
	}
	if l2.AllocatedEnd() != tornAt+recordOverhead {
		t.Fatalf("allocated end = %d", l2.AllocatedEnd())
	}
	if next := appendBytes(t, l2, []byte("after-torn")); next.Offset != tornAt+recordOverhead {
		t.Fatalf("append after torn header at %d, want %d", next.Offset, tornAt+recordOverhead)
	}
	if got := store.bytesAt(tornAt, 3); string(got) != "xyz" {
		t.Fatalf("append after recovery overwrote torn bytes")
	}
	if l2.VisibleEnd() < tornAt+recordOverhead+recordOverhead+int64(len("after-torn")) {
		t.Fatalf("visible end did not cover sealed tail")
	}
}

func TestRecoveryPreservesUnknownPrefixAndScanBoundary(t *testing.T) {
	legacy := bytes.Repeat([]byte("legacy-audit-data-"), 7)

	store := newMemStore()
	if _, err := store.WriteAt(legacy, 0); err != nil {
		t.Fatal(err)
	}
	l, err := OpenStore(store)
	if err != nil {
		t.Fatalf("open legacy prefix: %v", err)
	}
	recs := snapshot(t, l)
	if len(recs) != 1 || recs[0].Kind != KindHole {
		t.Fatalf("legacy prefix snapshot = %+v", recs)
	}
	if got := store.bytesAt(0, int64(len(legacy))); !bytes.Equal(got, legacy) {
		t.Fatalf("open rewrote legacy prefix")
	}

	appended := appendBytes(t, l, []byte("first-native-record"))
	if appended.Offset != int64(len(legacy)) {
		t.Fatalf("first native record offset = %d, want %d", appended.Offset, len(legacy))
	}

	// Reopen: recovery must scan across the preserved legacy prefix to find
	// the native record boundary, not swallow it into a trailing hole.
	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	recs = snapshot(t, l2)
	if len(recs) != 2 || recs[0].Kind != KindHole || recs[1].Kind != KindData {
		t.Fatalf("reopened snapshot = %+v", recs)
	}
	if !bytes.Equal(recs[1].Data, []byte("first-native-record")) {
		t.Fatalf("native record not recovered after legacy prefix")
	}
	if got := store.bytesAt(0, int64(len(legacy))); !bytes.Equal(got, legacy) {
		t.Fatalf("reopen rewrote legacy prefix")
	}

	next := appendBytes(t, l2, []byte("second-native-record"))
	if next.Offset != recs[1].End() {
		t.Fatalf("second native record offset = %d, want %d", next.Offset, recs[1].End())
	}
}

func TestRecoverySealsMissingTrailer(t *testing.T) {
	store := newMemStore()
	l1, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}
	const payloadLen = int64(len("payload-without-trailer"))
	r, err := l1.Reserve(payloadLen)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.Write([]byte("payload-without-trailer")); err != nil || n != int(payloadLen) {
		t.Fatalf("Write = (%d, %v)", n, err)
	}

	l2, err := OpenStore(store)
	if err != nil {
		t.Fatalf("recovery: %v", err)
	}
	recs := snapshot(t, l2)
	if len(recs) != 1 || recs[0].Kind != KindHole || recs[0].Length != payloadLen {
		t.Fatalf("recovered snapshot = %+v", recs)
	}
	if l2.VisibleEnd() != r.End() {
		t.Fatalf("visible end = %d, want %d", l2.VisibleEnd(), r.End())
	}
}

func TestFileOpenCloseReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first := appendBytes(t, l, []byte("persisted"))

	r, err := l.Reserve(32)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.Write([]byte("discarded")); err != nil || n != len("discarded") {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	// Close is a graceful interruption: the unfinished reservation must be
	// sealed as a hole, not silently dropped or reused.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != r.End() {
		t.Fatalf("file size = %d, want %d", fi.Size(), r.End())
	}

	l2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer l2.Close()

	recs := snapshot(t, l2)
	if len(recs) != 2 {
		t.Fatalf("reopened snapshot = %+v", recs)
	}
	if !bytes.Equal(recs[0].Data, []byte("persisted")) {
		t.Fatalf("first record = %q", recs[0].Data)
	}
	if recs[1].Kind != KindHole || recs[1].Length != 32 {
		t.Fatalf("sealed record = %+v", recs[1])
	}

	next := appendBytes(t, l2, []byte("after-reopen"))
	if next.Offset != first.Offset+int64(recordOverhead)+first.Length+int64(recordOverhead)+32 {
		t.Fatalf("append after reopen at %d", next.Offset)
	}
}

func TestReservationRejectsOverflowAndShortCommit(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.Reserve(4)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.Write([]byte("abcde")); n != 4 || !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow Write = (%d, %v)", n, err)
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit after full write: %v", err)
	}
	if _, err := r.Write([]byte("x")); !errors.Is(err, ErrResolved) {
		t.Fatalf("Write after commit = %v", err)
	}
	if recs := snapshot(t, l); len(recs) != 1 || string(recs[0].Data) != "abcd" {
		t.Fatalf("snapshot = %+v", recs)
	}

	r2, err := l.Reserve(5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := r2.Commit(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("short Commit = %v, want ErrIncomplete", err)
	}
	if err := r2.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
}

func TestConvenienceWritersAndReservationAccessors(t *testing.T) {
	store := newMemStore()
	l, err := OpenStore(store)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := l.Write([]byte("one")); err != nil || n != 3 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if n, err := l.WriteContext(context.Background(), []byte("two")); err != nil || n != 3 {
		t.Fatalf("WriteContext = (%d, %v)", n, err)
	}
	if recs := snapshot(t, l); len(recs) != 2 || string(recs[1].Data) != "two" {
		t.Fatalf("snapshot = %+v", recs)
	}
	if l.CommittedEnd() != l.VisibleEnd() {
		t.Fatalf("CommittedEnd and VisibleEnd disagree")
	}

	r, err := l.Reserve(3)
	if err != nil {
		t.Fatal(err)
	}
	if r.DataOffset() != r.Offset()+headerSize || r.Length() != 3 || r.Written() != 0 {
		t.Fatalf("reservation accessors: offset=%d data=%d length=%d written=%d",
			r.Offset(), r.DataOffset(), r.Length(), r.Written())
	}
	if n, err := r.WriteSegments([]byte("x"), []byte("yz")); err != nil || n != 3 {
		t.Fatalf("WriteSegments = (%d, %v)", n, err)
	}
	if r.Written() != 3 {
		t.Fatalf("Written = %d, want 3", r.Written())
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if recs := snapshot(t, l); len(recs) != 3 || string(recs[2].Data) != "xyz" {
		t.Fatalf("snapshot after segments = %+v", recs)
	}
}
