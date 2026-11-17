package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrInvalidRequest reports a restore request that cannot be planned,
	// such as a request without roots.
	ErrInvalidRequest = errors.New("netsec: invalid restore request")

	// ErrMissingObject reports that a required object is not present in the
	// reference graph.
	ErrMissingObject = errors.New("netsec: required object is missing from graph")

	// ErrUnauthorized reports that at least one object in the required closure
	// does not have a per-object restore permission.
	ErrUnauthorized = errors.New("netsec: required object is not authorized")

	// ErrPermissionChanged reports that a permission changed after the plan was
	// created. This includes a revoked permission and a revision change even
	// when the object is currently allowed again.
	ErrPermissionChanged = errors.New("netsec: permission changed after planning")

	// ErrPlanStale reports that a final review computed a different required
	// closure than the one recorded in the plan.
	ErrPlanStale = errors.New("netsec: restore plan is stale")

	// ErrAlreadyPublished reports an attempt to publish the same plan twice.
	ErrAlreadyPublished = errors.New("netsec: restore plan already published")
)

// ObjectID identifies an object in a restore graph. Directories and secrets
// use the same namespace so that every object is authorized individually.
type ObjectID string

// Reference is an edge in a restore graph.
//
// The zero value is a required reference. This is deliberately fail-closed:
// a reference is only optional when Optional is explicitly set to true.
type Reference struct {
	Target   ObjectID
	Optional bool
}

// Object is a node in a restore graph.
type Object struct {
	References []Reference
}

// Graph maps object IDs to their outgoing references.
//
// A reference does not grant permission to its target. Every object reached by
// a required reference is checked against Permissions separately.
type Graph map[ObjectID]Object

// Permission is the restore permission for one object.
//
// Revision is an opaque generation incremented by Grant, Revoke, and Set. The
// planner records the revision it observed. A publish-time review rejects the
// plan if the revision changed, even if the object was granted again between
// planning and publishing.
type Permission struct {
	Allowed  bool
	Revision uint64
}

// Permissions maps each object to its own restore permission. A missing entry
// is treated as denied.
type Permissions map[ObjectID]Permission

// Allows reports whether id is currently allowed. Missing entries are denied.
func (p Permissions) Allows(id ObjectID) bool {
	permission, ok := p[id]
	return ok && permission.Allowed
}

// Grant allows id and advances its revision.
func (p Permissions) Grant(id ObjectID) {
	p.Set(id, true)
}

// Revoke denies id and advances its revision.
func (p Permissions) Revoke(id ObjectID) {
	p.Set(id, false)
}

// Set updates id's permission and advances its revision. Permission revisions
// make a plan detect a revoke-and-regrant that occurs between planning and
// publishing.
func (p Permissions) Set(id ObjectID, allowed bool) {
	previous := p[id]
	p[id] = Permission{
		Allowed:  allowed,
		Revision: previous.Revision + 1,
	}
}

// OmittedReference records an optional edge that the plan deliberately did not
// follow. The edge must be restored as empty rather than fetched implicitly.
type OmittedReference struct {
	From ObjectID
	To   ObjectID
}

// UnauthorizedError describes a required object that lacks permission.
type UnauthorizedError struct {
	Object   ObjectID
	Root     ObjectID
	Path     []ObjectID
	Revision uint64
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf(
		"netsec: required object %q is not authorized (root %q, path %s)",
		e.Object, e.Root, formatPath(e.Path),
	)
}

// Unwrap makes errors.Is(err, ErrUnauthorized) work.
func (e *UnauthorizedError) Unwrap() error {
	return ErrUnauthorized
}

// MissingObjectError describes a required reference to an object that is not
// present in the graph.
type MissingObjectError struct {
	Object ObjectID
	Root   ObjectID
	Path   []ObjectID
}

func (e *MissingObjectError) Error() string {
	return fmt.Sprintf(
		"netsec: required object %q is missing from graph (root %q, path %s)",
		e.Object, e.Root, formatPath(e.Path),
	)
}

// Unwrap makes errors.Is(err, ErrMissingObject) work.
func (e *MissingObjectError) Unwrap() error {
	return ErrMissingObject
}

// RevokedError reports that an object in the plan is no longer allowed at
// publish time.
type RevokedError struct {
	Object  ObjectID
	Planned Permission
	Current Permission
}

func (e *RevokedError) Error() string {
	return fmt.Sprintf(
		"netsec: permission for %q was revoked after planning (revision %d -> %d)",
		e.Object, e.Planned.Revision, e.Current.Revision,
	)
}

// Unwrap supports both the general permission-change check and the more
// specific unauthorized check.
func (e *RevokedError) Unwrap() []error {
	return []error{ErrPermissionChanged, ErrUnauthorized}
}

// PermissionChangedError reports that an object's permission revision changed
// after planning, even though it is currently allowed.
type PermissionChangedError struct {
	Object  ObjectID
	Planned Permission
	Current Permission
}

func (e *PermissionChangedError) Error() string {
	return fmt.Sprintf(
		"netsec: permission for %q changed after planning (revision %d -> %d)",
		e.Object, e.Planned.Revision, e.Current.Revision,
	)
}

// Unwrap makes errors.Is(err, ErrPermissionChanged) work.
func (e *PermissionChangedError) Unwrap() error {
	return ErrPermissionChanged
}

// Plan is an immutable restore plan for a required closure.
type Plan struct {
	mu sync.Mutex

	graph       Graph
	roots       []ObjectID
	objects     []ObjectID
	omissions   []OmittedReference
	permissions Permissions
	published   bool
}

// PlanRestore computes the restore closure for roots.
//
// Each root and every object reached by a required reference must have its own
// allowed permission. If any required object is missing or unauthorized, no
// plan is returned. Optional references are never followed: they are recorded
// as explicit omissions so the restore host can leave those edges empty.
//
// The input graph and permissions are copied. Later mutations of either input
// do not change the plan's recorded closure or authorization snapshot.
func PlanRestore(graph Graph, permissions Permissions, roots ...ObjectID) (*Plan, error) {
	return planRestore(graph, permissions, roots)
}

func planRestore(graph Graph, permissions Permissions, requestedRoots []ObjectID) (*Plan, error) {
	if len(requestedRoots) == 0 {
		return nil, fmt.Errorf("%w: at least one root is required", ErrInvalidRequest)
	}

	graphCopy := cloneGraph(graph)
	permissionsCopy := clonePermissions(permissions)
	roots := uniqueIDs(requestedRoots)

	objects, omissions, err := computeClosure(graphCopy, permissionsCopy, roots)
	if err != nil {
		return nil, err
	}

	return &Plan{
		graph:       graphCopy,
		roots:       roots,
		objects:     objects,
		omissions:   omissions,
		permissions: permissionsCopy,
	}, nil
}

// Roots returns a copy of the requested restore roots.
func (p *Plan) Roots() []ObjectID {
	if p == nil {
		return nil
	}
	return append([]ObjectID(nil), p.roots...)
}

// Objects returns a sorted copy of the required restore closure.
func (p *Plan) Objects() []ObjectID {
	if p == nil {
		return nil
	}
	return append([]ObjectID(nil), p.objects...)
}

// OmittedOptional returns the optional references that must be restored as
// empty. The result is sorted and does not include any required edges.
func (p *Plan) OmittedOptional() []OmittedReference {
	if p == nil {
		return nil
	}
	return append([]OmittedReference(nil), p.omissions...)
}

// Review performs the final authorization check without publishing. It
// recomputes the required closure against the same graph and the supplied
// current permissions. A revocation, a permission revision change, or a
// changed required closure rejects the plan.
func (p *Plan) Review(current Permissions) error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrInvalidRequest)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.reviewLocked(current)
}

// Publish performs the final review and, only if it succeeds, returns the
// manifest that the restore host may execute. If review fails, no manifest is
// returned and no part of the restore is authorized for publication.
//
// A successful plan can be published once. A failed review does not consume
// the plan, so the host may retry if the current permission state can be
// restored to the exact snapshot the plan recorded. A revoke-and-regrant gives
// the object a new revision and therefore requires a new plan.
func (p *Plan) Publish(current Permissions) (*Manifest, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil plan", ErrInvalidRequest)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.published {
		return nil, ErrAlreadyPublished
	}
	if err := p.reviewLocked(current); err != nil {
		return nil, err
	}

	p.published = true
	return &Manifest{
		roots:     append([]ObjectID(nil), p.roots...),
		objects:   append([]ObjectID(nil), p.objects...),
		omissions: append([]OmittedReference(nil), p.omissions...),
	}, nil
}

func (p *Plan) reviewLocked(current Permissions) error {
	currentCopy := clonePermissions(current)

	for _, id := range p.objects {
		planned := p.permissions[id]
		now, ok := currentCopy[id]
		if !ok || !now.Allowed {
			return &RevokedError{
				Object:  id,
				Planned: planned,
				Current: now,
			}
		}
		if now.Revision != planned.Revision {
			return &PermissionChangedError{
				Object:  id,
				Planned: planned,
				Current: now,
			}
		}
	}

	objects, omissions, err := computeClosure(p.graph, currentCopy, p.roots)
	if err != nil {
		return fmt.Errorf("%w: final review: %w", ErrPermissionChanged, err)
	}
	if !equalIDs(objects, p.objects) || !equalOmissions(omissions, p.omissions) {
		return fmt.Errorf("%w: required closure changed", ErrPlanStale)
	}

	return nil
}

// Manifest is the authorized output of Publish. It is immutable.
type Manifest struct {
	roots     []ObjectID
	objects   []ObjectID
	omissions []OmittedReference
}

// Roots returns a copy of the restore roots.
func (m *Manifest) Roots() []ObjectID {
	if m == nil {
		return nil
	}
	return append([]ObjectID(nil), m.roots...)
}

// Objects returns a sorted copy of all objects that must be restored.
func (m *Manifest) Objects() []ObjectID {
	if m == nil {
		return nil
	}
	return append([]ObjectID(nil), m.objects...)
}

// OmittedOptional returns the optional edges that must be restored as empty.
func (m *Manifest) OmittedOptional() []OmittedReference {
	if m == nil {
		return nil
	}
	return append([]OmittedReference(nil), m.omissions...)
}

// Contains reports whether id is part of the authorized restore closure.
func (m *Manifest) Contains(id ObjectID) bool {
	if m == nil {
		return false
	}
	for _, object := range m.objects {
		if object == id {
			return true
		}
	}
	return false
}

func computeClosure(graph Graph, permissions Permissions, roots []ObjectID) ([]ObjectID, []OmittedReference, error) {
	included := make(map[ObjectID]struct{}, len(graph))
	omitted := make(map[OmittedReference]struct{})

	type frame struct {
		id     ObjectID
		root   ObjectID
		parent int
		next   int
	}

	stack := make([]frame, 0, len(graph))
	pathTo := func(index int) []ObjectID {
		var reversed []ObjectID
		for index >= 0 {
			reversed = append(reversed, stack[index].id)
			index = stack[index].parent
		}

		path := make([]ObjectID, len(reversed))
		for i := range reversed {
			path[len(reversed)-1-i] = reversed[i]
		}
		return path
	}
	enter := func(id, root ObjectID, parent int) error {
		if _, ok := included[id]; ok {
			return nil
		}

		if _, ok := graph[id]; !ok {
			return &MissingObjectError{
				Object: id,
				Root:   root,
				Path:   appendPath(pathTo(parent), id),
			}
		}
		permission, ok := permissions[id]
		if !ok || !permission.Allowed {
			return &UnauthorizedError{
				Object:   id,
				Root:     root,
				Path:     appendPath(pathTo(parent), id),
				Revision: permission.Revision,
			}
		}

		included[id] = struct{}{}
		stack = append(stack, frame{
			id:     id,
			root:   root,
			parent: parent,
			next:   0,
		})
		return nil
	}

	for _, root := range roots {
		if err := enter(root, root, -1); err != nil {
			return nil, nil, err
		}

		for len(stack) > 0 {
			topIndex := len(stack) - 1
			top := &stack[topIndex]
			object := graph[top.id]
			if top.next >= len(object.References) {
				stack = stack[:len(stack)-1]
				continue
			}

			reference := object.References[top.next]
			top.next++
			if reference.Optional {
				omitted[OmittedReference{From: top.id, To: reference.Target}] = struct{}{}
				continue
			}

			if err := enter(reference.Target, top.root, topIndex); err != nil {
				return nil, nil, err
			}
		}
	}

	objects := make([]ObjectID, 0, len(included))
	for id := range included {
		objects = append(objects, id)
	}
	sort.Slice(objects, func(i, j int) bool {
		return objects[i] < objects[j]
	})

	omissions := make([]OmittedReference, 0, len(omitted))
	for omission := range omitted {
		omissions = append(omissions, omission)
	}
	sort.Slice(omissions, func(i, j int) bool {
		if omissions[i].From != omissions[j].From {
			return omissions[i].From < omissions[j].From
		}
		return omissions[i].To < omissions[j].To
	})

	return objects, omissions, nil
}

func cloneGraph(graph Graph) Graph {
	if graph == nil {
		return Graph{}
	}

	cloned := make(Graph, len(graph))
	for id, object := range graph {
		references := make([]Reference, len(object.References))
		copy(references, object.References)
		cloned[id] = Object{References: references}
	}
	return cloned
}

func clonePermissions(permissions Permissions) Permissions {
	cloned := make(Permissions, len(permissions))
	for id, permission := range permissions {
		cloned[id] = permission
	}
	return cloned
}

func uniqueIDs(ids []ObjectID) []ObjectID {
	seen := make(map[ObjectID]struct{}, len(ids))
	unique := make([]ObjectID, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func appendPath(parent []ObjectID, id ObjectID) []ObjectID {
	path := make([]ObjectID, len(parent)+1)
	copy(path, parent)
	path[len(parent)] = id
	return path
}

func formatPath(path []ObjectID) string {
	if len(path) == 0 {
		return "<empty>"
	}

	var result strings.Builder
	for i, id := range path {
		if i > 0 {
			result.WriteString(" -> ")
		}
		result.WriteString(fmt.Sprintf("%q", id))
	}
	return result.String()
}

func equalIDs(left, right []ObjectID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalOmissions(left, right []OmittedReference) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
