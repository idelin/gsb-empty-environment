// Package ledger implements a double-entry ledger that records balanced
// vouchers in integer cents. Vouchers are submitted in atomic batches,
// reversals reference the original voucher and must negate it line by
// line, submissions are idempotent by request ID, and reversals that
// arrive before their original are parked until the original commits.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// Money is an amount in integer cents. Positive is a debit, negative a
// credit.
type Money int64

// Line is a single posting against an account.
type Line struct {
	Account string
	Amount  Money
}

// Voucher is a balanced set of lines. If Reverses is non-empty the
// voucher is a reversal of the referenced voucher and its lines must be
// the exact line-by-line negation of the original.
type Voucher struct {
	ID       string
	Reverses string
	Lines    []Line
}

// Batch is a group of vouchers committed atomically: either every
// voucher in the batch is recorded or none is.
type Batch struct {
	RequestID string
	Vouchers  []Voucher
}

// Rejection describes a parked reversal that was refused when its
// original voucher finally committed.
type Rejection struct {
	VoucherID string
	Reason    string
}

// Result reports the outcome of a committed batch.
type Result struct {
	Applied  []string    // voucher IDs recorded by this batch
	Pending  []string    // reversal voucher IDs parked awaiting their original
	Rejected []Rejection // parked reversals refused when their original arrived
}

var (
	ErrEmptyRequestID   = errors.New("ledger: request ID must not be empty")
	ErrRequestConflict  = errors.New("ledger: request ID reused with different content")
	ErrEmptyBatch       = errors.New("ledger: batch must contain at least one voucher")
	ErrEmptyVoucherID   = errors.New("ledger: voucher ID must not be empty")
	ErrDuplicateVoucher = errors.New("ledger: voucher ID already recorded")
	ErrEmptyLines       = errors.New("ledger: voucher must contain at least one line")
	ErrEmptyAccount     = errors.New("ledger: line account must not be empty")
	ErrZeroAmount       = errors.New("ledger: line amount must not be zero")
	ErrUnbalanced       = errors.New("ledger: voucher lines do not sum to zero")
	ErrSelfReversal     = errors.New("ledger: voucher cannot reverse itself")
	ErrUnknownReversal  = errors.New("ledger: reversal target is a reversal")
	ErrDoubleReversal   = errors.New("ledger: voucher already reversed")
	ErrReversalMismatch = errors.New("ledger: reversal lines are not the negation of the original")
)

type voucherState struct {
	voucher    Voucher
	isReversal bool
	reversed   bool
}

type requestRecord struct {
	digest string
	result Result
}

// Ledger is a thread-safe double-entry ledger.
type Ledger struct {
	mu       sync.Mutex
	requests map[string]requestRecord
	vouchers map[string]*voucherState
	pending  map[string][]Voucher // original voucher ID -> parked reversals, FIFO
	balances map[string]Money
	log      []Batch // committed batches, in order, for replay
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{
		requests: make(map[string]requestRecord),
		vouchers: make(map[string]*voucherState),
		pending:  make(map[string][]Voucher),
		balances: make(map[string]Money),
	}
}

// Submit validates and commits a batch atomically. Submitting the same
// request ID with identical content returns the original result without
// re-recording; the same request ID with different content is rejected.
// Only successfully committed batches reserve their request ID.
func (l *Ledger) Submit(b Batch) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if b.RequestID == "" {
		return Result{}, ErrEmptyRequestID
	}
	digest := hashBatch(b)
	if rec, ok := l.requests[b.RequestID]; ok {
		if rec.digest != digest {
			return Result{}, ErrRequestConflict
		}
		return rec.result, nil
	}
	res, err := l.apply(b)
	if err != nil {
		return Result{}, err
	}
	l.requests[b.RequestID] = requestRecord{digest: digest, result: res}
	l.log = append(l.log, b)
	return res, nil
}

// apply validates and commits a batch. It mutates the ledger only when
// the whole batch is valid.
func (l *Ledger) apply(b Batch) (Result, error) {
	if len(b.Vouchers) == 0 {
		return Result{}, ErrEmptyBatch
	}
	byID := make(map[string]Voucher, len(b.Vouchers))
	for _, v := range b.Vouchers {
		if v.ID == "" {
			return Result{}, ErrEmptyVoucherID
		}
		if _, dup := byID[v.ID]; dup {
			return Result{}, fmt.Errorf("%v: %s", ErrDuplicateVoucher, v.ID)
		}
		if _, exists := l.vouchers[v.ID]; exists {
			return Result{}, fmt.Errorf("%v: %s", ErrDuplicateVoucher, v.ID)
		}
		if err := validateBalanced(v); err != nil {
			return Result{}, fmt.Errorf("voucher %s: %v", v.ID, err)
		}
		byID[v.ID] = v
	}

	// Validate every voucher before touching any state so the batch is
	// all-or-nothing. Reversals whose original is not yet known are
	// structurally valid and get parked instead of applied.
	type decision struct {
		v       Voucher
		pending bool
	}
	decisions := make([]decision, 0, len(b.Vouchers))
	reversedInBatch := make(map[string]bool)
	for _, v := range b.Vouchers {
		if v.Reverses == "" {
			decisions = append(decisions, decision{v: v})
			continue
		}
		if v.Reverses == v.ID {
			return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrSelfReversal)
		}
		if orig, ok := byID[v.Reverses]; ok {
			// Original travels in the same batch.
			if orig.Reverses != "" {
				return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrUnknownReversal)
			}
			if reversedInBatch[v.Reverses] {
				return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrDoubleReversal)
			}
			if !linesOpposite(orig.Lines, v.Lines) {
				return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrReversalMismatch)
			}
			reversedInBatch[v.Reverses] = true
			decisions = append(decisions, decision{v: v})
			continue
		}
		st, ok := l.vouchers[v.Reverses]
		if !ok {
			// Original not seen yet: park the reversal.
			decisions = append(decisions, decision{v: v, pending: true})
			continue
		}
		if st.isReversal {
			return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrUnknownReversal)
		}
		if st.reversed {
			return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrDoubleReversal)
		}
		if !linesOpposite(st.voucher.Lines, v.Lines) {
			return Result{}, fmt.Errorf("voucher %s: %v", v.ID, ErrReversalMismatch)
		}
		decisions = append(decisions, decision{v: v})
	}

	res := Result{}
	var committed []string
	for _, d := range decisions {
		st := &voucherState{voucher: d.v, isReversal: d.v.Reverses != ""}
		l.vouchers[d.v.ID] = st
		if d.pending {
			l.pending[d.v.Reverses] = append(l.pending[d.v.Reverses], d.v)
			res.Pending = append(res.Pending, d.v.ID)
			continue
		}
		l.post(d.v)
		res.Applied = append(res.Applied, d.v.ID)
		committed = append(committed, d.v.ID)
	}

	// Newly committed vouchers may release parked reversals. A parked
	// reversal that proves invalid against its original is refused on its
	// own; the original's batch still stands.
	for _, id := range committed {
		queue := l.pending[id]
		if len(queue) == 0 {
			continue
		}
		delete(l.pending, id)
		st := l.vouchers[id]
		for _, r := range queue {
			if reason := checkReversal(st, r); reason != "" {
				res.Rejected = append(res.Rejected, Rejection{VoucherID: r.ID, Reason: reason})
				continue
			}
			l.vouchers[r.ID] = &voucherState{voucher: r, isReversal: true}
			l.post(r)
			res.Applied = append(res.Applied, r.ID)
			// The released reversal is itself a committed voucher now;
			// anything parked on it can only be an attempt to reverse a
			// reversal, which is refused.
			if more := l.pending[r.ID]; len(more) > 0 {
				delete(l.pending, r.ID)
				for _, rr := range more {
					res.Rejected = append(res.Rejected, Rejection{VoucherID: rr.ID, Reason: ErrUnknownReversal.Error()})
				}
			}
		}
	}
	return res, nil
}

// post applies a voucher's lines to the balances and marks its reversal
// target as reversed.
func (l *Ledger) post(v Voucher) {
	for _, ln := range v.Lines {
		l.balances[ln.Account] += ln.Amount
	}
	if v.Reverses != "" {
		if st, ok := l.vouchers[v.Reverses]; ok {
			st.reversed = true
		}
	}
}

// checkReversal returns an empty string if r is a valid reversal of the
// committed voucher st, otherwise the refusal reason.
func checkReversal(st *voucherState, r Voucher) string {
	if st.isReversal {
		return ErrUnknownReversal.Error()
	}
	if st.reversed {
		return ErrDoubleReversal.Error()
	}
	if !linesOpposite(st.voucher.Lines, r.Lines) {
		return ErrReversalMismatch.Error()
	}
	return ""
}

func validateBalanced(v Voucher) error {
	if len(v.Lines) == 0 {
		return ErrEmptyLines
	}
	var sum Money
	for _, ln := range v.Lines {
		if ln.Account == "" {
			return ErrEmptyAccount
		}
		if ln.Amount == 0 {
			return ErrZeroAmount
		}
		sum += ln.Amount
	}
	if sum != 0 {
		return ErrUnbalanced
	}
	return nil
}

// linesOpposite reports whether b is the line-by-line negation of a:
// same accounts, same count, each amount exactly negated.
func linesOpposite(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	as := sortedLines(a)
	bs := sortedLines(b)
	for i := range as {
		if as[i].Account != bs[i].Account || as[i].Amount != -bs[i].Amount {
			return false
		}
	}
	return true
}

func sortedLines(lines []Line) []Line {
	out := make([]Line, len(lines))
	copy(out, lines)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Account != out[j].Account {
			return out[i].Account < out[j].Account
		}
		return out[i].Amount < out[j].Amount
	})
	return out
}

// Balance returns the current balance of an account in cents.
func (l *Ledger) Balance(account string) Money {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balances[account]
}

// Balances returns a copy of all account balances.
func (l *Ledger) Balances() map[string]Money {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]Money, len(l.balances))
	for acc, amt := range l.balances {
		out[acc] = amt
	}
	return out
}

// Reversed reports whether the voucher has been reversed.
func (l *Ledger) Reversed(voucherID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.vouchers[voucherID]
	return ok && st.reversed
}

// Pending returns the number of reversals parked awaiting their original.
func (l *Ledger) Pending() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, q := range l.pending {
		n += len(q)
	}
	return n
}

// Verify checks the ledger's invariants: balance conservation (all
// account balances sum to zero) and replay consistency (replaying the
// committed batch log from scratch reproduces the exact live state).
func (l *Ledger) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var total Money
	for _, amt := range l.balances {
		total += amt
	}
	if total != 0 {
		return fmt.Errorf("ledger: conservation violated, balances sum to %d", total)
	}

	fresh := New()
	for _, b := range l.log {
		if _, err := fresh.apply(b); err != nil {
			return fmt.Errorf("ledger: replay of batch %s failed: %v", b.RequestID, err)
		}
	}
	if len(fresh.balances) != len(l.balances) {
		return fmt.Errorf("ledger: replay balance mismatch")
	}
	for acc, amt := range l.balances {
		if fresh.balances[acc] != amt {
			return fmt.Errorf("ledger: replay balance mismatch on %s: live %d, replayed %d", acc, amt, fresh.balances[acc])
		}
	}
	if len(fresh.vouchers) != len(l.vouchers) {
		return fmt.Errorf("ledger: replay voucher mismatch")
	}
	for id, st := range l.vouchers {
		rst, ok := fresh.vouchers[id]
		if !ok || rst.reversed != st.reversed || rst.isReversal != st.isReversal {
			return fmt.Errorf("ledger: replay state mismatch on voucher %s", id)
		}
	}
	if len(fresh.pending) != len(l.pending) {
		return fmt.Errorf("ledger: replay pending mismatch")
	}
	for id, q := range l.pending {
		if len(fresh.pending[id]) != len(q) {
			return fmt.Errorf("ledger: replay pending mismatch on voucher %s", id)
		}
	}
	return nil
}

// hashBatch produces a canonical digest of a batch's content for
// idempotency checks.
func hashBatch(b Batch) string {
	h := sha256.New()
	h.Write([]byte(strconv.Itoa(len(b.Vouchers))))
	for _, v := range b.Vouchers {
		h.Write([]byte{0})
		h.Write([]byte(v.ID))
		h.Write([]byte{1})
		h.Write([]byte(v.Reverses))
		h.Write([]byte{2})
		h.Write([]byte(strconv.Itoa(len(v.Lines))))
		for _, ln := range v.Lines {
			h.Write([]byte{3})
			h.Write([]byte(ln.Account))
			h.Write([]byte{4})
			h.Write([]byte(strconv.FormatInt(int64(ln.Amount), 10)))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
