package snapscan

import "sync"

// PermStore 是动态权限模型：它始终反映最新的授权状态，
// 只在每次交付时被查询，与数据快照无关。快照固定的是数据，
// 权限从不被快照固定。
type PermStore struct {
	mu      sync.RWMutex
	allowed map[string]bool
}

// NewPermStore 返回一个默认全部拒绝的权限存储。
func NewPermStore() *PermStore {
	return &PermStore{allowed: make(map[string]bool)}
}

// Grant 授权访问某个键。
func (p *PermStore) Grant(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowed[key] = true
}

// Revoke 撤销某个键的权限。撤权立即生效：
// 尚未交付的该键数据从此不得交付；已交付的不受影响。
func (p *PermStore) Revoke(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.allowed, key)
}

// Allow 在交付时刻查询某个键当前是否被允许访问。
func (p *PermStore) Allow(key string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allowed[key]
}
