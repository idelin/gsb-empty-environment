package netsec

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrNoRoute           = errors.New("no route for tenant")
	ErrContentBarrier    = errors.New("target behind content barrier")
	ErrRevocationBarrier = errors.New("target behind revocation barrier")
	ErrUnsealedBlocks    = errors.New("target holds blocks not sealed under current key generation and policy")
)

// Manifest is a copy batch snapshotted from the source pool. It carries the
// content versions and the authorization watermark at snapshot time.
type Manifest struct {
	Blocks           []Block
	ContentWatermark uint64
	AuthWatermark    uint64
}

// Router decides which pool serves tenant reads. Until Switchover succeeds
// the route stays on the source pool.
type Router struct {
	mu     sync.RWMutex
	routes map[string]*Pool
}

func NewRouter() *Router {
	return &Router{routes: make(map[string]*Pool)}
}

// PoolFor returns the pool currently serving the tenant.
func (r *Router) PoolFor(tenant string) *Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routes[tenant]
}

func (r *Router) set(tenant string, p *Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[tenant] = p
}

// Read resolves the current route and serves the read from that pool. A
// credential minted for the other pool is rejected by the serving pool.
func (r *Router) Read(cred Credentials, objectID string) (*Block, error) {
	p := r.PoolFor(cred.Tenant)
	if p == nil {
		return nil, ErrNoRoute
	}
	return p.Read(cred, objectID)
}

// Migrator moves a tenant between pools. It is step-driven: Snapshot, Copy,
// SyncAuth and Switchover are separate phases, so a simulation can pause
// between them and inject revocations, writes, or rotation failures.
type Migrator struct {
	tenant string
	src    *Pool
	dst    *Pool
	auth   *AuthLog
	router *Router
}

func NewMigrator(tenant string, src, dst *Pool, auth *AuthLog, router *Router) *Migrator {
	return &Migrator{tenant: tenant, src: src, dst: dst, auth: auth, router: router}
}

// Snapshot captures a copy manifest from the source pool, stamped with the
// current authorization head.
func (m *Migrator) Snapshot() Manifest {
	blocks, cw := m.src.dump()
	return Manifest{Blocks: blocks, ContentWatermark: cw, AuthWatermark: m.auth.Head()}
}

// Copy installs a manifest's blocks into the target pool, each resealed under
// the target's current key generation and policy digest, and advances the
// target's revocation state to the manifest's authorization watermark. The
// target watermark is monotonic: replaying a stale manifest never rolls back
// revocations the target has already applied.
func (m *Migrator) Copy(man Manifest) {
	for _, b := range man.Blocks {
		m.dst.install(b)
	}
	m.dst.ApplyAuth(m.auth, man.AuthWatermark)
}

// SyncAuth pulls the target's revocation state up to the current
// authorization head (the revocation barrier).
func (m *Migrator) SyncAuth() {
	m.dst.ApplyAuth(m.auth, m.auth.Head())
}

// Barriers returns the content and revocation barriers the target must reach
// before the read route may switch.
func (m *Migrator) Barriers() (content, revocation uint64) {
	return m.src.ContentWatermark(), m.auth.Head()
}

// Ready verifies both barriers and the target's sealing. Any miss keeps the
// source route.
func (m *Migrator) Ready() error {
	content, revocation := m.Barriers()
	if got := m.dst.ContentWatermark(); got < content {
		return fmt.Errorf("%w: target at %d, barrier %d", ErrContentBarrier, got, content)
	}
	if got := m.dst.AuthWatermark(); got < revocation {
		return fmt.Errorf("%w: target at %d, barrier %d", ErrRevocationBarrier, got, revocation)
	}
	if !m.dst.sealedCurrent() {
		return ErrUnsealedBlocks
	}
	return nil
}

// Switchover retires the source pool and moves the read route to the target,
// but only when Ready reports no barrier miss. On any miss the route stays on
// the source.
func (m *Migrator) Switchover() error {
	if err := m.Ready(); err != nil {
		return err
	}
	m.src.retire()
	m.router.set(m.tenant, m.dst)
	return nil
}
