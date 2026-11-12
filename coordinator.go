package netsec

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Clock supplies time to the coordinator. Tests inject a controllable clock
// to make lease expiry deterministic.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// ReplicaScanResult reports every key copy currently reachable for a volume.
// Replicas listed in Manifest are expected copies; anything else is an
// unknown copy that blocks destruction.
type ReplicaScanResult struct {
	Replicas []Replica
}

// ReplicaScanner observes the currently reachable key copies of a volume.
// Hosts integrate their storage discovery here.
type ReplicaScanner interface {
	ScanReplicas(ctx context.Context, id VolumeID, name string) (ReplicaScanResult, error)
}

// Eraser destroys the key material at one persisted replica location. The
// host is expected to overwrite or crypto-shred the material; the coordinator
// treats a nil return as a durable erasure confirmation.
type Eraser interface {
	Erase(ctx context.Context, id VolumeID, replica Replica) error
}

// CoordinatorOption configures a Coordinator at construction.
type CoordinatorOption func(*Coordinator)

// WithClock overrides the time source.
func WithClock(clock Clock) CoordinatorOption {
	return func(c *Coordinator) { c.clock = clock }
}

// WithScanner installs the replica observer used during reconciliation.
func WithScanner(scanner ReplicaScanner) CoordinatorOption {
	return func(c *Coordinator) { c.scanner = scanner }
}

// WithEraser installs the host erasure backend.
func WithEraser(eraser Eraser) CoordinatorOption {
	return func(c *Coordinator) { c.eraser = eraser }
}

// Coordinator is the volume-host-facing facade for key destruction
// coordination. It is safe for concurrent use.
type Coordinator struct {
	store   BlobStore
	clock   Clock
	scanner ReplicaScanner
	eraser  Eraser

	// mu serializes coordinator-level transitions. Individual operations
	// still read through the store, so coordinators restarted in different
	// processes coordinate purely through durable state.
	mu sync.Mutex
}

// NewCoordinator builds a coordinator over store and resumes every durable
// destruction transaction found there. A volume found mid-destruction is never
// restored to a usable state: its frozen flag is reasserted.
func NewCoordinator(store BlobStore, opts ...CoordinatorOption) (*Coordinator, error) {
	c := &Coordinator{
		store: store,
		clock: systemClock{},
	}
	for _, opt := range opts {
		opt(c)
	}
	if err := c.recoverAll(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// Volume is a handle to one volume generation.
type Volume struct {
	coordinator *Coordinator
	manifest    *volumeManifest
}

// ID returns the generation identifier.
func (v *Volume) ID() VolumeID { return v.manifest.ID }

// Name returns the human-assigned volume name.
func (v *Volume) Name() string { return v.manifest.Name }

// Replicas returns the manifest key copies.
func (v *Volume) Replicas() []Replica {
	out := make([]Replica, len(v.manifest.Replicas))
	copy(out, v.manifest.Replicas)
	return out
}

// Frozen reports whether new leases are permanently prohibited.
func (v *Volume) Frozen() bool { return v.manifest.Frozen }

// CreateVolume registers a new volume generation with its key replicas.
func (c *Coordinator) CreateVolume(ctx context.Context, name string, replicas []Replica) (*Volume, error) {
	if name == "" {
		return nil, ErrInvalidArgument
	}
	if len(replicas) == 0 {
		return nil, ErrInvalidArgument
	}
	if err := validateReplicas(replicas); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	existing, err := c.volumeIDByNameLocked(ctx, name)
	if err == nil && existing != "" && string(existing) != nameTombstone {
		return nil, ErrNameInUse
	} else if err != nil && !isNotFound(err) {
		return nil, err
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	manifest := &volumeManifest{
		ID:        VolumeID(id),
		Name:      name,
		Replicas:  append([]Replica(nil), replicas...),
		CreatedAt: c.clock.Now(),
	}
	if err := putJSON(ctx, c.store, manifestKey(manifest.ID), manifest); err != nil {
		return nil, err
	}
	if err := c.store.Put(ctx, nameKey(name), []byte(manifest.ID)); err != nil {
		return nil, err
	}
	return &Volume{coordinator: c, manifest: manifest}, nil
}

// OpenVolume resolves a live volume by name. Destroying or destroyed
// generations are not openable: they can never serve key material again.
func (c *Coordinator) OpenVolume(ctx context.Context, name string) (*Volume, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.volumeIDByNameLocked(ctx, name)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrVolumeNotFound
		}
		return nil, err
	}
	if string(id) == nameTombstone {
		return nil, ErrVolumeDestroyed
	}
	return c.openVolumeByIDLocked(ctx, id)
}

// VolumeByID resolves a volume generation directly.
func (c *Coordinator) VolumeByID(ctx context.Context, id VolumeID) (*Volume, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openVolumeByIDLocked(ctx, id)
}

func (c *Coordinator) openVolumeByIDLocked(ctx context.Context, id VolumeID) (*Volume, error) {
	if id == "" {
		return nil, ErrVolumeNotFound
	}
	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(id))
	if err != nil {
		if isNotFound(err) {
			return nil, ErrVolumeNotFound
		}
		return nil, err
	}
	record, err := c.loadRecordLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if record != nil {
		return nil, ErrVolumeDestroyed
	}
	return &Volume{coordinator: c, manifest: manifest}, nil
}

func (c *Coordinator) volumeIDByNameLocked(ctx context.Context, name string) (VolumeID, error) {
	data, err := c.store.Get(ctx, nameKey(name))
	if err != nil {
		if isNotFound(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return VolumeID(data), nil
}

// GrantLease issues a lease with ttl. Once destruction starts the volume is
// frozen and every grant fails with ErrLeasesProhibited.
func (v *Volume) GrantLease(ctx context.Context, ttl time.Duration) (*Lease, error) {
	if ttl <= 0 {
		return nil, ErrInvalidArgument
	}
	c := v.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	manifest, err := getJSON[volumeManifest](ctx, c.store, manifestKey(v.manifest.ID))
	if err != nil {
		return nil, err
	}
	v.manifest = manifest
	if manifest.Frozen {
		return nil, ErrLeasesProhibited
	}
	record, err := c.loadRecordLocked(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	if record != nil {
		return nil, ErrLeasesProhibited
	}

	leaseID, err := newID()
	if err != nil {
		return nil, err
	}
	now := c.clock.Now()
	lease := &Lease{
		ID:        leaseID,
		VolumeID:  manifest.ID,
		IssuedAt:  now,
		ExpiresAt: now.Add(ttl),
	}
	if err := putJSON(ctx, c.store, leaseKey(manifest.ID, lease.ID), lease); err != nil {
		return nil, err
	}
	return lease, nil
}

// ReleaseLease ends a lease ahead of its expiry. Releasing an already-gone
// lease is idempotent and succeeds.
func (v *Volume) ReleaseLease(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return ErrInvalidArgument
	}
	c := v.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()

	key := leaseKey(v.manifest.ID, leaseID)
	if _, err := c.store.Get(ctx, key); err != nil {
		if isNotFound(err) {
			// Releasing an already-gone lease is idempotent: both explicit
			// release and drain converge on the same durable state.
			return nil
		}
		return err
	}
	return c.store.Delete(ctx, key)
}

// ActiveLeases returns leases that have not expired at the coordinator clock.
func (v *Volume) ActiveLeases(ctx context.Context) ([]Lease, error) {
	c := v.coordinator
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activeLeasesLocked(ctx, v.manifest.ID)
}

func (c *Coordinator) activeLeasesLocked(ctx context.Context, id VolumeID) ([]Lease, error) {
	keys, err := c.store.List(ctx, leasePrefix(id))
	if err != nil {
		return nil, err
	}
	now := c.clock.Now()
	var active []Lease
	for _, key := range keys {
		lease, err := getJSON[Lease](ctx, c.store, key)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		if lease.Active(now) {
			active = append(active, *lease)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	return active, nil
}

func validateReplicas(replicas []Replica) error {
	seen := make(map[ReplicaID]bool, len(replicas))
	for _, replica := range replicas {
		if replica.ID == "" || replica.Location == "" {
			return ErrInvalidArgument
		}
		if seen[replica.ID] {
			return ErrInvalidArgument
		}
		seen[replica.ID] = true
	}
	return nil
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
