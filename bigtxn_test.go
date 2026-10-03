package bigtxn

import (
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	acceptRows   = 500000 // acceptance: one big update producing 500k changes
	acceptChunks = 100    // acceptance: split into 100 streamed batches
	acceptMemMB  = 10     // acceptance: peak memory stays under 10MB
)

// TestChunkerSplitsLargeTransaction verifies that a 500k-row transaction
// is split into exactly 100 phase-marked chunks.
func TestChunkerSplitsLargeTransaction(t *testing.T) {
	src := NewGeneratorSource(7, acceptRows, 32)
	chunker := &Chunker{MaxRows: acceptRows / acceptChunks}

	var phases []Phase
	total := 0
	err := chunker.Stream(src, func(c *Chunk) error {
		phases = append(phases, c.Phase)
		total += c.RowCount()
		if c.Seq != len(phases)-1 {
			t.Fatalf("chunk seq out of order: got %d", c.Seq)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != acceptRows {
		t.Fatalf("total rows = %d, want %d", total, acceptRows)
	}
	if len(phases) != acceptChunks {
		t.Fatalf("chunks = %d, want %d", len(phases), acceptChunks)
	}
	if phases[0] != PhaseBegin {
		t.Fatalf("first chunk phase = %s, want BEGIN", phases[0])
	}
	for i := 1; i < len(phases)-1; i++ {
		if phases[i] != PhaseContinue {
			t.Fatalf("chunk %d phase = %s, want CONTINUE", i, phases[i])
		}
	}
	if phases[len(phases)-1] != PhaseEnd {
		t.Fatalf("last chunk phase = %s, want END", phases[len(phases)-1])
	}
}

// TestChunkerSmallTransaction verifies a small transaction stays a
// single chunk marked PhaseSingle.
func TestChunkerSmallTransaction(t *testing.T) {
	src := NewGeneratorSource(1, 10, 16)
	var phases []Phase
	err := (&Chunker{MaxRows: 5000}).Stream(src, func(c *Chunk) error {
		phases = append(phases, c.Phase)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 1 || phases[0] != PhaseSingle {
		t.Fatalf("phases = %v, want [SINGLE]", phases)
	}
}

// TestTransferIntegrity runs the full pipeline and checks end-to-end
// transaction integrity: 100 chunks, 500k rows, matching checksums.
func TestTransferIntegrity(t *testing.T) {
	dir, err := ioutil.TempDir("", "bigtxn-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	spiller, err := NewSpillBuffer(dir)
	if err != nil {
		t.Fatal(err)
	}
	limiter := NewMemoryLimiter(4 << 20) // 4MB in-flight budget
	tr := &Transporter{
		Chunker: &Chunker{MaxRows: acceptRows / acceptChunks},
		Limiter: limiter,
		Spiller: spiller,
	}
	recv := &Receiver{}
	rep, err := tr.Transfer(NewGeneratorSource(42, acceptRows, 32), recv)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Chunks != acceptChunks {
		t.Fatalf("chunks = %d, want %d", rep.Chunks, acceptChunks)
	}
	if rep.Rows != acceptRows {
		t.Fatalf("rows = %d, want %d", rep.Rows, acceptRows)
	}
	if !recv.Complete() {
		t.Fatal("receiver did not see a complete transaction")
	}
	if recv.Rows() != acceptRows || recv.Chunks() != acceptChunks {
		t.Fatalf("receiver applied %d rows / %d chunks", recv.Rows(), recv.Chunks())
	}
	if recv.Checksum() != rep.Checksum {
		t.Fatalf("checksum mismatch: sender %08x, receiver %08x", rep.Checksum, recv.Checksum())
	}
	if rep.PeakInFlight > limiter.Budget() {
		t.Fatalf("peak in-flight %d exceeds budget %d", rep.PeakInFlight, limiter.Budget())
	}
}

// TestSpillUnderMemoryPressure forces the budget below one chunk so
// every chunk spills to disk, then verifies data integrity and cleanup.
func TestSpillUnderMemoryPressure(t *testing.T) {
	dir, err := ioutil.TempDir("", "bigtxn-spill-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	spiller, err := NewSpillBuffer(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr := &Transporter{
		Chunker: &Chunker{MaxRows: 1000},
		Limiter: NewMemoryLimiter(1), // smaller than any chunk: always spill
		Spiller: spiller,
	}
	recv := &Receiver{}
	rep, err := tr.Transfer(NewGeneratorSource(9, 10000, 64), recv)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SpilledChunks != rep.Chunks || rep.Chunks != 10 {
		t.Fatalf("spilled %d of %d chunks, want 10 of 10", rep.SpilledChunks, rep.Chunks)
	}
	if spiller.Written() != 10 {
		t.Fatalf("spill files written = %d, want 10", spiller.Written())
	}
	if recv.Rows() != 10000 || recv.Checksum() != rep.Checksum {
		t.Fatalf("integrity broken after spill: rows=%d", recv.Rows())
	}
	left, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d spill files left behind", len(left))
	}
}

// TestIntegrityDetectsLoss verifies the tracker rejects a transaction
// with a dropped or reordered chunk.
func TestIntegrityDetectsLoss(t *testing.T) {
	src := NewGeneratorSource(5, 10000, 16)
	chunker := &Chunker{MaxRows: 1000}
	var chunks []*Chunk
	if err := chunker.Stream(src, func(c *Chunk) error {
		chunks = append(chunks, c)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Drop chunk 3 of 10: the tracker must complain about the gap.
	var tracker TxnTracker
	var err error
	for _, c := range chunks {
		if c.Seq == 3 {
			continue
		}
		if err = tracker.Track(c); err != nil {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("expected out-of-order error, got %v", err)
	}

	// A tampered chunk must fail the receiver's checksum verification.
	recv := &Receiver{}
	tampered := chunks[0]
	tampered.Rows[0].Key ^= 0xdead
	if err := recv.WriteChunk(tampered); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

// TestPeakMemoryUnder10MB is the acceptance gate: streaming 500k changes
// in 100 batches must keep peak heap growth under 10MB.
func TestPeakMemoryUnder10MB(t *testing.T) {
	dir, err := ioutil.TempDir("", "bigtxn-mem-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	spiller, err := NewSpillBuffer(dir)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak uint64
	done := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak {
					peak = ms.HeapAlloc
				}
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	tr := &Transporter{
		Chunker: &Chunker{MaxRows: acceptRows / acceptChunks},
		Limiter: NewMemoryLimiter(4 << 20),
		Spiller: spiller,
	}
	recv := &Receiver{}
	rep, err := tr.Transfer(NewGeneratorSource(1, acceptRows, 32), recv)
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rows != acceptRows || rep.Chunks != acceptChunks {
		t.Fatalf("rows=%d chunks=%d", rep.Rows, rep.Chunks)
	}
	growth := int64(peak) - int64(base.HeapAlloc)
	t.Logf("peak heap growth: %.2f MB (limiter peak in-flight: %.2f MB)",
		float64(growth)/(1<<20), float64(rep.PeakInFlight)/(1<<20))
	if growth > acceptMemMB<<20 {
		t.Fatalf("peak heap growth %d bytes exceeds %dMB", growth, acceptMemMB)
	}
	if rep.PeakInFlight > acceptMemMB<<20 {
		t.Fatalf("limiter peak %d bytes exceeds %dMB", rep.PeakInFlight, acceptMemMB)
	}
}
