package proofcache

import (
	"fmt"
	"sync"
)

// Draft is one member's contribution to a fixed-point batch.
type Draft struct {
	// Label is the batch member this draft belongs to.
	Label Label
	// Fingerprint binds the member's IR, dependency contracts and rule
	// version. Its dependency claims describe the fixed point: edges to other
	// batch members must match their draft contracts.
	Fingerprint Fingerprint
	// Contract is the externally visible analysis summary other members and
	// future callers depend on.
	Contract Digest
	// Proof is the analyzer-produced proof payload.
	Proof Proof
	// Generation must equal Cache.Generation(Label) observed when the draft
	// was produced. A smaller value means the draft raced with an
	// invalidation or replacement commit for the same label and is rejected
	// rather than admitted.
	Generation uint64
}

// Batch is an in-progress fixed-point certificate. It is safe for concurrent
// use by the workers analyzing its members.
type Batch struct {
	cache      *Cache
	mu         sync.Mutex
	id         string
	members    []Label
	member     map[Label]bool
	eventEpoch uint64
	generation map[Label]uint64
	rule       RuleVersion
	drafts     map[Label]*entry
	closed     bool
}

// ID returns the batch identifier, embedded in every entry it publishes.
func (b *Batch) ID() string {
	return b.id
}

// EventEpoch returns the global event counter captured when the batch opened.
// It is recorded on published entries for ordering and diagnostics.
func (b *Batch) EventEpoch() uint64 {
	return b.eventEpoch
}

// Generation returns the per-label fencing generation captured for member.
// Drafts for member must carry this value.
func (b *Batch) Generation(member Label) uint64 {
	return b.generation[member]
}

// Members returns the batch members in sorted order.
func (b *Batch) Members() []Label {
	return append([]Label(nil), b.members...)
}

// IsClosed reports whether the batch has already committed or aborted.
func (b *Batch) IsClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

// Put stores a member draft. Drafts are invisible outside the batch until
// commit. Re-putting a member replaces its previous draft, but only while the
// member's generation remains current.
func (b *Batch) Put(draft Draft) error {
	if draft.Label == "" {
		return fmt.Errorf("%w: empty draft label", ErrInvalidArgument)
	}
	if draft.Contract.IsZero() {
		return fmt.Errorf("%w: zero contract for %q", ErrInvalidArgument, draft.Label)
	}
	if len(draft.Proof) == 0 {
		return fmt.Errorf("%w: empty proof for %q", ErrInvalidArgument, draft.Label)
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrBatchClosed, b.id)
	}
	if !b.member[draft.Label] {
		b.mu.Unlock()
		return fmt.Errorf("%w: %q is not a member of batch %s", ErrInvalidArgument, draft.Label, b.id)
	}
	expected := b.generation[draft.Label]
	if draft.Generation != expected || draft.Generation < expected {
		b.mu.Unlock()
		return fmt.Errorf("%w: draft for %q carries generation %d, batch opened at %d",
			ErrStale, draft.Label, draft.Generation, expected)
	}
	if draft.Fingerprint.Rule != b.rule {
		b.mu.Unlock()
		return fmt.Errorf("%w: draft for %q uses %s, batch uses %s",
			ErrRuleVersion, draft.Label, draft.Fingerprint.Rule, b.rule)
	}
	b.mu.Unlock()

	c := b.cache
	c.mu.RLock()
	// Re-check under the cache lock: an invalidation could have advanced the
	// label generation between the batch-lock release and now.
	if c.gen[draft.Label] != expected || draft.Generation < c.gen[draft.Label] {
		c.mu.RUnlock()
		return fmt.Errorf("%w: generation for %q advanced while verification was in flight",
			ErrStale, draft.Label)
	}
	if draft.Fingerprint.Rule != c.rule {
		c.mu.RUnlock()
		return fmt.Errorf("%w: cache rule version changed", ErrRuleVersion)
	}
	c.mu.RUnlock()

	record := &entry{
		label:    draft.Label,
		fp:       draft.Fingerprint,
		contract: draft.Contract,
		proof:    append(Proof(nil), draft.Proof...),
		batchID:  b.id,
		epoch:    b.eventEpoch,
	}
	record.binding = bindingDigest(record.label, record.fp, record.contract, record.proof, record.batchID, record.epoch)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("%w: %s", ErrBatchClosed, b.id)
	}
	if b.generation[draft.Label] != expected || draft.Generation < expected {
		return fmt.Errorf("%w: generation for %q advanced before draft storage", ErrStale, draft.Label)
	}
	b.drafts[draft.Label] = record
	return nil
}

// Commit verifies the whole fixed point and atomically publishes every member
// entry. Publication only happens when:
//
//   - every member has a draft,
//   - no member label generation advanced since the batch opened,
//   - every dependency edge inside the batch matches a draft contract,
//   - every dependency edge outside the batch matches a published contract,
//   - every entry binding recomputes correctly.
//
// On any failure the batch is closed without publishing and the error is
// returned; the host must re-analyze under the current generations.
func (b *Batch) Commit() ([]*Entry, error) {
	c := b.cache

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrBatchClosed, b.id)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Per-label fencing comes first: any invalidation or replacement commit
	// for a member makes the whole fixed point unrecoverable.
	for _, label := range b.members {
		if c.gen[label] != b.generation[label] {
			b.closeLocked()
			b.mu.Unlock()
			return nil, fmt.Errorf("%w: generation for %q advanced past batch %s",
				ErrStale, label, b.id)
		}
	}

	if len(b.drafts) != len(b.members) {
		missing := make([]Label, 0, len(b.members))
		for _, label := range b.members {
			if _, ok := b.drafts[label]; !ok {
				missing = append(missing, label)
			}
		}
		b.mu.Unlock()
		return nil, fmt.Errorf("%w: missing drafts for %v", ErrBatchIncomplete, missing)
	}

	overlay := make(map[Label]*entry, len(b.drafts))
	for label, record := range b.drafts {
		if record.fp.Rule != c.rule {
			b.closeLocked()
			b.mu.Unlock()
			return nil, fmt.Errorf("%w: %q built under %s", ErrRuleVersion, label, record.fp.Rule)
		}
		overlay[label] = record
	}

	for _, label := range b.members {
		if err := c.verifyClosureLocked(overlay[label], overlay); err != nil {
			b.closeLocked()
			b.mu.Unlock()
			return nil, fmt.Errorf("fixed point %s: %w", b.id, err)
		}
	}

	publishedEvent := c.epoch + 1
	published := make([]*Entry, 0, len(b.members))
	for _, label := range b.members {
		record := overlay[label]
		record.epoch = publishedEvent
		record.binding = bindingDigest(record.label, record.fp, record.contract, record.proof, record.batchID, record.epoch)
		c.entries[label] = record
		c.gen[label]++
		published = append(published, cloneEntry(record))
	}
	c.epoch = publishedEvent
	b.closeLocked()
	b.mu.Unlock()
	return published, nil
}

// Abort discards all drafts and closes the batch without publishing.
func (b *Batch) Abort() {
	c := b.cache
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	b.unregisterLocked()
}

// closeLocked marks the batch closed and removes its registrations. Callers
// must hold both b.mu and the cache lock.
func (b *Batch) closeLocked() {
	b.closed = true
	b.unregisterLocked()
}

func (b *Batch) unregisterLocked() {
	delete(b.cache.batches, b.id)
	for _, label := range b.members {
		delete(b.cache.openFor, label)
	}
}
