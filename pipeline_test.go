package logpipe

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// readWAL parses the WAL file and returns the persisted LSNs in file order.
func readWAL(t *testing.T, path string) []uint64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lsns []uint64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fields := strings.SplitN(sc.Text(), " ", 3)
		if len(fields) != 3 {
			t.Fatalf("malformed WAL line: %q", sc.Text())
		}
		lsn, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			t.Fatalf("bad LSN in WAL line %q: %v", sc.Text(), err)
		}
		lsns = append(lsns, lsn)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return lsns
}

// runProducers starts producers goroutines each appending perProducer
// records. It returns the LSNs assigned to each producer, in order.
func runProducers(p *Pipeline, producers, perProducer int) [][]uint64 {
	assigned := make([][]uint64, producers)
	var wg sync.WaitGroup
	for id := 0; id < producers; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				lsn, err := p.Append([]byte(fmt.Sprintf("stream-%02d-record-%04d", id, i)))
				if err != nil {
					return
				}
				assigned[id] = append(assigned[id], lsn)
			}
		}(id)
	}
	wg.Wait()
	return assigned
}

func insertBarriers(p *Pipeline, lsns ...uint64) ([]error, *sync.WaitGroup) {
	errs := make([]error, len(lsns))
	var wg sync.WaitGroup
	for i, lsn := range lsns {
		wg.Add(1)
		go func(i int, lsn uint64) {
			defer wg.Done()
			errs[i] = p.InsertBarrierAt(lsn)
		}(i, lsn)
	}
	return errs, &wg
}

// Acceptance test: 10 concurrent streams x 200 records, flush barriers at
// record 500 and 1200, injected write failure at record 1250. Afterwards
// every record with LSN <= 1200 must be durable and the in-memory buffer
// accounting must be conserved (zero unconfirmed entries/bytes).
func TestBarrierRollbackConsistency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	p, err := OpenWithOptions(path, Options{FailAtLSN: 1250})
	if err != nil {
		t.Fatal(err)
	}

	barrierErrs, bwg := insertBarriers(p, 500, 1200)
	assigned := runProducers(p, 10, 200)
	bwg.Wait()
	<-p.Done()

	if p.Err() == nil {
		t.Fatal("expected pipeline failure at LSN 1250")
	}
	for i, err := range barrierErrs {
		if err != nil {
			t.Fatalf("barrier %d did not complete: %v", i, err)
		}
	}

	// LSNs are unique and monotonic within every stream.
	seen := make(map[uint64]bool)
	total := 0
	for id, list := range assigned {
		for i, lsn := range list {
			if seen[lsn] {
				t.Fatalf("duplicate LSN %d", lsn)
			}
			seen[lsn] = true
			if i > 0 && lsn <= list[i-1] {
				t.Fatalf("stream %d: non-monotonic LSN %d after %d", id, lsn, list[i-1])
			}
			total++
		}
	}
	if total < 1250 {
		t.Fatalf("expected at least 1250 accepted records before failure, got %d", total)
	}

	// Memory conservation: rollback released every unconfirmed buffer.
	entries, bytes := p.MemStats()
	if entries != 0 || bytes != 0 {
		t.Fatalf("memory not conserved after rollback: entries=%d bytes=%d", entries, bytes)
	}
	if got := p.DurableLSN(); got < 1200 {
		t.Fatalf("durable LSN %d, want >= 1200", got)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	// Everything before the failure is persisted, in order; the failed
	// record (1250) and anything after it is absent.
	lsns := readWAL(t, path)
	if len(lsns) != 1249 {
		t.Fatalf("persisted %d records, want exactly 1249", len(lsns))
	}
	for i, lsn := range lsns {
		if want := uint64(i + 1); lsn != want {
			t.Fatalf("WAL record %d: got LSN %d, want %d", i, lsn, want)
		}
	}
	for lsn := uint64(1); lsn <= 1200; lsn++ {
		if lsns[lsn-1] != lsn {
			t.Fatalf("pre-barrier record LSN %d not persisted", lsn)
		}
	}
}

// Happy path: same workload without fault injection. Barriers complete,
// all 2000 records persist in LSN order, and buffers drain to zero.
func TestBarrierDurabilityHappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	barrierErrs, bwg := insertBarriers(p, 500, 1200)
	assigned := runProducers(p, 10, 200)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	bwg.Wait()

	for i, err := range barrierErrs {
		if err != nil {
			t.Fatalf("barrier %d did not complete: %v", i, err)
		}
	}
	total := 0
	for _, list := range assigned {
		total += len(list)
	}
	if total != 2000 {
		t.Fatalf("accepted %d records, want 2000", total)
	}
	if got := p.DurableLSN(); got != 2000 {
		t.Fatalf("durable LSN %d, want 2000", got)
	}
	entries, bytes := p.MemStats()
	if entries != 0 || bytes != 0 {
		t.Fatalf("buffers not drained: entries=%d bytes=%d", entries, bytes)
	}

	lsns := readWAL(t, path)
	if len(lsns) != 2000 {
		t.Fatalf("persisted %d records, want 2000", len(lsns))
	}
	for i, lsn := range lsns {
		if want := uint64(i + 1); lsn != want {
			t.Fatalf("WAL record %d: got LSN %d, want %d", i, lsn, want)
		}
	}
}

// A barrier at the current stream end blocks publishers until the
// preceding records are durable, then unblocks them.
func TestInsertBarrierBlocksPublishers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	p, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := 0; i < 5; i++ {
		if _, err := p.Append([]byte(fmt.Sprintf("rec-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.InsertBarrier(); err != nil {
		t.Fatal(err)
	}
	if got := p.DurableLSN(); got != 5 {
		t.Fatalf("durable LSN %d, want 5", got)
	}
	lsn, err := p.Append([]byte("after-barrier"))
	if err != nil {
		t.Fatal(err)
	}
	if lsn != 6 {
		t.Fatalf("got LSN %d, want 6", lsn)
	}
}

// After a failure, pending Appends and barriers wake up with an error and
// the unconfirmed buffers are released.
func TestRollbackWakesBlockedProducers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	p, err := OpenWithOptions(path, Options{FailAtLSN: 3})
	if err != nil {
		t.Fatal(err)
	}

	// Barrier at 10 can never complete: the pipeline fails at LSN 3.
	barrierErr := make(chan error, 1)
	go func() { barrierErr <- p.InsertBarrierAt(10) }()

	// Enough records to pass the failure point; producers block behind
	// the unsatisfiable barrier and must be released by the rollback.
	for i := 0; i < 20; i++ {
		p.Append([]byte(fmt.Sprintf("rec-%d", i)))
	}
	<-p.Done()

	if err := <-barrierErr; err != ErrPipelineFailed {
		t.Fatalf("barrier returned %v, want ErrPipelineFailed", err)
	}
	if _, err := p.Append([]byte("too-late")); err != ErrPipelineFailed {
		t.Fatalf("append after failure returned %v, want ErrPipelineFailed", err)
	}
	entries, bytes := p.MemStats()
	if entries != 0 || bytes != 0 {
		t.Fatalf("memory not conserved: entries=%d bytes=%d", entries, bytes)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	lsns := readWAL(t, path)
	if len(lsns) != 2 {
		t.Fatalf("persisted %d records, want 2 (LSN 1 and 2)", len(lsns))
	}
}
