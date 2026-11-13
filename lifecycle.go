package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
)

var (
	// ErrEmptyNodeID reports a node specification without an identity.
	ErrEmptyNodeID = errors.New("netsec: node ID must not be empty")
	// ErrEmptyKey reports an empty node key.
	ErrEmptyKey = errors.New("netsec: node key must not be empty")
	// ErrExpiryWithoutExemption reports a key with no expiry that was not
	// explicitly marked exempt by an administrator.
	ErrExpiryWithoutExemption = errors.New("netsec: empty expiry requires administrator exemption")
	// ErrNodeAlreadyExists reports a duplicate node identity.
	ErrNodeAlreadyExists = errors.New("netsec: node already exists")
	// ErrNodeNotFound reports an unknown node identity.
	ErrNodeNotFound = errors.New("netsec: node not found")
	// ErrKeyInUse reports a key already assigned to another node.
	ErrKeyInUse = errors.New("netsec: key is already in use")
	// ErrKeyRetired reports an attempt to reuse a key that was rotated or
	// removed. Retired keys must never become valid again.
	ErrKeyRetired = errors.New("netsec: key is retired and cannot be reused")
	// ErrOldKeyNotCurrent reports a rotation whose old key does not match the
	// node's current key.
	ErrOldKeyNotCurrent = errors.New("netsec: old key is not the node's current key")
	// ErrOldKeyExpired reports a rotation attempted at or after the old key's
	// expiry instant.
	ErrOldKeyExpired = errors.New("netsec: old key is expired")
	// ErrOldKeyAlreadyRotated reports replayed rotation material. A given old
	// key can authorize at most one rotation.
	ErrOldKeyAlreadyRotated = errors.New("netsec: old key has already been rotated")
	// ErrNewKeyInvalid reports a missing new key or one equal to the old key.
	ErrNewKeyInvalid = errors.New("netsec: new key must be non-empty and differ from the old key")
	// ErrSignatureVerifierMissing reports a store configured without a
	// signature verifier.
	ErrSignatureVerifierMissing = errors.New("netsec: no signature verifier configured")
	// ErrInvalidRotationSignature reports that the injected verifier rejected
	// the old-key signature over the new key.
	ErrInvalidRotationSignature = errors.New("netsec: rotation signature verification failed")
)

// SignatureVerifier verifies that oldKey signed newKey. Implementations are
// supplied by the caller, for example to check a WireGuard-style signature
// with the old public key. Returning true accepts the signature.
type SignatureVerifier func(oldKey, newKey string, signature []byte) bool

// Options configures a Store.
type Options struct {
	// Now supplies the current time. It defaults to time.Now. Injecting a
	// clock makes expiry boundaries and rotation replay deterministic.
	Now func() time.Time
	// Verify authenticates rotation requests. Rotations are rejected when it
	// is nil.
	Verify SignatureVerifier
}

// Node is the input used to add a key to the Store.
//
// ExpiresAt may be nil only when AdminExempt is true. Expiry is exclusive of
// the instant itself: a key is expired as soon as now >= ExpiresAt.
type Node struct {
	ID          string
	Key         string
	ExpiresAt   *time.Time
	AdminExempt bool
	Policy      any
}

// GraphNode is an active node in a generated network graph.
type GraphNode struct {
	NodeID      string
	Key         string
	ExpiresAt   *time.Time
	AdminExempt bool
	Policy      any
}

// Graph is a point-in-time network graph. Nodes with expired keys are omitted.
type Graph struct {
	Version     uint64
	GeneratedAt time.Time
	Nodes       []GraphNode
}

// DeltaOp identifies the operation carried by a Delta.
type DeltaOp string

const (
	// DeltaUpsert adds or refreshes a key in a peer's cached graph.
	DeltaUpsert DeltaOp = "upsert"
	// DeltaDelete removes a key from a peer's cached graph.
	DeltaDelete DeltaOp = "delete"
)

// Delta is one incremental graph update. Delete entries identify the old key
// that must be removed. Upsert entries carry the complete current node record.
// Deltas are ordered with all deletions first, then upserts, each sorted by
// node ID and key.
type Delta struct {
	Op     DeltaOp
	NodeID string
	Key    string
	Node   *GraphNode
}

// RotationRequest asks to replace a node's old key. Signature is produced by
// the old key over NewKey and is checked by the Store's injected verifier.
type RotationRequest struct {
	NodeID    string
	OldKey    string
	NewKey    string
	Signature []byte
}

type nodeState struct {
	id          string
	key         string
	expiresAt   *time.Time
	adminExempt bool
	policy      any
}

// Store is a concurrency-safe key lifecycle registry.
type Store struct {
	mu       sync.Mutex
	now      func() time.Time
	verify   SignatureVerifier
	nodes    map[string]*nodeState
	keyOwner map[string]string
	retired  map[string]struct{}
	rotated  map[string]struct{}
	version  uint64
}

// NewStore creates an empty key lifecycle store.
func NewStore(opts Options) *Store {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{
		now:      now,
		verify:   opts.Verify,
		nodes:    make(map[string]*nodeState),
		keyOwner: make(map[string]string),
		retired:  make(map[string]struct{}),
		rotated:  make(map[string]struct{}),
	}
}

// SetSignatureVerifier replaces the verifier used for subsequent rotations.
// It does not retroactively change any accepted rotation.
func (s *Store) SetSignatureVerifier(verify SignatureVerifier) {
	s.mu.Lock()
	s.verify = verify
	s.mu.Unlock()
}

// ExpiredAt applies the expiry boundary: a key is expired when expiry is
// non-nil and now is not before it. A nil expiry is treated as exempt.
func ExpiredAt(now time.Time, expiry *time.Time) bool {
	return expiry != nil && !now.Before(*expiry)
}

// AddNode registers a node key. Empty expiry is rejected unless AdminExempt is
// true. The key must be unique and must not revive a retired key.
func (s *Store) AddNode(node Node) error {
	if err := validateNode(node); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.nodes[node.ID]; ok {
		return fmt.Errorf("%w: %q", ErrNodeAlreadyExists, node.ID)
	}
	if owner, ok := s.keyOwner[node.Key]; ok {
		return fmt.Errorf("%w: key belongs to node %q", ErrKeyInUse, owner)
	}
	if _, ok := s.retired[node.Key]; ok {
		return fmt.Errorf("%w: %q", ErrKeyRetired, node.Key)
	}

	s.nodes[node.ID] = &nodeState{
		id:          node.ID,
		key:         node.Key,
		expiresAt:   cloneTime(node.ExpiresAt),
		adminExempt: node.AdminExempt,
		policy:      node.Policy,
	}
	s.keyOwner[node.Key] = node.ID
	s.version++
	return nil
}

// RemoveNode deletes a node immediately. Its key becomes retired and cannot be
// added again.
func (s *Store) RemoveNode(nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, nodeID)
	}
	delete(s.nodes, nodeID)
	delete(s.keyOwner, node.key)
	s.retired[node.key] = struct{}{}
	s.version++
	return nil
}

// SetPolicy updates a node's access policy without changing its key or expiry.
// Policy contents are retained as opaque caller data across rotation.
func (s *Store) SetPolicy(nodeID string, policy any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[nodeID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, nodeID)
	}
	node.policy = policy
	s.version++
	return nil
}

// Version returns the current mutation version.
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Graph returns the current network graph, omitting keys that are expired at
// the Store's current time.
func (s *Store) Graph() Graph {
	return s.GraphAt(s.now())
}

// GraphAt returns the network graph as of now, omitting keys that are expired
// at that instant or later.
func (s *Store) GraphAt(now time.Time) Graph {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.graphAtLocked(now)
}

// DeltaSince returns updates needed to bring a previously distributed graph up
// to date. Expired, removed, and rotated-away keys always produce DeltaDelete
// entries, even when the graph version did not change.
func (s *Store) DeltaSince(graph Graph) []Delta {
	return s.DeltaSinceAt(graph, s.now())
}

// DeltaSinceAt is DeltaSince evaluated at an explicit instant.
func (s *Store) DeltaSinceAt(graph Graph, now time.Time) []Delta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return DiffGraphs(graph, s.graphAtLocked(now))
}

// DiffGraphs computes a deletion-aware delta from a peer's cached graph to the
// current graph. It compares keys rather than node IDs so a rotated key yields
// a delete for the old key and an upsert for the replacement.
func DiffGraphs(base, current Graph) []Delta {
	baseByKey := make(map[string]GraphNode, len(base.Nodes))
	for _, node := range base.Nodes {
		baseByKey[node.Key] = node
	}
	currentByKey := make(map[string]GraphNode, len(current.Nodes))
	for _, node := range current.Nodes {
		currentByKey[node.Key] = node
	}

	var deletes []Delta
	var upserts []Delta

	for key, oldNode := range baseByKey {
		currentNode, present := currentByKey[key]
		if !present {
			deletes = append(deletes, Delta{
				Op:     DeltaDelete,
				NodeID: oldNode.NodeID,
				Key:    key,
			})
			continue
		}
		if !sameGraphNode(oldNode, currentNode) {
			upserts = append(upserts, upsertDelta(currentNode))
		}
	}
	for key, currentNode := range currentByKey {
		if _, present := baseByKey[key]; !present {
			upserts = append(upserts, upsertDelta(currentNode))
		}
	}

	sortDeltas(deletes)
	sortDeltas(upserts)
	deltas := make([]Delta, 0, len(deletes)+len(upserts))
	deltas = append(deltas, deletes...)
	deltas = append(deltas, upserts...)
	return deltas
}

// Rotate replaces a node's key after verifying oldKey's signature over newKey.
//
// The request is accepted only when the old key is still current, is not
// expired at the rotation instant, has not already authorized a rotation, and
// the injected verifier accepts the signature. On success, access policy and
// expiry settings are retained, the old key is retired immediately, and the
// returned node carries the new key.
func (s *Store) Rotate(req RotationRequest) (GraphNode, error) {
	s.mu.Lock()
	if err := s.validateRotationLocked(req); err != nil {
		s.mu.Unlock()
		return GraphNode{}, err
	}
	verify := s.verify
	s.mu.Unlock()

	if !verify(req.OldKey, req.NewKey, req.Signature) {
		return GraphNode{}, ErrInvalidRotationSignature
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Revalidate after invoking caller code: another request may have rotated
	// the key, and time may have crossed the expiry boundary.
	if err := s.validateRotationLocked(req); err != nil {
		return GraphNode{}, err
	}
	node := s.nodes[req.NodeID]

	delete(s.keyOwner, req.OldKey)
	s.retired[req.OldKey] = struct{}{}
	s.rotated[req.OldKey] = struct{}{}
	node.key = req.NewKey
	s.keyOwner[req.NewKey] = req.NodeID
	s.version++

	return node.graphNode(), nil
}

func (s *Store) validateRotationLocked(req RotationRequest) error {
	if _, replayed := s.rotated[req.OldKey]; replayed {
		return fmt.Errorf("%w: %q", ErrOldKeyAlreadyRotated, req.OldKey)
	}
	node, ok := s.nodes[req.NodeID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNodeNotFound, req.NodeID)
	}
	if node.key != req.OldKey {
		return fmt.Errorf("%w: %q", ErrOldKeyNotCurrent, req.OldKey)
	}
	if ExpiredAt(s.now(), node.expiresAt) {
		return fmt.Errorf("%w: %q", ErrOldKeyExpired, req.OldKey)
	}
	if req.NewKey == "" || req.NewKey == req.OldKey {
		return ErrNewKeyInvalid
	}
	if owner, used := s.keyOwner[req.NewKey]; used {
		if owner == req.NodeID {
			return ErrNewKeyInvalid
		}
		return fmt.Errorf("%w: key belongs to node %q", ErrKeyInUse, owner)
	}
	if _, retired := s.retired[req.NewKey]; retired {
		return fmt.Errorf("%w: %q", ErrKeyRetired, req.NewKey)
	}
	if s.verify == nil {
		return ErrSignatureVerifierMissing
	}
	return nil
}

func (s *Store) graphAtLocked(now time.Time) Graph {
	nodes := make([]GraphNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		if ExpiredAt(now, node.expiresAt) {
			continue
		}
		nodes = append(nodes, node.graphNode())
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].NodeID != nodes[j].NodeID {
			return nodes[i].NodeID < nodes[j].NodeID
		}
		return nodes[i].Key < nodes[j].Key
	})
	return Graph{
		Version:     s.version,
		GeneratedAt: now,
		Nodes:       nodes,
	}
}

func (n *nodeState) graphNode() GraphNode {
	return GraphNode{
		NodeID:      n.id,
		Key:         n.key,
		ExpiresAt:   cloneTime(n.expiresAt),
		AdminExempt: n.adminExempt,
		Policy:      n.policy,
	}
}

func validateNode(node Node) error {
	if node.ID == "" {
		return ErrEmptyNodeID
	}
	if node.Key == "" {
		return ErrEmptyKey
	}
	if node.ExpiresAt == nil && !node.AdminExempt {
		return fmt.Errorf("%w: node %q", ErrExpiryWithoutExemption, node.ID)
	}
	return nil
}

func sameGraphNode(a, b GraphNode) bool {
	return a.NodeID == b.NodeID &&
		a.Key == b.Key &&
		a.AdminExempt == b.AdminExempt &&
		equalTime(a.ExpiresAt, b.ExpiresAt) &&
		reflect.DeepEqual(a.Policy, b.Policy)
}

func equalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func upsertDelta(node GraphNode) Delta {
	return Delta{
		Op:     DeltaUpsert,
		NodeID: node.NodeID,
		Key:    node.Key,
		Node:   &node,
	}
}

func sortDeltas(deltas []Delta) {
	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].NodeID != deltas[j].NodeID {
			return deltas[i].NodeID < deltas[j].NodeID
		}
		return deltas[i].Key < deltas[j].Key
	})
}
