package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrNotFound           = errors.New("object not found")
	ErrRevoked            = errors.New("object authorization revoked")
	ErrStaleKeyGeneration = errors.New("block sealed with stale key generation")
	ErrPolicyMismatch     = errors.New("block policy digest mismatch")
	ErrPoolRetired        = errors.New("pool retired for tenant; route moved")
	ErrUnknownCredential  = errors.New("credential not valid for this pool")
	ErrKeyRotation        = errors.New("key rotation failed")
)

// Credentials are pool-scoped: they name the tenant and the pool that issued
// them. A credential minted for the old pool is never accepted by the new one.
type Credentials struct {
	Tenant string
	Pool   string
}

// Block is a sealed unit of stored content. Every block carries the key
// generation and policy digest of the pool that sealed it, plus the content
// version and the authorization version it was sealed under.
type Block struct {
	ObjectID     string
	Data         []byte
	ContentSeq   uint64
	AuthSeq      uint64
	KeyGen       uint64
	PolicyDigest [32]byte
}

// Pool is a storage pool serving one tenant in this simulation.
type Pool struct {
	mu               sync.Mutex
	name             string
	tenant           string
	keyGen           uint64
	policyDigest     [32]byte
	blocks           map[string]*Block
	contentSeq       map[string]uint64
	contentWatermark uint64
	authWatermark    uint64
	revoked          map[string]uint64
	retired          bool

	failNextReseal bool // test hook: next RotateKey fails during reseal
}

func NewPool(name, tenant, policy string) *Pool {
	return &Pool{
		name:         name,
		tenant:       tenant,
		keyGen:       1,
		policyDigest: sha256.Sum256([]byte(policy)),
		blocks:       make(map[string]*Block),
		contentSeq:   make(map[string]uint64),
		revoked:      make(map[string]uint64),
	}
}

func (p *Pool) Name() string { return p.name }

func (p *Pool) KeyGen() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.keyGen
}

func (p *Pool) PolicyDigest() [32]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.policyDigest
}

// ContentWatermark is the highest content sequence the pool has applied.
func (p *Pool) ContentWatermark() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.contentWatermark
}

// AuthWatermark is the highest authorization sequence the pool has applied.
// It only moves forward.
func (p *Pool) AuthWatermark() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authWatermark
}

// Write stores a new content version of objectID, sealed under the pool's
// current key generation and policy at the given authorization version.
func (p *Pool) Write(objectID string, data []byte, authSeq uint64) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.contentSeq[objectID]++
	seq := p.contentSeq[objectID]
	p.blocks[objectID] = &Block{
		ObjectID:     objectID,
		Data:         append([]byte(nil), data...),
		ContentSeq:   seq,
		AuthSeq:      authSeq,
		KeyGen:       p.keyGen,
		PolicyDigest: p.policyDigest,
	}
	if seq > p.contentWatermark {
		p.contentWatermark = seq
	}
	return seq
}

// Read serves a block only if the credential matches this pool, the pool is
// still serving the tenant, the object is not revoked, and the block is
// sealed under the pool's current key generation and policy.
func (p *Pool) Read(cred Credentials, objectID string) (*Block, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cred.Tenant != p.tenant || cred.Pool != p.name {
		return nil, ErrUnknownCredential
	}
	if p.retired {
		return nil, ErrPoolRetired
	}
	if _, ok := p.revoked[objectID]; ok {
		return nil, ErrRevoked
	}
	b, ok := p.blocks[objectID]
	if !ok {
		return nil, ErrNotFound
	}
	if b.KeyGen != p.keyGen {
		return nil, ErrStaleKeyGeneration
	}
	if b.PolicyDigest != p.policyDigest {
		return nil, ErrPolicyMismatch
	}
	cp := *b
	return &cp, nil
}

// ApplyAuth advances the pool's revocation state to the given authorization
// version by replaying the journal. The watermark is monotonic: a stale copy
// manifest can never roll back revocations the pool has already applied.
func (p *Pool) ApplyAuth(log *AuthLog, to uint64) {
	ops, head := log.Delta(0)
	p.mu.Lock()
	defer p.mu.Unlock()
	if to <= p.authWatermark {
		return
	}
	if to > head {
		to = head
	}
	for _, op := range ops {
		if op.Seq <= p.authWatermark || op.Seq > to {
			continue
		}
		if op.Revoked {
			p.revoked[op.ObjectID] = op.Seq
		}
	}
	p.authWatermark = to
}

// RotateKey advances the pool key generation and reseals every block under
// the new generation. If the reseal fails, blocks keep their old generation
// and become unreadable until a rotation completes successfully.
func (p *Pool) RotateKey() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keyGen++
	if p.failNextReseal {
		p.failNextReseal = false
		return fmt.Errorf("%w: reseal interrupted at generation %d", ErrKeyRotation, p.keyGen)
	}
	for _, b := range p.blocks {
		b.KeyGen = p.keyGen
		b.PolicyDigest = p.policyDigest
	}
	return nil
}

// install seals a copied block under this pool's current key generation and
// policy digest. Content never rolls back to an older version.
func (p *Pool) install(b Block) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.blocks[b.ObjectID]; ok && cur.ContentSeq >= b.ContentSeq {
		return
	}
	nb := b
	nb.KeyGen = p.keyGen
	nb.PolicyDigest = p.policyDigest
	p.blocks[b.ObjectID] = &nb
	if b.ContentSeq > p.contentWatermark {
		p.contentWatermark = b.ContentSeq
	}
}

// dump snapshots all blocks and the content watermark for a copy manifest.
func (p *Pool) dump() ([]Block, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Block, 0, len(p.blocks))
	for _, b := range p.blocks {
		out = append(out, *b)
	}
	return out, p.contentWatermark
}

// sealedCurrent reports whether every block is sealed under the pool's
// current key generation and policy digest.
func (p *Pool) sealedCurrent() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.blocks {
		if b.KeyGen != p.keyGen || b.PolicyDigest != p.policyDigest {
			return false
		}
	}
	return true
}

// retire stops the pool from serving the tenant. Retired pools reject every
// read, including requests carrying previously valid credentials.
func (p *Pool) retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retired = true
}

// failNextRotation arms the test hook that makes the next RotateKey fail
// during reseal.
func (p *Pool) failNextRotation() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNextReseal = true
}
