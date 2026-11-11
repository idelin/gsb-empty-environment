package gitaccess

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"
)

// Service owns the reference namespace and issues capabilities for tenants.
// Reference updates are serialized relative to capability issuance, so the
// snapshot embedded in a capability is always internally consistent.
type Service struct {
	db *ObjectDatabase

	mu   sync.RWMutex
	refs map[string]ObjectID
}

// NewService creates a service backed by db.
func NewService(db *ObjectDatabase) *Service {
	return &Service{db: db, refs: make(map[string]ObjectID)}
}

// UpdateRef atomically sets a reference to target. Replacing an existing
// reference of the same name only affects capabilities issued afterwards;
// capabilities issued earlier keep their original snapshot.
func (s *Service) UpdateRef(name string, target ObjectID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[name] = target
}

// DeleteRef atomically removes a reference. Existing capabilities are
// unaffected.
func (s *Service) DeleteRef(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.refs, name)
}

// ResolveRef returns the current target of a reference.
func (s *Service) ResolveRef(name string) (ObjectID, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.refs[name]
	return id, ok
}

// IssueCapability creates a read capability for tenant. The capability is
// bound to the exact object ids the named references point to right now and to
// every object reachable from them. Later reference updates cannot widen it.
func (s *Service) IssueCapability(ctx context.Context, tenant string, refNames []string) (*Capability, error) {
	s.mu.RLock()
	snapshot := make(map[string]ObjectID, len(refNames))
	for _, name := range refNames {
		id, ok := s.refs[name]
		if !ok {
			s.mu.RUnlock()
			return nil, fmt.Errorf("%w: %s", ErrRefNotFound, name)
		}
		snapshot[name] = id
	}
	s.mu.RUnlock()

	reachable, err := s.collectReachable(ctx, snapshot)
	if err != nil {
		return nil, err
	}

	return &Capability{
		tenant:   tenant,
		snapshot: snapshot,
		allowed:  reachable,
		db:       s.db,
		cache:    make(map[ObjectID]*Object),
	}, nil
}

// collectReachable walks the object graph starting at every reference target
// in snapshot. The resulting set is fixed for the capability's lifetime.
func (s *Service) collectReachable(ctx context.Context, snapshot map[string]ObjectID) (map[ObjectID]struct{}, error) {
	reachable := make(map[ObjectID]struct{})

	roots := make([]ObjectID, 0, len(snapshot))
	for _, id := range snapshot {
		roots = append(roots, id)
	}
	sort.Slice(roots, func(i, j int) bool { return bytes.Compare(roots[i][:], roots[j][:]) < 0 })

	for len(roots) > 0 {
		id := roots[0]
		roots = roots[1:]
		if _, seen := reachable[id]; seen {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		obj, err := s.db.LoadVerified(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolving reachable object %s: %w", id, err)
		}
		reachable[id] = struct{}{}

		children, err := childIDs(obj)
		if err != nil {
			return nil, fmt.Errorf("parsing %s %s: %w", obj.Type, id, err)
		}
		for _, child := range children {
			if _, seen := reachable[child]; !seen {
				roots = append(roots, child)
			}
		}
	}
	return reachable, nil
}

// Capability is an issued, immutable read grant for one tenant. It must not be
// copied after first use because it contains a per-capability cache.
type Capability struct {
	tenant   string
	snapshot map[string]ObjectID
	allowed  map[ObjectID]struct{}
	db       *ObjectDatabase

	mu    sync.Mutex
	cache map[ObjectID]*Object
}

// Tenant returns the tenant the capability was issued to.
func (c *Capability) Tenant() string { return c.tenant }

// Snapshot returns a copy of the fixed reference snapshot bound to the
// capability. It never changes, even when references are replaced later.
func (c *Capability) Snapshot() map[string]ObjectID {
	out := make(map[string]ObjectID, len(c.snapshot))
	for name, id := range c.snapshot {
		out[name] = id
	}
	return out
}

// Allowed reports whether id is part of the capability's reachable set.
func (c *Capability) Allowed(id ObjectID) bool {
	_, ok := c.allowed[id]
	return ok
}

// ReadObject returns a verified object if it is reachable from the fixed
// snapshot. The returned slice is a private copy: callers may modify it
// without affecting the capability cache or other tenants.
//
// Cached objects are only inserted after a successful, digest-verified and
// uncanceled load. A context cancellation (or any failure) therefore never
// poisons the cache and never becomes visible to another read or tenant.
func (c *Capability) ReadObject(ctx context.Context, id ObjectID) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := c.allowed[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, id)
	}

	c.mu.Lock()
	cached, ok := c.cache[id]
	c.mu.Unlock()
	if ok {
		return cloneObject(cached), nil
	}

	obj, err := c.db.LoadVerified(ctx, id)
	if err != nil {
		return nil, err
	}
	// Re-check cancellation after the potentially expensive load: a canceled
	// result must not be promoted into the shared capability cache.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if existing, ok := c.cache[id]; ok {
		cached = existing
	} else {
		c.cache[id] = obj
		cached = obj
	}
	c.mu.Unlock()
	return cloneObject(cached), nil
}

func cloneObject(obj *Object) *Object {
	content := make([]byte, len(obj.Content))
	copy(content, obj.Content)
	return &Object{Type: obj.Type, Content: content}
}

// childIDs parses one object and returns the ids it directly references.
func childIDs(obj *Object) ([]ObjectID, error) {
	switch obj.Type {
	case BlobObject:
		return nil, nil
	case CommitObject:
		return parseCommitChildren(obj.Content), nil
	case TagObject:
		return parseTagChildren(obj.Content), nil
	case TreeObject:
		return parseTreeEntries(obj.Content)
	default:
		return nil, fmt.Errorf("unknown object type %d", obj.Type)
	}
}

func parseCommitChildren(content []byte) []ObjectID {
	var ids []ObjectID
	for _, line := range splitHeaderLines(content) {
		if bytes.HasPrefix(line, []byte("tree ")) || bytes.HasPrefix(line, []byte("parent ")) {
			if id, err := ParseOID(string(extractOID(line))); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func parseTagChildren(content []byte) []ObjectID {
	var ids []ObjectID
	for _, line := range splitHeaderLines(content) {
		if bytes.HasPrefix(line, []byte("object ")) {
			if id, err := ParseOID(string(line[len("object "):])); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func extractOID(line []byte) []byte {
	fields := bytes.Fields(line)
	if len(fields) >= 2 {
		return fields[1]
	}
	return nil
}

// splitHeaderLines returns the header section (the lines before the blank
// line separating headers from the message body).
func splitHeaderLines(content []byte) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(content, []byte("\n")) {
		if len(line) == 0 {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

// parseTreeEntries decodes tree entries: "<mode> <name>\0<20 byte oid>".
func parseTreeEntries(content []byte) ([]ObjectID, error) {
	var ids []ObjectID
	for len(content) > 0 {
		space := bytes.IndexByte(content, ' ')
		if space < 0 {
			return nil, fmt.Errorf("tree entry missing mode")
		}
		null := bytes.IndexByte(content, 0)
		if null < 0 {
			return nil, fmt.Errorf("tree entry missing name terminator")
		}
		if null+1+OIDSize > len(content) {
			return nil, fmt.Errorf("tree entry truncated oid")
		}
		var id ObjectID
		copy(id[:], content[null+1:null+1+OIDSize])
		ids = append(ids, id)
		content = content[null+1+OIDSize:]
	}
	return ids, nil
}
