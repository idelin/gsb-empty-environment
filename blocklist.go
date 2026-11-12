package netsec

import "sync"

// Blocklist is a set of blocked content objects keyed by multihash. Because
// the key is the multihash rather than a rendered CID, a single entry blocks
// the content regardless of whether it is requested as CIDv0, CIDv1, or any
// multibase spelling of either.
//
// A Blocklist is safe for concurrent use.
type Blocklist struct {
	mu      sync.RWMutex
	blocked map[string]struct{}
}

// NewBlocklist creates an empty blocklist.
func NewBlocklist() *Blocklist {
	return &Blocklist{blocked: make(map[string]struct{})}
}

// Add blocks a multihash. The entry may be raw encoded bytes, a "z"-prefixed
// base58btc multibase string, or a bare base58btc multihash.
func (b *Blocklist) Add(multihash string) error {
	mh, err := parseMultihash(multihash)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.blocked[string(mh.raw)] = struct{}{}
	b.mu.Unlock()
	return nil
}

// AddBytes blocks an already-encoded multihash.
func (b *Blocklist) AddBytes(raw []byte) error {
	mh, err := MultihashFromBytes(raw)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.blocked[string(mh.raw)] = struct{}{}
	b.mu.Unlock()
	return nil
}

// Remove unblocks a multihash previously added with the same encoding.
func (b *Blocklist) Remove(multihash string) error {
	mh, err := parseMultihash(multihash)
	if err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.blocked, string(mh.raw))
	b.mu.Unlock()
	return nil
}

// Contains reports whether the multihash underlying cid is blocked. This is the
// sole enforcement primitive: every CID spelling normalizes here first.
func (b *Blocklist) Contains(mh Multihash) bool {
	if b == nil || mh.IsZero() {
		return false
	}
	b.mu.RLock()
	_, ok := b.blocked[string(mh.raw)]
	b.mu.RUnlock()
	return ok
}

// Len returns the number of distinct blocked multihashes.
func (b *Blocklist) Len() int {
	b.mu.RLock()
	n := len(b.blocked)
	b.mu.RUnlock()
	return n
}
