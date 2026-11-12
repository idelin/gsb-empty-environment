package netsec

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Clock returns the time used for expiry checks. In production callers pass
// time.Now; tests inject a controllable clock to reproduce freeze attacks.
type Clock func() time.Time

// Store holds the client's currently trusted TUF metadata. It is safe for
// concurrent use by a single download flow's goroutines.
type Store struct {
	mu    sync.Mutex
	clock Clock

	root      *Root
	timestamp *Timestamp
	snapshot  *Snapshot
	targets   *Targets
}

// NewStore bootstraps trust from root metadata obtained out of band (shipped
// with the client or verified during first install). The bootstrap root itself
// is not signature-checked because it is the initial trust anchor; it must be
// well-formed and unexpired relative to the clock.
func NewStore(rootBytes []byte, clock Clock) (*Store, error) {
	if clock == nil {
		clock = time.Now
	}
	_, root, _, err := parseEnvelope[*Root](rootBytes, RootRole)
	if err != nil {
		return nil, err
	}
	if err := root.wellFormed(); err != nil {
		return nil, err
	}
	if err := checkFreshness(clock(), root.Expires); err != nil {
		return nil, err
	}
	return &Store{
		clock: clock,
		root:  root,
	}, nil
}

// UpdateRoot installs a rotated root. The new root must satisfy all of:
//   - well-formed and unexpired against the injected clock;
//   - version exactly one greater than the currently trusted root;
//   - threshold of the root role reached under the OLD root's keys;
//   - threshold of the root role reached under the NEW root's own keys.
//
// On success the cached timestamp and snapshot are discarded because the key
// and version policies they were validated under may have changed.
func (s *Store) UpdateRoot(newRootBytes []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	env, newRoot, signed, err := parseEnvelope[*Root](newRootBytes, RootRole)
	if err != nil {
		return err
	}
	if err := newRoot.wellFormed(); err != nil {
		return err
	}
	if err := checkFreshness(s.clock(), newRoot.Expires); err != nil {
		return err
	}
	if newRoot.Version != s.root.Version+1 {
		return fmt.Errorf("%w: root version %d does not follow trusted version %d",
			ErrVersion, newRoot.Version, s.root.Version)
	}
	// Signatures must reach the threshold under BOTH root key sets.
	if err := verifyThreshold(RootRole, signed, env, s.root); err != nil {
		return err
	}
	if err := verifyThreshold(RootRole, signed, env, newRoot); err != nil {
		return err
	}

	s.root = newRoot
	// Rotation invalidates roles delegated by the old root.
	s.timestamp = nil
	s.snapshot = nil
	return nil
}

// UpdateTimestamp installs fresh timestamp metadata: it must be signed to the
// timestamp threshold, unexpired, and have a version no lower than the stored
// one.
func (s *Store) UpdateTimestamp(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	env, ts, signed, err := parseEnvelope[*Timestamp](data, TimestampRole)
	if err != nil {
		return err
	}
	if err := checkFreshness(s.clock(), ts.Expires); err != nil {
		return err
	}
	if s.timestamp != nil && ts.Version < s.timestamp.Version {
		return fmt.Errorf("%w: timestamp version %d below trusted %d",
			ErrRollback, ts.Version, s.timestamp.Version)
	}
	if err := verifyThreshold(TimestampRole, signed, env, s.root); err != nil {
		return err
	}
	s.timestamp = ts
	return nil
}

// UpdateSnapshot installs fresh snapshot metadata. Beyond the normal
// freshness/rollback/threshold checks it must match the snapshot pin recorded
// by the trusted timestamp (version and, when present, hash/length).
func (s *Store) UpdateSnapshot(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.timestamp == nil {
		return fmt.Errorf("%w: timestamp must be installed before snapshot", ErrUnavailable)
	}
	env, snap, signed, err := parseEnvelope[*Snapshot](data, SnapshotRole)
	if err != nil {
		return err
	}
	if err := checkFreshness(s.clock(), snap.Expires); err != nil {
		return err
	}
	if s.snapshot != nil && snap.Version < s.snapshot.Version {
		return fmt.Errorf("%w: snapshot version %d below trusted %d",
			ErrRollback, snap.Version, s.snapshot.Version)
	}
	if err := verifyThreshold(SnapshotRole, signed, env, s.root); err != nil {
		return err
	}
	pin, ok := s.timestamp.Meta[SnapshotFile]
	if !ok {
		return fmt.Errorf("%w: timestamp has no %s record", ErrMalformed, SnapshotFile)
	}
	if pin.Version != nil && *pin.Version != snap.Version {
		return fmt.Errorf("%w: snapshot version %d != timestamp pin %d",
			ErrRollback, snap.Version, *pin.Version)
	}
	if err := verifyFileMeta(pin, data); err != nil {
		return err
	}

	s.snapshot = snap
	return nil
}

// UpdateTargets installs targets metadata for the given targets role file name
// (TargetsFile for the top-level role). It must satisfy the targets threshold
// and match the version/hash pin recorded for it inside snapshot.
func (s *Store) UpdateTargets(roleFile string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.snapshot == nil {
		return fmt.Errorf("%w: snapshot must be installed before targets", ErrUnavailable)
	}
	env, targs, signed, err := parseEnvelope[*Targets](data, TargetsRole)
	if err != nil {
		return err
	}
	if err := checkFreshness(s.clock(), targs.Expires); err != nil {
		return err
	}
	if s.targets != nil && targs.Version < s.targets.Version {
		return fmt.Errorf("%w: targets version %d below trusted %d",
			ErrRollback, targs.Version, s.targets.Version)
	}
	if err := verifyThreshold(TargetsRole, signed, env, s.root); err != nil {
		return err
	}
	pin, ok := s.snapshot.Meta[roleFile]
	if !ok {
		return fmt.Errorf("%w: snapshot has no record for %q", ErrMalformed, roleFile)
	}
	if pin.Version != nil && *pin.Version != targs.Version {
		return fmt.Errorf("%w: targets version %d != snapshot pin %d",
			ErrRollback, targs.Version, *pin.Version)
	}
	if err := verifyFileMeta(pin, data); err != nil {
		return err
	}

	if roleFile == TargetsFile {
		s.targets = targs
	}
	return nil
}

// VerifyTarget checks downloaded target-file bytes against the hash and length
// recorded in the trusted top-level targets metadata.
func (s *Store) VerifyTarget(name string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.targets == nil {
		return fmt.Errorf("%w: targets metadata not installed", ErrUnavailable)
	}
	meta, ok := s.targets.Targets[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return verifyFileMeta(meta, data)
}

// RootVersion reports the version of the trusted root.
func (s *Store) RootVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Version
}

// TimestampVersion reports the trusted timestamp version, or 0 if absent.
func (s *Store) TimestampVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timestamp == nil {
		return 0
	}
	return s.timestamp.Version
}

// SnapshotVersion reports the trusted snapshot version, or 0 if absent.
func (s *Store) SnapshotVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot == nil {
		return 0
	}
	return s.snapshot.Version
}

// TargetsVersion reports the trusted targets version, or 0 if absent.
func (s *Store) TargetsVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.targets == nil {
		return 0
	}
	return s.targets.Version
}

// HasTimestamp reports whether timestamp metadata is currently installed.
func (s *Store) HasTimestamp() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timestamp != nil
}

// HasSnapshot reports whether snapshot metadata is currently installed.
func (s *Store) HasSnapshot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot != nil
}

// wellFormed validates the structural invariants of root metadata required for
// the rest of the verifier to make trust decisions safely.
func (r *Root) wellFormed() error {
	for _, role := range []string{RootRole, TimestampRole, SnapshotRole, TargetsRole} {
		roleDef, ok := r.Roles[role]
		if !ok {
			return fmt.Errorf("%w: root missing role %q", ErrMalformed, role)
		}
		if roleDef.Threshold < 1 {
			return fmt.Errorf("%w: non-positive threshold for role %q", ErrMalformed, role)
		}
		if int(roleDef.Threshold) > len(roleDef.KeyIDs) {
			return fmt.Errorf("%w: threshold exceeds number of keys for role %q",
				ErrMalformed, role)
		}
		for _, id := range roleDef.KeyIDs {
			key, ok := r.Keys[id]
			if !ok {
				return fmt.Errorf("%w: role %q references unknown key %q",
					ErrMalformed, role, id)
			}
			if key.Type != KeyTypeEd25519 {
				return fmt.Errorf("%w: key %q uses unsupported type %q",
					ErrUnsupported, id, key.Type)
			}
			raw, err := hex.DecodeString(key.Value)
			if err != nil || len(raw) == 0 {
				return fmt.Errorf("%w: key %q has malformed public value", ErrMalformed, id)
			}
		}
	}
	return nil
}
