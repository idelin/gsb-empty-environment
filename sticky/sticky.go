// Package sticky implements a stateful sticky-session maintainer with
// failover. Users are bound to a backend node via their cookie; while the
// node is healthy every request is routed to that same node. When a node
// fails, all of its sessions are redistributed evenly across the remaining
// healthy nodes using a secondary hash, and the binding marker in each
// session header is rewritten.
package sticky

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
)

// BindingHeader is the session-header key that carries the sticky binding
// marker, e.g. "node=B;gen=2;via=failover".
const BindingHeader = "X-Sticky-Binding"

var (
	ErrUnknownNode   = errors.New("sticky: unknown node")
	ErrUnknownCookie = errors.New("sticky: unknown cookie")
	ErrNoHealthyNode = errors.New("sticky: no healthy node available")
)

// Node is a backend server that sessions can stick to.
type Node struct {
	ID      string
	healthy bool
}

// Healthy reports whether the node is currently accepting sessions.
func (n *Node) Healthy() bool { return n.healthy }

// Session is a stateful user connection bound to one node.
type Session struct {
	ID         string            // stable session id derived from the cookie
	Cookie     string            // original user cookie
	NodeID     string            // node the session is currently bound to
	Generation int               // bumped on every (re)binding
	Header     map[string]string // session headers; BindingHeader is rewritten on failover
}

// Manager keeps cookie->session->node bindings and performs failover.
type Manager struct {
	mu       sync.Mutex
	nodes    map[string]*Node
	order    []string // deterministic node id order (sorted)
	sessions map[string]*Session
}

// NewManager creates a Manager supervising the given node ids.
func NewManager(nodeIDs ...string) *Manager {
	m := &Manager{
		nodes:    make(map[string]*Node, len(nodeIDs)),
		sessions: make(map[string]*Session),
	}
	for _, id := range nodeIDs {
		if _, dup := m.nodes[id]; dup {
			continue
		}
		m.nodes[id] = &Node{ID: id, healthy: true}
		m.order = append(m.order, id)
	}
	sort.Strings(m.order)
	return m
}

// Bind returns the session for a cookie, creating and binding it to a
// healthy node on first sight. Subsequent calls with the same cookie return
// the existing binding, so a user stays on the same node while it lives.
func (m *Manager) Bind(cookie string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[cookie]; ok {
		if m.nodes[s.NodeID].healthy {
			return s, nil
		}
		// Lazy self-heal: node died without HandleNodeFailure being called.
		if err := m.failoverLocked(s.NodeID); err != nil {
			return nil, err
		}
		return s, nil
	}

	node, err := m.pickLocked(primaryHash(cookie))
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID:     sessionID(cookie),
		Cookie: cookie,
		Header: map[string]string{},
	}
	m.bindLocked(s, node, "primary")
	m.sessions[cookie] = s
	return s, nil
}

// Route resolves the node a cookie's traffic must be sent to right now.
func (m *Manager) Route(cookie string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[cookie]
	if !ok {
		return "", ErrUnknownCookie
	}
	if !m.nodes[s.NodeID].healthy {
		if err := m.failoverLocked(s.NodeID); err != nil {
			return "", err
		}
	}
	return s.NodeID, nil
}

// SetNodeHealth marks a node up or down. Marking a node down does not by
// itself move sessions; call HandleNodeFailure (or let Bind/Route heal
// lazily) to redistribute them.
func (m *Manager) SetNodeHealth(nodeID string, healthy bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return ErrUnknownNode
	}
	n.healthy = healthy
	return nil
}

// HandleNodeFailure is the exception hook invoked when nodeID is detected
// down. It marks the node unhealthy and immediately redistributes every
// session bound to it across the remaining healthy nodes using a secondary
// hash keyed by (session id, failed node), rewriting each session's binding
// marker. Returns the number of sessions migrated.
func (m *Manager) HandleNodeFailure(nodeID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return 0, ErrUnknownNode
	}
	n.healthy = false
	migrated := 0
	for _, s := range m.sessions {
		if s.NodeID == nodeID {
			migrated++
		}
	}
	if err := m.failoverLocked(nodeID); err != nil {
		return 0, err
	}
	return migrated, nil
}

// Distribution returns how many sessions are currently bound to each node.
func (m *Manager) Distribution() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	dist := make(map[string]int, len(m.nodes))
	for id := range m.nodes {
		dist[id] = 0
	}
	for _, s := range m.sessions {
		dist[s.NodeID]++
	}
	return dist
}

// Session returns the session bound to a cookie, or nil.
func (m *Manager) Session(cookie string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[cookie]
}

// failoverLocked moves every session stuck on failedNode to a healthy node
// chosen by the secondary hash. Callers must hold m.mu.
func (m *Manager) failoverLocked(failedNode string) error {
	targets := m.healthyLocked()
	if len(targets) == 0 {
		return ErrNoHealthyNode
	}
	for _, s := range m.sessions {
		if s.NodeID != failedNode {
			continue
		}
		idx := secondaryHash(s.ID, failedNode) % uint64(len(targets))
		m.bindLocked(s, targets[idx], "failover")
	}
	return nil
}

// bindLocked (re)binds a session to a node and rewrites the binding marker.
func (m *Manager) bindLocked(s *Session, n *Node, via string) {
	s.NodeID = n.ID
	s.Generation++
	s.Header[BindingHeader] = fmt.Sprintf("node=%s;gen=%d;via=%s", n.ID, s.Generation, via)
}

// pickLocked selects a healthy node by hash with deterministic fallthrough.
func (m *Manager) pickLocked(h uint64) (*Node, error) {
	targets := m.healthyLocked()
	if len(targets) == 0 {
		return nil, ErrNoHealthyNode
	}
	return targets[h%uint64(len(targets))], nil
}

// healthyLocked lists healthy nodes in deterministic (sorted) order.
func (m *Manager) healthyLocked() []*Node {
	var out []*Node
	for _, id := range m.order {
		if n := m.nodes[id]; n.healthy {
			out = append(out, n)
		}
	}
	return out
}

// primaryHash assigns a new cookie to a node slot.
func primaryHash(cookie string) uint64 {
	return mix(fnv64(cookie))
}

// secondaryHash spreads the failed node's sessions over the survivors. It is
// keyed by session id AND the failed node so the same sessions would spread
// differently if another node failed later.
func secondaryHash(sessionID, failedNode string) uint64 {
	return mix(fnv64(sessionID) ^ mix(fnv64(failedNode)))
}

// sessionID derives a stable session id from the user cookie.
func sessionID(cookie string) string {
	return fmt.Sprintf("sess-%016x", fnv64("sid:"+cookie))
}

func fnv64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// mix is a 64-bit finalizer (murmur3-style) so the low bits used by the
// modulo are well distributed.
func mix(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
