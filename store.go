// Package objstore implements an object library with soft deletion.
//
// Lifecycle: an object starts Active. Delete moves it to PendingDeletion and
// schedules a cleanup task at now+gracePeriod. During the grace period a new
// valid reference (AddRef) revokes the pending deletion, but the object's
// generation is incremented; cleanup tasks are bound to the generation that
// scheduled them, so a stale task can never delete a revived object. Once the
// grace period elapses the cleanup task hard-deletes the object; afterwards
// AddRef fails with ErrObjectDeleted and never implicitly recreates it.
package objstore

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	// ErrObjectNotFound is returned when the object id has never existed.
	ErrObjectNotFound = errors.New("objstore: object not found")
	// ErrObjectExists is returned by Create when the object is live.
	ErrObjectExists = errors.New("objstore: object already exists")
	// ErrObjectDeleted is returned when the object was hard-deleted.
	// References must fail rather than implicitly rebuild the object.
	ErrObjectDeleted = errors.New("objstore: object permanently deleted")
	// ErrRefNotFound is returned when removing an unknown reference.
	ErrRefNotFound = errors.New("objstore: reference not found")
)

// State is the lifecycle state of an object.
type State int

const (
	// StateActive means the object is fully usable.
	StateActive State = iota
	// StatePendingDeletion means the object is inside its deletion grace
	// period and can still be revived by a new reference.
	StatePendingDeletion
	// StateDeleted means the object is gone for good; only an explicit
	// Create may start a new incarnation.
	StateDeleted
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StatePendingDeletion:
		return "pending-deletion"
	case StateDeleted:
		return "deleted"
	}
	return "unknown"
}

// Info is a point-in-time snapshot of an object, used by callers that
// reconcile the reference ledger against object state.
type Info struct {
	ID         string
	State      State
	Generation uint64
	RefCount   int
	// DeleteAt is the scheduled hard-deletion time; zero unless the object
	// is in StatePendingDeletion.
	DeleteAt time.Time
}

type object struct {
	generation uint64
	state      State
	refs       map[string]struct{}
	deleteAt   time.Time
}

// Store is a concurrency-safe object library keyed by string id.
type Store struct {
	mu      sync.Mutex
	clock   Clock
	grace   time.Duration
	objects map[string]*object
}

// NewStore returns a Store using clock for all time decisions and
// gracePeriod as the deletion grace period.
func NewStore(clock Clock, gracePeriod time.Duration) *Store {
	return &Store{clock: clock, grace: gracePeriod, objects: make(map[string]*object)}
}

// Create registers a new object. It fails with ErrObjectExists if the object
// is live (active or pending deletion). If the object was hard-deleted,
// Create explicitly starts a new incarnation with an incremented generation;
// this is the only way back from StateDeleted.
func (s *Store) Create(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if obj, ok := s.objects[id]; ok {
		if obj.state == StateDeleted {
			obj.state = StateActive
			obj.generation++
			obj.refs = make(map[string]struct{})
			obj.deleteAt = time.Time{}
			return nil
		}
		return ErrObjectExists
	}
	s.objects[id] = &object{generation: 1, state: StateActive, refs: make(map[string]struct{})}
	return nil
}

// AddRef attaches reference ref to object id. If the object is in its
// deletion grace period, the valid new reference revokes the pending
// deletion and bumps the generation, invalidating every cleanup task
// scheduled for older generations. If the object was hard-deleted, AddRef
// fails with ErrObjectDeleted; it never implicitly recreates the object.
// Adding the same ref twice is idempotent.
func (s *Store) AddRef(id, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	switch obj.state {
	case StateDeleted:
		return ErrObjectDeleted
	case StatePendingDeletion:
		obj.state = StateActive
		obj.generation++
		obj.deleteAt = time.Time{}
	}
	obj.refs[ref] = struct{}{}
	return nil
}

// RemoveRef detaches reference ref from object id.
func (s *Store) RemoveRef(id, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	if obj.state == StateDeleted {
		return ErrObjectDeleted
	}
	if _, ok := obj.refs[ref]; !ok {
		return ErrRefNotFound
	}
	delete(obj.refs, ref)
	return nil
}

// Delete moves a live object into the pending-deletion state and schedules a
// cleanup task bound to the current generation at now+gracePeriod. Deleting
// an already pending object is a no-op and does not extend the grace period.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	obj, ok := s.objects[id]
	if !ok {
		s.mu.Unlock()
		return ErrObjectNotFound
	}
	switch obj.state {
	case StateDeleted:
		s.mu.Unlock()
		return ErrObjectDeleted
	case StatePendingDeletion:
		s.mu.Unlock()
		return nil
	}
	obj.state = StatePendingDeletion
	obj.deleteAt = s.clock.Now().Add(s.grace)
	generation := obj.generation
	s.mu.Unlock()
	s.clock.AfterFunc(s.grace, func() { s.cleanup(id, generation) })
	return nil
}

// cleanup hard-deletes id, but only if the object is still pending deletion
// at the exact generation that scheduled this task and the grace deadline
// has actually been reached. A task from a superseded generation is a no-op.
func (s *Store) cleanup(id string, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok || obj.state != StatePendingDeletion {
		return
	}
	if obj.generation != generation {
		return
	}
	if s.clock.Now().Before(obj.deleteAt) {
		return
	}
	obj.state = StateDeleted
	obj.refs = make(map[string]struct{})
	obj.deleteAt = time.Time{}
}

// Reconcile sweeps the ledger and hard-deletes every object whose grace
// period has already elapsed but whose cleanup task never ran (for example
// after a restart that lost scheduled timers). It returns the ids it purged.
func (s *Store) Reconcile() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	var purged []string
	for id, obj := range s.objects {
		if obj.state == StatePendingDeletion && !now.Before(obj.deleteAt) {
			obj.state = StateDeleted
			obj.refs = make(map[string]struct{})
			obj.deleteAt = time.Time{}
			purged = append(purged, id)
		}
	}
	sort.Strings(purged)
	return purged
}

// Get returns a snapshot of object id.
func (s *Store) Get(id string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return Info{}, ErrObjectNotFound
	}
	return Info{
		ID:         id,
		State:      obj.state,
		Generation: obj.generation,
		RefCount:   len(obj.refs),
		DeleteAt:   obj.deleteAt,
	}, nil
}
