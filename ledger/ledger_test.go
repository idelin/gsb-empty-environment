package ledger

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func req(b byte) RequestID {
	var id RequestID
	for i := range id {
		id[i] = b
	}
	return id
}

func openTemp(t *testing.T) (*Ledger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.dat")
	l, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l, path
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

// fund deposits cents into an account from the reserved external
// account 0 (the treasury / outside world).
func fund(t *testing.T, l *Ledger, reqID RequestID, to uint64, cents int64) {
	t.Helper()
	if _, err := l.Transfer(reqID, ExternalAccount, to, cents); err != nil {
		t.Fatalf("fund: %v", err)
	}
}

func TestTransferAndBalance(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 500_00)
	fund(t, l, req(2), 2, 100_00)
	if _, err := l.Transfer(req(3), 1, 2, 123_45); err != nil {
		t.Fatal(err)
	}
	if got := l.Balance(1); got != 500_00-123_45 {
		t.Fatalf("acct1 = %d", got)
	}
	if got := l.Balance(2); got != 100_00+123_45 {
		t.Fatalf("acct2 = %d", got)
	}
}

func TestInsufficientFundsWritesNothing(t *testing.T) {
	l, path := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 50_00)
	before := fileSize(t, path)
	if _, err := l.Transfer(req(2), 1, 2, 50_01); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	if after := fileSize(t, path); after != before {
		t.Fatalf("rejected transfer grew file: %d -> %d", before, after)
	}
	if l.Balance(1) != 50_00 || l.Balance(2) != 0 {
		t.Fatal("rejected transfer moved money")
	}
}

func TestIdempotentReplayAndConflict(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 900_00)
	first, err := l.Transfer(req(9), 1, 2, 10_00)
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.Transfer(req(9), 1, 2, 10_00)
	if err != nil {
		t.Fatalf("replay with same content must succeed: %v", err)
	}
	if again != first {
		t.Fatal("replay must return the original record")
	}
	if _, err := l.Transfer(req(9), 1, 2, 11_00); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if l.Balance(2) != 10_00 {
		t.Fatal("replay must not move money twice")
	}
}

func TestDuplicateReversalRejected(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 300_00)
	tx, err := l.Transfer(req(2), 1, 2, 200_00)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := l.Reverse(req(3), tx.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if rev.ReversalOf != tx.Seq || rev.From != 2 || rev.To != 1 {
		t.Fatalf("bad reversal record: %+v", rev)
	}
	// Same request ID replayed: returns the original reversal.
	again, err := l.Reverse(req(3), tx.Seq)
	if err != nil || again != rev {
		t.Fatalf("idempotent reversal replay failed: %v", err)
	}
	// A genuinely new reversal of the same tx must fail.
	if _, err := l.Reverse(req(4), tx.Seq); !errors.Is(err, ErrAlreadyReversed) {
		t.Fatalf("want ErrAlreadyReversed, got %v", err)
	}
	if l.Balance(1) != 300_00 || l.Balance(2) != 0 {
		t.Fatal("reversal did not restore balances")
	}
	// Reversing a reversal is not allowed.
	if _, err := l.Reverse(req(5), rev.Seq); !errors.Is(err, ErrNotReversible) {
		t.Fatalf("want ErrNotReversible, got %v", err)
	}
}

func TestReversalInsufficientFundsAtomic(t *testing.T) {
	l, path := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 100_00)
	tx, _ := l.Transfer(req(2), 1, 2, 100_00)
	// Account 2 spends the money, so the reversal cannot be covered.
	if _, err := l.Transfer(req(3), 2, 3, 100_00); err != nil {
		t.Fatal(err)
	}
	before := fileSize(t, path)
	if _, err := l.Reverse(req(4), tx.Seq); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	if after := fileSize(t, path); after != before {
		t.Fatal("failed reversal must not append half a record")
	}
	if l.reversed[tx.Seq] {
		t.Fatal("failed reversal must not mark tx reversed")
	}
}

func TestCrashMidWriteDropsOnlyTail(t *testing.T) {
	l, path := openTemp(t)
	fund(t, l, req(1), 1, 400_00)
	if _, err := l.Transfer(req(2), 1, 2, 150_00); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: half-written third record at the tail.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, RecordSize/2)); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after crash: %v", err)
	}
	defer l2.Close()
	if l2.LastSeq() != 2 {
		t.Fatalf("want 2 committed records, got %d", l2.LastSeq())
	}
	if fileSize(t, path) != 2*RecordSize {
		t.Fatal("incomplete tail was not truncated")
	}
	if l2.Balance(1) != 250_00 || l2.Balance(2) != 150_00 {
		t.Fatal("balances wrong after recovery")
	}
	// Ledger must keep appending cleanly after recovery.
	if _, err := l2.Transfer(req(3), 1, 2, 50_00); err != nil {
		t.Fatal(err)
	}
	if l2.LastSeq() != 3 {
		t.Fatal("append after recovery failed")
	}
}

func TestCorruptCompleteRecordReportsPosition(t *testing.T) {
	l, path := openTemp(t)
	fund(t, l, req(1), 1, 100_00)
	l.Transfer(req(2), 1, 2, 40_00)
	l.Close()
	// Flip a byte inside the second (complete) record.
	f, _ := os.OpenFile(path, os.O_RDWR, 0o600)
	if _, err := f.WriteAt([]byte{0xFF}, RecordSize+40); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err := Open(path)
	var ce *CorruptError
	if !errors.As(err, &ce) {
		t.Fatalf("want *CorruptError, got %v", err)
	}
	if ce.Offset != RecordSize || ce.Seq != 2 {
		t.Fatalf("wrong corruption position: %+v", ce)
	}
	// The independent verifier must agree.
	if _, _, err := VerifyFile(path); !errors.As(err, &ce) {
		t.Fatalf("verifier: want *CorruptError, got %v", err)
	}
}

func TestTwoAccountsReverseDirectionConservation(t *testing.T) {
	l, path := openTemp(t)
	fund(t, l, req(1), 1, 700_00)
	fund(t, l, req(2), 2, 300_00)
	total := l.Balance(1) + l.Balance(2) + l.Balance(ExternalAccount)
	// Ping-pong transfers in both directions.
	for i := byte(10); i < 30; i++ {
		if _, err := l.Transfer(req(i), 1, 2, 10_00); err != nil {
			t.Fatal(err)
		}
		if _, err := l.Transfer(req(i+100), 2, 1, 7_00); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.Balance(1) + l.Balance(2) + l.Balance(ExternalAccount); got != total {
		t.Fatalf("conservation violated: %d != %d", got, total)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen and check persistence + independent verification agree.
	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	bal, n, err := VerifyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if int(l2.LastSeq()) != n {
		t.Fatalf("verifier counted %d records, writer has %d", n, l2.LastSeq())
	}
	for acct := range bal {
		if bal[acct] != l2.Balance(acct) {
			t.Fatalf("acct %d: verifier %d, writer %d", acct, bal[acct], l2.Balance(acct))
		}
	}
	// Idempotency survives reopen: replaying an old request returns the
	// original record and moves nothing.
	before := l2.Balance(1)
	rec, err := l2.Transfer(req(10), 1, 2, 10_00)
	if err != nil || rec.Seq != 3 {
		t.Fatalf("replay after reopen: %v seq=%d", err, rec.Seq)
	}
	if l2.Balance(1) != before {
		t.Fatal("replay after reopen moved money")
	}
}

func TestSnapshotAndExport(t *testing.T) {
	l, _ := openTemp(t)
	defer l.Close()
	fund(t, l, req(1), 1, 500_00)
	l.Transfer(req(2), 1, 2, 200_00)
	l.Transfer(req(3), 2, 1, 50_00)
	snap, err := l.Snapshot(2)
	if err != nil {
		t.Fatal(err)
	}
	if snap[1] != 300_00 || snap[2] != 200_00 {
		t.Fatalf("snapshot at seq 2 wrong: %v", snap)
	}
	var buf bytes.Buffer
	if err := l.Export(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 3*RecordSize {
		t.Fatalf("export size %d", buf.Len())
	}
	bal, n, err := Verify(&buf)
	if err != nil || n != 3 {
		t.Fatalf("verify exported bytes: %v n=%d", err, n)
	}
	if bal[1] != l.Balance(1) || bal[2] != l.Balance(2) {
		t.Fatal("exported ledger disagrees with writer")
	}
}
