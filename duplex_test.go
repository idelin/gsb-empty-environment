package netsec

import (
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
)

// chaosWriter is a thread-safe io.Writer producing random short writes
// and failures, used to stress the duplex pumps under -race.
type chaosWriter struct {
	mu    sync.Mutex
	rng   *rand.Rand
	total int
}

func (c *chaosWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	roll := c.rng.IntN(10)
	c.mu.Unlock()

	switch {
	case roll < 2 && len(p) > 1: // 20% short write, no error
		n := len(p)/2 + 1
		c.mu.Lock()
		c.total += n
		c.mu.Unlock()
		return n, nil
	case roll == 9 && len(p) > 1: // 10% partial write plus error
		n := len(p) / 2
		c.mu.Lock()
		c.total += n
		c.mu.Unlock()
		return n, errors.New("chaos: injected transport failure")
	default:
		c.mu.Lock()
		c.total += len(p)
		c.mu.Unlock()
		return len(p), nil
	}
}

func (c *chaosWriter) bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// duplexPump performs exactly two rounds of metered writes per
// direction: a fixed body-phase round before the upgrade, then a fixed
// tunnel-phase round after the handshake wins. The upgrade waits on
// barrier and unblocks through proceed, so every pump is guaranteed to
// produce both body and tunnel leases without any polling.
func duplexPump(t *testing.T, w *LeasedWriter, rounds int, barrier, proceed chan struct{}, dir Direction) {
	t.Helper()
	payload := make([]byte, 8)
	writeRound := func(label string) {
		for i := 0; i < rounds; i++ {
			if _, err := w.Write(payload); errors.Is(err, ErrBudgetExceeded) {
				t.Errorf("%s %s pump hit budget unexpectedly: %v", dir, label, err)
				return
			}
			// Short writes and transient transport errors are allowed:
			// Charge has already settled the lease exactly once.
		}
	}

	writeRound("body")
	barrier <- struct{}{}
	<-proceed
	if got := w.Phase(); got != PhaseTunnel {
		t.Errorf("%s pump phase after upgrade: got %s, want tunnel", dir, got)
		return
	}
	writeRound("tunnel")
}

// TestUpgradeRaceDuplex runs upload and download pumps concurrently while
// a handshake win races the in-flight writes: phases and generations stay
// pinned per lease, both caps hold independently, and every byte observed
// on the wire reconciles with the meter.
func TestUpgradeRaceDuplex(t *testing.T) {
	const (
		upCap    = int64(4000)
		downCap  = int64(4000)
		perRound = 25
	)
	m := NewMeter(upCap, downCap)

	upSink := &chaosWriter{rng: rand.New(rand.NewPCG(1, 7))}
	downSink := &chaosWriter{rng: rand.New(rand.NewPCG(2, 9))}

	upWriter, err := m.NewWriter(ClientUpload, PhaseBody, upSink)
	if err != nil {
		t.Fatalf("upload writer: %v", err)
	}
	downWriter, err := m.NewWriter(UpstreamDownload, PhaseBody, downSink)
	if err != nil {
		t.Fatalf("download writer: %v", err)
	}

	barrier := make(chan struct{}, 2)
	proceed := make(chan struct{}, 2)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		duplexPump(t, upWriter, perRound, barrier, proceed, ClientUpload)
	}()
	go func() {
		defer wg.Done()
		duplexPump(t, downWriter, perRound, barrier, proceed, UpstreamDownload)
	}()

	// Both directions have sent body-phase traffic; now the upgrade and
	// generation switch race the moment the pumps resume writing.
	<-barrier
	<-barrier
	genBefore := m.Generation()
	if err := upWriter.SetPhase(PhaseTunnel); err != nil {
		t.Fatalf("upload SetPhase: %v", err)
	}
	if err := downWriter.SetPhase(PhaseTunnel); err != nil {
		t.Fatalf("download SetPhase: %v", err)
	}
	genAfter := m.NextGeneration()
	if genAfter != genBefore+1 {
		t.Fatalf("generation switch: got %d, want %d", genAfter, genBefore+1)
	}
	proceed <- struct{}{}
	proceed <- struct{}{}
	wg.Wait()

	upUsage := mustUsage(t, m, ClientUpload)
	downUsage := mustUsage(t, m, UpstreamDownload)

	// Independent hard caps: one direction can never borrow the other's.
	if upUsage.Committed > upCap {
		t.Fatalf("upload cap breached: committed=%d cap=%d", upUsage.Committed, upCap)
	}
	if downUsage.Committed > downCap {
		t.Fatalf("download cap breached: committed=%d cap=%d", downUsage.Committed, downCap)
	}
	// Every lease is settled once pumps stop.
	if upUsage.Reserved != 0 || downUsage.Reserved != 0 {
		t.Fatalf("outstanding reservations remain: up=%d down=%d",
			upUsage.Reserved, downUsage.Reserved)
	}
	// Meter accounting must match the bytes that actually hit the wire.
	if int64(upSink.bytes()) != upUsage.Committed {
		t.Fatalf("upload wire bytes %d != committed %d", upSink.bytes(), upUsage.Committed)
	}
	if int64(downSink.bytes()) != downUsage.Committed {
		t.Fatalf("download wire bytes %d != committed %d", downSink.bytes(), downUsage.Committed)
	}

	for _, tc := range []struct {
		name string
		dir  Direction
		used Usage
	}{
		{"upload", ClientUpload, upUsage},
		{"download", UpstreamDownload, downUsage},
	} {
		bd, err := m.Breakdown(tc.dir)
		if err != nil {
			t.Fatalf("%s breakdown: %v", tc.name, err)
		}
		var genSum, phaseSum int64
		for _, n := range bd.ByGeneration {
			genSum += n
		}
		for _, n := range bd.ByPhase {
			phaseSum += n
		}
		if genSum != tc.used.Committed || phaseSum != tc.used.Committed {
			t.Fatalf("%s attribution does not reconcile: gen=%d phase=%d committed=%d",
				tc.name, genSum, phaseSum, tc.used.Committed)
		}
		if bd.ByPhase[PhaseBody] == 0 || bd.ByPhase[PhaseTunnel] == 0 {
			t.Fatalf("%s must span body and tunnel phases, got %+v", tc.name, bd.ByPhase)
		}
		if bd.ByGeneration[genBefore] == 0 || bd.ByGeneration[genAfter] == 0 {
			t.Fatalf("%s must attribute bytes to both generations, got %+v",
				tc.name, bd.ByGeneration)
		}
	}

	// Headroom stays non-negative; reservations beyond the caps fail.
	if upUsage.Available() < 0 || downUsage.Available() < 0 {
		t.Fatalf("negative availability: up=%d down=%d",
			upUsage.Available(), downUsage.Available())
	}
	if _, err := m.Reserve(ClientUpload, PhaseTunnel, upCap+1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("upload oversize reservation: want ErrBudgetExceeded, got %v", err)
	}
	if _, err := m.Reserve(UpstreamDownload, PhaseTunnel, downCap+1); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("download oversize reservation: want ErrBudgetExceeded, got %v", err)
	}
}

// TestConcurrentSettleIsOneShot hammers each lease with competing
// commit/release calls: exactly one must win and reserved bytes must
// return to zero with no double charge or double refund.
func TestConcurrentSettleIsOneShot(t *testing.T) {
	const count = 64
	m := NewMeter(int64(count*10), int64(count*10))
	leases := make([]*Lease, count)
	for i := range leases {
		l, err := m.Reserve(ClientUpload, PhaseBody, 10)
		if err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
		leases[i] = l
	}

	var wg sync.WaitGroup
	var winsMu sync.Mutex
	wins := make([]int, count)
	for i, l := range leases {
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(idx int, lease *Lease, attempt int) {
				defer wg.Done()
				var err error
				if attempt%2 == 0 {
					_, err = lease.Settle(10)
				} else {
					err = lease.Release()
				}
				if err == nil {
					winsMu.Lock()
					wins[idx]++
					winsMu.Unlock()
				} else if !errors.Is(err, ErrLeaseSettled) {
					t.Errorf("lease %d attempt %d: unexpected error %v", idx, attempt, err)
				}
			}(i, l, g)
		}
	}
	wg.Wait()

	for i, w := range wins {
		if w != 1 {
			t.Fatalf("lease %d settled %d times, want exactly 1", i, w)
		}
	}
	u := mustUsage(t, m, ClientUpload)
	if u.Reserved != 0 {
		t.Fatalf("reserved leaked: %d", u.Reserved)
	}
	if u.Committed < 0 || u.Committed > int64(count*10) {
		t.Fatalf("committed out of range: %d", u.Committed)
	}
	if u.Committed+u.Available() != u.Cap {
		t.Fatalf("budget does not reconcile: %+v", u)
	}
	bd, err := m.Breakdown(ClientUpload)
	if err != nil {
		t.Fatalf("breakdown: %v", err)
	}
	var phaseSum int64
	for _, n := range bd.ByPhase {
		phaseSum += n
	}
	if phaseSum != u.Committed {
		t.Fatalf("phase breakdown %d != committed %d", phaseSum, u.Committed)
	}
}

func TestNewWriterRejectsBadInputs(t *testing.T) {
	m := NewMeter(10, 10)
	if _, err := m.NewWriter(Direction(99), PhaseBody, &chaosWriter{}); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("bad direction: want ErrInvalidDirection, got %v", err)
	}
	if _, err := m.NewWriter(ClientUpload, Phase(99), &chaosWriter{}); !errors.Is(err, ErrInvalidPhase) {
		t.Fatalf("bad phase: want ErrInvalidPhase, got %v", err)
	}
	if _, err := m.NewWriter(ClientUpload, PhaseBody, nil); err == nil {
		t.Fatal("nil writer must be rejected")
	}
}
