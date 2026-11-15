package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"slices"
	"sync"
)

// record 是注册器内一条能力记录，字段除 revoked/leases 外创建后不变。
type record struct {
	tenant     string
	ops        map[string]bool
	generation int
	family     int
	revoked    bool
	leases     int
}

// family 跟踪同一能力血缘的当前代次；旧代次 token 会被拒绝新租约。
type family struct {
	current int
}

// Registry 是可信能力注册器，把不可猜测 token 映射为
// 租户、操作集合、代次与撤销状态，并管理使用租约。
// 所有方法都可并发调用。
type Registry struct {
	mu       sync.Mutex
	seq      int
	recs     map[string]*record
	families map[int]*family
}

// NewRegistry 返回空注册器。
func NewRegistry() *Registry {
	return &Registry{
		recs:     make(map[string]*record),
		families: make(map[int]*family),
	}
}

// newToken 生成 256 位随机 token，不可猜测。
func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Register 为租户登记一个能力，返回其 token，初始代次为 1。
func (r *Registry) Register(tenant string, ops []string) string {
	token := newToken()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	r.families[r.seq] = &family{current: 1}
	r.recs[token] = &record{
		tenant:     tenant,
		ops:        opSet(ops),
		generation: 1,
		family:     r.seq,
	}
	return token
}

// Reissue 为既有能力签发新一代 token 并推进代次。
// 旧 token 保留在注册器中但代次过期，无法再取得新租约；
// 已撤销的 token 不允许续发。token 不存在时返回 ok=false。
func (r *Registry) Reissue(token string) (newTok string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.recs[token]
	if !ok || rec.revoked {
		return "", false
	}
	fam := r.families[rec.family]
	fam.current++
	nt := newToken()
	r.recs[nt] = &record{
		tenant:     rec.tenant,
		ops:        rec.ops,
		generation: fam.current,
		family:     rec.family,
	}
	return nt, true
}

// Revoke 撤销 token。撤销返回后该 token 不能再取得新租约，
// 已持有的租约可正常完成并释放。重复撤销是幂等的。
func (r *Registry) Revoke(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.recs[token]; ok {
		rec.revoked = true
	}
}

// ActiveLeases 返回 token 当前持有的租约数，供宿主监控与测试断言。
func (r *Registry) ActiveLeases(token string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.recs[token]; ok {
		return rec.leases
	}
	return 0
}

// checkTokenLocked 校验 token 是否可租：存在、属于该租户、
// 处于当前代次且未撤销。调用方必须持有 r.mu。
func (r *Registry) checkTokenLocked(tenant, token string) error {
	rec, ok := r.recs[token]
	if !ok {
		return ErrUnknownCapability
	}
	if rec.tenant != tenant {
		return ErrCrossTenant
	}
	if rec.generation != r.families[rec.family].current {
		return ErrStaleGeneration
	}
	if rec.revoked {
		return ErrRevoked
	}
	return nil
}

// checkToken 是验证阶段使用的只读校验；原子租约阶段会在锁内复检。
func (r *Registry) checkToken(tenant, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checkTokenLocked(tenant, token)
}

// acquireAll 原子地为全部去重后的 token 取得一个租约：
// 在同一把锁内先复检所有 token，全部通过才统一计数，
// 任一失败则一个租约也不产生。
func (r *Registry) acquireAll(tenant string, tokens []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tokens {
		if err := r.checkTokenLocked(tenant, t); err != nil {
			return err
		}
	}
	for _, t := range tokens {
		r.recs[t].leases++
	}
	return nil
}

// releaseAll 释放一组租约，计数不会低于零，重复释放安全。
func (r *Registry) releaseAll(tokens []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tokens {
		if rec, ok := r.recs[t]; ok && rec.leases > 0 {
			rec.leases--
		}
	}
}

// snapshotCapabilities 为已租 token 导出不可变的能力句柄元数据。
func (r *Registry) snapshotCapabilities(tokens []string) map[string]*Capability {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*Capability, len(tokens))
	for _, t := range tokens {
		rec := r.recs[t]
		out[t] = &Capability{
			tenant:     rec.tenant,
			ops:        maps.Clone(rec.ops),
			generation: rec.generation,
		}
	}
	return out
}

// leaseSet 是一次解码取得的全部租约，release 幂等且并发安全。
type leaseSet struct {
	reg    *Registry
	tokens []string
	once   sync.Once
}

func (l *leaseSet) release() {
	l.once.Do(func() {
		l.reg.releaseAll(l.tokens)
	})
}

func opSet(ops []string) map[string]bool {
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set
}

// Ops 返回能力包含的操作集合，按字典序排列。
func (c *Capability) Ops() []string {
	return slices.Sorted(maps.Keys(c.ops))
}
