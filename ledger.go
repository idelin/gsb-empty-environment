package ledger

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Sentinel errors returned by Ledger operations.
var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrConflict          = errors.New("request ID replayed with different content")
	ErrAlreadyReversed   = errors.New("transaction already reversed")
	ErrNotFound          = errors.New("referenced transaction not found")
	ErrNotTransfer       = errors.New("referenced record is not a transfer")
	ErrBadRequest        = errors.New("invalid request")
)

// CorruptError reports a complete record whose digest or chain link is
// broken. Offset is the byte offset of the bad record in the file and
// Seq is the sequence number expected at that position.
type CorruptError struct {
	Offset int64
	Seq    uint64
	Reason string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("ledger corrupt at offset %d (record seq %d): %s", e.Offset, e.Seq, e.Reason)
}

// reqEntry remembers a committed request for idempotent replay.
type reqEntry struct {
	key string // canonical content key
	seq uint64
}

// Ledger is an append-only ledger backed by a single file. Each commit
// is one fixed-size record written with a single write followed by
// fsync, so a crash can only leave a partial tail record.
type Ledger struct {
	f          *os.File
	path       string
	n          uint64 // number of committed records
	bal        map[string]int64
	byReq      map[[ReqIDLen]byte]reqEntry
	reversed   map[uint64]bool
	lastDigest [DigestLen]byte
	closed     bool
}

// scanStats describes what a file scan found.
type scanStats struct {
	good      []*Record // verified prefix records
	tailBytes int64     // trailing partial-record bytes
	corrupt   *CorruptError
}

// scanFile reads and verifies complete records. A partial tail is not
// an error; a corrupt complete record is reported with its position.
func scanFile(f *os.File) (*scanStats, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	stt := &scanStats{tailBytes: size % RecordSize}
	complete := size / RecordSize
	buf := make([]byte, RecordSize)
	var prev [DigestLen]byte
	for i := int64(0); i < complete; i++ {
		if _, err := f.ReadAt(buf, i*RecordSize); err != nil {
			return nil, err
		}
		rec, err := UnmarshalRecord(buf)
		if err != nil {
			stt.corrupt = &CorruptError{Offset: i * RecordSize, Seq: uint64(i) + 1, Reason: err.Error()}
			break
		}
		if rec.Seq != uint64(i)+1 {
			stt.corrupt = &CorruptError{Offset: i * RecordSize, Seq: uint64(i) + 1,
				Reason: fmt.Sprintf("out-of-order seq %d", rec.Seq)}
			break
		}
		if rec.PrevDigest != prev {
			stt.corrupt = &CorruptError{Offset: i * RecordSize, Seq: uint64(i) + 1,
				Reason: "previous-record digest does not match chain"}
			break
		}
		stt.good = append(stt.good, rec)
		prev = rec.Digest
	}
	return stt, nil
}

func truncate(f *os.File, size int64) error {
	if err := f.Truncate(size); err != nil {
		return err
	}
	_, err := f.Seek(size, io.SeekStart)
	return err
}

// Open opens or creates the ledger file. A partial tail record left by
// an interrupted write is discarded. If a complete record is corrupt,
// Open reports its position via *CorruptError and no ledger is
// returned; use Recover to keep the valid prefix instead.
func Open(path string) (*Ledger, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	stt, err := scanFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if stt.corrupt != nil {
		f.Close()
		return nil, stt.corrupt
	}
	// Position the write offset at the end of the valid prefix,
	// discarding any partial tail left by an interrupted write.
	if err := truncate(f, int64(len(stt.good))*RecordSize); err != nil {
		f.Close()
		return nil, err
	}
	l := newLedger(f, path)
	for _, rec := range stt.good {
		l.replay(rec)
	}
	return l, nil
}

// Recover opens the ledger keeping only the valid prefix: a partial
// tail and everything from the first corrupt complete record onward is
// discarded. The returned report describes what was dropped.
func Recover(path string) (*Ledger, *RecoverReport, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, err
	}
	stt, err := scanFile(f)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	rep := &RecoverReport{TailBytesDropped: stt.tailBytes}
	keep := int64(len(stt.good)) * RecordSize
	if stt.corrupt != nil {
		rep.Corrupt = stt.corrupt
		rep.RecordsDropped = stt.corrupt.Seq
	}
	if err := truncate(f, keep); err != nil {
		f.Close()
		return nil, nil, err
	}
	l := newLedger(f, path)
	for _, rec := range stt.good {
		l.replay(rec)
	}
	return l, rep, nil
}

// RecoverReport describes what Recover discarded.
type RecoverReport struct {
	TailBytesDropped int64         // partial tail bytes removed
	Corrupt          *CorruptError // first corrupt complete record, if any
	RecordsDropped   uint64        // seq of first dropped complete record
}

func newLedger(f *os.File, path string) *Ledger {
	return &Ledger{
		f:        f,
		path:     path,
		bal:      make(map[string]int64),
		byReq:    make(map[[ReqIDLen]byte]reqEntry),
		reversed: make(map[uint64]bool),
	}
}

// replay folds a committed record into in-memory state. It is the only
// state-update path used by both Open and commit.
func (l *Ledger) replay(rec *Record) {
	l.n = rec.Seq
	l.lastDigest = rec.Digest
	l.bal[TrimString(rec.AcctA[:])] += rec.DeltaA
	if rec.AcctB != zeroAcct {
		l.bal[TrimString(rec.AcctB[:])] += rec.DeltaB
	}
	l.byReq[rec.ReqID] = reqEntry{key: contentKey(rec), seq: rec.Seq}
	if rec.Kind == KindReverse {
		l.reversed[rec.RefSeq] = true
	}
}

// contentKey canonically encodes the semantic content of a request so
// replays can be compared byte-for-byte.
func contentKey(r *Record) string {
	h := r.header()[8:]                 // skip seq; identical requests differ only in seq
	return string(h[:len(h)-DigestLen]) // skip prevDigest
}

// Close flushes and closes the ledger file.
func (l *Ledger) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	return l.f.Close()
}

// Len returns the number of committed records.
func (l *Ledger) Len() uint64 { return l.n }

// Balance returns the current balance of acct in cents.
func (l *Ledger) Balance(acct string) int64 { return l.bal[acct] }

// checkReplay implements idempotency: same request ID and same content
// returns the original result; same ID with different content is a
// conflict. handled=true means the caller must return (seq, err) as-is.
func (l *Ledger) checkReplay(reqID [ReqIDLen]byte, rec *Record) (seq uint64, err error, handled bool) {
	if prev, ok := l.byReq[reqID]; ok {
		if prev.key == contentKey(rec) {
			return prev.seq, nil, true
		}
		return 0, ErrConflict, true
	}
	return 0, nil, false
}

// commit writes the record atomically and only then updates state.
func (l *Ledger) commit(rec *Record) (uint64, error) {
	rec.Seq = l.n + 1
	rec.PrevDigest = l.lastDigest
	data := rec.Marshal()
	if _, err := l.f.Write(data); err != nil {
		return 0, fmt.Errorf("commit write: %w", err)
	}
	if err := l.f.Sync(); err != nil {
		return 0, fmt.Errorf("commit sync: %w", err)
	}
	l.replay(rec)
	return rec.Seq, nil
}

// Fund credits acct with amount cents (external money enters the
// system). amount must be positive.
func (l *Ledger) Fund(reqID, acct string, amount int64) (uint64, error) {
	rid, err := padReqID(reqID)
	if err != nil {
		return 0, err
	}
	a, err := padAcct(acct)
	if err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, fmt.Errorf("%w: fund amount must be positive", ErrBadRequest)
	}
	rec := &Record{Kind: KindFund, ReqID: rid, AcctA: a, DeltaA: amount}
	if seq, err, done := l.checkReplay(rid, rec); done {
		return seq, err
	}
	return l.commit(rec)
}

// Transfer moves amount cents from one account to another atomically:
// the balance check, the audit record and both balance changes are one
// commit. Insufficient funds reject the whole transfer.
func (l *Ledger) Transfer(reqID, from, to string, amount int64) (uint64, error) {
	rid, err := padReqID(reqID)
	if err != nil {
		return 0, err
	}
	fa, err := padAcct(from)
	if err != nil {
		return 0, err
	}
	ta, err := padAcct(to)
	if err != nil {
		return 0, err
	}
	if amount <= 0 {
		return 0, fmt.Errorf("%w: transfer amount must be positive", ErrBadRequest)
	}
	if from == to {
		return 0, fmt.Errorf("%w: from and to must differ", ErrBadRequest)
	}
	rec := &Record{Kind: KindTransfer, ReqID: rid, AcctA: fa, DeltaA: -amount, AcctB: ta, DeltaB: amount}
	if seq, err, done := l.checkReplay(rid, rec); done {
		return seq, err
	}
	if l.bal[from] < amount {
		return 0, ErrInsufficientFunds
	}
	return l.commit(rec)
}

// Reverse appends a new record that fully undoes the transfer at
// refSeq. The original record is never modified or deleted. Each
// transfer can be reversed at most once. If undoing it would drive any
// account negative, the reversal is rejected as a whole; nothing is
// written.
func (l *Ledger) Reverse(reqID string, refSeq uint64) (uint64, error) {
	rid, err := padReqID(reqID)
	if err != nil {
		return 0, err
	}
	if refSeq == 0 || refSeq > l.n {
		return 0, ErrNotFound
	}
	orig, err := l.recordAt(refSeq)
	if err != nil {
		return 0, err
	}
	if orig.Kind != KindTransfer {
		return 0, ErrNotTransfer
	}
	rec := &Record{
		Kind:   KindReverse,
		ReqID:  rid,
		RefSeq: refSeq,
		AcctA:  orig.AcctA,
		DeltaA: -orig.DeltaA,
		AcctB:  orig.AcctB,
		DeltaB: -orig.DeltaB,
	}
	if seq, err, done := l.checkReplay(rid, rec); done {
		return seq, err
	}
	if l.reversed[refSeq] {
		return 0, ErrAlreadyReversed
	}
	// All-or-nothing check before anything is written.
	if l.bal[TrimString(orig.AcctA[:])]+rec.DeltaA < 0 ||
		(orig.AcctB != zeroAcct && l.bal[TrimString(orig.AcctB[:])]+rec.DeltaB < 0) {
		return 0, ErrInsufficientFunds
	}
	return l.commit(rec)
}

// recordAt reads a committed record from the file by sequence number.
func (l *Ledger) recordAt(seq uint64) (*Record, error) {
	if seq == 0 || seq > l.n {
		return nil, ErrNotFound
	}
	buf := make([]byte, RecordSize)
	if _, err := l.f.ReadAt(buf, int64(seq-1)*RecordSize); err != nil {
		return nil, err
	}
	return UnmarshalRecord(buf)
}

// BalancesAt replays records 1..seq and returns the balance snapshot
// as of that sequence number.
func (l *Ledger) BalancesAt(seq uint64) (map[string]int64, error) {
	if seq > l.n {
		return nil, fmt.Errorf("%w: seq %d beyond ledger length %d", ErrNotFound, seq, l.n)
	}
	out := make(map[string]int64)
	for i := uint64(1); i <= seq; i++ {
		rec, err := l.recordAt(i)
		if err != nil {
			return nil, err
		}
		out[TrimString(rec.AcctA[:])] += rec.DeltaA
		if rec.AcctB != zeroAcct {
			out[TrimString(rec.AcctB[:])] += rec.DeltaB
		}
	}
	return out, nil
}

// ExportedRecord is the portable form of one record.
type ExportedRecord struct {
	Seq        uint64 `json:"seq"`
	Kind       string `json:"kind"`
	ReqID      string `json:"req_id"`
	RefSeq     uint64 `json:"ref_seq,omitempty"`
	AcctA      string `json:"acct_a"`
	DeltaA     int64  `json:"delta_a"`
	AcctB      string `json:"acct_b,omitempty"`
	DeltaB     int64  `json:"delta_b,omitempty"`
	PrevDigest string `json:"prev_digest"`
	Digest     string `json:"digest"`
}

// Export writes the whole ledger to w as JSON, one record per line.
func (l *Ledger) Export(w io.Writer) error {
	enc := json.NewEncoder(w)
	for i := uint64(1); i <= l.n; i++ {
		rec, err := l.recordAt(i)
		if err != nil {
			return err
		}
		er := ExportedRecord{
			Seq:        rec.Seq,
			Kind:       rec.Kind.String(),
			ReqID:      TrimString(rec.ReqID[:]),
			RefSeq:     rec.RefSeq,
			AcctA:      TrimString(rec.AcctA[:]),
			DeltaA:     rec.DeltaA,
			PrevDigest: hex.EncodeToString(rec.PrevDigest[:]),
			Digest:     hex.EncodeToString(rec.Digest[:]),
		}
		if rec.AcctB != zeroAcct {
			er.AcctB = TrimString(rec.AcctB[:])
			er.DeltaB = rec.DeltaB
		}
		if err := enc.Encode(er); err != nil {
			return err
		}
	}
	return nil
}
