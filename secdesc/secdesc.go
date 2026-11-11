// Package secdesc models Windows-style security descriptors with discretionary
// access control lists (DACLs) and automatic inheritance of access control
// entries (ACEs).
//
// A tree of nodes (directories and files) is versioned: every mutating
// operation recomputes the effective DACL of the whole tree on a private copy
// and publishes a new immutable Snapshot in a single atomic step. Readers that
// hold an older Snapshot keep observing the old, fully consistent state.
package secdesc

import (
	"errors"
	"sync"
	"sync/atomic"
)

// RootID is the fixed identifier of the tree root.
const RootID = ""

// ObjectKind distinguishes container objects (directories) from leaf objects
// (files).
type ObjectKind int

const (
	// KindContainer is a directory-like object that may have children.
	KindContainer ObjectKind = iota
	// KindFile is a leaf object.
	KindFile
)

// Inheritance flag bits. They mirror the Windows SEC_ACE_FLAG_* semantics.
const (
	// FlagContainerInherit (CI): the ACE is inherited by child containers.
	FlagContainerInherit uint8 = 1 << iota
	// FlagObjectInherit (OI): the ACE is inherited by child leaf objects.
	FlagObjectInherit
	// FlagInheritOnly (IO): the ACE does not apply to the object it is
	// attached to, only to inheriting descendants.
	FlagInheritOnly
	// FlagNoPropagate (NP): inheritance stops at the immediate child ("this
	// generation only"); the child copy carries no inheritance flags.
	FlagNoPropagate
	// FlagInherited marks an ACE that was received from a parent instead of
	// being declared explicitly on the object.
	FlagInherited
)

// ACEType selects between allow and deny entries.
type ACEType int

const (
	// ACEAllow grants the rights in Mask.
	ACEAllow ACEType = iota
	// ACEDeny denies the rights in Mask.
	ACEDeny
)

// ACE is a single access control entry.
type ACE struct {
	// Trustee identifies the principal (user/group) the entry applies to.
	Trustee string
	// Type is allow or deny.
	Type ACEType
	// Mask is the bitmask of access rights governed by the entry.
	Mask uint32
	// Flags holds the CI/OI/IO/NP/Inherited inheritance bits.
	Flags uint8
}

// IsInherited reports whether the entry was propagated from a parent.
func (a ACE) IsInherited() bool { return a.Flags&FlagInherited != 0 }

// AppliesToSelf reports whether the entry is effective on the object carrying
// it. Inherit-only (IO) entries only affect descendants.
func (a ACE) AppliesToSelf() bool { return a.Flags&FlagInheritOnly == 0 }

// SecurityDescriptor is the explicitly configured state of a node.
type SecurityDescriptor struct {
	// Protected blocks inheritance from the parent. When true the effective
	// DACL consists of ExplicitACEs only, and nothing is propagated from
	// ancestors (explicit entries still propagate to descendants).
	Protected bool
	// ExplicitACEs keep their declared relative order. They always precede
	// inherited entries in the effective DACL.
	ExplicitACEs []ACE
}

// Node is an immutable view of a tree object inside a Snapshot.
type Node interface {
	ID() string
	ParentID() string
	Kind() ObjectKind
	SecurityDescriptor() SecurityDescriptor
}

// Snapshot is an immutable, point-in-time view of the whole tree.
type Snapshot interface {
	// Version is the monotonically increasing generation number. The initial
	// root publication is version 1; every published mutation increments it.
	Version() uint64
	// Root returns the root node.
	Root() Node
	// Node looks up a node by identifier.
	Node(id string) (Node, bool)
	// EffectiveACEs returns the computed DACL of a node: explicit entries in
	// their declared order followed by inherited entries in the order in which
	// the parent enumerates them. The returned slice is a private copy.
	EffectiveACEs(id string) ([]ACE, error)
}

// Errors returned by tree operations.
var (
	// ErrNoSuchNode indicates that the referenced node does not exist.
	ErrNoSuchNode = errors.New("secdesc: node does not exist")
	// ErrRootExists indicates that a root was added more than once.
	ErrRootExists = errors.New("secdesc: root already exists")
	// ErrParentNotContainer indicates that a node was attached to a file.
	ErrParentNotContainer = errors.New("secdesc: parent is not a container")
	// ErrCannotMoveRoot indicates that the root node cannot be relocated.
	ErrCannotMoveRoot = errors.New("secdesc: cannot move the root node")
	// ErrMoveIntoDescendant indicates a cyclic move below the moved subtree.
	ErrMoveIntoDescendant = errors.New("secdesc: cannot move a node into its own descendant")
)

type node struct {
	id        string
	parentID  string
	kind      ObjectKind
	sd        SecurityDescriptor
	effective []ACE
}

func (n *node) ID() string       { return n.id }
func (n *node) ParentID() string { return n.parentID }
func (n *node) Kind() ObjectKind { return n.kind }
func (n *node) SecurityDescriptor() SecurityDescriptor {
	return cloneSD(n.sd)
}

type snapshot struct {
	version uint64
	nodes   map[string]*node
}

func (s *snapshot) Version() uint64 { return s.version }

func (s *snapshot) Root() Node { return s.nodes[RootID] }

func (s *snapshot) Node(id string) (Node, bool) {
	n, ok := s.nodes[id]
	return n, ok
}

func (s *snapshot) EffectiveACEs(id string) ([]ACE, error) {
	n, ok := s.nodes[id]
	if !ok {
		return nil, ErrNoSuchNode
	}
	out := make([]ACE, len(n.effective))
	copy(out, n.effective)
	return out, nil
}

// Tree is a versioned object tree. Mutations are serialized; reads are
// lock-free and operate on the published Snapshot.
type Tree struct {
	mu      sync.Mutex
	version uint64
	current atomic.Pointer[snapshot]
}

// NewTree creates an empty tree.
func NewTree() *Tree { return &Tree{} }

// Current returns the most recently published snapshot, or nil before the root
// has been added.
func (t *Tree) Current() Snapshot {
	s := t.current.Load()
	if s == nil {
		return nil
	}
	return s
}

// Add inserts a node. The root is added with parentID RootID; every other node
// requires an existing container parent. The new snapshot is published
// atomically after inheritance has been recomputed.
func (t *Tree) Add(id string, parentID string, kind ObjectKind, sd SecurityDescriptor) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var prev map[string]*node
	if cur := t.current.Load(); cur != nil {
		prev = cur.nodes
	}

	if id == RootID {
		if _, exists := prev[RootID]; exists {
			return nil, ErrRootExists
		}
	} else {
		parent, ok := prev[parentID]
		if !ok {
			return nil, ErrNoSuchNode
		}
		if parent.kind != KindContainer {
			return nil, ErrParentNotContainer
		}
	}
	if _, exists := prev[id]; exists {
		return nil, errors.New("secdesc: node already exists: " + id)
	}

	next := cloneNodes(prev)
	next[id] = &node{
		id:       id,
		parentID: parentID,
		kind:     kind,
		sd:       cloneSD(sd),
	}

	return t.publish(next)
}

// Move relocates the subtree rooted at id under newParentID and recomputes
// inherited ACEs for the entire tree before publishing a new snapshot. The
// moved subtree keeps its explicit ACEs, their relative order and its
// protected flags; only inherited entries are recalculated.
func (t *Tree) Move(id string, newParentID string) (Snapshot, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cur := t.current.Load()
	if cur == nil {
		return nil, ErrNoSuchNode
	}

	if id == RootID {
		return nil, ErrCannotMoveRoot
	}
	moved, ok := cur.nodes[id]
	if !ok {
		return nil, ErrNoSuchNode
	}
	newParent, ok := cur.nodes[newParentID]
	if !ok {
		return nil, ErrNoSuchNode
	}
	if newParent.kind != KindContainer {
		return nil, ErrParentNotContainer
	}
	if newParentID == id || isAncestor(cur.nodes, id, newParentID) {
		return nil, ErrMoveIntoDescendant
	}
	if moved.parentID == newParentID {
		return cur, nil
	}

	next := cloneNodes(cur.nodes)
	next[id].parentID = newParentID

	return t.publish(next)
}

// publish recomputes every effective DACL on the private node set and swaps it
// in as the new current snapshot. The swap is the single publication point, so
// readers either see the complete old generation or the complete new one.
func (t *Tree) publish(next map[string]*node) (Snapshot, error) {
	root, ok := next[RootID]
	if !ok {
		return nil, ErrNoSuchNode
	}
	children := make(map[string][]*node, len(next))
	for id, n := range next {
		if id == RootID {
			continue
		}
		children[n.parentID] = append(children[n.parentID], n)
	}
	recompute(children, root, nil)

	t.version++
	snap := &snapshot{version: t.version, nodes: next}
	t.current.Store(snap)
	return snap, nil
}

// isAncestor reports whether ancestorID is a strict ancestor of nodeID.
func isAncestor(nodes map[string]*node, ancestorID, nodeID string) bool {
	cur := nodes[nodeID]
	for cur != nil && cur.id != RootID {
		if cur.parentID == ancestorID {
			return true
		}
		cur = nodes[cur.parentID]
	}
	return false
}

// recompute walks the tree depth-first, deriving each node's effective DACL
// from its explicit configuration and the parent's already computed DACL.
func recompute(children map[string][]*node, n *node, parentEffective []ACE) {
	explicit := n.sd.ExplicitACEs

	var effective []ACE
	effective = append(effective, explicit...)
	if !n.sd.Protected {
		for _, ace := range parentEffective {
			if inherited, ok := propagate(ace, n.kind); ok {
				effective = append(effective, inherited)
			}
		}
	}
	n.effective = effective

	for _, child := range children[n.id] {
		recompute(children, child, effective)
	}
}

// propagate decides whether a parent ACE reaches a child of the given kind and
// returns the child's copy with the correct flags.
//
// Containers inherit CI entries, plus OI entries that are marked inherit-only
// (they must pass through the directory to reach files below). Files inherit
// OI entries. A child copy always carries FlagInherited. On a file the
// inheritance flags are stripped; on a container they are preserved so the
// entry keeps propagating, unless NP is set, in which case the chain stops
// here and the copy retains no inheritance flags.
func propagate(parent ACE, childKind ObjectKind) (ACE, bool) {
	child := parent
	ci := parent.Flags&FlagContainerInherit != 0
	oi := parent.Flags&FlagObjectInherit != 0
	io := parent.Flags&FlagInheritOnly != 0
	np := parent.Flags&FlagNoPropagate != 0

	switch childKind {
	case KindContainer:
		if !ci && !(oi && io) {
			return ACE{}, false
		}
		if np {
			child.Flags = FlagInherited
		} else {
			child.Flags = parent.Flags | FlagInherited
		}
	case KindFile:
		if !oi {
			return ACE{}, false
		}
		child.Flags = FlagInherited
	}
	return child, true
}

func cloneSD(sd SecurityDescriptor) SecurityDescriptor {
	aces := make([]ACE, len(sd.ExplicitACEs))
	copy(aces, sd.ExplicitACEs)
	return SecurityDescriptor{Protected: sd.Protected, ExplicitACEs: aces}
}

func cloneNodes(in map[string]*node) map[string]*node {
	out := make(map[string]*node, len(in))
	for id, n := range in {
		cp := *n
		cp.sd = cloneSD(n.sd)
		cp.effective = nil
		out[id] = &cp
	}
	return out
}
