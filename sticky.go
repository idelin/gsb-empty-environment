// Package sticky 实现故障转移粘性会话（Sticky Session）维护器。
//
// 根据用户 Cookie 建立粘性绑定：节点健康时，同一会话始终被导向同一节点；
// 一旦节点故障，该节点上的所有会话会基于二级哈希被均匀打散，
// 重定向到其余健康节点，并在会话头中重写绑定标记。
package sticky

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	// UserCookie 是承载用户会话标识的 Cookie 名。
	UserCookie = "sticky_uid"
	// BindingHeader 是写回会话绑定标记的响应头，格式为 "<uid>@<nodeID>"。
	BindingHeader = "X-Sticky-Binding"
)

// ErrNoHealthyNode 表示当前没有任何健康节点可供路由。
var ErrNoHealthyNode = errors.New("sticky: no healthy node available")

// Node 表示一个后端节点。
type Node struct {
	ID      string
	healthy atomic.Bool
}

// NewNode 创建一个初始状态为健康的节点。
func NewNode(id string) *Node {
	n := &Node{ID: id}
	n.healthy.Store(true)
	return n
}

// Healthy 报告节点当前是否健康。
func (n *Node) Healthy() bool { return n.healthy.Load() }

// SetHealthy 直接设置节点健康状态（模拟探活结果）。
func (n *Node) SetHealthy(healthy bool) { n.healthy.Store(healthy) }

// Manager 维护用户会话到节点的粘性绑定，并负责故障转移。
type Manager struct {
	salt string

	mu       sync.RWMutex
	nodes    map[string]*Node
	bindings map[string]string // uid -> nodeID
}

// NewManager 创建一个会话管理器。salt 用于打散哈希，避免不同部署间分布雷同。
func NewManager(salt string, nodes ...*Node) *Manager {
	m := &Manager{
		salt:     salt,
		nodes:    make(map[string]*Node, len(nodes)),
		bindings: make(map[string]string),
	}
	for _, n := range nodes {
		m.nodes[n.ID] = n
	}
	return m
}

// hash 是一个带域分隔的 FNV-1a 哈希工具。
func hash(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// healthyIDsLocked 返回按 ID 排序的健康节点列表，保证取模结果确定。
// 调用方必须持有 m.mu（读或写）。
func (m *Manager) healthyIDsLocked() []string {
	ids := make([]string, 0, len(m.nodes))
	for id, n := range m.nodes {
		if n.Healthy() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// assignPrimaryLocked 用一级哈希把新会话分配到健康节点。
func (m *Manager) assignPrimaryLocked(uid string) (string, error) {
	ids := m.healthyIDsLocked()
	if len(ids) == 0 {
		return "", ErrNoHealthyNode
	}
	return ids[hash("primary", m.salt, uid)%uint64(len(ids))], nil
}

// Bind 返回 uid 应导向的节点。
//
// 已绑定且节点健康时原样返回（粘性）；绑定失效或首次访问时按一级哈希分配。
func (m *Manager) Bind(uid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if nodeID, ok := m.bindings[uid]; ok {
		if n, ok := m.nodes[nodeID]; ok && n.Healthy() {
			return nodeID, nil
		}
		delete(m.bindings, uid)
	}
	nodeID, err := m.assignPrimaryLocked(uid)
	if err != nil {
		return "", err
	}
	m.bindings[uid] = nodeID
	return nodeID, nil
}

// Failover 将 nodeID 标记为故障，并把其上的全部会话基于二级哈希
// 均匀打散重定向到其余健康节点。返回迁移的会话数。
func (m *Manager) Failover(nodeID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[nodeID]
	if !ok {
		return 0, fmt.Errorf("sticky: unknown node %q", nodeID)
	}
	n.SetHealthy(false)

	remaining := m.healthyIDsLocked()
	if len(remaining) == 0 {
		return 0, fmt.Errorf("sticky: failover of %q: %w", nodeID, ErrNoHealthyNode)
	}

	moved := 0
	for uid, bound := range m.bindings {
		if bound != nodeID {
			continue
		}
		// 二级哈希：以故障节点 ID 为盐，使重定向分布与一级分配相互独立。
		next := remaining[hash("failover", m.salt, nodeID, uid)%uint64(len(remaining))]
		m.bindings[uid] = next
		moved++
	}
	return moved, nil
}

// BindingOf 返回 uid 当前的绑定节点。
func (m *Manager) BindingOf(uid string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	nodeID, ok := m.bindings[uid]
	return nodeID, ok
}

// Distribution 统计当前各节点上的会话数。
func (m *Manager) Distribution() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	dist := make(map[string]int, len(m.nodes))
	for _, nodeID := range m.bindings {
		dist[nodeID]++
	}
	return dist
}

// Route 是 HTTP 入口：从请求 Cookie 解析（或签发）用户标识，
// 完成粘性绑定后，在响应头中重写绑定标记并回写用户 Cookie。
// 返回 (uid, nodeID, error)。
func (m *Manager) Route(w http.ResponseWriter, r *http.Request) (string, string, error) {
	uid := ""
	if c, err := r.Cookie(UserCookie); err == nil {
		uid = c.Value
	}
	if uid == "" {
		uid = newUserID()
	}

	nodeID, err := m.Bind(uid)
	if err != nil {
		return "", "", err
	}

	http.SetCookie(w, &http.Cookie{Name: UserCookie, Value: uid, Path: "/"})
	w.Header().Set(BindingHeader, uid+"@"+nodeID)
	return uid, nodeID, nil
}

// newUserID 生成一个随机会话标识。
func newUserID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "u-" + hex.EncodeToString(b[:])
}
