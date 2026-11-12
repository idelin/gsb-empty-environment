package netsec

import (
	"errors"
	"io"
	"sync"
	"testing"
)

// flakyWriter is a thread-safe io.Writer that performs short writes and
// occasional errors, mirroring a congested duplex tunnel.
type flakyWriter struct {
	mu      sync.Mutex
	total   int
	calls   int
	failOn  map[int]bool
	shortOn map[int]bool
}

func (f *flakyWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	fail := f.failOn[call]
	short := f.shortOn[call]
	f.mu.Unlock()

	switch {
	case fail && len(p) > 1:
		n := len(p) / 2
		f.mu.Lock()
		f.total += n
		f.mu.Unlock()
		return n, errors.New("flaky: injected write failure")
	case short && len(p) > 1:
		n := len(p)/2 + 1
		f.mu.Lock()
		f.total += n
		f.mu.Unlock()
		return n, nil
	default:
		f.mu.Lock()
		f.total += len(p)
		f.mu.Unlock()
		return len(p), nil
	}
}

func (f *flakyWriter) bytes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total
}

// recordingWriter fails the test if written to after a budget rejection.
type recordingWriter struct {
	mu    sync.Mutex
	calls int
	total int
}

func (r *recordingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.total += len(p)
	return len(p), nil
}

func mustUsage(t *testing.T, m *Meter, dir Direction) Usage {
	t.Helper()
	u, err := m.Usage(dir)
	if err != nil {
		t.Fatalf("Usage(%s): %v", dir, err)
	}
	return u
}

func TestIndependentDirectionCaps(t *testing.T) {
	const upCap, downCap = int64(100), int64(250)
	m := NewMeter(upCap, downCap)

	upLease, err := m.Reserve(ClientUpload, PhaseBody, upCap)
	if err != nil {
		t.Fatalf("reserve full upload budget: %v", err)
	}
	if err := upLease.Commit(); err != nil {
		t.Fatalf("commit upload: %v", err)
	}

	// Upload is exhausted but must not touch the download budget.
	if _, err := m.Reserve(ClientUpload, PhaseBody, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("upload over cap: want ErrBudgetExceeded, got %v", err)
	}
	downLease, err := m.Reserve(UpstreamDownload, PhaseBody, downCap)
	if err != nil {
		t.Fatalf("download budget must stay independent, got %v", err)
	}
	if err := downLease.Commit(); err != nil {
		t.Fatalf("commit download: %v", err)
	}
	if _, err := m.Reserve(UpstreamDownload, PhaseBody, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("download over cap: want ErrBudgetExceeded, got %v", err)
	}

	up := mustUsage(t, m, ClientUpload)
	down := mustUsage(t, m, UpstreamDownload)
	if up.Committed != upCap || up.Reserved != 0 || up.Available() != 0 {
		t.Fatalf("upload snapshot wrong: %+v", up)
	}
	if down.Committed != downCap || down.Reserved != 0 || down.Available() != 0 {
		t.Fatalf("download snapshot wrong: %+v", down)
	}
}

func TestReserveRejectionHasNoSideEffect(t *testing.T) {
	m := NewMeter(20, 20)
	rw := &recordingWriter{}

	lease, err := m.Reserve(ClientUpload, PhaseBody, 10)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := m.Charge(ClientUpload, PhaseBody, rw, make([]byte, 4)); err != nil {
		t.Fatalf("charge within reservation window: %v", err)
	}

	// With 4 committed and 10 reserved only 6 bytes are available, so this
	// charge cannot fit and the writer must never be invoked.
	if _, err := m.Charge(ClientUpload, PhaseBody, rw, make([]byte, 7)); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want ErrBudgetExceeded, got %v", err)
	}
	if rw.calls != 1 {
		t.Fatalf("rejected charge invoked underlying writer %d times", rw.calls)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	u := mustUsage(t, m, ClientUpload)
	if u.Reserved != 0 || u.Committed != 4 || u.Available() != 16 {
		t.Fatalf("budget after rejection/release wrong: %+v", u)
	}
}

func TestShortWriteCommitsActualAndReleasesRest(t *testing.T) {
	m := NewMeter(100, 100)
	fw := &flakyWriter{shortOn: map[int]bool{1: true}}

	n, err := m.Charge(ClientUpload, PhaseBody, fw, make([]byte, 10))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got %v", err)
	}
	if n != 6 {
		t.Fatalf("short write count: got %d, want 6", n)
	}

	u := mustUsage(t, m, ClientUpload)
	if u.Committed != 6 || u.Reserved != 0 || u.Available() != 94 {
		t.Fatalf("short write accounting wrong: %+v", u)
	}
	// Released bytes are bookable again.
	lease, err := m.Reserve(ClientUpload, PhaseBody, 94)
	if err != nil {
		t.Fatalf("released bytes should be reusable: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestWriteFailureChargesOnceAndSettlesOnce(t *testing.T) {
	m := NewMeter(100, 100)
	fw := &flakyWriter{failOn: map[int]bool{1: true, 2: true}}

	n, err := m.Charge(ClientUpload, PhaseBody, fw, make([]byte, 10))
	if err == nil || errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("want injected write error, got %v", err)
	}
	if n != 5 {
		t.Fatalf("partial count before error: got %d, want 5", n)
	}
	u := mustUsage(t, m, ClientUpload)
	if u.Committed != 5 || u.Reserved != 0 || u.Available() != 95 {
		t.Fatalf("failure path accounting wrong: %+v", u)
	}

	// Retrying must reuse the released five bytes rather than double refund.
	n, err = m.Charge(ClientUpload, PhaseBody, fw, make([]byte, 10))
	if err == nil {
		t.Fatal("want second injected error")
	}
	if n != 5 {
		t.Fatalf("retry partial count: got %d, want 5", n)
	}
	u = mustUsage(t, m, ClientUpload)
	if u.Committed != 10 || u.Reserved != 0 || u.Available() != 90 {
		t.Fatalf("retry must not double refund: %+v", u)
	}

	// One-shot settlement: neither commit nor release can run twice.
	lease, lerr := m.Reserve(ClientUpload, PhaseBody, 4)
	if lerr != nil {
		t.Fatalf("reserve: %v", lerr)
	}
	if err := lease.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := lease.Commit(); !errors.Is(err, ErrLeaseSettled) {
		t.Fatalf("double commit: want ErrLeaseSettled, got %v", err)
	}
	if err := lease.Release(); !errors.Is(err, ErrLeaseSettled) {
		t.Fatalf("release after commit: want ErrLeaseSettled, got %v", err)
	}
	u = mustUsage(t, m, ClientUpload)
	if u.Committed != 14 || u.Reserved != 0 || u.Available() != 86 {
		t.Fatalf("one-shot settlement invariant broken: %+v", u)
	}
}

func TestCommittedBytesAreNonRefundable(t *testing.T) {
	m := NewMeter(10, 10)
	lease, err := m.Reserve(ClientUpload, PhaseBody, 10)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := lease.Settle(7); err != nil {
		t.Fatalf("partial settle: %v", err)
	}
	if _, err := lease.Settle(0); !errors.Is(err, ErrLeaseSettled) {
		t.Fatalf("second settle must be rejected, got %v", err)
	}
	if err := lease.Release(); !errors.Is(err, ErrLeaseSettled) {
		t.Fatalf("release after settle must be rejected, got %v", err)
	}
	u := mustUsage(t, m, ClientUpload)
	if u.Committed != 7 || u.Reserved != 0 || u.Available() != 3 {
		t.Fatalf("committed bytes leaked back: %+v", u)
	}

	if _, err := m.Reserve(ClientUpload, PhaseBody, -1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("negative reserve: want ErrInvalidSize, got %v", err)
	}
	if _, err := m.Reserve(Direction(99), PhaseBody, 1); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("bad direction: want ErrInvalidDirection, got %v", err)
	}
	if _, err := m.Reserve(ClientUpload, Phase(99), 1); !errors.Is(err, ErrInvalidPhase) {
		t.Fatalf("bad phase: want ErrInvalidPhase, got %v", err)
	}
}

func TestGenerationAndPhaseArePinnedToLease(t *testing.T) {
	m := NewMeter(1000, 1000)

	gen1 := m.Generation()
	lease1, err := m.Reserve(ClientUpload, PhaseHeader, 10)
	if err != nil {
		t.Fatalf("reserve gen1 header: %v", err)
	}
	if lease1.Generation() != gen1 || lease1.Phase() != PhaseHeader || lease1.Direction() != ClientUpload {
		t.Fatalf("lease attributes not fixed: gen=%d phase=%s dir=%s",
			lease1.Generation(), lease1.Phase(), lease1.Direction())
	}

	gen2 := m.NextGeneration()
	if gen2 != gen1+1 {
		t.Fatalf("generation bump: got %d, want %d", gen2, gen1+1)
	}
	lease2, err := m.Reserve(ClientUpload, PhaseTunnel, 20)
	if err != nil {
		t.Fatalf("reserve gen2 tunnel: %v", err)
	}

	// The old lease still settles against its original generation/phase.
	if err := lease1.Commit(); err != nil {
		t.Fatalf("commit lease1: %v", err)
	}
	if err := lease2.Commit(); err != nil {
		t.Fatalf("commit lease2: %v", err)
	}

	bd, err := m.Breakdown(ClientUpload)
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	if bd.ByGeneration[gen1] != 10 || bd.ByGeneration[gen2] != 20 {
		t.Fatalf("generation attribution wrong: %+v", bd.ByGeneration)
	}
	if bd.ByPhase[PhaseHeader] != 10 || bd.ByPhase[PhaseTunnel] != 20 {
		t.Fatalf("phase attribution wrong: %+v", bd.ByPhase)
	}
	// Breakdown maps are copies: mutating them cannot corrupt the meter.
	bd.ByGeneration[gen1] = 999
	again, _ := m.Breakdown(ClientUpload)
	if again.ByGeneration[gen1] != 10 {
		t.Fatal("breakdown returned shared mutable state")
	}
}

func TestZeroSizeAndZeroCap(t *testing.T) {
	m := NewMeter(0, 0)
	lease, err := m.Reserve(ClientUpload, PhaseBody, 0)
	if err != nil {
		t.Fatalf("zero-byte reserve on zero cap: %v", err)
	}
	if lease.Size() != 0 {
		t.Fatalf("lease size: got %d, want 0", lease.Size())
	}
	if _, err := lease.Settle(0); err != nil {
		t.Fatalf("zero settle: %v", err)
	}
	if err := lease.Release(); !errors.Is(err, ErrLeaseSettled) {
		t.Fatalf("double zero settle: want ErrLeaseSettled, got %v", err)
	}
	if _, err := m.Reserve(UpstreamDownload, PhaseBody, 1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("zero cap: want ErrBudgetExceeded, got %v", err)
	}
}
