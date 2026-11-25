package replayhandoff

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
)

const batchHeaderVersion uint64 = 1

var (
	// ErrNothingToCommit means a commit only contained already-delivered
	// sequence numbers, so no batch was appended.
	ErrNothingToCommit = errors.New("replayhandoff: no new sequence number to commit")
)

// BatchHeader is authenticated by the batch root. It binds the previous root,
// security-context digest and consecutive generation.
type BatchHeader struct {
	Version       uint64
	Node          NodeID
	ContextDigest [32]byte
	Generation    uint64
	PreviousRoot  Root
}

// BatchEntry is the copy-on-write delivery set advanced by one batch for one
// partition.
type BatchEntry struct {
	Partition Partition
	Delivered IntervalSet
}

// Batch is one signed node-log entry. Root authenticates the header and every
// entry; Signature authenticates Root with the node key.
type Batch struct {
	Header    BatchHeader
	Entries   []BatchEntry
	Root      Root
	Signature []byte
}

func encodeBatchEntries(entries []BatchEntry) []byte {
	entries = cloneSortedEntries(entries)
	out := appendU64(nil, uint64(len(entries)))
	for _, entry := range entries {
		out = appendPartition(out, entry.Partition)
		out = appendIntervalSet(out, entry.Delivered)
	}
	return out
}

func encodeBatchHeader(header BatchHeader) []byte {
	out := appendU64(nil, header.Version)
	out = appendLP(out, []byte(header.Node))
	out = appendDigest(out, header.ContextDigest)
	out = appendU64(out, header.Generation)
	out = appendRoot(out, header.PreviousRoot)
	return out
}

// ComputeBatchRoot computes the canonical hash of a batch body.
func ComputeBatchRoot(header BatchHeader, entries []BatchEntry) Root {
	payload := appendLP(nil, []byte("replayhandoff/batch-root/v1"))
	payload = append(payload, encodeBatchHeader(header)...)
	payload = append(payload, encodeBatchEntries(entries)...)
	return hashParts(payload)
}

// BatchSigningMessage is the domain-separated message signed by a node.
func BatchSigningMessage(header BatchHeader, entries []BatchEntry, root Root) []byte {
	payload := appendLP(nil, []byte("replayhandoff/batch-sign/v1"))
	payload = append(payload, encodeBatchHeader(header)...)
	payload = append(payload, encodeBatchEntries(entries)...)
	payload = appendRoot(payload, root)
	return SigningMessage("batch", payload)
}

func cloneSortedEntries(entries []BatchEntry) []BatchEntry {
	out := make([]BatchEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, BatchEntry{
			Partition: entry.Partition,
			Delivered: entry.Delivered,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Partition.Context != out[j].Partition.Context {
			return out[i].Partition.Context < out[j].Partition.Context
		}
		return out[i].Partition.Sender < out[j].Partition.Sender
	})
	return out
}

// Verify validates the root, the header digest and the node signature.
func (b Batch) Verify(keys KeyRing) error {
	if b.Header.Version != batchHeaderVersion {
		return fmt.Errorf("replayhandoff: unsupported batch header version %d", b.Header.Version)
	}
	if b.Header.Node == "" {
		return errors.New("replayhandoff: batch node must not be empty")
	}
	if b.Header.Generation == 0 {
		return errors.New("replayhandoff: batch generation must start at 1")
	}
	for _, entry := range b.Entries {
		if err := entry.Partition.Validate(); err != nil {
			return fmt.Errorf("replayhandoff: batch entry partition: %w", err)
		}
		digest := entry.Partition.Digest()
		if !bytes.Equal(digest[:], b.Header.ContextDigest[:]) {
			return errors.New("replayhandoff: batch entry context does not match header digest")
		}
		if err := ValidateSet(entry.Delivered); err != nil {
			return fmt.Errorf("replayhandoff: batch entry delivered set: %w", err)
		}
	}
	if ComputeBatchRoot(b.Header, b.Entries) != b.Root {
		return errors.New("replayhandoff: batch root mismatch")
	}
	return keys.Verify(b.Header.Node, BatchSigningMessage(b.Header, b.Entries, b.Root), b.Signature)
}

// NodeLog is a consecutive, signed batch chain for one node and one security
// context. It owns the latest immutable interval-set snapshots. Readers may
// retain a snapshot while a writer commits more batches.
type NodeLog struct {
	mu         sync.RWMutex
	node       NodeID
	context    ContextID
	state      map[Partition]IntervalSet
	batches    []Batch
	root       Root
	generation uint64
}

// NewNodeLog creates an empty log. A signer may be supplied to assert that the
// log belongs to that node; all writes still require a signer.
func NewNodeLog(node NodeID, context ContextID, signers ...*NodeSigner) (*NodeLog, error) {
	if node == "" {
		return nil, errors.New("replayhandoff: log node must not be empty")
	}
	if context == "" {
		return nil, errors.New("replayhandoff: log context must not be empty")
	}
	for _, signer := range signers {
		if signer != nil && signer.Node != node {
			return nil, fmt.Errorf("replayhandoff: signer belongs to %q, not %q", signer.Node, node)
		}
	}
	return &NodeLog{
		node:    node,
		context: context,
		state:   make(map[Partition]IntervalSet),
		root:    ZeroRoot,
	}, nil
}

// Node returns the node identity.
func (l *NodeLog) Node() NodeID {
	return l.node
}

// Context returns the security context.
func (l *NodeLog) Context() ContextID {
	return l.context
}

// Generation returns the number of signed batches in the log.
func (l *NodeLog) Generation() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.generation
}

// Barrier is the generation at which a proof would be taken.
func (l *NodeLog) Barrier() Barrier {
	return Barrier(l.Generation())
}

// Root returns the latest authenticated root.
func (l *NodeLog) Root() Root {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.root
}

// Delivered returns an immutable snapshot of one partition. Retaining the
// result does not copy or block future commits.
func (l *NodeLog) Delivered(partition Partition) IntervalSet {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state[partition]
}

// Batches returns a defensive copy of the signed chain.
func (l *NodeLog) Batches() []Batch {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Batch, len(l.batches))
	for i, batch := range l.batches {
		out[i] = batch
		out[i].Entries = cloneSortedEntries(batch.Entries)
		out[i].Signature = append([]byte(nil), batch.Signature...)
	}
	return out
}

// Commit records a successful business delivery. Already delivered members are
// idempotently removed before signing; if nothing remains, no batch is
// appended.
func (l *NodeLog) Commit(partition Partition, delivered IntervalSet, signer *NodeSigner) (uint64, error) {
	if err := partition.Validate(); err != nil {
		return l.Generation(), err
	}
	if partition.Context != l.context {
		return l.Generation(), fmt.Errorf("replayhandoff: partition context %q does not match log context %q", partition.Context, l.context)
	}
	if err := ValidateSet(delivered); err != nil {
		return l.Generation(), err
	}
	if signer == nil || signer.Node != l.node {
		return l.Generation(), fmt.Errorf("replayhandoff: log write requires signer for node %q", l.node)
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	delta := delivered.Subtract(l.state[partition])
	if delta.Empty() {
		return l.generation, nil
	}
	entries := []BatchEntry{{Partition: partition, Delivered: delta}}
	return l.appendLocked(entries, signer)
}

// CommitInterval is the common single-range variant of Commit.
func (l *NodeLog) CommitInterval(partition Partition, delivered Interval, signer *NodeSigner) (uint64, error) {
	if err := delivered.Validate(); err != nil {
		return l.Generation(), err
	}
	set, err := NewIntervalSet(delivered)
	if err != nil {
		return l.Generation(), err
	}
	return l.Commit(partition, set, signer)
}

// Append signs one batch containing multiple partition deltas. Only new
// intervals are committed; entries that add nothing are omitted.
func (l *NodeLog) Append(entries []BatchEntry, signer *NodeSigner) (Batch, error) {
	if signer == nil || signer.Node != l.node {
		return Batch{}, fmt.Errorf("replayhandoff: log write requires signer for node %q", l.node)
	}
	if len(entries) == 0 {
		return Batch{}, ErrNothingToCommit
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	normalized := make([]BatchEntry, 0, len(entries))
	for _, entry := range entries {
		if err := entry.Partition.Validate(); err != nil {
			return Batch{}, err
		}
		if entry.Partition.Context != l.context {
			return Batch{}, fmt.Errorf("replayhandoff: entry context %q does not match log context %q", entry.Partition.Context, l.context)
		}
		if err := ValidateSet(entry.Delivered); err != nil {
			return Batch{}, err
		}
		delta := entry.Delivered.Subtract(l.state[entry.Partition])
		if delta.Empty() {
			continue
		}
		normalized = append(normalized, BatchEntry{Partition: entry.Partition, Delivered: delta})
	}
	if len(normalized) == 0 {
		return Batch{}, ErrNothingToCommit
	}
	_, err := l.appendLocked(normalized, signer)
	if err != nil {
		return Batch{}, err
	}
	return l.batches[len(l.batches)-1], nil
}

func (l *NodeLog) appendLocked(entries []BatchEntry, signer *NodeSigner) (uint64, error) {
	entries = cloneSortedEntries(entries)
	nextGeneration := l.generation + 1
	header := BatchHeader{
		Version:       batchHeaderVersion,
		Node:          l.node,
		ContextDigest: ContextDigestFor(l.context),
		Generation:    nextGeneration,
		PreviousRoot:  l.root,
	}
	root := ComputeBatchRoot(header, entries)
	batch := Batch{
		Header:    header,
		Entries:   entries,
		Root:      root,
		Signature: signer.Sign(BatchSigningMessage(header, entries, root)),
	}
	for _, entry := range entries {
		current := l.state[entry.Partition]
		l.state[entry.Partition] = current.Union(entry.Delivered)
	}
	l.batches = append(l.batches, batch)
	l.root = root
	l.generation = nextGeneration
	return l.generation, nil
}

// Verify checks the entire signed chain and continuity.
func (l *NodeLog) Verify(keys KeyRing) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	prev := ZeroRoot
	digest := ContextDigestFor(l.context)
	var state map[Partition]IntervalSet
	if len(l.batches) > 0 {
		state = make(map[Partition]IntervalSet)
	}
	for i, batch := range l.batches {
		wantGeneration := uint64(i + 1)
		if batch.Header.Version != batchHeaderVersion {
			return fmt.Errorf("replayhandoff: batch %d has unsupported header version", wantGeneration)
		}
		if batch.Header.Node != l.node {
			return fmt.Errorf("replayhandoff: batch %d node mismatch", wantGeneration)
		}
		if !bytes.Equal(batch.Header.ContextDigest[:], digest[:]) {
			return fmt.Errorf("replayhandoff: batch %d context digest mismatch", wantGeneration)
		}
		if batch.Header.Generation != wantGeneration {
			return fmt.Errorf("replayhandoff: batch generation gap: got %d want %d", batch.Header.Generation, wantGeneration)
		}
		if batch.Header.PreviousRoot != prev {
			return fmt.Errorf("replayhandoff: batch %d previous-root mismatch", wantGeneration)
		}
		if ComputeBatchRoot(batch.Header, batch.Entries) != batch.Root {
			return fmt.Errorf("replayhandoff: batch %d root mismatch", wantGeneration)
		}
		if err := keys.Verify(l.node, BatchSigningMessage(batch.Header, batch.Entries, batch.Root), batch.Signature); err != nil {
			return fmt.Errorf("replayhandoff: batch %d: %w", wantGeneration, err)
		}
		for _, entry := range batch.Entries {
			if err := entry.Partition.Validate(); err != nil {
				return fmt.Errorf("replayhandoff: batch %d entry: %w", wantGeneration, err)
			}
			if entry.Partition.Context != l.context {
				return fmt.Errorf("replayhandoff: batch %d has foreign context entry", wantGeneration)
			}
			if err := ValidateSet(entry.Delivered); err != nil {
				return fmt.Errorf("replayhandoff: batch %d entry: %w", wantGeneration, err)
			}
			state[entry.Partition] = state[entry.Partition].Union(entry.Delivered)
		}
		prev = batch.Root
	}
	if prev != l.root || uint64(len(l.batches)) != l.generation {
		return errors.New("replayhandoff: log state does not match signed chain")
	}
	for partition, want := range state {
		if !l.state[partition].Equal(want) {
			return fmt.Errorf("replayhandoff: log state mismatch for partition %s", partition)
		}
	}
	if len(l.state) != len(state) {
		return errors.New("replayhandoff: log contains uncommitted partitions")
	}
	return nil
}
