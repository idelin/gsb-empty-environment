package migsim

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrUnknownBlock    = errors.New("migsim: unknown block")
	ErrBlockExists     = errors.New("migsim: block already exists")
	ErrUnknownVolume   = errors.New("migsim: unknown volume")
	ErrNoRefs          = errors.New("migsim: block has no references")
	ErrDeleted         = errors.New("migsim: block is deleted")
	ErrNotMigrating    = errors.New("migsim: block is not migrating")
	ErrMigrationExists = errors.New("migsim: migration already in progress")
	ErrSameVolume      = errors.New("migsim: migration target equals source volume")
	ErrSourceLost      = errors.New("migsim: source replica lost")
	ErrSourceMutated   = errors.New("migsim: source content mutated during copy")
	ErrDigestMismatch  = errors.New("migsim: copy digest mismatch")
	ErrCancelled       = errors.New("migsim: migration cancelled")
	ErrStaleReceipt    = errors.New("migsim: receipt matches no prepared migration")
	ErrReplicaGone     = errors.New("migsim: pinned replica is gone")
)

// migrationState tracks one in-flight copy from the current generation's
// replica to a staged replica on the destination volume.
type migrationState struct {
	fromGen   int
	toGen     int
	dst       string
	digest    Digest // source digest snapshot taken at BeginMigration
	cancelled bool
}

type blockState struct {
	refs      int
	gen       int
	replicas  map[int]ReplicaRef // live physical copies, keyed by generation
	handles   map[int]int        // open read handles, keyed by generation
	deleted   bool
	mig       *migrationState
	cancelGen int // toGen of the most recently cancelled migration
}

// Store manages logical blocks, their references, per-block generations and
// migrations between volumes. All methods are safe for concurrent use.
//
// Lock ordering: Store.mu -> (Ledger via events | Volume.mu). Subscribers
// must never call back into the Store.
type Store struct {
	mu      sync.Mutex
	vols    map[string]*Volume
	ordered []*Volume
	blocks  map[BlockID]*blockState
	subs    []func(Event)

	// CopyHook, if set, is invoked while a migration copy is in flight,
	// after the source has been read and before the staged copy is written.
	// Tests use it as a concurrency barrier.
	CopyHook func(BlockID)

	// Corrupt, if set, rewrites the bytes about to be staged. Tests use it
	// to inject copy faults; the digest check must then prevent the switch.
	Corrupt func(BlockID, []byte) []byte
}

// NewStore creates a Store over the given volumes.
func NewStore(vols ...*Volume) *Store {
	s := &Store{
		vols:   make(map[string]*Volume),
		blocks: make(map[BlockID]*blockState),
	}
	for _, v := range vols {
		s.vols[v.ID()] = v
		s.ordered = append(s.ordered, v)
	}
	return s
}

// Subscribe registers fn to receive every state-transition event in order.
// fn is called with the store lock held and must not call back into the Store.
func (s *Store) Subscribe(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

// Volumes returns the volumes backing the store, in construction order.
func (s *Store) Volumes() []*Volume { return s.ordered }

func replicaKey(id BlockID, gen int) string { return fmt.Sprintf("%s@%d", id, gen) }

func (s *Store) emitLocked(e Event) {
	for _, fn := range s.subs {
		fn(e)
	}
}

// Put creates a new block with content data on volume volID and an initial
// reference from obj. Block content is immutable for the block's lifetime.
func (s *Store) Put(id BlockID, obj ObjectID, data []byte, volID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.blocks[id]; ok {
		return ErrBlockExists
	}
	vol := s.vols[volID]
	if vol == nil {
		return ErrUnknownVolume
	}
	key := replicaKey(id, 0)
	vol.Put(key, data)
	ref := ReplicaRef{Volume: volID, Key: key}
	s.blocks[id] = &blockState{
		refs:     1,
		gen:      0,
		replicas: map[int]ReplicaRef{0: ref},
		handles:  make(map[int]int),
	}
	s.emitLocked(Event{Kind: EventBlockCreated, Block: id, Object: obj, Gen: 0, Replica: ref, Digest: DigestOf(data)})
	return nil
}

// AddRef adds a reference to id from obj. References may be added while a
// migration is in flight; they bind to the logical block, not a generation.
func (s *Store) AddRef(obj ObjectID, id BlockID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return ErrUnknownBlock
	}
	if bs.deleted {
		return ErrDeleted
	}
	bs.refs++
	s.emitLocked(Event{Kind: EventRefAdded, Block: id, Object: obj})
	return nil
}

// RemoveRef drops one reference to id. When the last reference is removed
// the block is deleted: any in-flight migration is cancelled and its
// replicas are reclaimed once pinned handles drain.
func (s *Store) RemoveRef(obj ObjectID, id BlockID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return ErrUnknownBlock
	}
	if bs.refs == 0 {
		return ErrNoRefs
	}
	bs.refs--
	s.emitLocked(Event{Kind: EventRefRemoved, Block: id, Object: obj})
	if bs.refs == 0 {
		bs.deleted = true
		if bs.mig != nil {
			bs.mig.cancelled = true
		}
		s.emitLocked(Event{Kind: EventBlockDeleted, Block: id})
	}
	return nil
}

// Open pins the block's current generation and returns a read handle to that
// specific replica. The replica is kept until the handle is closed, even if
// a migration switches the block to a newer generation meanwhile.
func (s *Store) Open(id BlockID) (*Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return nil, ErrUnknownBlock
	}
	if bs.deleted {
		return nil, ErrDeleted
	}
	gen := bs.gen
	ref := bs.replicas[gen]
	bs.handles[gen]++
	s.emitLocked(Event{Kind: EventHandleOpened, Block: id, Gen: gen, Replica: ref})
	return &Handle{store: s, block: id, gen: gen, vol: s.vols[ref.Volume], key: ref.Key}, nil
}

func (s *Store) closeHandle(h *Handle) {
	s.mu.Lock()
	if bs := s.blocks[h.block]; bs != nil {
		if n := bs.handles[h.gen]; n > 1 {
			bs.handles[h.gen] = n - 1
		} else {
			delete(bs.handles, h.gen)
		}
	}
	s.emitLocked(Event{Kind: EventHandleClosed, Block: h.block, Gen: h.gen})
	s.mu.Unlock()
	s.Reclaim()
}

// BeginMigration starts moving id's current generation to volume dstID. The
// source content is digest-snapshotted; the copy commits via Migrate.
func (s *Store) BeginMigration(id BlockID, dstID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return ErrUnknownBlock
	}
	if bs.deleted {
		return ErrDeleted
	}
	if bs.mig != nil {
		return ErrMigrationExists
	}
	dst := s.vols[dstID]
	if dst == nil {
		return ErrUnknownVolume
	}
	src := bs.replicas[bs.gen]
	if src.Volume == dstID {
		return ErrSameVolume
	}
	data, ok := s.vols[src.Volume].Get(src.Key)
	if !ok {
		return ErrSourceLost
	}
	bs.mig = &migrationState{
		fromGen: bs.gen,
		toGen:   bs.gen + 1,
		dst:     dstID,
		digest:  DigestOf(data),
	}
	s.emitLocked(Event{Kind: EventMigrationBegan, Block: id, Gen: bs.gen + 1})
	return nil
}

// Migrate performs the copy for the block's active migration: it reads the
// source, stages the copy on the destination volume, verifies the staged
// digest against the begin-time snapshot, re-verifies that the source did
// not mutate during the copy, and commits the switch by applying a receipt.
// Any digest failure or cancellation leaves the generation untouched.
func (s *Store) Migrate(id BlockID) error {
	s.mu.Lock()
	bs := s.blocks[id]
	if bs == nil {
		s.mu.Unlock()
		return ErrUnknownBlock
	}
	mig := bs.mig
	if mig == nil {
		s.mu.Unlock()
		return ErrNotMigrating
	}
	src := bs.replicas[mig.fromGen]
	s.mu.Unlock()

	srcVol := s.vols[src.Volume]
	data, ok := srcVol.Get(src.Key)
	if !ok {
		s.failMigration(id, mig)
		return ErrSourceLost
	}
	if s.CopyHook != nil {
		s.CopyHook(id)
	}
	if s.Corrupt != nil {
		data = s.Corrupt(id, data)
	}
	dstVol := s.vols[mig.dst]
	stagedKey := replicaKey(id, mig.toGen)
	dstVol.Put(stagedKey, data)

	copied, _ := dstVol.Get(stagedKey)
	if DigestOf(copied) != mig.digest {
		dstVol.Delete(stagedKey)
		s.failMigration(id, mig)
		return ErrDigestMismatch
	}
	again, ok := srcVol.Get(src.Key)
	if !ok || DigestOf(again) != mig.digest {
		dstVol.Delete(stagedKey)
		s.failMigration(id, mig)
		return ErrSourceMutated
	}
	return s.ApplyReceipt(Receipt{Block: id, FromGen: mig.fromGen, ToGen: mig.toGen, Digest: mig.digest})
}

// failMigration aborts mig if it is still the block's active migration.
func (s *Store) failMigration(id BlockID, mig *migrationState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bs := s.blocks[id]; bs != nil && bs.mig == mig {
		bs.mig = nil
		s.emitLocked(Event{Kind: EventMigrationFailed, Block: id, Gen: mig.toGen})
	}
}

// CancelMigration cancels the block's active migration. If the copy has not
// committed, the migration is torn down and any staged copy is removed; a
// concurrent Migrate will observe the cancellation and fail with
// ErrCancelled. Cancelling after a commit reports ErrNotMigrating and never
// reverts the switch.
func (s *Store) CancelMigration(id BlockID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return ErrUnknownBlock
	}
	mig := bs.mig
	if mig == nil {
		return ErrNotMigrating
	}
	mig.cancelled = true
	bs.cancelGen = mig.toGen
	s.vols[mig.dst].Delete(replicaKey(id, mig.toGen))
	bs.mig = nil
	s.emitLocked(Event{Kind: EventMigrationCancelled, Block: id, Gen: mig.toGen})
	return nil
}

// ApplyReceipt commits a prepared migration described by r. It is idempotent:
// receipts for generations at or below the block's current generation are
// stale replays and are ignored without effect. A receipt that matches the
// active migration re-verifies the staged copy's digest before switching;
// any mismatch or prior cancellation aborts without switching.
func (s *Store) ApplyReceipt(r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[r.Block]
	if bs == nil {
		return ErrUnknownBlock
	}
	if r.ToGen <= bs.gen {
		return nil // stale replay: already at or past this generation
	}
	mig := bs.mig
	if mig == nil {
		// The migration was finalized (cancelled) while its copy was in
		// flight; drop any orphaned staged copy.
		s.dropStagedLocked(r.Block, r.ToGen)
		if bs.cancelGen == r.ToGen {
			return ErrCancelled
		}
		return ErrStaleReceipt
	}
	if mig.fromGen != r.FromGen || mig.toGen != r.ToGen {
		return ErrStaleReceipt
	}
	stagedKey := replicaKey(r.Block, r.ToGen)
	dstVol := s.vols[mig.dst]
	if mig.cancelled || bs.deleted {
		dstVol.Delete(stagedKey)
		bs.mig = nil
		s.emitLocked(Event{Kind: EventMigrationCancelled, Block: r.Block, Gen: r.ToGen})
		return ErrCancelled
	}
	if r.Digest != mig.digest {
		dstVol.Delete(stagedKey)
		bs.mig = nil
		s.emitLocked(Event{Kind: EventMigrationFailed, Block: r.Block, Gen: r.ToGen})
		return ErrDigestMismatch
	}
	copied, ok := dstVol.Get(stagedKey)
	if !ok || DigestOf(copied) != r.Digest {
		dstVol.Delete(stagedKey)
		bs.mig = nil
		s.emitLocked(Event{Kind: EventMigrationFailed, Block: r.Block, Gen: r.ToGen})
		return ErrDigestMismatch
	}
	bs.gen = r.ToGen
	ref := ReplicaRef{Volume: mig.dst, Key: stagedKey}
	bs.replicas[r.ToGen] = ref
	bs.mig = nil
	s.emitLocked(Event{Kind: EventSwitched, Block: r.Block, Gen: r.ToGen, Replica: ref, Receipt: r, Digest: r.Digest})
	return nil
}

func (s *Store) dropStagedLocked(id BlockID, gen int) {
	key := replicaKey(id, gen)
	for _, v := range s.ordered {
		v.Delete(key)
	}
}

// Reclaim frees every replica that is neither the block's current generation
// (for live blocks) nor pinned by an open handle. Reclamation therefore
// always waits for old handles to be released.
func (s *Store) Reclaim() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, bs := range s.blocks {
		for gen, ref := range bs.replicas {
			if !bs.deleted && gen == bs.gen {
				continue
			}
			if bs.handles[gen] > 0 {
				continue
			}
			if vol := s.vols[ref.Volume]; vol != nil {
				vol.Delete(ref.Key)
			}
			delete(bs.replicas, gen)
			s.emitLocked(Event{Kind: EventReplicaFreed, Block: id, Gen: gen, Replica: ref})
		}
	}
}

// Generation returns the block's current generation.
func (s *Store) Generation(id BlockID) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return 0, ErrUnknownBlock
	}
	return bs.gen, nil
}

// Replica returns the replica of the block's current generation.
func (s *Store) Replica(id BlockID) (ReplicaRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bs := s.blocks[id]
	if bs == nil {
		return ReplicaRef{}, ErrUnknownBlock
	}
	return bs.replicas[bs.gen], nil
}

// Handle is a read handle pinned to one specific generation's replica.
type Handle struct {
	store *Store
	block BlockID
	gen   int
	vol   *Volume
	key   string
	once  sync.Once
}

// Block returns the handle's block.
func (h *Handle) Block() BlockID { return h.block }

// Gen returns the pinned generation.
func (h *Handle) Gen() int { return h.gen }

// Read returns the content of the pinned replica. It keeps reading the
// replica captured at Open time regardless of later generation switches.
func (h *Handle) Read() ([]byte, error) {
	b, ok := h.vol.Get(h.key)
	if !ok {
		return nil, ErrReplicaGone
	}
	return b, nil
}

// Close releases the pin on the replica, making it eligible for reclamation.
func (h *Handle) Close() {
	h.once.Do(func() { h.store.closeHandle(h) })
}
