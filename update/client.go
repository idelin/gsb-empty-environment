// Package update implements a TUF-inspired secure software update client.
//
// The client persists a trusted root and the metadata derived from it. Root
// rotation requires the proposed root to satisfy both the old and the new
// role thresholds, counting each public-key alias at most once. Every
// metadata refresh is validated inside one storage transaction: versions,
// expirations and snapshot digests are all checked before the single atomic
// commit, so an interrupted or rejected update cannot leave mixed
// generations on disk.
package update

import (
	"context"
	"fmt"
	"time"
)

// Bundle is the set of canonical metadata envelopes offered by a mirror for
// one refresh attempt. Targets maps target path to its targets envelope.
type Bundle struct {
	Root      *Envelope
	Timestamp *Envelope
	Snapshot  *Envelope
	Targets   map[string]*Envelope
}

// Clock allows tests to control expiry evaluation.
type Clock func() time.Time

// Client runs secure updates against one durable Store.
type Client struct {
	store Store
	now   Clock
}

// NewClient opens a client over store. The default clock is time.Now.
func NewClient(store Store) *Client {
	return &Client{store: store, now: time.Now}
}

// WithClock returns a copy of the client using an injected time source.
func (c *Client) WithClock(clock Clock) *Client {
	clone := *c
	clone.now = clock
	return &clone
}

// Bootstrap installs the out-of-band trusted root onto an empty store. The
// root must be self-consistent and self-signed at its own root threshold.
// Bootstrapping an already populated store is rejected.
func (c *Client) Bootstrap(ctx context.Context, rootEnvelope *Envelope) error {
	if rootEnvelope == nil {
		return fmt.Errorf("%w: root", ErrMissingMetadata)
	}
	root, err := parseRoot(rootEnvelope)
	if err != nil {
		return err
	}
	if err := requireFresh(root.Expires, c.now()); err != nil {
		return err
	}
	if err := verifyRole(root, RoleRoot, rootEnvelope); err != nil {
		return err
	}

	tx, err := c.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Discard()

	if existing, ok := tx.Get(stateRoot); ok && len(existing) > 0 {
		return ErrAlreadyBootstrapped
	}
	tx.Put(stateRoot, rootEnvelope.Bytes())
	return tx.Commit(ctx)
}

// Refresh validates bundle and, only if every check passes, commits the new
// trusted generation in a single atomic transaction.
//
// The transaction enforces, before commit:
//   - optional root rotation signed at both the old and the new thresholds;
//   - strict monotonic versions for root, timestamp, snapshot and targets;
//   - expiration of every accepted document;
//   - timestamp-pinned length and digests of the snapshot bytes;
//   - snapshot-pinned version, length and digests of every targets envelope.
func (c *Client) Refresh(ctx context.Context, bundle Bundle) error {
	if bundle.Timestamp == nil {
		return fmt.Errorf("%w: timestamp", ErrMissingMetadata)
	}
	if bundle.Snapshot == nil {
		return fmt.Errorf("%w: snapshot", ErrMissingMetadata)
	}

	tx, err := c.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Discard()

	now := c.now()

	// Load and parse the currently trusted root.
	trustedRootBytes, ok := tx.Get(stateRoot)
	if !ok {
		return ErrNoState
	}
	trustedRootEnvelope, err := ParseEnvelope(trustedRootBytes)
	if err != nil {
		return fmt.Errorf("update: reload trusted root: %w", err)
	}
	oldRoot, err := parseRoot(trustedRootEnvelope)
	if err != nil {
		return err
	}

	// Resolve the effective root for this refresh. Rotation is accepted
	// only when the candidate passes the threshold of both generations.
	effectiveRoot := oldRoot
	rootEnvelopeToCommit := (*Envelope)(nil)
	if bundle.Root != nil {
		if err := checkContext(ctx); err != nil {
			return err
		}
		newRoot, err := parseRoot(bundle.Root)
		if err != nil {
			return err
		}
		if newRoot.Version <= oldRoot.Version {
			return fmt.Errorf("%w: root version %d must exceed %d",
				ErrRollback, newRoot.Version, oldRoot.Version)
		}
		if err := requireFresh(newRoot.Expires, now); err != nil {
			return err
		}
		if err := verifyRole(oldRoot, RoleRoot, bundle.Root); err != nil {
			return fmt.Errorf("%w: old threshold: %w", ErrRootRotation, err)
		}
		if err := verifyRole(newRoot, RoleRoot, bundle.Root); err != nil {
			return fmt.Errorf("%w: new threshold: %w", ErrRootRotation, err)
		}
		effectiveRoot = newRoot
		rootEnvelopeToCommit = bundle.Root
	}

	// Timestamp: signature under the effective root, monotonic version,
	// fresh expiry and an in-date snapshot reference.
	if err := checkContext(ctx); err != nil {
		return err
	}
	timestamp, err := parseTimestamp(bundle.Timestamp)
	if err != nil {
		return err
	}
	if err := verifyRole(effectiveRoot, RoleTimestamp, bundle.Timestamp); err != nil {
		return err
	}
	if err := requireFresh(timestamp.Expires, now); err != nil {
		return err
	}

	// Snapshot: signature, version pinned by timestamp, expiry, length and
	// every advertised digest, all compared against the canonical bytes.
	if err := checkContext(ctx); err != nil {
		return err
	}
	snapshot, err := parseSnapshot(bundle.Snapshot)
	if err != nil {
		return err
	}
	if err := verifyRole(effectiveRoot, RoleSnapshot, bundle.Snapshot); err != nil {
		return err
	}
	if snapshot.Version != timestamp.Meta.Version {
		return fmt.Errorf("%w: snapshot version %d != timestamp-pinned %d",
			ErrRollback, snapshot.Version, timestamp.Meta.Version)
	}
	if err := requireFresh(snapshot.Expires, now); err != nil {
		return err
	}
	if err := verifyDigests(timestamp.Meta, bundle.Snapshot.Bytes()); err != nil {
		return err
	}

	// Targets: one envelope per snapshot entry and vice versa. Each is
	// signed, fresh, monotonic against the cached generation, and matches
	// the version/length/digests pinned by the snapshot.
	if len(bundle.Targets) != len(snapshot.Meta) {
		return fmt.Errorf("%w: snapshot lists %d targets, bundle carries %d",
			ErrMissingMetadata, len(snapshot.Meta), len(bundle.Targets))
	}
	for targetPath, targetsEnvelope := range bundle.Targets {
		if err := checkContext(ctx); err != nil {
			return err
		}
		ref, ok := snapshot.Meta[targetPath]
		if !ok {
			return fmt.Errorf("%w: snapshot has no reference for %q", ErrMissingMetadata, targetPath)
		}
		targets, err := parseTargets(targetsEnvelope)
		if err != nil {
			return err
		}
		if err := verifyRole(effectiveRoot, RoleTargets, targetsEnvelope); err != nil {
			return err
		}
		if err := requireFresh(targets.Expires, now); err != nil {
			return err
		}
		if targets.Version != ref.Version {
			return fmt.Errorf("%w: targets %q version %d != snapshot-pinned %d",
				ErrRollback, targetPath, targets.Version, ref.Version)
		}
		if err := verifyDigests(ref, targetsEnvelope.Bytes()); err != nil {
			return fmt.Errorf("update: targets %q envelope: %w", targetPath, err)
		}
	}

	// Monotonicity is checked as one transactional guard after every
	// cross-document version and digest link has been validated.
	if trusted, ok := tx.Get(stateTimestamp); ok {
		previous, err := parseTimestamp(mustParse(trusted))
		if err != nil {
			return err
		}
		if timestamp.Version <= previous.Version {
			return fmt.Errorf("%w: timestamp version %d must exceed %d",
				ErrRollback, timestamp.Version, previous.Version)
		}
	}
	if trusted, ok := tx.Get(stateSnapshot); ok {
		previous, err := parseSnapshot(mustParse(trusted))
		if err != nil {
			return err
		}
		if snapshot.Version <= previous.Version {
			return fmt.Errorf("%w: snapshot version %d must exceed %d",
				ErrRollback, snapshot.Version, previous.Version)
		}
	}
	for targetPath, targetsEnvelope := range bundle.Targets {
		cached, ok := tx.Get(targetsKey(targetPath))
		if !ok {
			continue
		}
		previous, err := parseTargets(mustParse(cached))
		if err != nil {
			return err
		}
		targets, err := parseTargets(targetsEnvelope)
		if err != nil {
			return err
		}
		if targets.Version <= previous.Version {
			return fmt.Errorf("%w: targets %q version %d must exceed %d",
				ErrRollback, targetPath, targets.Version, previous.Version)
		}
	}

	// All validation has succeeded. Stage the complete next generation and
	// publish it with one atomic commit so no partial state can survive.
	if rootEnvelopeToCommit != nil {
		tx.Put(stateRoot, rootEnvelopeToCommit.Bytes())
	}
	tx.Put(stateTimestamp, bundle.Timestamp.Bytes())
	tx.Put(stateSnapshot, bundle.Snapshot.Bytes())
	for targetPath, targetsEnvelope := range bundle.Targets {
		tx.Put(targetsKey(targetPath), targetsEnvelope.Bytes())
	}
	if err := checkContext(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifyTarget checks a downloaded payload against the cached, verified
// targets metadata and re-evaluates timestamp freshness, rejecting stale
// timestamp metadata even when the payload itself matches.
func (c *Client) VerifyTarget(ctx context.Context, targetPath string, payload []byte) (*TargetFile, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Discard()

	now := c.now()

	timestampBytes, ok := tx.Get(stateTimestamp)
	if !ok {
		return nil, fmt.Errorf("%w: timestamp", ErrMissingMetadata)
	}
	timestamp, err := parseTimestamp(mustParse(timestampBytes))
	if err != nil {
		return nil, err
	}
	if err := requireFresh(timestamp.Expires, now); err != nil {
		return nil, err
	}

	targetsBytes, ok := tx.Get(targetsKey(targetPath))
	if !ok {
		return nil, fmt.Errorf("%w: targets %q", ErrMissingMetadata, targetPath)
	}
	targetsEnvelope, err := ParseEnvelope(targetsBytes)
	if err != nil {
		return nil, err
	}
	targets, err := parseTargets(targetsEnvelope)
	if err != nil {
		return nil, err
	}
	if err := requireFresh(targets.Expires, now); err != nil {
		return nil, err
	}
	ref := MetaRef{Version: targets.Version, Length: targets.Length, Hashes: targets.Hashes}
	if err := verifyDigests(ref, payload); err != nil {
		return nil, err
	}
	return targets, nil
}

// TrustedRootVersion returns the version of the currently persisted root.
func (c *Client) TrustedRootVersion(ctx context.Context) (int64, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Discard()
	raw, ok := tx.Get(stateRoot)
	if !ok {
		return 0, ErrNoState
	}
	root, err := parseRoot(mustParse(raw))
	if err != nil {
		return 0, err
	}
	return root.Version, nil
}

func mustParse(raw []byte) *Envelope {
	envelope, err := ParseEnvelope(raw)
	if err != nil {
		panic(fmt.Sprintf("update: stored metadata is not a valid envelope: %v", err))
	}
	return envelope
}

func requireFresh(expires, now time.Time) error {
	if !expires.After(now) {
		return fmt.Errorf("%w: expired at %s, current time %s", ErrExpired, expires.UTC(), now.UTC())
	}
	return nil
}

func checkContext(ctx context.Context) error {
	return ctx.Err()
}
