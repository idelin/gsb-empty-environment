package recovery

import "errors"

// Key is a key in the key/value store.
type Key string

// Value is a value stored under a key. A missing key is represented by the
// empty value.
type Value string

// TxnID identifies a transaction; IDs are unique within a Problem.
type TxnID uint8

// Group is the identifier of the transaction group used for ordered prefixes
// and parity summaries.
type Group uint8

// Read is a complete half-open interval read predicate. The predicate holds
// iff every key in [Low, High) currently maps to Want (the empty Want means
// the key must be absent).
type Read struct {
	Low  Key
	High Key
	Want Value
}

// Txn is one transaction of the batch. Within a group, transactions are
// totally ordered by increasing ID and each transaction's predecessor is the
// previous transaction of the same group.
type Txn struct {
	ID       TxnID
	Group    Group
	Reads    []Read
	WriteKey Key
	WriteVal Value
	// HasWrite is false for read-only transactions.
	HasWrite bool
}

// Bitset is a set of transaction IDs. With at most ten transactions a uint16
// covers every possible set.
type Bitset uint16

// BitsetAll returns the bitset covering n transactions with IDs 0..n-1.
func BitsetAll(n int) Bitset {
	return Bitset(uint16(1)<<uint(n) - 1)
}

func (b Bitset) has(id TxnID) bool { return b&(Bitset(1)<<id) != 0 }

// Contains reports whether id is committed in the bitset.
func (b Bitset) Contains(id TxnID) bool { return b.has(id) }

func (b *Bitset) add(id TxnID) { *b |= Bitset(1) << id }

// Count returns the number of set bits.
func (b Bitset) Count() int {
	n := 0
	for x := b; x != 0; x >>= 1 {
		n += int(x & 1)
	}
	return n
}

// Parity is the parity of a count of committed transactions.
type Parity uint8

const (
	Even Parity = iota
	Odd
)

func parityOf(n int) Parity { return Parity(n & 1) }

// String renders the parity for diagnostics.
func (p Parity) String() string {
	if p == Odd {
		return "odd"
	}
	return "even"
}

// Site is one candidate replica's persisted state at recovery time.
type Site struct {
	Name      string
	Committed Bitset
	KV        map[Key]Value
}

// Command replays an ordered prefix of a single transaction group against one
// replica. Every attempt costs Cost even when it rejects or disconnects.
type Command struct {
	Name  string
	Group Group
	// Prefix is the number of group transactions attempted, in group order.
	Prefix int
	Cost   int
}

// Probe requests the parity of the committed count of one transaction group.
type Probe struct {
	Name  string
	Group Group
	Cost  int
}

// Problem is the fully specified, finite recovery instance.
type Problem struct {
	Txns            []Txn
	Sites           []Site
	TargetKV        map[Key]Value
	TargetCommitted Bitset
	Commands        []Command
	Probes          []Probe

	cachedKeys   []Key
	cachedGroups map[Group][]Txn
}

// Receipt is the public, observable result of an attempt.
type Receipt interface{ receiptTag() }

// OKReceipt reports that every attempt of a commit command was accepted.
type OKReceipt struct{}

func (OKReceipt) receiptTag() {}

// RejectReceipt reports that a commit command stopped before RejectID; the
// rejected transaction was neither executed nor written.
type RejectReceipt struct {
	RejectID TxnID
}

func (RejectReceipt) receiptTag() {}

// DisconnectReceipt reports only that the environment disconnected; the number
// of transactions that executed before the break is not disclosed.
type DisconnectReceipt struct{}

func (DisconnectReceipt) receiptTag() {}

// ProbeReceipt is the reliable answer to a parity probe.
type ProbeReceipt struct {
	Group  Group
	Answer Parity
}

func (ProbeReceipt) receiptTag() {}

// Errors returned by validation and by the coordinator.
var (
	ErrInvalidProblem    = errors.New("recovery: invalid problem")
	ErrUnknownAction     = errors.New("recovery: unknown action")
	ErrUnexpectedReceipt = errors.New("recovery: receipt does not match current action")
	ErrNoPolicy          = errors.New("recovery: no policy for current belief")
	ErrNotSolved         = errors.New("recovery: problem has no successful strategy")
	ErrBeliefCapExceeded = errors.New("recovery: reachable belief set exceeds configured cap")
)
