package migsim

import (
	"fmt"
	"sync"
)

// Volume is a physical storage volume holding immutable replicas of block
// content, keyed by address. Addresses are unique per volume and never
// reused, so a stale receipt referencing an old address can never collide
// with a newer replica.
type Volume struct {
	mu     sync.Mutex
	name   string
	next   uint64
	copies map[string][]byte

	// OnPut, if set, is invoked right after a replica is stored. Test hook
	// used to inject corruption for digest-failure scenarios.
	OnPut func(addr string)
}

// NewVolume returns an empty volume with the given name.
func NewVolume(name string) *Volume {
	return &Volume{name: name, copies: make(map[string][]byte)}
}

// Name returns the volume name.
func (v *Volume) Name() string { return v.name }

// Put stores a copy of data and returns its freshly allocated address.
func (v *Volume) Put(data []byte) string {
	v.mu.Lock()
	v.next++
	addr := fmt.Sprintf("%s#%d", v.name, v.next)
	v.copies[addr] = append([]byte(nil), data...)
	hook := v.OnPut
	v.mu.Unlock()
	if hook != nil {
		hook(addr)
	}
	return addr
}

// Get returns a copy of the replica stored at addr.
func (v *Volume) Get(addr string) ([]byte, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	data, ok := v.copies[addr]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), data...), true
}

// Delete removes the replica at addr. It is idempotent: replaying a stale
// delete receipt for an already-reclaimed address is a safe no-op.
func (v *Volume) Delete(addr string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.copies, addr)
}

// Has reports whether a replica exists at addr.
func (v *Volume) Has(addr string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.copies[addr]
	return ok
}

// Count returns the number of replicas currently stored.
func (v *Volume) Count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.copies)
}

// Corrupt flips a byte of the replica at addr. Test aid used to simulate a
// bad copy that must fail digest verification.
func (v *Volume) Corrupt(addr string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if data, ok := v.copies[addr]; ok && len(data) > 0 {
		data[0] ^= 0xff
	}
}
