// Package dircat implements an in-memory directory of objects indexed by
// id, unique name, and repeatable tags. A single dictionary (id -> Object)
// is the source of truth; all other indexes are derived from it and can be
// rebuilt for verification.
package dircat

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// Object is a directory entry. Tags may repeat across objects; Name is
// unique directory-wide.
type Object struct {
	ID   string
	Name string
	Tags []string
}

// normalize returns a copy of o with sorted, deduplicated tags.
func (o Object) normalize() Object {
	tags := make([]string, 0, len(o.Tags))
	seen := make(map[string]struct{}, len(o.Tags))
	for _, t := range o.Tags {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return Object{ID: o.ID, Name: o.Name, Tags: tags}
}

// state is immutable once published to a Directory. All readers that hold a
// *state observe a cross-index consistent snapshot.
type state struct {
	objects map[string]Object              // id -> object (source of truth)
	byName  map[string]string              // name -> id (unique)
	byTag   map[string]map[string]struct{} // tag -> set of ids
	tagsOf  map[string]map[string]struct{} // id -> set of tags (reverse refs)
}

func newState() *state {
	return &state{
		objects: make(map[string]Object),
		byName:  make(map[string]string),
		byTag:   make(map[string]map[string]struct{}),
		tagsOf:  make(map[string]map[string]struct{}),
	}
}

// clone deep-copies every map so the copy can be mutated without affecting
// the published state.
func (s *state) clone() *state {
	next := &state{
		objects: make(map[string]Object, len(s.objects)),
		byName:  make(map[string]string, len(s.byName)),
		byTag:   make(map[string]map[string]struct{}, len(s.byTag)),
		tagsOf:  make(map[string]map[string]struct{}, len(s.tagsOf)),
	}
	for id, o := range s.objects {
		next.objects[id] = o
	}
	for name, id := range s.byName {
		next.byName[name] = id
	}
	for tag, ids := range s.byTag {
		set := make(map[string]struct{}, len(ids))
		for id := range ids {
			set[id] = struct{}{}
		}
		next.byTag[tag] = set
	}
	for id, tags := range s.tagsOf {
		set := make(map[string]struct{}, len(tags))
		for t := range tags {
			set[t] = struct{}{}
		}
		next.tagsOf[id] = set
	}
	return next
}

// unbind removes id and all of its forward/reverse index references.
func (s *state) unbind(id string) {
	o, ok := s.objects[id]
	if !ok {
		return
	}
	delete(s.objects, id)
	delete(s.byName, o.Name)
	for t := range s.tagsOf[id] {
		ids := s.byTag[t]
		delete(ids, id)
		if len(ids) == 0 {
			delete(s.byTag, t)
		}
	}
	delete(s.tagsOf, id)
}

// bind inserts o into the dictionary and every index. Callers must ensure
// o.Name is free.
func (s *state) bind(o Object) {
	s.objects[o.ID] = o
	s.byName[o.Name] = o.ID
	if len(o.Tags) > 0 {
		set := make(map[string]struct{}, len(o.Tags))
		for _, t := range o.Tags {
			set[t] = struct{}{}
			ids := s.byTag[t]
			if ids == nil {
				ids = make(map[string]struct{})
				s.byTag[t] = ids
			}
			ids[o.ID] = struct{}{}
		}
		s.tagsOf[o.ID] = set
	}
}

// rebuildIndexes derives every index from the objects dictionary alone.
// It is the independent recomputation used to cross-check live indexes.
func rebuildIndexes(objects map[string]Object) (byName map[string]string, byTag, tagsOf map[string]map[string]struct{}, err error) {
	byName = make(map[string]string, len(objects))
	byTag = make(map[string]map[string]struct{})
	tagsOf = make(map[string]map[string]struct{})
	for _, o := range objects {
		if prev, dup := byName[o.Name]; dup {
			return nil, nil, nil, fmt.Errorf("name %q held by both %q and %q", o.Name, prev, o.ID)
		}
		byName[o.Name] = o.ID
		if len(o.Tags) > 0 {
			set := make(map[string]struct{}, len(o.Tags))
			for _, t := range o.Tags {
				set[t] = struct{}{}
				ids := byTag[t]
				if ids == nil {
					ids = make(map[string]struct{})
					byTag[t] = ids
				}
				ids[o.ID] = struct{}{}
			}
			tagsOf[o.ID] = set
		}
	}
	return byName, byTag, tagsOf, nil
}

// verify rebuilds all indexes from the dictionary and deep-compares them
// with the live indexes.
func (s *state) verify() error {
	byName, byTag, tagsOf, err := rebuildIndexes(s.objects)
	if err != nil {
		return fmt.Errorf("rebuild: %v", err)
	}
	if !reflect.DeepEqual(byName, s.byName) {
		return fmt.Errorf("byName drift: rebuilt %v, live %v", byName, s.byName)
	}
	if !reflect.DeepEqual(byTag, s.byTag) {
		return fmt.Errorf("byTag drift: rebuilt %v, live %v", byTag, s.byTag)
	}
	if !reflect.DeepEqual(tagsOf, s.tagsOf) {
		return fmt.Errorf("tagsOf drift: rebuilt %v, live %v", tagsOf, s.tagsOf)
	}
	return nil
}

// ConflictError reports a name-uniqueness violation inside a batch.
type ConflictError struct {
	Name   string
	HeldBy string // id currently holding Name ("" if conflicting within batch)
}

func (e *ConflictError) Error() string {
	if e.HeldBy == "" {
		return fmt.Sprintf("name %q assigned twice in batch", e.Name)
	}
	return fmt.Sprintf("name %q already held by object %q", e.Name, e.HeldBy)
}

// Op is a single mutation inside a batch.
type Op struct {
	delete bool
	obj    Object
	id     string // delete target
}

// Put inserts or replaces an object (matched by ID).
func Put(o Object) Op { return Op{obj: o} }

// Delete removes the object with the given id.
func Delete(id string) Op { return Op{delete: true, id: id} }

// Directory is a concurrency-safe in-memory object directory.
type Directory struct {
	mu sync.RWMutex
	st *state // immutable; replaced wholesale on every commit
}

// New returns an empty directory.
func New() *Directory {
	return &Directory{st: newState()}
}

// Apply commits a batch atomically. Objects are unbound in a first phase,
// so two objects may swap names within one batch. Any conflict aborts the
// whole batch and leaves the directory untouched.
func (d *Directory) Apply(ops ...Op) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Collapse to last-op-per-id so phase 2 never fights an earlier put.
	type keyed struct {
		put    bool
		obj    Object
		delete bool
	}
	order := make([]string, 0, len(ops))
	last := make(map[string]keyed, len(ops))
	for _, op := range ops {
		id := op.id
		k := keyed{delete: true}
		if !op.delete {
			o := op.obj.normalize()
			if o.ID == "" {
				return fmt.Errorf("put: empty id")
			}
			if o.Name == "" {
				return fmt.Errorf("put %q: empty name", o.ID)
			}
			id = o.ID
			k = keyed{put: true, obj: o}
		}
		if _, seen := last[id]; !seen {
			order = append(order, id)
		}
		last[id] = k
	}

	next := d.st.clone()

	// Phase 1: unbind every touched object (frees names for swaps).
	for _, id := range order {
		next.unbind(id)
	}

	// Phase 2: bind puts, enforcing name uniqueness.
	for _, id := range order {
		k := last[id]
		if !k.put {
			continue
		}
		if holder, taken := next.byName[k.obj.Name]; taken {
			return &ConflictError{Name: k.obj.Name, HeldBy: holder}
		}
		next.bind(k.obj)
	}

	if err := next.verify(); err != nil {
		return fmt.Errorf("post-commit index check: %v", err)
	}
	d.st = next
	return nil
}

// snapshot returns the current immutable state.
func (d *Directory) snapshot() *state {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.st
}

// Snapshot captures the current state. It never changes afterwards, so all
// of its indexes are mutually consistent and safe for concurrent reads.
func (d *Directory) Snapshot() *Snapshot {
	return &Snapshot{st: d.snapshot()}
}

// Verify rebuilds every index from the object dictionary and reports any
// drift between the live indexes and the rebuilt ones.
func (d *Directory) Verify() error {
	return d.snapshot().verify()
}

// GetByID returns the object with the given id.
func (d *Directory) GetByID(id string) (Object, bool) {
	return d.snapshot().getByID(id)
}

// GetByName returns the object with the given unique name.
func (d *Directory) GetByName(name string) (Object, bool) {
	return d.snapshot().getByName(name)
}

// GetByTag returns all objects carrying the tag, sorted by id.
func (d *Directory) GetByTag(tag string) []Object {
	return d.snapshot().getByTag(tag)
}

func (s *state) getByID(id string) (Object, bool) {
	o, ok := s.objects[id]
	return o, ok
}

func (s *state) getByName(name string) (Object, bool) {
	id, ok := s.byName[name]
	if !ok {
		return Object{}, false
	}
	return s.objects[id], true
}

func (s *state) getByTag(tag string) []Object {
	ids := s.byTag[tag]
	out := make([]Object, 0, len(ids))
	for id := range ids {
		out = append(out, s.objects[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Snapshot is an immutable, cross-index consistent view of a Directory.
type Snapshot struct {
	st *state
}

// GetByID returns the object with the given id as of the snapshot.
func (s *Snapshot) GetByID(id string) (Object, bool) { return s.st.getByID(id) }

// GetByName returns the object with the given name as of the snapshot.
func (s *Snapshot) GetByName(name string) (Object, bool) { return s.st.getByName(name) }

// GetByTag returns all objects carrying the tag as of the snapshot.
func (s *Snapshot) GetByTag(tag string) []Object { return s.st.getByTag(tag) }

// Len reports how many objects the snapshot contains.
func (s *Snapshot) Len() int { return len(s.st.objects) }

// Verify rebuilds every index from the snapshot's dictionary and reports
// any drift.
func (s *Snapshot) Verify() error { return s.st.verify() }
