package netsec

import (
	"fmt"
	"sync"
)

// MigrationKind selects how a partition changes at the next window.
type MigrationKind uint8

const (
	// MigrationAdd introduces a partition that did not exist before.
	MigrationAdd MigrationKind = iota + 1
	// MigrationMigrate re-anchors an existing partition, for example after
	// key rotation or stream replay.
	MigrationMigrate
	// MigrationRemove retires a partition; it stops participating from the
	// next window onward.
	MigrationRemove
)

// Migration describes one partition change that becomes effective only when
// the next window opens. Anchor is required for MigrationAdd and
// MigrationMigrate and ignored for MigrationRemove.
type Migration struct {
	Kind      MigrationKind
	Partition PartitionID
	Anchor    Digest
}

// Coordinator owns the ordered sequence of sealing windows. It holds the
// active manifest and barrier, queues partition migrations for the next
// window and chains each new manifest onto the previous signed root. This is
// where generation separation is enforced: a migration never mutates the
// window already in progress.
type Coordinator struct {
	mu sync.Mutex

	manifest  Manifest
	barrier   *Barrier
	pending   []Migration
	lastRoot  Digest
	committed bool
}

// NewCoordinator opens the genesis window described by manifest. The
// manifest's PrevRoot must be the zero digest.
func NewCoordinator(manifest Manifest) (*Coordinator, error) {
	if manifest.PrevRoot != (Digest{}) {
		return nil, fmt.Errorf("%w: genesis manifest must not carry a previous root",
			ErrInvalidManifest)
	}
	barrier, err := NewBarrier(manifest)
	if err != nil {
		return nil, err
	}
	return &Coordinator{
		manifest: manifest,
		barrier:  barrier,
	}, nil
}

// CurrentManifest returns a copy of the active window manifest.
func (c *Coordinator) CurrentManifest() Manifest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cloneManifest()
}

// Barrier returns the active window's barrier.
func (c *Coordinator) Barrier() *Barrier {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.barrier
}

// Window returns the active window ID.
func (c *Coordinator) Window() WindowID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.manifest.Window
}

// PendingMigrations returns a copy of the migrations queued for the next
// window.
func (c *Coordinator) PendingMigrations() []Migration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Migration, len(c.pending))
	copy(out, c.pending)
	return out
}

// SetMigration queues migrations for the next window, replacing any earlier
// queue. Nothing changes in the active window until Advance runs.
func (c *Coordinator) SetMigration(migrations ...Migration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateMigrationsLocked(migrations); err != nil {
		return err
	}
	c.pending = append([]Migration(nil), migrations...)
	return nil
}

// ClearMigrations discards queued migrations without touching the active
// window.
func (c *Coordinator) ClearMigrations() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
}

func (c *Coordinator) validateMigrationsLocked(migrations []Migration) error {
	seen := make(map[PartitionID]MigrationKind, len(migrations))
	for _, migration := range migrations {
		if migration.Partition == "" {
			return fmt.Errorf("%w: empty partition id", ErrInvalidMigration)
		}
		if _, dup := seen[migration.Partition]; dup {
			return fmt.Errorf("%w: duplicate migration for partition %q",
				ErrInvalidMigration, migration.Partition)
		}
		seen[migration.Partition] = migration.Kind

		_, exists := currentSpec(c.manifest, migration.Partition)
		switch migration.Kind {
		case MigrationAdd:
			if exists {
				return fmt.Errorf("%w: partition %q already exists",
					ErrInvalidMigration, migration.Partition)
			}
		case MigrationMigrate:
			if !exists {
				return fmt.Errorf("%w: unknown partition %q",
					ErrInvalidMigration, migration.Partition)
			}
		case MigrationRemove:
			if !exists {
				return fmt.Errorf("%w: unknown partition %q",
					ErrInvalidMigration, migration.Partition)
			}
		default:
			return fmt.Errorf("%w: unknown kind %d",
				ErrInvalidMigration, migration.Kind)
		}
	}
	return nil
}

// Commit records the signed attestation of the active window. Its root
// becomes the PrevRoot of the next window and, for partitions that continue
// without an explicit migration, their new chain anchor. The attestation
// must belong to the active window.
func (c *Coordinator) Commit(attestation *Attestation) error {
	if attestation == nil {
		return fmt.Errorf("%w: nil attestation", ErrUnknownWindow)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if attestation.Window != c.manifest.Window {
		return fmt.Errorf("%w: attestation window %d, active window %d",
			ErrUnknownWindow, attestation.Window, c.manifest.Window)
	}
	c.lastRoot = attestation.Root
	c.committed = true
	return nil
}

// Advance opens the next window. nextWindow must be strictly greater than
// the active window and upperBounds gives the inclusive sequence upper bound
// for every partition present after migration.
//
// Pending migrations are applied here, never earlier. Continuing partitions
// anchor on their last root from attestation, unless a MigrationMigrate
// entry explicitly supplies a new anchor. The new manifest is chained onto
// the committed previous window root so every generation links to the one
// before it.
func (c *Coordinator) Advance(nextWindow WindowID, upperBounds map[PartitionID]Seq, attestation *Attestation) (*Barrier, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if nextWindow <= c.manifest.Window {
		return nil, fmt.Errorf("%w: next window %d must exceed active window %d",
			ErrUnknownWindow, nextWindow, c.manifest.Window)
	}
	if attestation == nil {
		return nil, fmt.Errorf("%w: previous-window attestation is required",
			ErrUnknownWindow)
	}
	if attestation.Window != c.manifest.Window {
		return nil, fmt.Errorf("%w: attestation window %d, active window %d",
			ErrUnknownWindow, attestation.Window, c.manifest.Window)
	}
	if attestation.Root != c.lastRoot || !c.committed {
		return nil, fmt.Errorf("%w: attestation was not committed for window %d",
			ErrUnknownWindow, c.manifest.Window)
	}
	if err := c.validateMigrationsLocked(c.pending); err != nil {
		return nil, err
	}

	next, err := c.buildNextManifestLocked(nextWindow, upperBounds, attestation)
	if err != nil {
		return nil, err
	}
	barrier, err := NewBarrier(next)
	if err != nil {
		return nil, err
	}

	c.manifest = next
	c.barrier = barrier
	c.pending = nil
	return barrier, nil
}

func (c *Coordinator) buildNextManifestLocked(nextWindow WindowID, upperBounds map[PartitionID]Seq, attestation *Attestation) (Manifest, error) {
	migrations := make(map[PartitionID]Migration, len(c.pending))
	for _, migration := range c.pending {
		migrations[migration.Partition] = migration
	}

	var specs []PartitionSpec
	seen := make(map[PartitionID]bool)

	// Carry forward every partition that is not removed.
	for _, spec := range c.manifest.Partitions {
		if migration, ok := migrations[spec.Partition]; ok && migration.Kind == MigrationRemove {
			continue
		}
		bound, ok := upperBounds[spec.Partition]
		if !ok {
			return Manifest{}, fmt.Errorf("%w: missing upper bound for continuing partition %q",
				ErrInvalidMigration, spec.Partition)
		}
		anchor := spec.Anchor
		if root, ok := attestation.RootFor(spec.Partition); ok {
			anchor = root
		}
		if migration, ok := migrations[spec.Partition]; ok && migration.Kind == MigrationMigrate {
			anchor = migration.Anchor
		}
		specs = append(specs, PartitionSpec{
			Partition:  spec.Partition,
			UpperBound: bound,
			Anchor:     anchor,
		})
		seen[spec.Partition] = true
	}

	// Add newly introduced partitions.
	for _, migration := range c.pending {
		if migration.Kind != MigrationAdd || seen[migration.Partition] {
			continue
		}
		bound, ok := upperBounds[migration.Partition]
		if !ok {
			return Manifest{}, fmt.Errorf("%w: missing upper bound for added partition %q",
				ErrInvalidMigration, migration.Partition)
		}
		specs = append(specs, PartitionSpec{
			Partition:  migration.Partition,
			UpperBound: bound,
			Anchor:     migration.Anchor,
		})
		seen[migration.Partition] = true
	}

	for partition := range upperBounds {
		if !seen[partition] {
			return Manifest{}, fmt.Errorf("%w: upper bound for unknown partition %q",
				ErrInvalidMigration, partition)
		}
	}

	return Manifest{
		Window:     nextWindow,
		Partitions: specs,
		PrevRoot:   attestation.Root,
	}, nil
}

func (c *Coordinator) cloneManifest() Manifest {
	clone := Manifest{
		Window:     c.manifest.Window,
		Partitions: append([]PartitionSpec(nil), c.manifest.Partitions...),
		PrevRoot:   c.manifest.PrevRoot,
	}
	return clone
}

func currentSpec(manifest Manifest, partition PartitionID) (PartitionSpec, bool) {
	for _, spec := range manifest.Partitions {
		if spec.Partition == partition {
			return spec, true
		}
	}
	return PartitionSpec{}, false
}
