package energy

import "fmt"

// OpKind enumerates the mutations supported by a transaction.
type OpKind uint8

const (
	// OpAdd inserts a sample.
	OpAdd OpKind = iota + 1
	// OpRemove deletes a sample and its contribution to every frequency.
	OpRemove
	// OpSetExecs replaces the valid execution count of a sample.
	OpSetExecs
	// OpReplaceCoverage replaces the covered feature set of a sample.
	OpReplaceCoverage
)

// String names the operation kind for error messages.
func (kind OpKind) String() string {
	switch kind {
	case OpAdd:
		return "add"
	case OpRemove:
		return "remove"
	case OpSetExecs:
		return "set-execs"
	case OpReplaceCoverage:
		return "replace-coverage"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(kind))
	}
}

// Op is one staged mutation. Build values with the Op* constructors rather
// than filling the struct literally so that unused fields stay nil.
type Op struct {
	Kind     OpKind
	Sample   Sample
	ID       string
	Execs    int64
	Coverage []Feature
}

// OpAddOp stages insertion of sample.
func OpAddOp(sample Sample) Op {
	return Op{Kind: OpAdd, Sample: sample}
}

// OpRemoveOp stages removal of the sample with id.
func OpRemoveOp(id string) Op {
	return Op{Kind: OpRemove, ID: id}
}

// OpSetExecsOp stages replacing the execution count of the sample with id.
func OpSetExecsOp(id string, execs int64) Op {
	return Op{Kind: OpSetExecs, ID: id, Execs: execs}
}

// OpReplaceCoverageOp stages replacing the coverage of the sample with id.
func OpReplaceCoverageOp(id string, coverage []Feature) Op {
	return Op{Kind: OpReplaceCoverage, ID: id, Coverage: coverage}
}

func (op Op) apply(state *Allocator) error {
	switch op.Kind {
	case OpAdd:
		if op.Sample.Execs < 0 {
			return fmt.Errorf("%w: sample %q", ErrInvalidExecs, op.Sample.ID)
		}
		if _, ok := state.samples[op.Sample.ID]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateID, op.Sample.ID)
		}
		state.addLocked(op.Sample)
	case OpRemove:
		sample, ok := state.samples[op.ID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSampleNotFound, op.ID)
		}
		state.removeLocked(sample)
	case OpSetExecs:
		if op.Execs < 0 {
			return ErrInvalidExecs
		}
		sample, ok := state.samples[op.ID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSampleNotFound, op.ID)
		}
		state.setExecsLocked(sample, op.Execs)
	case OpReplaceCoverage:
		sample, ok := state.samples[op.ID]
		if !ok {
			return fmt.Errorf("%w: %q", ErrSampleNotFound, op.ID)
		}
		state.replaceCoverageLocked(sample, op.Coverage)
	default:
		return fmt.Errorf("energy: unknown op kind %d", op.Kind)
	}
	return nil
}

// Merge applies ops atomically. The whole batch is validated and applied on a
// private copy of the state first; the live allocator is swapped to that copy
// only when every operation succeeds, so a batch containing a failed sample
// (unknown ID, duplicate ID, negative execs, ...) is rolled back completely
// and leaves the allocator unchanged.
func (a *Allocator) Merge(ops []Op) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	clone := a.cloneLocked()
	for index, op := range ops {
		if err := op.apply(clone); err != nil {
			return fmt.Errorf("energy: merge op %d (%s): %w", index, op.Kind, err)
		}
	}
	a.samples = clone.samples
	a.frequency = clone.frequency
	return nil
}

// cloneLocked produces an independent deep copy for transactional staging.
func (a *Allocator) cloneLocked() *Allocator {
	clone := &Allocator{
		samples:   make(map[string]*Sample, len(a.samples)),
		frequency: make(map[Feature]int64, len(a.frequency)),
	}
	for id, sample := range a.samples {
		coverage := make([]Feature, len(sample.Coverage))
		copy(coverage, sample.Coverage)
		clone.samples[id] = &Sample{
			ID:       sample.ID,
			Coverage: coverage,
			Execs:    sample.Execs,
		}
	}
	for feature, count := range a.frequency {
		clone.frequency[feature] = count
	}
	return clone
}

// Tx is a staged batch of mutations. A transaction does not hold the
// allocator lock while operations are being appended, so many goroutines may
// build transactions concurrently; their commits are serialized by Merge.
type Tx struct {
	allocator *Allocator
	ops       []Op
	closed    bool
}

// Begin opens an empty transaction on the allocator.
func (a *Allocator) Begin() *Tx {
	return &Tx{allocator: a}
}

// Len reports the number of staged operations.
func (tx *Tx) Len() int {
	return len(tx.ops)
}

func (tx *Tx) stage(op Op) error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.ops = append(tx.ops, op)
	return nil
}

// Add stages insertion of sample.
func (tx *Tx) Add(sample Sample) error { return tx.stage(OpAddOp(sample)) }

// Remove stages removal of the sample with id.
func (tx *Tx) Remove(id string) error { return tx.stage(OpRemoveOp(id)) }

// SetExecs stages replacing the execution count of the sample with id.
func (tx *Tx) SetExecs(id string, execs int64) error {
	return tx.stage(OpSetExecsOp(id, execs))
}

// ReplaceCoverage stages replacing the coverage of the sample with id.
func (tx *Tx) ReplaceCoverage(id string, coverage []Feature) error {
	return tx.stage(OpReplaceCoverageOp(id, coverage))
}

// Commit validates and applies every staged operation atomically via Merge.
// On failure the transaction is closed and the allocator is untouched. A
// transaction cannot be committed twice.
func (tx *Tx) Commit() error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.closed = true
	return tx.allocator.Merge(tx.ops)
}

// Rollback discards every staged operation without touching the allocator and
// closes the transaction.
func (tx *Tx) Rollback() {
	tx.closed = true
	tx.ops = nil
}
