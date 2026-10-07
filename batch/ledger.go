// Package batch implements an integer-quantity batch (lot) ledger.
//
// The ledger records every movement as an operation node in a directed
// source graph: batches are vertices, operations are edges from consumed
// (input) batches to produced (output) batches. Supported operations are
// inbound, split, merge and outbound. All quantities are integers.
//
// Undo is lineage-aware: an ancestor operation can only be undone while
// every batch it produced is still fully intact and has no active
// descendant operations. If any descendant has been consumed, Undo
// rejects with an *UndoError carrying the dependency chain from the
// operation to the blocking consumption. A sufficient total quantity
// elsewhere in the ledger never makes an undo legal.
package batch

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrNonPositiveQty  = errors.New("batch: quantity must be a positive integer")
	ErrUnknownBatch    = errors.New("batch: unknown batch")
	ErrUnknownOp       = errors.New("batch: unknown operation")
	ErrAlreadyUndone   = errors.New("batch: operation already undone")
	ErrDuplicateSource = errors.New("batch: duplicate merge source")
	ErrEmptySplit      = errors.New("batch: split needs at least one part")
)

// InsufficientError reports that a batch does not hold enough remaining
// quantity for a requested movement.
type InsufficientError struct {
	BatchID string
	Want    int
	Have    int
}

func (e *InsufficientError) Error() string {
	return fmt.Sprintf("batch: %s holds %d, need %d", e.BatchID, e.Have, e.Want)
}

// UndoError reports that an operation cannot be undone because descendant
// operations depend on its output. Chain lists the node IDs (operations
// and batches, alternating) from the operation being undone down to the
// blocking consumption.
type UndoError struct {
	OpID  string
	Chain []string
}

func (e *UndoError) Error() string {
	return fmt.Sprintf("batch: cannot undo %s, dependency chain: %s",
		e.OpID, strings.Join(e.Chain, " -> "))
}

// OpKind identifies the type of a ledger operation.
type OpKind int

const (
	OpInbound OpKind = iota
	OpSplit
	OpMerge
	OpOutbound
)

func (k OpKind) String() string {
	switch k {
	case OpInbound:
		return "inbound"
	case OpSplit:
		return "split"
	case OpMerge:
		return "merge"
	case OpOutbound:
		return "outbound"
	}
	return "unknown"
}

// Portion is a quantity taken from or credited to one batch.
type Portion struct {
	BatchID string
	Qty     int
}

// Op is a node in the directed source graph.
type Op struct {
	ID      string
	Kind    OpKind
	Inputs  []Portion // batches consumed from
	Outputs []Portion // batches produced
	Undone  bool
}

type batchNode struct {
	id        string
	initial   int
	remaining int
	createdBy string
	alive     bool // false once its creating operation was undone
}

// Ledger is a batch inventory book. It is safe for concurrent use.
type Ledger struct {
	mu        sync.Mutex
	batches   map[string]*batchNode
	ops       map[string]*Op
	consumers map[string][]string // batch ID -> IDs of ops consuming from it
	batchSeq  int
	opSeq     int
}

func NewLedger() *Ledger {
	return &Ledger{
		batches:   make(map[string]*batchNode),
		ops:       make(map[string]*Op),
		consumers: make(map[string][]string),
	}
}

func (l *Ledger) nextBatchID() string {
	l.batchSeq++
	return fmt.Sprintf("B%d", l.batchSeq)
}

func (l *Ledger) nextOpID() string {
	l.opSeq++
	return fmt.Sprintf("OP%d", l.opSeq)
}

// Inbound registers a new root batch holding qty units.
func (l *Ledger) Inbound(qty int) (batchID, opID string, err error) {
	if qty <= 0 {
		return "", "", ErrNonPositiveQty
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	batchID = l.nextBatchID()
	opID = l.nextOpID()
	l.batches[batchID] = &batchNode{id: batchID, initial: qty, remaining: qty, createdBy: opID, alive: true}
	l.ops[opID] = &Op{ID: opID, Kind: OpInbound, Outputs: []Portion{{BatchID: batchID, Qty: qty}}}
	return batchID, opID, nil
}

// Split divides parts of src into new child batches. The parts may sum to
// less than the remaining quantity (partial split); the unspent remainder
// stays on src.
func (l *Ledger) Split(src string, parts []int) (batchIDs []string, opID string, err error) {
	if len(parts) == 0 {
		return nil, "", ErrEmptySplit
	}
	total := 0
	for _, p := range parts {
		if p <= 0 {
			return nil, "", ErrNonPositiveQty
		}
		total += p
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.batches[src]
	if !ok || !b.alive {
		return nil, "", ErrUnknownBatch
	}
	if b.remaining < total {
		return nil, "", &InsufficientError{BatchID: src, Want: total, Have: b.remaining}
	}

	// All validation done above; mutation below is atomic.
	opID = l.nextOpID()
	op := &Op{ID: opID, Kind: OpSplit, Inputs: []Portion{{BatchID: src, Qty: total}}}
	for _, p := range parts {
		id := l.nextBatchID()
		l.batches[id] = &batchNode{id: id, initial: p, remaining: p, createdBy: opID, alive: true}
		op.Outputs = append(op.Outputs, Portion{BatchID: id, Qty: p})
		batchIDs = append(batchIDs, id)
	}
	b.remaining -= total
	l.consumers[src] = append(l.consumers[src], opID)
	l.ops[opID] = op
	return batchIDs, opID, nil
}

// Merge combines the given portions of several source batches into one new
// batch. Portions may be partial, so several merges can share one source.
// If any source is unknown or insufficient, nothing is mutated.
func (l *Ledger) Merge(sources []Portion) (batchID, opID string, err error) {
	if len(sources) == 0 {
		return "", "", ErrEmptySplit
	}
	seen := make(map[string]bool, len(sources))
	total := 0
	for _, s := range sources {
		if s.Qty <= 0 {
			return "", "", ErrNonPositiveQty
		}
		if seen[s.BatchID] {
			return "", "", ErrDuplicateSource
		}
		seen[s.BatchID] = true
		total += s.Qty
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for _, s := range sources {
		b, ok := l.batches[s.BatchID]
		if !ok || !b.alive {
			return "", "", ErrUnknownBatch
		}
		if b.remaining < s.Qty {
			return "", "", &InsufficientError{BatchID: s.BatchID, Want: s.Qty, Have: b.remaining}
		}
	}

	// All validation done above; mutation below is atomic.
	batchID = l.nextBatchID()
	opID = l.nextOpID()
	op := &Op{ID: opID, Kind: OpMerge, Outputs: []Portion{{BatchID: batchID, Qty: total}}}
	for _, s := range sources {
		l.batches[s.BatchID].remaining -= s.Qty
		l.consumers[s.BatchID] = append(l.consumers[s.BatchID], opID)
		op.Inputs = append(op.Inputs, Portion{BatchID: s.BatchID, Qty: s.Qty})
	}
	l.batches[batchID] = &batchNode{id: batchID, initial: total, remaining: total, createdBy: opID, alive: true}
	l.ops[opID] = op
	return batchID, opID, nil
}

// Outbound removes qty units from src, e.g. for shipping or consumption.
func (l *Ledger) Outbound(src string, qty int) (opID string, err error) {
	if qty <= 0 {
		return "", ErrNonPositiveQty
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.batches[src]
	if !ok || !b.alive {
		return "", ErrUnknownBatch
	}
	if b.remaining < qty {
		return "", &InsufficientError{BatchID: src, Want: qty, Have: b.remaining}
	}

	opID = l.nextOpID()
	b.remaining -= qty
	l.consumers[src] = append(l.consumers[src], opID)
	l.ops[opID] = &Op{ID: opID, Kind: OpOutbound, Inputs: []Portion{{BatchID: src, Qty: qty}}}
	return opID, nil
}

// Undo reverses an operation.
//
// For inbound, split and merge this succeeds only while every produced
// batch is still fully intact and no active descendant operation depends
// on it; otherwise an *UndoError with the dependency chain is returned.
// Total quantity available elsewhere in the ledger is never a substitute.
// For outbound it credits the quantity back to its source batch.
func (l *Ledger) Undo(opID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	op, ok := l.ops[opID]
	if !ok {
		return ErrUnknownOp
	}
	if op.Undone {
		return ErrAlreadyUndone
	}

	if op.Kind == OpOutbound {
		in := op.Inputs[0]
		b := l.batches[in.BatchID]
		if b == nil || !b.alive {
			return &UndoError{OpID: opID, Chain: []string{opID, in.BatchID}}
		}
		b.remaining += in.Qty
		op.Undone = true
		return nil
	}

	for _, out := range op.Outputs {
		b := l.batches[out.BatchID]
		if b == nil || !b.alive {
			return &UndoError{OpID: opID, Chain: []string{opID, out.BatchID}}
		}
		if blocker := l.activeConsumer(out.BatchID); blocker != "" {
			chain := append([]string{opID, out.BatchID}, l.descend(blocker)...)
			return &UndoError{OpID: opID, Chain: chain}
		}
		if b.remaining != out.Qty {
			return &UndoError{OpID: opID, Chain: []string{opID, out.BatchID}}
		}
	}

	for _, in := range op.Inputs {
		l.batches[in.BatchID].remaining += in.Qty
	}
	for _, out := range op.Outputs {
		b := l.batches[out.BatchID]
		b.remaining = 0
		b.alive = false
	}
	op.Undone = true
	return nil
}

// activeConsumer returns the ID of a non-undone op consuming from batchID,
// preferring outbound ops so dependency chains terminate at real
// consumption. Returns "" when the batch has no active consumer.
func (l *Ledger) activeConsumer(batchID string) string {
	fallback := ""
	for _, cid := range l.consumers[batchID] {
		c := l.ops[cid]
		if c.Undone {
			continue
		}
		if c.Kind == OpOutbound {
			return cid
		}
		if fallback == "" {
			fallback = cid
		}
	}
	return fallback
}

// descend builds the dependency chain from a blocking op down to the
// deepest blocking descendant, preferring paths that end in outbound
// consumption.
func (l *Ledger) descend(opID string) []string {
	op := l.ops[opID]
	chain := []string{opID}
	if op.Kind == OpOutbound {
		return chain
	}
	for _, out := range op.Outputs {
		if next := l.activeConsumer(out.BatchID); next != "" {
			chain = append(chain, out.BatchID)
			return append(chain, l.descend(next)...)
		}
		if b := l.batches[out.BatchID]; b == nil || !b.alive || b.remaining != out.Qty {
			return append(chain, out.BatchID)
		}
	}
	return chain
}

// Remaining returns the remaining quantity of a live batch.
func (l *Ledger) Remaining(batchID string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.batches[batchID]
	if !ok || !b.alive {
		return 0, false
	}
	return b.remaining, true
}

// Op returns a copy of the recorded operation.
func (l *Ledger) Op(opID string) (Op, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	op, ok := l.ops[opID]
	if !ok {
		return Op{}, false
	}
	cp := *op
	cp.Inputs = append([]Portion(nil), op.Inputs...)
	cp.Outputs = append([]Portion(nil), op.Outputs...)
	return cp, true
}

// Consumers lists the IDs of all operations (undone or not) that consumed
// from batchID, in recording order. Together with Op it exposes the
// directed source graph.
func (l *Ledger) Consumers(batchID string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.consumers[batchID]...)
}

// Verify walks the whole source graph and checks per-batch conservation:
// for every live batch, remaining == initial - sum(portions consumed by
// active ops); for every undone-creation batch, remaining == 0; and for
// every active split/merge, sum(inputs) == sum(outputs).
func (l *Ledger) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for id, b := range l.batches {
		if !b.alive {
			if b.remaining != 0 {
				return fmt.Errorf("batch: %s was revoked but still holds %d", id, b.remaining)
			}
			continue
		}
		consumed := 0
		for _, cid := range l.consumers[id] {
			c := l.ops[cid]
			if c.Undone {
				continue
			}
			for _, in := range c.Inputs {
				if in.BatchID == id {
					consumed += in.Qty
				}
			}
		}
		if b.remaining != b.initial-consumed {
			return fmt.Errorf("batch: %s conservation broken: initial %d, consumed %d, remaining %d",
				id, b.initial, consumed, b.remaining)
		}
		if b.remaining < 0 {
			return fmt.Errorf("batch: %s has negative remaining %d", id, b.remaining)
		}
	}

	for id, op := range l.ops {
		if op.Undone || op.Kind == OpInbound || op.Kind == OpOutbound {
			continue
		}
		in, out := 0, 0
		for _, p := range op.Inputs {
			in += p.Qty
		}
		for _, p := range op.Outputs {
			out += p.Qty
		}
		if in != out {
			return fmt.Errorf("batch: %s (%s) not conservative: in %d, out %d", id, op.Kind, in, out)
		}
	}
	return nil
}
