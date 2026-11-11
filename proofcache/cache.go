// Package proofcache is a content-bound cache of function-level security
// proofs for a Go program-analysis host.
//
// # What a proof is bound to
//
// Every cached proof is bound to a Fingerprint: the input IR digest, the
// contract digest of each dependency and the exact RuleVersion. Lookup only
// succeeds when the caller-supplied fingerprint matches the stored one and
// when the whole transitive dependency closure re-verifies against current
// entries. A change to a callee contract therefore invalidates every
// dependent proof, and a change to the verification rules invalidates every
// proof produced under the old rules.
//
// # Cyclic dependencies and fixed-point certificates
//
// Mutually recursive functions have no acyclic publish order. They are
// analyzed together in a Batch, which acts as a fixed-point certificate:
// each member supplies a draft bound to the contracts the fixed point was
// reached against, and Batch.Commit only publishes when every member draft
// is present and every dependency edge inside and outside the batch checks
// out. Nothing is visible to Lookup until the whole batch commits, so an
// outside observer can never observe a half-reached fixed point.
//
// # Reuse of old proofs
//
// Old proofs cannot be smuggled into a new generation. Each batch captures the
// per-label fencing generation at creation. An invalidation of a label, or a
// commit that replaces it, advances only that label's generation, so a draft
// arriving late for that label is rejected while unrelated concurrent batches
// keep running. Stored entries additionally carry a binding digest over
// label, fingerprint, contract, proof bytes, batch and event epoch, so
// relabeling a proof or reusing it under a different fingerprint fails either
// the key match or the binding check.
package proofcache

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// Cache is a concurrency-safe proof cache. The zero value is not usable;
// create one with New.
type Cache struct {
	mu         sync.RWMutex
	rule       RuleVersion
	entries    map[Label]*entry
	batches    map[string]*Batch
	openFor    map[Label]*Batch
	epoch      uint64
	gen        map[Label]uint64
	newBatchID func() (string, error)
}

// Stats is a point-in-time cache snapshot for observability.
type Stats struct {
	Entries     int
	OpenBatches int
	Epoch       uint64
	Rule        RuleVersion
}

// New creates a cache pinned to the given rule version.
func New(rule RuleVersion) *Cache {
	if rule.Name == "" || rule.Version == "" {
		panic("proofcache: New requires a fully specified rule version")
	}
	return &Cache{
		rule:       rule,
		entries:    make(map[Label]*entry),
		batches:    make(map[string]*Batch),
		openFor:    make(map[Label]*Batch),
		gen:        make(map[Label]uint64),
		newBatchID: randomBatchID,
	}
}

// Epoch returns the global monotonic event counter. It advances on every
// invalidation and commit and is embedded in entries for ordering. Fencing of
// in-flight work is tracked per label; see Generation.
func (c *Cache) Epoch() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.epoch
}

// Generation returns the current fencing generation for label. Any batch
// opened for label captures this value; drafts must carry the same value.
// Invalidation of label, or a commit that replaces label, advances it, which
// fences every in-flight batch touching label without disturbing batches for
// unrelated labels.
func (c *Cache) Generation(label Label) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen[label]
}

// Rule returns the rule version the cache is pinned to.
func (c *Cache) Rule() RuleVersion {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rule
}

// Stats returns a point-in-time observability snapshot.
func (c *Cache) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Stats{
		Entries:     len(c.entries),
		OpenBatches: len(c.batches),
		Epoch:       c.epoch,
		Rule:        c.rule,
	}
}

// Lookup returns the published entry for label whose stored fingerprint
// matches want exactly. It re-verifies the full dependency closure and the
// entry binding, so a stale or tampered entry behaves as a miss.
func (c *Cache) Lookup(label Label, want Fingerprint) (*Entry, error) {
	if label == "" {
		return nil, fmt.Errorf("%w: empty label", ErrInvalidArgument)
	}
	if want.Rule != c.rule {
		return nil, fmt.Errorf("%w: want %s, cache pinned to %s",
			ErrRuleVersion, want.Rule, c.rule)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	stored, ok := c.entries[label]
	if !ok {
		return nil, fmt.Errorf("%w: label %q", ErrNotFound, label)
	}
	if stored.fp.Sum() != want.Sum() {
		return nil, fmt.Errorf("%w: fingerprint changed for %q", ErrNotFound, label)
	}
	if stored.binding != bindingDigest(stored.label, stored.fp, stored.contract, stored.proof, stored.batchID, stored.epoch) {
		return nil, fmt.Errorf("%w: binding check failed for %q", ErrNotFound, label)
	}
	if err := c.verifyClosureLocked(stored, nil); err != nil {
		return nil, err
	}
	return cloneEntry(stored), nil
}

// Invalidate drops the entries for the given labels, advances the global event
// counter and bumps the per-label generation of each target, fencing off every
// open batch and draft touching those labels.
func (c *Cache) Invalidate(labels ...Label) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for _, label := range labels {
		if _, ok := c.entries[label]; ok {
			delete(c.entries, label)
			removed++
		}
		c.gen[label]++
	}
	c.epoch++

	// Any open batch touching an invalidated label can never commit: its
	// captured generation is now behind. Close it and release its labels so
	// the host can immediately open a replacement batch.
	dead := make(map[*Batch]bool)
	for _, label := range labels {
		if batch, ok := c.openFor[label]; ok {
			dead[batch] = true
		}
	}
	for batch := range dead {
		delete(c.batches, batch.id)
		for _, member := range batch.members {
			delete(c.openFor, member)
		}
		batch.mu.Lock()
		batch.closed = true
		batch.mu.Unlock()
	}
	return removed
}

// BeginBatch opens a fixed-point certificate over members. Every member must
// supply exactly one draft before commit. A label may not belong to two open
// batches at once.
func (c *Cache) BeginBatch(members []Label) (*Batch, error) {
	if len(members) == 0 {
		return nil, fmt.Errorf("%w: batch requires at least one member", ErrInvalidArgument)
	}
	seen := make(map[Label]struct{}, len(members))
	for _, label := range members {
		if label == "" {
			return nil, fmt.Errorf("%w: empty batch member label", ErrInvalidArgument)
		}
		if _, dup := seen[label]; dup {
			return nil, fmt.Errorf("%w: duplicate batch member %q", ErrInvalidArgument, label)
		}
		seen[label] = struct{}{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, label := range members {
		if existing, ok := c.openFor[label]; ok {
			return nil, fmt.Errorf("%w: label %q open in batch %s", ErrBatchConflict, label, existing.id)
		}
	}
	id, err := c.newBatchID()
	if err != nil {
		return nil, fmt.Errorf("proofcache: allocate batch id: %w", err)
	}
	batch := &Batch{
		cache:      c,
		id:         id,
		members:    sortedLabels(members),
		member:     copyLabelSet(seen),
		eventEpoch: c.epoch,
		generation: make(map[Label]uint64, len(members)),
		rule:       c.rule,
		drafts:     make(map[Label]*entry),
	}
	for _, label := range batch.members {
		batch.generation[label] = c.gen[label]
	}
	c.batches[id] = batch
	for _, label := range batch.members {
		c.openFor[label] = batch
	}
	return batch, nil
}

// verifyClosureLocked walks the transitive dependency closure of root. The
// overlay, when non-nil, supplies batch-local entries that are not yet
// published. Edges into the batch resolve against the overlay; edges outside
// resolve against published entries. Cycles are allowed and terminate the
// walk through the visited set, which is what validates a fixed-point batch.
func (c *Cache) verifyClosureLocked(root *entry, overlay map[Label]*entry) error {
	visited := map[Label]bool{}
	stack := []*entry{root}

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur.label] {
			continue
		}
		visited[cur.label] = true

		for _, dep := range cur.fp.Deps {
			depEntry := overlay[dep.Label]
			if depEntry == nil {
				depEntry = c.entries[dep.Label]
			}
			if depEntry == nil {
				return fmt.Errorf("%w: %q -> %q has no published contract", ErrDependency, cur.label, dep.Label)
			}
			if depEntry.contract != dep.Contract {
				return fmt.Errorf("%w: %q expected contract %s for %q, found %s",
					ErrDependency, cur.label, dep.Contract, dep.Label, depEntry.contract)
			}
			if depEntry.fp.Rule != c.rule {
				return fmt.Errorf("%w: %q proof built under %s", ErrRuleVersion, dep.Label, depEntry.fp.Rule)
			}
			if depEntry.binding != bindingDigest(depEntry.label, depEntry.fp, depEntry.contract, depEntry.proof, depEntry.batchID, depEntry.epoch) {
				return fmt.Errorf("%w: binding check failed for %q", ErrDependency, dep.Label)
			}
			if !visited[dep.Label] {
				stack = append(stack, depEntry)
			}
		}
	}
	return nil
}

func randomBatchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "batch-" + hex.EncodeToString(raw[:]), nil
}

func copyLabelSet(in map[Label]struct{}) map[Label]bool {
	out := make(map[Label]bool, len(in))
	for label := range in {
		out[label] = true
	}
	return out
}
