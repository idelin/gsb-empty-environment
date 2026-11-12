package taskpkg

import (
	"crypto/ed25519"
	"sync"
	"time"
)

// Principal 是经过宿主信任边界认证后的调用方身份。
//
// 宿主假定走到该接口的身份已由外层认证（mTLS、鉴权中间件等）确认；
// 本包负责的是认证之后的对象级授权，而不是身份认证本身。
type Principal struct {
	TenantID string
}

// tenant 保存租户的签名公钥与数据加密密钥。
type tenant struct {
	pub ed25519.PublicKey
	dek []byte
}

// object 是小对象模型：单条密文记录，版本单调递增，代次随替换/移交推进。
type object struct {
	id         string
	owner      string
	version    int64
	generation int64
	ciphertext []byte
}

// cacheKey 让解包缓存与对象 ID、能力代次、租户三者同时绑定。
// 代次或租户任一变化都会落到不同键，旧明文不可能被新所有者或旧句柄命中。
type cacheKey struct {
	objectID   string
	generation int64
	tenant     string
}

// Host 是供 Go 存储宿主调用的能力句柄服务。
type Host struct {
	mu        sync.RWMutex
	tenants   map[string]*tenant
	objects   map[string]*object
	plaintext map[cacheKey][]byte
	usedNonce map[string]struct{}

	locks *LockManager
	now   func() time.Time
	ttl   time.Duration
}

// Option 配置宿主行为，主要用于测试注入时钟。
type Option func(*Host)

// WithClock 注入宿主时钟。
func WithClock(now func() time.Time) Option {
	return func(h *Host) { h.now = now }
}

// WithTicketTTL 覆盖移交票据的默认有效期（1 分钟）。
func WithTicketTTL(ttl time.Duration) Option {
	return func(h *Host) { h.ttl = ttl }
}

// NewHost 创建空宿主并生成锁管理器。
func NewHost(opts ...Option) *Host {
	h := &Host{
		tenants:   make(map[string]*tenant),
		objects:   make(map[string]*object),
		plaintext: make(map[cacheKey][]byte),
		usedNonce: make(map[string]struct{}),
		locks:     NewLockManager(),
		now:       time.Now,
		ttl:       time.Minute,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Locks 暴露锁管理器。锁只做冲突控制，宿主不会据此推断任何读权限。
func (h *Host) Locks() *LockManager { return h.locks }

// RegisterTenant 登记租户签名公钥，并为其发放数据加密密钥。
func (h *Host) RegisterTenant(id string, pub ed25519.PublicKey) error {
	if id == "" || len(pub) != ed25519.PublicKeySize {
		return ErrUnknownTenant
	}
	dek, err := newDataKey()
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tenants[id] = &tenant{pub: append(ed25519.PublicKey(nil), pub...), dek: dek}
	return nil
}

// Create 让租户创建一个全新的小对象，初始版本与能力代次均为 1。
func (h *Host) Create(p Principal, objectID string, plaintext []byte) error {
	if objectID == "" {
		return ErrUnknownObject
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tenants[p.TenantID]
	if !ok {
		return ErrUnknownTenant
	}
	if _, exists := h.objects[objectID]; exists {
		return ErrLockConflict
	}
	sealed, err := seal(t.dek, plaintext)
	if err != nil {
		return err
	}
	h.objects[objectID] = &object{
		id:         objectID,
		owner:      p.TenantID,
		version:    1,
		generation: 1,
		ciphertext: sealed,
	}
	return nil
}

// Open 是可信打开操作：校验身份与对象归属后签发能力句柄。
//
// 能力固化对象 ID、当前版本、租户、当前能力代次和请求的读写权限。
// perms 必须是 PermRead / PermWrite 的非空子集。
func (h *Host) Open(p Principal, objectID string, perms uint8) (Capability, error) {
	if perms == 0 || perms&^(PermRead|PermWrite) != 0 {
		return Capability{}, ErrAccessDenied
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.tenants[p.TenantID]; !ok {
		return Capability{}, ErrUnknownTenant
	}
	obj, ok := h.objects[objectID]
	if !ok {
		return Capability{}, ErrUnknownObject
	}
	if obj.owner != p.TenantID {
		// 跨租户打开直接拒绝；锁持有状态不参与判定。
		return Capability{}, ErrWrongTenant
	}
	return Capability{
		objectID:   obj.id,
		version:    obj.version,
		tenantID:   obj.owner,
		generation: obj.generation,
		perms:      perms,
	}, nil
}

// Read 在每次读取时重新核对同一能力代次，然后返回明文副本。
//
// 授权完全基于能力：是否持有数据库锁对结果没有任何影响。
// 解包缓存仅在全部授权检查通过后才允许读取或写入。
func (h *Host) Read(c Capability) ([]byte, error) {
	if !c.CanRead() {
		return nil, ErrAccessDenied
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	t, ok := h.tenants[c.tenantID]
	if !ok {
		return nil, ErrUnknownTenant
	}
	obj, ok := h.objects[c.objectID]
	if !ok {
		return nil, ErrUnknownObject
	}
	if obj.owner != c.tenantID {
		return nil, ErrWrongTenant
	}
	// 每次读取都重新核对同一能力代次：替换或移交推进代次后，旧句柄立即失效。
	if obj.generation != c.generation {
		return nil, ErrStaleCapability
	}
	if obj.version != c.version {
		return nil, ErrStaleCapability
	}

	key := cacheKey{objectID: obj.id, generation: obj.generation, tenant: obj.owner}
	if pt, hit := h.plaintext[key]; hit {
		return append([]byte(nil), pt...), nil
	}
	pt, err := open(t.dek, obj.ciphertext)
	if err != nil {
		return nil, err
	}
	h.plaintext[key] = append([]byte(nil), pt...)
	return append([]byte(nil), pt...), nil
}

// Replace 在排他锁保护下替换对象内容，推进版本与能力代次并清空解包缓存。
//
// 调用方必须以自身租户身份持有该对象的排他锁；锁本身不授予写权限，
// 能力仍须带 PermWrite 且代次与当前对象一致。
func (h *Host) Replace(p Principal, c Capability, plaintext []byte) error {
	if !c.CanWrite() {
		return ErrAccessDenied
	}
	if p.TenantID != c.tenantID {
		return ErrWrongTenant
	}
	if !h.locks.HoldsExclusive(c.objectID, p.TenantID) {
		return ErrNotLocked
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	t, ok := h.tenants[c.tenantID]
	if !ok {
		return ErrUnknownTenant
	}
	obj, ok := h.objects[c.objectID]
	if !ok {
		return ErrUnknownObject
	}
	if obj.owner != c.tenantID {
		return ErrWrongTenant
	}
	if obj.generation != c.generation || obj.version != c.version {
		return ErrStaleCapability
	}
	sealed, err := seal(t.dek, plaintext)
	if err != nil {
		return err
	}
	obj.ciphertext = sealed
	obj.version++
	obj.generation++
	h.purgeCacheLocked(obj.id)
	return nil
}

// Transfer 凭双方签名的目标绑定票据完成跨租户移交。
//
// 所有票据校验（结构、租户、时效、重放、版本代次、双签名）全部通过后，
// 才会在宿主内部解包并用目标租户密钥重新封装；任何失败路径都不触碰
// 解包缓存，也不向外返回明文，保证失败移交不泄漏解包数据。
func (h *Host) Transfer(p Principal, t Ticket) error {
	if t.ObjectID == "" || t.FromTenant == "" || t.ToTenant == "" || len(t.Nonce) == 0 {
		return ErrInvalidTicket
	}
	if t.FromTenant == t.ToTenant {
		return ErrInvalidTicket
	}
	if p.TenantID != t.FromTenant {
		return ErrWrongTenant
	}
	if !h.locks.HoldsExclusive(t.ObjectID, t.FromTenant) {
		return ErrNotLocked
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	src, ok := h.tenants[t.FromTenant]
	if !ok {
		return ErrUnknownTenant
	}
	dst, ok := h.tenants[t.ToTenant]
	if !ok {
		return ErrUnknownTenant
	}
	if !t.ExpiresAt.After(h.now()) {
		return ErrTicketExpired
	}
	nonceKey := string(t.Nonce)
	if _, replay := h.usedNonce[nonceKey]; replay {
		return ErrTicketReplay
	}
	obj, ok := h.objects[t.ObjectID]
	if !ok {
		return ErrUnknownObject
	}
	if obj.owner != t.FromTenant {
		return ErrWrongTenant
	}
	if obj.version != t.Version || obj.generation != t.Generation {
		return ErrStaleCapability
	}
	if !t.Verify(src.pub, dst.pub) {
		return ErrSignature
	}

	// 校验全部通过后才允许明文在宿主内存中短暂出现。
	pt, err := open(src.dek, obj.ciphertext)
	if err != nil {
		return err
	}
	resealed, err := seal(dst.dek, pt)
	if err != nil {
		return err
	}
	obj.ciphertext = resealed
	obj.owner = t.ToTenant
	obj.version++
	obj.generation++
	h.purgeCacheLocked(obj.id)
	h.usedNonce[nonceKey] = struct{}{}
	return nil
}

// purgeCacheLocked 删除对象在所有代次、所有租户下的解包缓存条目。
func (h *Host) purgeCacheLocked(objectID string) {
	for k := range h.plaintext {
		if k.objectID == objectID {
			delete(h.plaintext, k)
		}
	}
}

// cacheSize 仅供测试断言缓存行为。
func (h *Host) cacheSize() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.plaintext)
}

// hasCacheEntry 仅供测试断言指定键是否存在解包缓存。
func (h *Host) hasCacheEntry(objectID, tenant string, generation int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.plaintext[cacheKey{objectID: objectID, generation: generation, tenant: tenant}]
	return ok
}
