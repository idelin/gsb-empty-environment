package openauth

import (
	"context"
	"fmt"
	"sync"
)

type memNode struct {
	id      NodeID
	name    string
	uid     uint32
	gid     uint32
	mode    uint32
	isDir   bool
	size    uint64
	verf    Verifier
	hasVerf bool
}

func (n *memNode) ID() NodeID   { return n.id }
func (n *memNode) UID() uint32  { return n.uid }
func (n *memNode) GID() uint32  { return n.gid }
func (n *memNode) Mode() uint32 { return n.mode }
func (n *memNode) IsDir() bool  { return n.isDir }

// memFS is a minimal in-memory Backend for tests.
type memFS struct {
	mu      sync.Mutex
	root    *memNode
	entries map[NodeID]map[string]*memNode
	counter uint64
}

func newMemFS() *memFS {
	root := &memNode{id: "/", name: "/", uid: 0, gid: 0, mode: 0o777, isDir: true}
	return &memFS{
		root:    root,
		entries: map[NodeID]map[string]*memNode{root.id: {}},
	}
}

func (fs *memFS) Lookup(_ context.Context, parent Node, name string) (Node, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	dir, ok := fs.entries[parent.ID()]
	if !ok {
		return nil, fmt.Errorf("memfs: not a directory: %q", parent.ID())
	}
	node, ok := dir[name]
	if !ok {
		return nil, ErrNotExist
	}
	return node, nil
}

func (fs *memFS) Create(_ context.Context, parent Node, name string, attrs InitialAttrs) (Node, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	dir, ok := fs.entries[parent.ID()]
	if !ok {
		return nil, fmt.Errorf("memfs: not a directory: %q", parent.ID())
	}
	if _, exists := dir[name]; exists {
		return nil, fmt.Errorf("memfs: %q already exists", name)
	}
	fs.counter++
	node := &memNode{
		id:   NodeID(fmt.Sprintf("node-%d", fs.counter)),
		name: name,
		uid:  attrs.UID,
		gid:  attrs.GID,
		mode: attrs.Mode,
		size: attrs.Size,
	}
	dir[name] = node
	return node, nil
}

func (fs *memFS) Remove(_ context.Context, parent Node, name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	dir, ok := fs.entries[parent.ID()]
	if !ok {
		return fmt.Errorf("memfs: not a directory: %q", parent.ID())
	}
	if _, ok := dir[name]; !ok {
		return ErrNotExist
	}
	delete(dir, name)
	return nil
}

func (fs *memFS) Access(_ context.Context, id Identity, node Node) (AccessRights, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return UnixAccess(id, node), nil
}

func (fs *memFS) SetCreateVerifier(_ context.Context, node Node, verifier Verifier) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	mn, ok := node.(*memNode)
	if !ok {
		return fmt.Errorf("memfs: unexpected node type")
	}
	mn.verf = verifier
	mn.hasVerf = true
	return nil
}

func (fs *memFS) CreateVerifier(_ context.Context, node Node) (Verifier, bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	mn, ok := node.(*memNode)
	if !ok {
		return Verifier{}, false, fmt.Errorf("memfs: unexpected node type")
	}
	return mn.verf, mn.hasVerf, nil
}

func (fs *memFS) rootNode() *memNode { return fs.root }

func (fs *memFS) exists(name string) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	_, ok := fs.entries[fs.root.id][name]
	return ok
}

func (fs *memFS) get(name string) *memNode {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.entries[fs.root.id][name]
}

// flakyBackend wraps a Backend and injects failures at chosen seams so the
// rollback path can be exercised.
type flakyBackend struct {
	Backend
	failVerifier bool
	failRemove   bool
}

func (f *flakyBackend) SetCreateVerifier(ctx context.Context, node Node, verifier Verifier) error {
	if f.failVerifier {
		return fmt.Errorf("injected verifier store failure")
	}
	return f.Backend.SetCreateVerifier(ctx, node, verifier)
}

func (f *flakyBackend) Remove(ctx context.Context, parent Node, name string) error {
	if f.failRemove {
		return fmt.Errorf("injected remove failure")
	}
	return f.Backend.Remove(ctx, parent, name)
}

// aliasCreateBackend makes every created node reuse AliasNode's identity so
// the share-conflict path can be exercised against a held reservation.
type aliasCreateBackend struct {
	*memFS
	AliasNode Node
}

func (b *aliasCreateBackend) Create(ctx context.Context, parent Node, name string, attrs InitialAttrs) (Node, error) {
	if _, err := b.memFS.Create(ctx, parent, name, attrs); err != nil {
		return nil, err
	}
	return b.AliasNode, nil
}
