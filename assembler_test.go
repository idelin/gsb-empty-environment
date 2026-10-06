package chunkedtx

import (
	"crypto/sha256"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for expiry tests.
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time          { return f.now }
func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }

func newAssembler(t *testing.T, budget int64, clock *fakeClock) (*Assembler, *[]Commit) {
	t.Helper()
	if clock == nil {
		clock = &fakeClock{now: time.Unix(1700000000, 0)}
	}
	var commits []Commit
	a, err := New(Config{MaxStagedBytes: budget, Now: clock.Now}, func(c Commit) {
		commits = append(commits, c)
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a, &commits
}

// makeTx builds n chunks for txID whose concatenated payloads hash to
// the total digest carried by the final chunk.
func makeTx(txID string, parts ...string) []Chunk {
	full := strings.Join(parts, "")
	digest := sha256.Sum256([]byte(full))
	chunks := make([]Chunk, len(parts))
	for i, p := range parts {
		chunks[i] = Chunk{TxID: txID, Seq: uint32(i), Payload: []byte(p)}
	}
	chunks[len(chunks)-1].Final = true
	chunks[len(chunks)-1].TotalDigest = digest
	return chunks
}

func mustStaged(t *testing.T, a *Assembler, c Chunk) {
	t.Helper()
	out, err := a.Ingest(c)
	if err != nil {
		t.Fatalf("Ingest(seq=%d): %v", c.Seq, err)
	}
	if out.Status != StatusStaged {
		t.Fatalf("Ingest(seq=%d) status = %v, want StatusStaged", c.Seq, out.Status)
	}
}

func TestCommitInOrder(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx1", "hello, ", "chunked ", "world")
	for _, c := range chunks[:len(chunks)-1] {
		mustStaged(t, a, c)
	}
	out, err := a.Ingest(chunks[len(chunks)-1])
	if err != nil {
		t.Fatalf("final Ingest: %v", err)
	}
	if out.Status != StatusCommitted {
		t.Fatalf("status = %v, want StatusCommitted", out.Status)
	}
	if got := string(out.Commit.Payload); got != "hello, chunked world" {
		t.Fatalf("payload = %q", got)
	}
	if out.Commit.Chunks != 3 {
		t.Fatalf("chunks = %d, want 3", out.Commit.Chunks)
	}
	if len(*commits) != 1 || (*commits)[0].TxID != "tx1" {
		t.Fatalf("onCommit fired %d times, want 1 for tx1", len(*commits))
	}
	if a.StagedBytes() != 0 || a.InFlight() != 0 {
		t.Fatalf("budget not released after commit: staged=%d inflight=%d", a.StagedBytes(), a.InFlight())
	}
}

func TestCommitOutOfOrder(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	parts := []string{"a", "b", "c", "d", "e", "f"}
	chunks := makeTx("tx-oo", parts...)

	// Shuffle delivery; only the chunk that fills the last gap may commit.
	perm := rand.New(rand.NewSource(42)).Perm(len(chunks))
	for i, idx := range perm {
		out, err := a.Ingest(chunks[idx])
		if err != nil {
			t.Fatalf("Ingest(seq=%d): %v", chunks[idx].Seq, err)
		}
		isLast := i == len(perm)-1
		if !isLast && out.Status == StatusCommitted {
			t.Fatalf("premature commit after %d/%d chunks", i+1, len(perm))
		}
		if isLast && out.Status != StatusCommitted {
			t.Fatalf("status = %v after last gap filled, want StatusCommitted", out.Status)
		}
	}
	if len(*commits) != 1 {
		t.Fatalf("commits = %d, want exactly 1", len(*commits))
	}
	if got := string((*commits)[0].Payload); got != "abcdef" {
		t.Fatalf("assembled payload = %q, want %q", got, "abcdef")
	}
}

func TestNoPrematureCommitWithGap(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-gap", "x", "y", "z")

	// Deliver final chunk first, then seq 0; seq 1 is still missing.
	mustStaged(t, a, chunks[2])
	mustStaged(t, a, chunks[0])
	if len(*commits) != 0 {
		t.Fatal("committed with a gap in the sequence")
	}
	out, err := a.Ingest(chunks[1])
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if out.Status != StatusCommitted {
		t.Fatalf("status = %v, want StatusCommitted", out.Status)
	}
	if got := string(out.Commit.Payload); got != "xyz" {
		t.Fatalf("payload = %q, want %q", got, "xyz")
	}
}

func TestDuplicateIdenticalChunk(t *testing.T) {
	a, _ := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-dup", "p0", "p1")
	mustStaged(t, a, chunks[0])
	stagedAfterFirst := a.StagedBytes()

	out, err := a.Ingest(chunks[0])
	if err != nil {
		t.Fatalf("duplicate Ingest: %v", err)
	}
	if out.Status != StatusDuplicate {
		t.Fatalf("status = %v, want StatusDuplicate", out.Status)
	}
	if a.StagedBytes() != stagedAfterFirst {
		t.Fatal("duplicate chunk consumed budget twice")
	}
}

func TestConflictPermanentlyFails(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-conflict", "good", "tail")
	mustStaged(t, a, chunks[0])

	evil := chunks[0]
	evil.Payload = []byte("EVIL")
	if _, err := a.Ingest(evil); err == nil {
		t.Fatal("conflicting chunk accepted")
	}
	if a.InFlight() != 0 || a.StagedBytes() != 0 {
		t.Fatal("failed transaction still holds budget")
	}

	// The transaction is permanently failed: even a complete, valid
	// set of chunks can never commit it.
	for _, c := range chunks {
		if _, err := a.Ingest(c); err != ErrTxFailed {
			t.Fatalf("Ingest after failure err = %v, want ErrTxFailed", err)
		}
	}
	if len(*commits) != 0 {
		t.Fatal("failed transaction committed")
	}
}

func TestConflictingFinalChunksFail(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-2final", "aa", "bb", "cc")
	mustStaged(t, a, chunks[0])
	mustStaged(t, a, chunks[2]) // final at seq 2; seq 1 still missing

	other := chunks[2]
	other.Seq = 7
	if _, err := a.Ingest(other); err == nil {
		t.Fatal("second final chunk accepted")
	}
	if _, err := a.Ingest(chunks[1]); err != ErrTxFailed {
		t.Fatalf("err = %v, want ErrTxFailed", err)
	}
	if len(*commits) != 0 {
		t.Fatal("transaction with conflicting finals committed")
	}
}

func TestDigestMismatchFailsPermanently(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-bad-digest", "real ", "data")
	chunks[1].TotalDigest = sha256.Sum256([]byte("something else"))

	mustStaged(t, a, chunks[0])
	if _, err := a.Ingest(chunks[1]); err == nil {
		t.Fatal("digest mismatch did not fail the transaction")
	}
	if len(*commits) != 0 {
		t.Fatal("committed despite digest mismatch")
	}
	if _, err := a.Ingest(chunks[0]); err != ErrTxFailed {
		t.Fatalf("err = %v, want ErrTxFailed", err)
	}
}

func TestBudgetExceeded(t *testing.T) {
	a, commits := newAssembler(t, 10, nil)

	big := Chunk{TxID: "tx-big", Seq: 0, Payload: []byte("01234567890")}
	if _, err := a.Ingest(big); err != ErrBudgetExceeded {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if a.InFlight() != 0 {
		t.Fatal("rejected chunk left state behind")
	}

	// A transaction that fits can still commit and frees its budget.
	chunks := makeTx("tx-fit", "12345", "67890")
	mustStaged(t, a, chunks[0])
	if _, err := a.Ingest(chunks[1]); err != nil {
		t.Fatalf("commit within budget: %v", err)
	}
	if len(*commits) != 1 {
		t.Fatal("expected commit within budget")
	}

	// Budget is shared globally across transactions.
	mustStaged(t, a, makeTx("tx-a", "12345", "0")[0])
	if _, err := a.Ingest(makeTx("tx-b", "123456", "0")[0]); err != ErrBudgetExceeded {
		t.Fatalf("global budget not enforced: err = %v", err)
	}
}

func TestExpiryReapsAndRejectsLateEvents(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	a, commits := newAssembler(t, 1048576, clock)

	chunks := makeTx("tx-ttl", "half-", "done")
	chunks[0].ExpiresAt = clock.now.Add(time.Minute)
	chunks[1].ExpiresAt = clock.now.Add(time.Minute)
	mustStaged(t, a, chunks[0])

	clock.Advance(2 * time.Minute)

	// The late chunk is rejected and nothing commits.
	if _, err := a.Ingest(chunks[1]); err != ErrTxExpired {
		t.Fatalf("late chunk err = %v, want ErrTxExpired", err)
	}
	if len(*commits) != 0 {
		t.Fatal("expired transaction committed")
	}
	if a.StagedBytes() != 0 {
		t.Fatal("expired transaction still holds budget")
	}

	// Replaying the whole transaction cannot resurrect it.
	for _, c := range chunks {
		if _, err := a.Ingest(c); err != ErrTxFailed {
			t.Fatalf("replay err = %v, want ErrTxFailed", err)
		}
	}
}

func TestReapExpiresIdleTransactions(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	a, commits := newAssembler(t, 1048576, clock)

	c1 := makeTx("tx-reap", "aa", "bb")[0]
	c1.ExpiresAt = clock.now.Add(time.Minute)
	mustStaged(t, a, c1)
	mustStaged(t, a, makeTx("tx-keep", "cc", "dd")[0]) // no expiry

	clock.Advance(90 * time.Second)
	reaped := a.Reap()
	if len(reaped) != 1 || reaped[0] != "tx-reap" {
		t.Fatalf("reaped = %v, want [tx-reap]", reaped)
	}
	if a.InFlight() != 1 {
		t.Fatalf("inflight = %d, want 1 (tx-keep survives)", a.InFlight())
	}
	if len(*commits) != 0 {
		t.Fatal("reaping published a commit")
	}
}

func TestExpiredChunkRejectedOnArrival(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	a, _ := newAssembler(t, 1048576, clock)

	stale := makeTx("tx-stale", "x")[0]
	stale.ExpiresAt = clock.now.Add(-time.Second)
	if _, err := a.Ingest(stale); err != ErrChunkExpired {
		t.Fatalf("err = %v, want ErrChunkExpired", err)
	}
	if a.InFlight() != 0 {
		t.Fatal("expired chunk was staged")
	}
}

func TestLateChunkAfterCommitRejected(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	chunks := makeTx("tx-done", "fin")
	if _, err := a.Ingest(chunks[0]); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// A retransmitted chunk for the committed transaction must not
	// stage a new transaction or commit again.
	if _, err := a.Ingest(chunks[0]); err != ErrTxCommitted {
		t.Fatalf("err = %v, want ErrTxCommitted", err)
	}
	if a.InFlight() != 0 || len(*commits) != 1 {
		t.Fatal("late chunk resurrected a committed transaction")
	}
}

func TestValidation(t *testing.T) {
	a, _ := newAssembler(t, 1048576, nil)

	if _, err := a.Ingest(Chunk{Seq: 0, Payload: []byte("x")}); err == nil {
		t.Fatal("empty tx id accepted")
	}
	nonFinal := Chunk{TxID: "t", Seq: 0, Payload: []byte("x"), TotalDigest: sha256.Sum256([]byte("x"))}
	if _, err := a.Ingest(nonFinal); err == nil {
		t.Fatal("total digest on non-final chunk accepted")
	}
	bareFinal := Chunk{TxID: "t", Seq: 0, Final: true, Payload: []byte("x")}
	if _, err := a.Ingest(bareFinal); err == nil {
		t.Fatal("final chunk without total digest accepted")
	}
	if _, err := New(Config{MaxStagedBytes: 0}, nil); err == nil {
		t.Fatal("zero budget accepted")
	}
}

func TestConcurrentIngest(t *testing.T) {
	a, commits := newAssembler(t, 1048576, nil)
	const txs = 8
	done := make(chan error, txs)
	for i := 0; i < txs; i++ {
		go func(i int) {
			id := fmt.Sprintf("tx-%d", i)
			for _, c := range makeTx(id, "part0-", "part1-", "part2") {
				if _, err := a.Ingest(c); err != nil && err != ErrTxCommitted {
					done <- err
					return
				}
			}
			done <- nil
		}(i)
	}
	for i := 0; i < txs; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent ingest: %v", err)
		}
	}
	if len(*commits) != txs {
		t.Fatalf("commits = %d, want %d", len(*commits), txs)
	}
	if a.StagedBytes() != 0 {
		t.Fatal("budget leaked after concurrent commits")
	}
}

func TestErrorsAreSentinels(t *testing.T) {
	// Callers must be able to compare terminal errors directly.
	if ErrTxFailed != ErrTxFailed || ErrTxFailed == ErrTxCommitted {
		t.Fatal("sentinel comparison broken")
	}
}
