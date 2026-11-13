package netsec

import (
	"fmt"
	"sort"
)

// Manifest is an immutable, complete entry table for a set of code units.
// Publishing a manifest is the only operation that changes which versions new
// calls can observe.
type Manifest struct {
	root       Digest
	parentRoot Digest
	units      []*UnitVersion
	entries    map[string]*UnitVersion
}

// BuildManifest verifies every unit, rejects duplicate unit IDs and computes
// the manifest root. parent is optional, but when supplied its root becomes
// the manifest's parent root for lineage/recovery.
func BuildManifest(units []*UnitVersion, parent *Manifest, validators Validators) (*Manifest, error) {
	entries := make(map[string]*UnitVersion, len(units))
	sorted := make([]*UnitVersion, 0, len(units))
	for _, u := range units {
		if u == nil {
			return nil, fmt.Errorf("%w: nil unit", ErrInvalidManifest)
		}
		if _, exists := entries[u.UnitID()]; exists {
			return nil, fmt.Errorf("%w: duplicate unit ID %q", ErrInvalidManifest, u.UnitID())
		}
		if err := u.Verify(validators); err != nil {
			return nil, fmt.Errorf("%w: unit %s: %w", ErrInvalidManifest, u.UnitID(), err)
		}
		entries[u.UnitID()] = u
		sorted = append(sorted, u)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].UnitID() < sorted[j].UnitID() })
	var parentRoot Digest
	if parent != nil {
		parentRoot = parent.root
	}
	root := computeManifestRoot(parentRoot, sorted)
	return &Manifest{
		root:       root,
		parentRoot: parentRoot,
		units:      sorted,
		entries:    entries,
	}, nil
}

func computeManifestRoot(parentRoot Digest, units []*UnitVersion) Digest {
	var c canonical
	c.bytes(parentRoot[:])
	c.u64(uint64(len(units)))
	for _, u := range units {
		c.str(u.unitID)
		c.bytes(u.contentRoot[:])
	}
	return digestOf("netsec/manifest/v1", c.b)
}

// Verify revalidates every contained unit and recomputes the manifest root.
func (m *Manifest) Verify(validators Validators) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	seen := make(map[string]struct{}, len(m.units))
	for _, u := range m.units {
		if u == nil {
			return fmt.Errorf("%w: nil unit", ErrInvalidManifest)
		}
		if _, exists := seen[u.UnitID()]; exists {
			return fmt.Errorf("%w: duplicate unit ID %q", ErrInvalidManifest, u.UnitID())
		}
		seen[u.UnitID()] = struct{}{}
		if err := u.Verify(validators); err != nil {
			return fmt.Errorf("%w: unit %s: %w", ErrInvalidManifest, u.UnitID(), err)
		}
	}
	if want := computeManifestRoot(m.parentRoot, m.units); want != m.root {
		return fmt.Errorf("%w: manifest root", ErrDigestMismatch)
	}
	if len(m.entries) != len(m.units) {
		return fmt.Errorf("%w: entry table is incomplete", ErrInvalidManifest)
	}
	for id, u := range m.entries {
		if u == nil || u.UnitID() != id {
			return fmt.Errorf("%w: entry table disagrees with units", ErrInvalidManifest)
		}
	}
	return nil
}

// Root returns the manifest root digest.
func (m *Manifest) Root() Digest { return m.root }

// ParentRoot returns the root of the manifest this one was derived from.
func (m *Manifest) ParentRoot() Digest { return m.parentRoot }

// Len returns the number of entry-table entries.
func (m *Manifest) Len() int { return len(m.units) }

// Units returns the manifest's units in UnitID order. The returned slice is a
// copy; the UnitVersion values are immutable.
func (m *Manifest) Units() []*UnitVersion {
	return append([]*UnitVersion(nil), m.units...)
}

// Resolve returns the version bound to unitID in this manifest.
func (m *Manifest) Resolve(unitID string) (*UnitVersion, bool) {
	u, ok := m.entries[unitID]
	return u, ok
}

// UnitIDs returns the sorted set of entry-table keys.
func (m *Manifest) UnitIDs() []string {
	ids := make([]string, 0, len(m.units))
	for _, u := range m.units {
		ids = append(ids, u.UnitID())
	}
	return ids
}

func (m *Manifest) sameContent(other *Manifest) bool {
	if m == nil || other == nil {
		return m == other
	}
	return m.root == other.root
}
