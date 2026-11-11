package policymv

import (
	"sort"
	"sync"
)

// Version is an immutable, reader-side handle to one policy version.
// Methods on a Version never take the store lock and remain valid after newer
// versions are published, so a reader can pin one version for its whole
// request and observe a fixed policy.
type Version struct {
	id   int
	snap *snapshot
}

// ID returns the monotonically increasing version identifier.
func (v *Version) ID() int { return v.id }

// HasPermission reports whether role holds permission p in this version,
// accounting for the full transitive inheritance chain.
func (v *Version) HasPermission(role Role, p Permission) bool {
	return v.snap.hasPermission(role, p)
}

// EffectivePerms returns a sorted copy of every permission role effectively
// holds in this version, including inherited permissions.
func (v *Version) EffectivePerms(role Role) []Permission {
	perms, ok := v.snap.effective[role]
	if !ok {
		return nil
	}
	out := make([]Permission, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Exists reports whether role is known in this version.
func (v *Version) Exists(role Role) bool {
	_, ok := v.snap.roles[role]
	return ok
}

// entry is one retained version plus the original change set that produced it.
type entry struct {
	id     int
	snap   *snapshot
	change ChangeSet
}

// Service is a concurrency-safe, multi-versioned role policy store.
// Versions are append-only; the fully built new snapshot is swapped in under a
// single lock operation, so no reader can ever observe a half-applied change.
type Service struct {
	mu       sync.RWMutex
	versions []*entry // index equals version id; versions[0] is the empty root
}

// New creates a store whose empty root version has id 0.
func New() *Service {
	return &Service{
		versions: []*entry{{id: 0, snap: newSnapshot()}},
	}
}

// Current returns the latest published version.
func (s *Service) Current() *Version {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentLocked()
}

func (s *Service) currentLocked() *Version {
	head := s.versions[len(s.versions)-1]
	return &Version{id: head.id, snap: head.snap}
}

// Version returns a pinned handle for an existing version id.
func (s *Service) Version(id int) (*Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if id < 0 || id >= len(s.versions) {
		return nil, ErrUnknownVersion
	}
	e := s.versions[id]
	return &Version{id: e.id, snap: e.snap}, nil
}

// Publish atomically applies change on top of base, which must be the current
// version. It returns ErrStaleBase otherwise; use Submit to rebase and merge a
// change based on an older version.
func (s *Service) Publish(base int, change ChangeSet) (*Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	head := s.versions[len(s.versions)-1]
	if base != head.id {
		return nil, ErrStaleBase
	}
	return s.appendLocked(head.snap, change)
}

// Submit applies a change set authored against base.
//
// When base is current this is a plain atomic publication. When other
// administrators have published versions in the meantime, the change is
// rebased over them. Automatic merge is allowed only when the dependency
// closure of the new change is disjoint from the closure of every intervening
// change (all evaluated on the common base graph); otherwise
// ErrDependencyConflict is returned and no new version is created.
func (s *Service) Submit(base int, change ChangeSet) (*Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if base < 0 || base >= len(s.versions) {
		return nil, ErrUnknownVersion
	}
	baseEntry := s.versions[base]
	head := s.versions[len(s.versions)-1]

	// Validate semantics and cycle rejection against the author's base.
	if _, err := baseEntry.snap.apply(change); err != nil {
		return nil, err
	}

	if base == head.id {
		return s.appendLocked(baseEntry.snap, change)
	}

	newClosure := baseEntry.snap.closure(change)
	for i := base + 1; i <= head.id; i++ {
		other := s.versions[i].change
		if intersects(newClosure, baseEntry.snap.closure(other)) {
			return nil, ErrDependencyConflict
		}
	}

	// Disjoint closures mean the rebased operations remain valid and cannot
	// introduce a cycle; apply still re-runs every check defensively.
	return s.appendLocked(head.snap, change)
}

// appendLocked builds the complete new snapshot first and only then appends it,
// so the version switch is a single atomic pointer publication.
func (s *Service) appendLocked(parent *snapshot, change ChangeSet) (*Version, error) {
	next, err := parent.apply(change)
	if err != nil {
		return nil, err
	}
	e := &entry{id: len(s.versions), snap: next, change: change}
	s.versions = append(s.versions, e)
	return &Version{id: e.id, snap: e.snap}, nil
}

func intersects(a, b map[Role]struct{}) bool {
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	for r := range small {
		if _, ok := large[r]; ok {
			return true
		}
	}
	return false
}
