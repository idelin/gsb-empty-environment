package netsec

import "sync"

// Keypair bundles a transport keypair's receiver index with its replay
// window. Cryptographic key material is managed by the caller.
type Keypair struct {
	Index  uint32
	window SlidingWindow
}

// NewKeypair returns a Keypair for the given local receiver index.
func NewKeypair(index uint32) *Keypair {
	return &Keypair{Index: index}
}

// KeyRotation tracks the current and previous transport keypairs of a peer
// session. After a rotation the previous keypair is retained so that
// in-flight packets encrypted under it can still be authenticated; it is
// discarded once the first valid data packet arrives under the new keypair.
type KeyRotation struct {
	mu       sync.Mutex
	current  *Keypair
	previous *Keypair
}

// Rotate installs kp as the current keypair, demoting the existing current
// keypair to previous. Any earlier previous keypair is dropped.
func (k *KeyRotation) Rotate(kp *Keypair) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.previous = k.current
	k.current = kp
}

// Lookup returns the keypair matching a packet's receiver index, searching
// the current keypair and the retained previous one. It returns nil if no
// keypair matches.
func (k *KeyRotation) Lookup(index uint32) *Keypair {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.current != nil && k.current.Index == index {
		return k.current
	}
	if k.previous != nil && k.previous.Index == index {
		return k.previous
	}
	return nil
}

// ReceiveCheck reports whether a data packet on kp with the given counter
// may proceed to AEAD authentication. It rejects keypairs that are no
// longer registered (for example a retired previous keypair the caller
// still holds a pointer to). Nothing is recorded: a packet that fails
// authentication must not consume its counter.
func (k *KeyRotation) ReceiveCheck(kp *Keypair, counter uint64) bool {
	if kp == nil {
		return false
	}
	k.mu.Lock()
	registered := k.current == kp || k.previous == kp
	k.mu.Unlock()
	return registered && kp.window.Check(counter)
}

// ConfirmValid records counter in kp's replay window after the packet's
// AEAD authentication succeeded. If kp is the current keypair, the retained
// previous keypair is discarded, completing the rotation. It returns false
// if counter is a replay or has fallen behind the window.
func (k *KeyRotation) ConfirmValid(kp *Keypair, counter uint64) bool {
	if kp == nil {
		return false
	}
	if !kp.window.Mark(counter) {
		return false
	}
	k.mu.Lock()
	if k.current == kp {
		k.previous = nil
	}
	k.mu.Unlock()
	return true
}

// HasPrevious reports whether a previous keypair is still being retained.
func (k *KeyRotation) HasPrevious() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.previous != nil
}
