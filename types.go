package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
)

// DeviceID identifies a member device. It is the raw 32-byte Ed25519 public
// key of the device; DeviceFromPublic converts an ed25519.PublicKey.
type DeviceID [32]byte

// ChainID identifies a sender chain. ChainIDs must never be reused within a
// group, across every epoch that group ever reaches.
type ChainID [16]byte

// Handle is an opaque reference to a chain key held in the host key store.
type Handle string

// Digest is a 32-byte member-set digest (SHA-256 over a canonical encoding).
type Digest [32]byte

// DeviceFromPublic converts an Ed25519 public key to a DeviceID. It panics if
// the key is not exactly 32 bytes.
func DeviceFromPublic(pub ed25519.PublicKey) DeviceID {
	if len(pub) != ed25519.PublicKeySize {
		panic("netsec: ed25519 public key must be 32 bytes")
	}
	var id DeviceID
	copy(id[:], pub)
	return id
}

// MemberSet is the set of member device public keys authorized for one epoch.
type MemberSet map[DeviceID]struct{}

// NewMemberSet builds a MemberSet from the given device identifiers, dropping
// duplicates.
func NewMemberSet(devices ...DeviceID) MemberSet {
	set := make(MemberSet, len(devices))
	for _, device := range devices {
		set[device] = struct{}{}
	}
	return set
}

// Contains reports whether device is a member.
func (m MemberSet) Contains(device DeviceID) bool {
	_, ok := m[device]
	return ok
}

// Clone returns an independent copy of the member set.
func (m MemberSet) Clone() MemberSet {
	clone := make(MemberSet, len(m))
	for device := range m {
		clone[device] = struct{}{}
	}
	return clone
}

// Equal reports whether two member sets contain exactly the same devices.
func (m MemberSet) Equal(other MemberSet) bool {
	if len(m) != len(other) {
		return false
	}
	for device := range m {
		if !other.Contains(device) {
			return false
		}
	}
	return true
}

// Digest computes the canonical member-set digest. The encoding is
// length-prefixed, lexicographically sorted raw device public keys, so two
// equal sets always produce the same digest regardless of insertion order.
func (m MemberSet) Digest() Digest {
	devices := make([][32]byte, 0, len(m))
	for device := range m {
		devices = append(devices, device)
	}
	// Fixed-size byte arrays implement a total lexicographic order.
	sort32(devices)

	h := sha256.New()
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(devices)))
	h.Write(length[:])
	for _, device := range devices {
		h.Write(device[:])
	}
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

func sort32(values [][32]byte) {
	// Insertion sort keeps this dependency-free; member sets in messaging
	// hosts are small. correctness only requires a deterministic ordering.
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && less32(values[j], values[j-1]); j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func less32(a, b [32]byte) bool {
	for i := 0; i < 32; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func encodeUint64(index uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, index)
	return buf
}

func encodeLengthPrefixed(data []byte) []byte {
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	return buf
}

func digestBytes(data []byte) Digest {
	return sha256.Sum256(data)
}
