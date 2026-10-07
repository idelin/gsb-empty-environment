package snapshotscan

import "sync"

// PermissionModel is the dynamic, always-latest permission view.
//
// Unlike the data snapshot, permissions are never pinned: every Allowed
// call observes the most recent Grant/Revoke, so a revocation takes
// effect at the very next delivery while an item already delivered is
// never retroactively touched.
type PermissionModel struct {
	mu      sync.RWMutex
	version uint64
	allowed map[string]map[string]bool // subject -> key -> allowed
}

func NewPermissionModel() *PermissionModel {
	return &PermissionModel{allowed: map[string]map[string]bool{}}
}

func (p *PermissionModel) Grant(subject, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	set, ok := p.allowed[subject]
	if !ok {
		set = map[string]bool{}
		p.allowed[subject] = set
	}
	set[key] = true
	p.version++
}

func (p *PermissionModel) Revoke(subject, key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if set, ok := p.allowed[subject]; ok {
		delete(set, key)
	}
	p.version++
}

// Allowed evaluates the latest permission state.
func (p *PermissionModel) Allowed(subject, key string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.allowed[subject][key]
}

// Version exposes the current permission version, monotonically
// increasing with every Grant/Revoke.
func (p *PermissionModel) Version() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.version
}
