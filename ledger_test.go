package ledger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ledger.dat")
}

func mustFund(t *testing.T, l *Ledger, req, acct string, cents int64) uint64 {
	t.Helper()
	seq, err := l.Fund(req, acct, cents)
	if err != nil {
		t.Fatalf("Fund(%s,%s,%d): %v", req, acct, cents, err)
	}
	return seq
}

func mustTransfer(t *testing.T, l *Ledger, req, from, to string, cents int64) uint64 {
	t.Helper()
	seq, err := l.Transfer(req, from, to, cents)
	if err != nil {
		t.Fatalf("Transfer(%s,%s,%s,%d): %v", req, from, to, cents, err)
	}
	return seq
}

// totalCents sums all balances; transfers and reversals must conserve it.
func totalCents(bal map[string]int64) int64 {
	var sum int64
	for _, v := range bal {
		sum += v
	}
	return sum
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// TestMidWriteFailure simulates a crash that leaves a half-written tail
// record: reopening must discard only the partial tail and keep every
// complete record intact.
func TestMidWriteFailure(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustFund(t, l, "fund-1", "alice", 10000)
	mustTransfer(t, l, "tx-1", "alice", "bob", 2500)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	sizeBefore := fileSize(t, path)

	// Simulate an interrupted write: append half a record.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, RecordSize/2)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l, err = Open(path)
	if err != nil {
		t.Fatalf("Open after crash: %v", err)
	}
	defer l.Close()
	if got := l.Balance("alice"); got != 7500 {
		t.Fatalf("alice balance = %d, want 7500", got)
	}
	if got := l.Balance("bob"); got != 2500 {
		t.Fatalf("bob balance = %d, want 2500", got)
	}
	if l.Len() != 2 {
		t.Fatalf("len = %d, want 2", l.Len())
	}
	if got := fileSize(t, path); got != sizeBefore {
		t.Fatalf("partial tail not truncated: size %d, want %d", got, sizeBefore)
	}
	// The ledger must keep working after recovery.
	mustTransfer(t, l, "tx-2", "bob", "alice", 500)
	if got := l.Balance("alice"); got != 8000 {
		t.Fatalf("alice balance = %d, want 8000", got)
	}
	if _, err := Verify(path); err != nil {
		t.Fatalf("Verify after recovery: %v", err)
	}
}

// TestCorruptCompleteRecord flips a byte inside a complete record:
// Open must report the exact position, and Recover must keep only the
// valid prefix.
func TestCorruptCompleteRecord(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustFund(t, l, "fund-1", "alice", 10000)
	mustTransfer(t, l, "tx-1", "alice", "bob", 2500)
	mustTransfer(t, l, "tx-2", "alice", "bob", 1000)
	l.Close()

	// Corrupt one byte in the middle of record 2 (offset RecordSize+10).
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	b := []byte{0}
	if _, err := f.ReadAt(b, RecordSize+10); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, RecordSize+10); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = Open(path)
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("Open: got %v, want *CorruptError", err)
	}
	if ce.Offset != RecordSize || ce.Seq != 2 {
		t.Fatalf("corruption reported at offset=%d seq=%d, want offset=%d seq=2",
			ce.Offset, ce.Seq, RecordSize)
	}

	// Recover keeps the valid prefix (record 1) and reports the drop.
	l2, rep, err := Recover(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if rep.Corrupt == nil || rep.Corrupt.Seq != 2 {
		t.Fatalf("report = %+v, want corrupt at seq 2", rep)
	}
	if l2.Len() != 1 {
		t.Fatalf("recovered len = %d, want 1", l2.Len())
	}
	if got := l2.Balance("alice"); got != 10000 {
		t.Fatalf("alice balance = %d, want 10000", got)
	}
	if got := fileSize(t, path); got != RecordSize {
		t.Fatalf("file size = %d, want %d (one record)", got, RecordSize)
	}
}

// TestDuplicateReversal: a transfer can be reversed exactly once.
func TestDuplicateReversal(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mustFund(t, l, "fund-1", "alice", 10000)
	txSeq := mustTransfer(t, l, "tx-1", "alice", "bob", 4000)

	revSeq, err := l.Reverse("rev-1", txSeq)
	if err != nil {
		t.Fatalf("Reverse: %v", err)
	}
	if got := l.Balance("alice"); got != 10000 {
		t.Fatalf("alice = %d, want 10000 after reversal", got)
	}
	if got := l.Balance("bob"); got != 0 {
		t.Fatalf("bob = %d, want 0 after reversal", got)
	}
	// Second reversal of the same transfer must fail and write nothing.
	if _, err := l.Reverse("rev-2", txSeq); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("second Reverse: got %v, want ErrAlreadyReversed", err)
	}
	if l.Len() != revSeq {
		t.Fatalf("len = %d, want %d (no extra record)", l.Len(), revSeq)
	}
	// Replaying the same reversal request returns the original record.
	seq, err := l.Reverse("rev-1", txSeq)
	if err != nil || seq != revSeq {
		t.Fatalf("replayed Reverse = seq %d, err %v; want seq %d, nil", seq, err, revSeq)
	}
	// The original transfer record is still in place, unmodified.
	snap, err := l.BalancesAt(txSeq)
	if err != nil {
		t.Fatal(err)
	}
	if snap["alice"] != 6000 || snap["bob"] != 4000 {
		t.Fatalf("snapshot at %d = %v, want alice=6000 bob=4000", txSeq, snap)
	}
}

// TestReverseTransferConservation moves money back and forth between
// two accounts and checks conservation after every step, including
// after a reversal, with the independent reader agreeing.
func TestReverseTransferConservation(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustFund(t, l, "fund-1", "alice", 5000)
	mustFund(t, l, "fund-2", "bob", 3000)
	const total = 8000

	check := func(step string) {
		t.Helper()
		bal, err := Verify(path)
		if err != nil {
			t.Fatalf("%s: Verify: %v", step, err)
		}
		if got := totalCents(bal); got != total {
			t.Fatalf("%s: total = %d, want %d", step, got, total)
		}
		if bal["alice"] != l.Balance("alice") || bal["bob"] != l.Balance("bob") {
			t.Fatalf("%s: reader %v disagrees with writer alice=%d bob=%d",
				step, bal, l.Balance("alice"), l.Balance("bob"))
		}
	}

	mustTransfer(t, l, "tx-1", "alice", "bob", 2000)
	check("alice->bob")
	mustTransfer(t, l, "tx-2", "bob", "alice", 4500)
	check("bob->alice")
	if l.Balance("alice") != 7500 || l.Balance("bob") != 500 {
		t.Fatalf("balances alice=%d bob=%d, want 7500/500",
			l.Balance("alice"), l.Balance("bob"))
	}
	if _, err := l.Reverse("rev-1", 4); err != nil { // undo tx-2
		t.Fatalf("Reverse: %v", err)
	}
	check("reverse tx-2")
	if l.Balance("alice") != 3000 || l.Balance("bob") != 5000 {
		t.Fatalf("after reversal alice=%d bob=%d, want 3000/5000",
			l.Balance("alice"), l.Balance("bob"))
	}
	l.Close()
}

// TestReopenIdempotency: after reopening the file, replaying a request
// ID returns the original result, and the same ID with different
// content is a conflict.
func TestReopenIdempotency(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustFund(t, l, "fund-1", "alice", 10000)
	txSeq := mustTransfer(t, l, "tx-1", "alice", "bob", 2500)
	l.Close()

	l, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// Same request ID, same content: original seq, no new record.
	seq, err := l.Transfer("tx-1", "alice", "bob", 2500)
	if err != nil || seq != txSeq {
		t.Fatalf("replay = seq %d err %v, want seq %d nil", seq, err, txSeq)
	}
	if l.Len() != 2 {
		t.Fatalf("len = %d, want 2 after idempotent replay", l.Len())
	}
	// Same request ID, different content: conflict, nothing written.
	if _, err := l.Transfer("tx-1", "alice", "bob", 9999); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting replay: got %v, want ErrConflict", err)
	}
	if _, err := l.Fund("fund-1", "alice", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting fund replay: got %v, want ErrConflict", err)
	}
	if l.Len() != 2 {
		t.Fatalf("len = %d, want 2 after conflicts", l.Len())
	}
	if got := l.Balance("alice"); got != 7500 {
		t.Fatalf("alice = %d, want 7500", got)
	}
	// A new commit after reopening a clean file must append, not overwrite.
	mustTransfer(t, l, "tx-2", "alice", "bob", 500)
	if l.Len() != 3 {
		t.Fatalf("len = %d, want 3", l.Len())
	}
	if got := fileSize(t, path); got != 3*RecordSize {
		t.Fatalf("file size = %d, want %d", got, 3*RecordSize)
	}
	if _, err := Verify(path); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestInsufficientFunds: overdrawn transfers and reversals are rejected
// atomically — no partial record may appear.
func TestInsufficientFunds(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mustFund(t, l, "fund-1", "alice", 1000)

	if _, err := l.Transfer("tx-1", "alice", "bob", 1001); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("overdraw: got %v, want ErrInsufficientFunds", err)
	}
	if l.Len() != 1 {
		t.Fatalf("len = %d, want 1 (rejected transfer wrote nothing)", l.Len())
	}

	// alice -> bob 1000, bob spends it, then reversing must fail as a
	// whole because bob cannot give the money back.
	mustTransfer(t, l, "tx-2", "alice", "bob", 1000)
	mustTransfer(t, l, "tx-3", "bob", "carol", 1000)
	if _, err := l.Reverse("rev-1", 2); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("underfunded reversal: got %v, want ErrInsufficientFunds", err)
	}
	if l.Len() != 3 {
		t.Fatalf("len = %d, want 3 (rejected reversal wrote nothing)", l.Len())
	}
	if got := fileSize(t, path); got != 3*RecordSize {
		t.Fatalf("file size = %d, want %d", got, 3*RecordSize)
	}
	if _, err := Verify(path); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestSnapshotAndExport checks historical balance snapshots by seq and
// the JSON export.
func TestSnapshotAndExport(t *testing.T) {
	path := tempPath(t)
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	mustFund(t, l, "fund-1", "alice", 10000)
	mustTransfer(t, l, "tx-1", "alice", "bob", 3000)
	mustTransfer(t, l, "tx-2", "alice", "bob", 2000)

	snap, err := l.BalancesAt(1)
	if err != nil {
		t.Fatal(err)
	}
	if snap["alice"] != 10000 {
		t.Fatalf("snapshot@1 alice = %d, want 10000", snap["alice"])
	}
	snap, err = l.BalancesAt(2)
	if err != nil {
		t.Fatal(err)
	}
	if snap["alice"] != 7000 || snap["bob"] != 3000 {
		t.Fatalf("snapshot@2 = %v, want alice=7000 bob=3000", snap)
	}
	if _, err := l.BalancesAt(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot@99: got %v, want ErrNotFound", err)
	}

	var sb strings.Builder
	if err := l.Export(&sb); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(sb.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("export has %d lines, want 3", len(lines))
	}
	if !strings.Contains(lines[1], `"kind":"transfer"`) || !strings.Contains(lines[1], `"delta_a":-3000`) {
		t.Fatalf("export line 2 unexpected: %s", lines[1])
	}
}
