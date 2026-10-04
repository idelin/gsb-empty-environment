package ledger

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ExternalAccount is the reserved account 0 representing the outside
// world (treasury). Transfers FROM it are deposits and are exempt from
// the balance check; every other account must fund its transfers.
// Conservation still holds across all accounts including account 0.
const ExternalAccount = 0

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrConflict          = errors.New("request ID replayed with different content")
	ErrAlreadyReversed   = errors.New("transaction already reversed")
	ErrNotFound          = errors.New("referenced transaction not found")
	ErrNotReversible     = errors.New("only plain transfers can be reversed")
)

// Ledger is the write side: an append-only file plus in-memory indexes
// rebuilt from the file on Open.
type Ledger struct {
	f         *os.File
	nextSeq   uint64
	lastHash  [32]byte
	balances  map[uint64]int64
	byRequest map[RequestID]Record
	bySeq     map[uint64]Record
	reversed  map[uint64]bool // seq of original -> already reversed
}

// Open opens or creates the ledger file and replays it. A trailing
// partial record (crash mid-write) is truncated; a complete record with
// a broken hash chain yields *CorruptError naming the position.
func Open(path string) (*Ledger, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	l := &Ledger{
		f:         f,
		nextSeq:   1,
		balances:  map[uint64]int64{},
		byRequest: map[RequestID]Record{},
		bySeq:     map[uint64]Record{},
		reversed:  map[uint64]bool{},
	}
	if err := l.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}

// recover scans the file, truncates an incomplete tail record, and
// rebuilds all in-memory state by applying every record.
func (l *Ledger) recover() error {
	st, err := l.f.Stat()
	if err != nil {
		return err
	}
	if rem := st.Size() % RecordSize; rem != 0 {
		// Crash mid-write: only the incomplete tail may be dropped.
		if err := l.f.Truncate(st.Size() - rem); err != nil {
			return fmt.Errorf("truncate incomplete tail: %w", err)
		}
	}
	buf := make([]byte, RecordSize)
	for off := int64(0); ; off += RecordSize {
		if _, err := l.f.ReadAt(buf, off); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		rec, err := DecodeRecord(buf)
		if err != nil {
			return &CorruptError{Offset: off, Seq: l.nextSeq}
		}
		if rec.Seq != l.nextSeq || rec.PrevHash != l.lastHash {
			return &CorruptError{Offset: off, Seq: l.nextSeq}
		}
		if err := l.apply(rec); err != nil {
			return fmt.Errorf("replay seq %d: %w", rec.Seq, err)
		}
	}
}

// apply is the writer's state transition: balances and indexes move
// exactly once per committed record.
func (l *Ledger) apply(rec Record) error {
	if rec.ReversalOf != 0 {
		if l.reversed[rec.ReversalOf] {
			return ErrAlreadyReversed
		}
		l.reversed[rec.ReversalOf] = true
	}
	l.balances[rec.From] -= rec.Amount
	l.balances[rec.To] += rec.Amount
	l.byRequest[rec.RequestID] = rec
	l.bySeq[rec.Seq] = rec
	l.lastHash = rec.Hash
	l.nextSeq = rec.Seq + 1
	return nil
}

// commit appends one record atomically: the file write+fsync either
// fully succeeds (balances then move) or the file is truncated back and
// no state changes. A transfer and its audit record are the same bytes.
func (l *Ledger) commit(rec *Record) error {
	rec.Seq = l.nextSeq
	rec.PrevHash = l.lastHash
	var buf [RecordSize]byte
	rec.Encode(buf[:])
	off, err := l.f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(buf[:]); err != nil {
		l.f.Truncate(off) // never leave half a record behind
		return fmt.Errorf("write record: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		l.f.Truncate(off)
		return fmt.Errorf("sync record: %w", err)
	}
	return l.apply(*rec)
}

// sameContent reports whether a replayed request matches the stored record.
func sameContent(a, b Record) bool {
	return a.From == b.From && a.To == b.To && a.Amount == b.Amount &&
		a.ReversalOf == b.ReversalOf
}

// Transfer moves amount cents from one account to another and commits
// the audit record atomically. Insufficient funds reject before anything
// is written. Replaying req with identical content returns the original
// record; different content returns ErrConflict.
func (l *Ledger) Transfer(req RequestID, from, to uint64, amount int64) (Record, error) {
	want := Record{RequestID: req, From: from, To: to, Amount: amount}
	if prev, ok := l.byRequest[req]; ok {
		if sameContent(prev, want) {
			return prev, nil
		}
		return Record{}, ErrConflict
	}
	if amount <= 0 {
		return Record{}, fmt.Errorf("amount must be positive cents, got %d", amount)
	}
	if from == to {
		return Record{}, errors.New("from and to accounts must differ")
	}
	if from != ExternalAccount && l.balances[from] < amount {
		return Record{}, ErrInsufficientFunds
	}
	if err := l.commit(&want); err != nil {
		return Record{}, err
	}
	return want, nil
}

// Reverse appends a compensating record for the committed transfer
// origSeq, moving the money back. Each transaction may be reversed at
// most once; the original record is never deleted. If the funds are no
// longer available the reversal is rejected as a whole — nothing is
// written. Reversals are idempotent on req like transfers.
func (l *Ledger) Reverse(req RequestID, origSeq uint64) (Record, error) {
	orig, ok := l.bySeq[origSeq]
	if !ok {
		return Record{}, ErrNotFound
	}
	if orig.ReversalOf != 0 {
		return Record{}, ErrNotReversible
	}
	want := Record{RequestID: req, From: orig.To, To: orig.From,
		Amount: orig.Amount, ReversalOf: origSeq}
	if prev, ok := l.byRequest[req]; ok {
		if sameContent(prev, want) {
			return prev, nil
		}
		return Record{}, ErrConflict
	}
	if l.reversed[origSeq] {
		return Record{}, ErrAlreadyReversed
	}
	if l.balances[orig.To] < orig.Amount {
		return Record{}, ErrInsufficientFunds
	}
	if err := l.commit(&want); err != nil {
		return Record{}, err
	}
	return want, nil
}

// Balance returns the current balance of acct in cents.
func (l *Ledger) Balance(acct uint64) int64 { return l.balances[acct] }

// Snapshot replays committed records up to and including seq and returns
// the balances at that point in history.
func (l *Ledger) Snapshot(seq uint64) (map[uint64]int64, error) {
	if seq >= l.nextSeq {
		return nil, fmt.Errorf("seq %d not committed yet", seq)
	}
	bal := map[uint64]int64{}
	for i := uint64(1); i <= seq; i++ {
		rec, ok := l.bySeq[i]
		if !ok {
			return nil, fmt.Errorf("seq %d missing from chain", i)
		}
		bal[rec.From] -= rec.Amount
		bal[rec.To] += rec.Amount
	}
	return bal, nil
}

// Export writes the raw ledger bytes (the authoritative, hash-chained
// form) to w.
func (l *Ledger) Export(w io.Writer) error {
	if _, err := l.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.Copy(w, l.f)
	return err
}

// LastSeq returns the sequence number of the newest committed record
// (0 when the ledger is empty).
func (l *Ledger) LastSeq() uint64 { return l.nextSeq - 1 }

// Record returns the committed record with the given sequence number.
func (l *Ledger) Record(seq uint64) (Record, bool) {
	rec, ok := l.bySeq[seq]
	return rec, ok
}

// Close flushes and closes the ledger file.
func (l *Ledger) Close() error {
	if err := l.f.Sync(); err != nil {
		return err
	}
	return l.f.Close()
}
