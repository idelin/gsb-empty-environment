package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// ResetToken is a 16-byte QUIC stateless reset token.
type ResetToken [StatelessResetTokenSize]byte

// Hex returns the canonical hex representation of a token.
func (t ResetToken) Hex() string { return hex.EncodeToString(t[:]) }

// Bytes returns a copy of the token.
func (t ResetToken) Bytes() []byte {
	out := make([]byte, len(t))
	copy(out, t[:])
	return out
}

// Equal compares tokens in constant time.
func (t ResetToken) Equal(other ResetToken) bool {
	return subtle.ConstantTimeCompare(t[:], other[:]) == 1
}

func hkdfExtract(salt, ikm []byte) [32]byte {
	mac := hmac.New(sha256.New, salt)
	_, _ = mac.Write(ikm)
	var prk [32]byte
	copy(prk[:], mac.Sum(nil))
	return prk
}

func hkdfExpand(prk, info []byte, length int) []byte {
	out := make([]byte, 0, length)
	var block []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		_, _ = mac.Write(block)
		_, _ = mac.Write(info)
		_, _ = mac.Write([]byte{counter})
		block = mac.Sum(nil)
		out = append(out, block...)
	}
	return out[:length]
}

func tokenInfo(generation uint64, route RoutePartition, cid []byte) []byte {
	var info []byte
	info = appendField(info, []byte("q0183/quic/stateless-reset/v1"))
	info = appendU64(info, generation)
	info = appendField(info, []byte(route))
	info = appendField(info, cid)
	return info
}

// DeriveResetToken derives a stateless reset token with HKDF-SHA256. The
// generation, routing partition, and complete CID are all bound into the
// expand info, so changing any one of them changes the token.
func DeriveResetToken(generationKey []byte, generation uint64, route RoutePartition, cid []byte) (ResetToken, error) {
	if len(generationKey) < 16 {
		return ResetToken{}, fmt.Errorf("%w: generation key too short", ErrInvalidConfig)
	}
	if len(cid) == 0 {
		return ResetToken{}, fmt.Errorf("%w: empty CID", ErrInvalidCID)
	}
	prk := hkdfExtract([]byte("q0183/quic/stateless-reset/salt/v1"), generationKey)
	okm := hkdfExpand(prk[:], tokenInfo(generation, route, cid), StatelessResetTokenSize)
	var out ResetToken
	copy(out[:], okm)
	return out, nil
}

// KeyRing owns generation-scoped key material for one QUIC connection.
//
// Every issued CID acquires one reference to its generation. The reference is
// released only when the CID reaches expired, after both the maximum packet
// lifetime and confirmation grace have elapsed. Destroy refuses to erase key
// material while any active or quarantined CID still references it.
type KeyRing struct {
	mu      sync.Mutex
	clock   Clock
	master  [32]byte
	current uint64
	gens    map[uint64]*keyGeneration
}

type keyGeneration struct {
	id        uint64
	key       [32]byte
	createdAt time.Time
	refs      int
	destroyed bool
}

// NewKeyRing creates a key ring. If masterSecret is nil, a random master
// secret is generated. A caller that needs crash recovery should persist and
// reuse the same master secret.
func NewKeyRing(masterSecret []byte, clock Clock) (*KeyRing, error) {
	if clock == nil {
		clock = RealClock{}
	}
	k := &KeyRing{
		clock: clock,
		gens:  make(map[uint64]*keyGeneration),
	}
	if masterSecret == nil {
		if _, err := rand.Read(k.master[:]); err != nil {
			return nil, fmt.Errorf("netsec: generate master key: %w", err)
		}
	} else {
		if len(masterSecret) < 16 {
			return nil, fmt.Errorf("%w: master secret too short", ErrInvalidConfig)
		}
		sum := sha256.Sum256(masterSecret)
		k.master = sum
	}
	k.gens[0] = k.deriveGenerationLocked(0)
	return k, nil
}

func (k *KeyRing) deriveGenerationLocked(id uint64) *keyGeneration {
	prk := hkdfExtract([]byte("q0183/quic/key-generation/salt/v1"), k.master[:])
	key := hkdfExpand(prk[:], appendField([]byte("q0183/quic/key-generation/v1"), appendU64(nil, id)), 32)
	g := &keyGeneration{id: id, createdAt: k.clock.Now()}
	copy(g.key[:], key)
	return g
}

// CurrentGeneration returns the generation used for new CIDs.
func (k *KeyRing) CurrentGeneration() uint64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.current
}

// Rotate advances to a new generation. The old generation and all of its
// tokens remain available until their CID references expire.
func (k *KeyRing) Rotate() (oldGeneration, newGeneration uint64, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	old := k.current
	next := old + 1
	if _, exists := k.gens[next]; exists {
		return 0, 0, fmt.Errorf("netsec: key generation %d already exists", next)
	}
	k.gens[next] = k.deriveGenerationLocked(next)
	k.current = next
	return old, next, nil
}

// HasGeneration reports whether a non-destroyed generation exists.
func (k *KeyRing) HasGeneration(id uint64) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	return ok && !g.destroyed
}

// Token derives the token for a CID in a specific generation and route.
func (k *KeyRing) Token(id uint64, route RoutePartition, cid []byte) (ResetToken, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	if !ok || g.destroyed {
		return ResetToken{}, ErrKeyGenerationNotFound
	}
	return DeriveResetToken(g.key[:], id, route, cid)
}

// Acquire adds a CID reference to a generation. It is called exactly once
// when a CID is issued and released exactly once when it expires.
func (k *KeyRing) Acquire(id uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	if !ok || g.destroyed {
		return ErrKeyGenerationNotFound
	}
	g.refs++
	return nil
}

// Release drops a CID reference from a generation.
func (k *KeyRing) Release(id uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	if !ok || g.destroyed {
		return ErrKeyGenerationNotFound
	}
	if g.refs <= 0 {
		return fmt.Errorf("netsec: key generation %d reference underflow", id)
	}
	g.refs--
	return nil
}

// References returns the number of live (active or quarantined) CIDs that
// still reference a generation.
func (k *KeyRing) References(id uint64) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	if !ok {
		return 0
	}
	return g.refs
}

// Destroy erases a non-current generation only when no CID references it.
func (k *KeyRing) Destroy(id uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	g, ok := k.gens[id]
	if !ok || g.destroyed {
		return ErrKeyGenerationNotFound
	}
	if id == k.current {
		return ErrCurrentKeyGeneration
	}
	if g.refs != 0 {
		return fmt.Errorf("%w: generation %d has %d references", ErrKeyInUse, id, g.refs)
	}
	for i := range g.key {
		g.key[i] = 0
	}
	g.destroyed = true
	return nil
}

// recoveryKey derives a checkpoint MAC key from the master secret. It is
// intentionally distinct from all token and packet-protection keys.
func (k *KeyRing) recoveryKey() [32]byte {
	k.mu.Lock()
	defer k.mu.Unlock()
	prk := hkdfExtract([]byte("q0183/quic/recovery/salt/v1"), k.master[:])
	out := hkdfExpand(prk[:], []byte("q0183/quic/recovery-checkpoint/v1"), 32)
	var key [32]byte
	copy(key[:], out)
	return key
}

// setCurrentForRecovery replays a key rotation record without generating a
// different key schedule.
func (k *KeyRing) setCurrentForRecovery(id uint64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if id <= k.current {
		return fmt.Errorf("%w: key generation goes backwards", ErrJournalCorrupt)
	}
	if id != k.current+1 {
		return fmt.Errorf("%w: key generation jumped from %d to %d", ErrJournalCorrupt, k.current, id)
	}
	k.gens[id] = k.deriveGenerationLocked(id)
	k.current = id
	return nil
}
