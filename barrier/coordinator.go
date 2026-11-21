package barrier

import (
	"crypto/ed25519"
	"fmt"
)

// Migration is a queued partition-set change. Queued migrations never
// affect the current window; they are applied to the manifest of the
// next window when AdvanceWindow is called.
type Migration struct {
	Add    []string
	Remove []string
}

// Coordinator drives the sealing barrier for a sequence of windows. It
// is not safe for concurrent use; the host must serialize calls.
type Coordinator struct {
	key       ed25519.PrivateKey
	manifest  Manifest
	proofs    map[string]PrefixProof
	seal      *Seal
	migration Migration
}

// NewCoordinator creates a coordinator at genesis: window windowID with
// the given partitions, each starting at sequence zero with a zero base
// root. Bounds for the genesis window are set with SetBounds before any
// proof is accepted.
func NewCoordinator(key ed25519.PrivateKey, windowID uint64, partitions []string) *Coordinator {
	m := Manifest{WindowID: windowID, Partitions: map[string]PartitionSpec{}}
	for _, p := range partitions {
		m.Partitions[p] = PartitionSpec{}
	}
	return &Coordinator{key: key, manifest: m, proofs: map[string]PrefixProof{}}
}

// Manifest returns the manifest of the current window.
func (c *Coordinator) Manifest() Manifest { return c.manifest }

// CurrentSeal returns the seal of the current window, or nil if the
// window is not sealed yet.
func (c *Coordinator) CurrentSeal() *Seal { return c.seal }

// SetBounds fixes each partition's upper sequence bound for the current
// window. It must be called before proofs are submitted and cannot be
// changed afterwards: the manifest is the fixed sealing contract.
func (c *Coordinator) SetBounds(bounds map[string]uint64) error {
	if c.seal != nil {
		return ErrWindowSealed
	}
	if len(c.proofs) != 0 {
		return fmt.Errorf("barrier: bounds already locked by submitted proofs")
	}
	for id, bound := range bounds {
		spec, ok := c.manifest.Partitions[id]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownPartition, id)
		}
		if bound < spec.Base {
			return fmt.Errorf("barrier: bound %d below base %d for partition %q", bound, spec.Base, id)
		}
		spec.Bound = bound
		c.manifest.Partitions[id] = spec
	}
	return nil
}

// QueueMigration registers partition-set changes that take effect in
// the next window, never the current one.
func (c *Coordinator) QueueMigration(m Migration) {
	c.migration.Add = append(c.migration.Add, m.Add...)
	c.migration.Remove = append(c.migration.Remove, m.Remove...)
}

// Submit validates a partition's prefix proof against the current
// window manifest and records it. Proofs from other windows, unknown
// partitions, broken chains, or gapped ranges are rejected.
func (c *Coordinator) Submit(p PrefixProof) error {
	if c.seal != nil {
		return ErrWindowSealed
	}
	if err := p.Validate(c.manifest); err != nil {
		return err
	}
	if _, dup := c.proofs[p.Partition]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicateProof, p.Partition)
	}
	c.proofs[p.Partition] = p
	return nil
}

// Seal signs the aggregate root once every manifest partition has
// supplied a valid proof. A missing partition fails the seal; no other
// partition's root can stand in for it.
func (c *Coordinator) SealWindow() (*Seal, error) {
	if c.seal != nil {
		return nil, ErrDuplicateSeal
	}
	var missing []string
	for _, id := range c.manifest.PartitionIDs() {
		if _, ok := c.proofs[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, &MissingPartitionsError{Partitions: missing}
	}
	roots := make(map[string][32]byte, len(c.proofs))
	for id, p := range c.proofs {
		roots[id] = p.Root()
	}
	agg := AggregateRoot(c.manifest.WindowID, roots)
	c.seal = &Seal{
		WindowID:       c.manifest.WindowID,
		PartitionRoots: roots,
		AggregateRoot:  agg,
		Signature:      ed25519.Sign(c.key, agg[:]),
	}
	return c.seal, nil
}

// AdvanceWindow seals the generational boundary: the current window
// must be sealed, queued migrations are applied, and the next manifest
// chains each surviving partition from its freshly sealed root. Proofs
// built for the old window cannot validate in the new one.
func (c *Coordinator) AdvanceWindow() (Manifest, error) {
	if c.seal == nil {
		return Manifest{}, ErrWindowNotSealed
	}
	next := Manifest{
		WindowID:   c.manifest.WindowID + 1,
		Partitions: make(map[string]PartitionSpec, len(c.manifest.Partitions)),
	}
	for id, spec := range c.manifest.Partitions {
		next.Partitions[id] = PartitionSpec{
			Base:     spec.Bound,
			Bound:    spec.Bound,
			BaseRoot: c.seal.PartitionRoots[id],
		}
	}
	for _, id := range c.migration.Remove {
		delete(next.Partitions, id)
	}
	for _, id := range c.migration.Add {
		if _, ok := next.Partitions[id]; !ok {
			next.Partitions[id] = PartitionSpec{}
		}
	}
	c.manifest = next
	c.proofs = map[string]PrefixProof{}
	c.seal = nil
	c.migration = Migration{}
	return next, nil
}

// VerifySeal checks a seal's signature and internal consistency against
// the given public key.
func VerifySeal(pub ed25519.PublicKey, s *Seal) bool {
	if s == nil {
		return false
	}
	if AggregateRoot(s.WindowID, s.PartitionRoots) != s.AggregateRoot {
		return false
	}
	return ed25519.Verify(pub, s.AggregateRoot[:], s.Signature)
}
