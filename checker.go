package netsec

import (
	"fmt"
	"sync"
)

// CallerID identifies the process submitting transactions.
type CallerID = uint64

// TransactionID identifies a transaction within a caller.
type TransactionID = uint64

// Limits contains the buffer limits derived from the configured total B.
type Limits struct {
	BufferBytes    int
	OneWayBytes    int
	PerCallerBytes int
}

// Checker accounts for pending one-way transactions. It is safe for
// concurrent use.
type Checker struct {
	limits Limits

	mu           sync.Mutex
	pendingBytes int64
	pending      map[transactionKey]*Reservation
	callers      map[CallerID]int64
}

type transactionKey struct {
	caller        CallerID
	transactionID TransactionID
}

// Reservation proves that a transaction was admitted and retains the amount
// of buffer charged to its caller.
type Reservation struct {
	checker       *Checker
	caller        CallerID
	transactionID TransactionID
	size          int
}

// New creates a Checker for a buffer of bufferBytes. All pending one-way
// transactions may use at most half of the buffer, and each caller may use at
// most a quarter. It panics if bufferBytes is negative.
func New(bufferBytes int) *Checker {
	if bufferBytes < 0 {
		panic("netsec: negative buffer size")
	}

	return &Checker{
		limits: Limits{
			BufferBytes:    bufferBytes,
			OneWayBytes:    bufferBytes / 2,
			PerCallerBytes: bufferBytes / 4,
		},
		pending: make(map[transactionKey]*Reservation),
		callers: make(map[CallerID]int64),
	}
}

// NewChecker is an alias for New.
func NewChecker(bufferBytes int) *Checker {
	return New(bufferBytes)
}

// Limits returns the configured buffer limits.
func (c *Checker) Limits() Limits {
	return c.limits
}

// Admit validates a transaction and reserves its buffer occupancy for caller.
// transactionID must be unique among that caller's pending transactions.
//
// A rejected transaction reserves nothing. In particular, exceeding one
// caller's limit never consumes shared capacity or causes another caller's
// pending transactions to fail.
func (c *Checker) Admit(caller CallerID, transactionID TransactionID, data []byte, offsets []uint64) (*Reservation, error) {
	if c == nil {
		return nil, fmt.Errorf("netsec: nil checker")
	}
	if err := Validate(data, offsets); err != nil {
		return nil, err
	}
	size, err := TransactionSize(len(data), len(offsets))
	if err != nil {
		return nil, err
	}
	return c.admitSize(caller, transactionID, size)
}

// AdmitSigned is like Admit but accepts signed offsets.
func (c *Checker) AdmitSigned(caller CallerID, transactionID TransactionID, data []byte, offsets []int) (*Reservation, error) {
	if c == nil {
		return nil, fmt.Errorf("netsec: nil checker")
	}
	if err := ValidateSignedOffsets(data, offsets); err != nil {
		return nil, err
	}
	size, err := TransactionSize(len(data), len(offsets))
	if err != nil {
		return nil, err
	}
	return c.admitSize(caller, transactionID, size)
}

func (c *Checker) admitSize(caller CallerID, transactionID TransactionID, size int) (*Reservation, error) {
	key := transactionKey{caller: caller, transactionID: transactionID}
	requested := int64(size)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.pending[key]; exists {
		return nil, fmt.Errorf("%w: caller %d transaction %d", ErrDuplicateTransaction, caller, transactionID)
	}

	callerPending := c.callers[caller]
	callerLimit := int64(c.limits.PerCallerBytes)
	if requested > callerLimit-callerPending {
		return nil, fmt.Errorf(
			"%w: caller %d requested %d bytes with %d bytes pending, limit %d",
			ErrCallerQuota, caller, requested, callerPending, c.limits.PerCallerBytes,
		)
	}
	oneWayLimit := int64(c.limits.OneWayBytes)
	if requested > oneWayLimit-c.pendingBytes {
		return nil, fmt.Errorf(
			"%w: caller %d requested %d bytes, %d bytes pending, limit %d",
			ErrOneWayQuota, caller, requested, c.pendingBytes, c.limits.OneWayBytes,
		)
	}

	reservation := &Reservation{
		checker:       c,
		caller:        caller,
		transactionID: transactionID,
		size:          size,
	}
	c.pending[key] = reservation
	c.pendingBytes += requested
	c.callers[caller] = callerPending + requested
	return reservation, nil
}

// Complete releases the buffer held by a transaction after its completion
// event. It returns false if the transaction is not pending, including when a
// completion event is delivered more than once.
func (c *Checker) Complete(caller CallerID, transactionID TransactionID) bool {
	if c == nil {
		return false
	}
	return c.release(transactionKey{caller: caller, transactionID: transactionID}, nil)
}

// Release releases a reservation token. It returns false if the reservation
// was already released or belongs to another Checker.
func (c *Checker) Release(reservation *Reservation) bool {
	if c == nil || reservation == nil || reservation.checker != c {
		return false
	}
	return c.release(
		transactionKey{caller: reservation.caller, transactionID: reservation.transactionID},
		reservation,
	)
}

func (c *Checker) release(key transactionKey, expected *Reservation) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	reservation, exists := c.pending[key]
	if !exists || (expected != nil && reservation != expected) {
		return false
	}

	delete(c.pending, key)
	c.pendingBytes -= int64(reservation.size)
	remaining := c.callers[key.caller] - int64(reservation.size)
	if remaining == 0 {
		delete(c.callers, key.caller)
	} else {
		c.callers[key.caller] = remaining
	}
	return true
}

// PendingBytes returns the total occupancy of all pending one-way
// transactions.
func (c *Checker) PendingBytes() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(c.pendingBytes)
}

// PendingBytesFor returns the occupancy of caller's pending transactions.
func (c *Checker) PendingBytesFor(caller CallerID) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(c.callers[caller])
}

// PendingTransactions returns the number of pending transactions.
func (c *Checker) PendingTransactions() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// Caller returns the caller that owns the reservation.
func (r *Reservation) Caller() CallerID {
	if r == nil {
		return 0
	}
	return r.caller
}

// TransactionID returns the transaction identifier for the reservation.
func (r *Reservation) TransactionID() TransactionID {
	if r == nil {
		return 0
	}
	return r.transactionID
}

// Size returns the buffer occupancy charged for the reservation.
func (r *Reservation) Size() int {
	if r == nil {
		return 0
	}
	return r.size
}

// Release releases the reservation. It is equivalent to calling
// Checker.Release(r).
func (r *Reservation) Release() bool {
	if r == nil || r.checker == nil {
		return false
	}
	return r.checker.Release(r)
}

// Done reports whether the reservation has been released.
func (r *Reservation) Done() bool {
	if r == nil || r.checker == nil {
		return true
	}
	return !r.checker.isPending(r)
}

func (c *Checker) isPending(reservation *Reservation) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := transactionKey{caller: reservation.caller, transactionID: reservation.transactionID}
	return c.pending[key] == reservation
}
