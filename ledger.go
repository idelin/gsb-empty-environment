// Package ledger implements a double-entry ledger with integer-cent
// amounts, idempotent batch submission, atomic all-or-nothing batches,
// and out-of-order reversal support.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

var (
	// ErrUnbalanced is returned when a voucher's debits do not equal its credits.
	ErrUnbalanced = errors.New("ledger: voucher debits do not equal credits")
	// ErrConflict is returned when an id (batch or voucher) is resubmitted
	// with different content.
	ErrConflict = errors.New("ledger: duplicate id with different content")
	// ErrAlreadyReversed is returned when a voucher is reversed more than once.
	ErrAlreadyReversed = errors.New("ledger: voucher already reversed")
	// ErrReversalMismatch is returned when a reversal does not mirror the
	// original voucher line by line.
	ErrReversalMismatch = errors.New("ledger: reversal does not mirror original line by line")
	// ErrReversalOfReversal is returned when reversing a reversal voucher.
	ErrReversalOfReversal = errors.New("ledger: cannot reverse a reversal voucher")
	// ErrInvalidEntry is returned for malformed entries or vouchers.
	ErrInvalidEntry = errors.New("ledger: invalid entry")
)

// Entry is a single debit or credit line, in integer cents.
// Exactly one of Debit/Credit must be positive; the other must be zero.
type Entry struct {
	Account string
	Debit   int64
	Credit  int64
}

// Voucher is a balanced set of entries. A voucher with ReversalOf set is a
// reversal of the referenced voucher and must mirror it line by line with
// debit/credit swapped.
type Voucher struct {
	ID         string
	Memo       string
	Entries    []Entry
	ReversalOf string
}

// Batch is an atomic group of vouchers: either all are applied or none.
type Batch struct {
	ID       string
	Vouchers []Voucher
}

// Ledger keeps committed vouchers, account balances (debit-positive cents),
// pending out-of-order reversals, and a journal for replay verification.
type Ledger struct {
	vouchers map[string]*Voucher // committed vouchers by id
	known    map[string]string   // voucher id -> content fingerprint (committed, pending, rejected)
	batches  map[string]string   // batch id -> content fingerprint
	balances map[string]int64
	reversed map[string]bool
	pending  map[string][]*Voucher // original voucher id -> unsettled reversals
	rejected map[string]bool       // reversal voucher ids rejected at settle time
	journal  []Batch
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{
		vouchers: make(map[string]*Voucher),
		known:    make(map[string]string),
		batches:  make(map[string]string),
		balances: make(map[string]int64),
		reversed: make(map[string]bool),
		pending:  make(map[string][]*Voucher),
		rejected: make(map[string]bool),
	}
}

// Balance returns the debit-positive balance of an account in cents.
func (l *Ledger) Balance(account string) int64 { return l.balances[account] }

// Reversed reports whether the voucher has been reversed.
func (l *Ledger) Reversed(voucherID string) bool { return l.reversed[voucherID] }

// PendingReversals returns the number of unsettled reversals waiting on the
// given original voucher id.
func (l *Ledger) PendingReversals(originalID string) int { return len(l.pending[originalID]) }

// Rejected reports whether a reversal voucher was rejected when its original
// arrived and it failed validation.
func (l *Ledger) Rejected(voucherID string) bool { return l.rejected[voucherID] }

// Submit applies a batch atomically. Resubmitting the same batch id with
// identical content is a no-op; with different content it returns ErrConflict.
func (l *Ledger) Submit(b Batch) error {
	if b.ID == "" {
		return fmt.Errorf("ledger: empty batch id: %v", ErrInvalidEntry)
	}
	fp := fingerprintBatch(b)
	if prev, ok := l.batches[b.ID]; ok {
		if prev == fp {
			return nil // idempotent duplicate request
		}
		return fmt.Errorf("batch %q: %v", b.ID, ErrConflict)
	}

	work := l.clone()
	for i := range b.Vouchers {
		if err := work.applyVoucher(&b.Vouchers[i]); err != nil {
			return fmt.Errorf("batch %q: %v", b.ID, err) // nothing committed
		}
	}
	work.batches[b.ID] = fp
	work.journal = append(work.journal, b)
	*l = *work
	return nil
}

// applyVoucher validates and stages a single voucher on the working copy.
func (l *Ledger) applyVoucher(v *Voucher) error {
	if v.ID == "" || len(v.Entries) == 0 {
		return fmt.Errorf("voucher %q: %v", v.ID, ErrInvalidEntry)
	}
	fp := fingerprintVoucher(*v)
	if prev, ok := l.known[v.ID]; ok {
		if prev == fp {
			return nil // idempotent duplicate
		}
		return fmt.Errorf("voucher %q: %v", v.ID, ErrConflict)
	}
	if err := validateShape(v); err != nil {
		return err
	}
	l.known[v.ID] = fp

	if v.ReversalOf == "" {
		l.commit(v)
		return nil
	}
	return l.applyReversal(v, fp)
}

func (l *Ledger) applyReversal(v *Voucher, fp string) error {
	if v.ReversalOf == v.ID {
		return fmt.Errorf("voucher %q: %v", v.ID, ErrInvalidEntry)
	}
	if l.reversed[v.ReversalOf] || len(l.pending[v.ReversalOf]) > 0 {
		return fmt.Errorf("voucher %q reverses %q: %v", v.ID, v.ReversalOf, ErrAlreadyReversed)
	}
	orig, ok := l.vouchers[v.ReversalOf]
	if !ok {
		// Original not posted yet: hold the reversal as pending.
		l.pending[v.ReversalOf] = append(l.pending[v.ReversalOf], v)
		return nil
	}
	if err := checkReversible(orig, v); err != nil {
		return err
	}
	l.reversed[v.ReversalOf] = true
	l.commit(v)
	return nil
}

// commit records a voucher, updates balances, and settles any pending
// reversals that were waiting for it.
func (l *Ledger) commit(v *Voucher) {
	l.vouchers[v.ID] = v
	for _, e := range v.Entries {
		l.balances[e.Account] += e.Debit - e.Credit
	}
	waiting := l.pending[v.ID]
	delete(l.pending, v.ID)
	for _, r := range waiting {
		if err := checkReversible(v, r); err != nil {
			// The reversal batch was accepted as pending earlier; now that the
			// original exists it fails validation, so it is rejected, not applied.
			l.rejected[r.ID] = true
			continue
		}
		l.reversed[v.ID] = true
		l.commit(r)
	}
}

func checkReversible(orig, reversal *Voucher) error {
	if orig.ReversalOf != "" {
		return fmt.Errorf("voucher %q: %v", reversal.ID, ErrReversalOfReversal)
	}
	if !mirrors(orig, reversal) {
		return fmt.Errorf("voucher %q: %v", reversal.ID, ErrReversalMismatch)
	}
	return nil
}

// mirrors reports whether r is the exact line-by-line opposite of o.
func mirrors(o, r *Voucher) bool {
	if len(o.Entries) != len(r.Entries) {
		return false
	}
	for i := range o.Entries {
		oe, re := o.Entries[i], r.Entries[i]
		if oe.Account != re.Account || oe.Debit != re.Credit || oe.Credit != re.Debit {
			return false
		}
	}
	return true
}

func validateShape(v *Voucher) error {
	var debit, credit int64
	for _, e := range v.Entries {
		if e.Account == "" || e.Debit < 0 || e.Credit < 0 ||
			(e.Debit == 0 && e.Credit == 0) || (e.Debit > 0 && e.Credit > 0) {
			return fmt.Errorf("voucher %q: %v", v.ID, ErrInvalidEntry)
		}
		debit += e.Debit
		credit += e.Credit
	}
	if debit != credit {
		return fmt.Errorf("voucher %q: %v", v.ID, ErrUnbalanced)
	}
	return nil
}

// Conservation verifies global balance conservation: every committed voucher
// is balanced and all account balances sum to zero.
func (l *Ledger) Conservation() error {
	for _, v := range l.vouchers {
		if err := validateShape(v); err != nil {
			return err
		}
	}
	var total int64
	for _, b := range l.balances {
		total += b
	}
	if total != 0 {
		return fmt.Errorf("ledger: balances sum to %d, want 0", total)
	}
	return nil
}

// VerifyReplay rebuilds a ledger from the journal and checks that the
// replayed state matches the current state, proving cross-batch
// dependencies (including pending reversals) resolve deterministically.
func (l *Ledger) VerifyReplay() error {
	r := New()
	for _, b := range l.journal {
		if err := r.Submit(b); err != nil {
			return fmt.Errorf("replay batch %q: %v", b.ID, err)
		}
	}
	if err := r.Conservation(); err != nil {
		return fmt.Errorf("replay: %v", err)
	}
	if !equalBalances(l.balances, r.balances) {
		return errors.New("ledger: replayed balances differ from current state")
	}
	if len(l.vouchers) != len(r.vouchers) || len(l.reversed) != len(r.reversed) ||
		len(l.pending) != len(r.pending) || len(l.rejected) != len(r.rejected) {
		return errors.New("ledger: replayed state differs from current state")
	}
	for id := range l.vouchers {
		if _, ok := r.vouchers[id]; !ok {
			return fmt.Errorf("ledger: replay missing voucher %q", id)
		}
	}
	for id := range l.reversed {
		if !r.reversed[id] {
			return fmt.Errorf("ledger: replay missing reversal of %q", id)
		}
	}
	return nil
}

func equalBalances(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (l *Ledger) clone() *Ledger {
	c := New()
	for k, v := range l.vouchers {
		c.vouchers[k] = v
	}
	for k, v := range l.known {
		c.known[k] = v
	}
	for k, v := range l.batches {
		c.batches[k] = v
	}
	for k, v := range l.balances {
		c.balances[k] = v
	}
	for k, v := range l.reversed {
		c.reversed[k] = v
	}
	for k, v := range l.pending {
		cp := make([]*Voucher, len(v))
		copy(cp, v)
		c.pending[k] = cp
	}
	for k, v := range l.rejected {
		c.rejected[k] = v
	}
	c.journal = l.journal
	return c
}

func fingerprintVoucher(v Voucher) string {
	h := sha256.New()
	h.Write([]byte(v.ID))
	h.Write([]byte{0})
	h.Write([]byte(v.ReversalOf))
	h.Write([]byte{0})
	h.Write([]byte(v.Memo))
	h.Write([]byte{0})
	for _, e := range v.Entries {
		h.Write([]byte(e.Account))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(e.Debit, 10)))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatInt(e.Credit, 10)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fingerprintBatch(b Batch) string {
	h := sha256.New()
	h.Write([]byte(b.ID))
	h.Write([]byte{0})
	for _, v := range b.Vouchers {
		h.Write([]byte(fingerprintVoucher(v)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
