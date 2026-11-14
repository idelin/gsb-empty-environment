package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// DeviceID identifies one tunnel device instance. A binding is never valid
// for another device, even if all numeric and cryptographic fields happen to
// collide.
type DeviceID string

// Index is the receiver index carried by WireGuard messages. It is a routing
// label, not an identity: more than one binding may temporarily be associated
// with the same numeric index while an old binding is draining.
type Index uint32

// ReceiverIndex is an alias for Index used by host adapters.
type ReceiverIndex = Index

// PublicKey is a WireGuard Curve25519 public key.
type PublicKey [32]byte

// MAC is an authenticated MAC carried by a handshake message. WireGuard uses
// 16-byte MAC1 values.
type MAC [16]byte

// HandshakeHash is the hash that identifies one handshake instance.
type HandshakeHash [32]byte

// MessageDigest identifies the handshake message that caused a cookie reply.
type MessageDigest [32]byte

// Nonce is a random value created together with a binding. It is distinct from
// the WireGuard handshake nonce (the temporary key and handshake hash cover
// that value).
type Nonce [16]byte

// BindingID is an opaque capability handle. Callers must keep it (or the
// *Binding) for asynchronous work instead of looking work up by numeric index.
type BindingID [16]byte

// Capability is a digest of the immutable binding identity: device instance,
// peer static key, handshake generation, temporary key, creation nonce, and
// binding ID.
type Capability [32]byte

// Timestamp is WireGuard's 12-byte handshake timestamp.
type Timestamp [12]byte

// AddressFamily is the address family of a cookie source.
type AddressFamily uint8

const (
	AddressFamilyUnknown AddressFamily = iota
	AddressFamilyIPv4
	AddressFamilyIPv6
)

// LifecycleState is the state of one receiver-index capability.
type LifecycleState uint8

// BindingState is an alias for LifecycleState.
type BindingState = LifecycleState

const (
	StateUnallocated LifecycleState = iota
	StateInitiationSent
	StateResponseAccepted
	StateKeypairActive
	StateRetired
	StateQuarantine
)

// Short aliases make transition tables and host code easier to read.
const (
	InitiationSent   = StateInitiationSent
	ResponseAccepted = StateResponseAccepted
	KeypairActive    = StateKeypairActive
	Retired          = StateRetired
	Quarantine       = StateQuarantine
	Quarantined      = StateQuarantine
	StateQuarantined = StateQuarantine
	StateActive      = StateKeypairActive
	Active           = StateKeypairActive
)

// String returns a stable state name for logs.
func (s LifecycleState) String() string {
	switch s {
	case StateUnallocated:
		return "unallocated"
	case StateInitiationSent:
		return "InitiationSent"
	case StateResponseAccepted:
		return "ResponseAccepted"
	case StateKeypairActive:
		return "KeypairActive"
	case StateRetired:
		return "Retired"
	case StateQuarantine:
		return "Quarantine"
	default:
		return fmt.Sprintf("LifecycleState(%d)", uint8(s))
	}
}

// CanTransitionTo reports whether a lifecycle transition is legal. Reclaiming
// an index is a manager operation, not a lifecycle transition.
func (s LifecycleState) CanTransitionTo(next LifecycleState) bool {
	switch s {
	case StateInitiationSent:
		return next == StateResponseAccepted || next == StateRetired
	case StateResponseAccepted:
		return next == StateKeypairActive || next == StateRetired
	case StateKeypairActive:
		return next == StateRetired
	case StateRetired:
		return next == StateQuarantine
	default:
		return false
	}
}

// IsZero reports whether the key is all zeroes.
func (k PublicKey) IsZero() bool {
	var zero PublicKey
	return subtle.ConstantTimeCompare(k[:], zero[:]) == 1
}

// Bytes returns a copy of the key.
func (k PublicKey) Bytes() []byte {
	out := make([]byte, len(k))
	copy(out, k[:])
	return out
}

// String returns the lower-case hexadecimal encoding.
func (k PublicKey) String() string { return hex.EncodeToString(k[:]) }

// PublicKeyFromBytes converts exactly 32 bytes into a PublicKey.
func PublicKeyFromBytes(b []byte) (PublicKey, error) {
	var k PublicKey
	if len(b) != len(k) {
		return k, fmt.Errorf("netsec: public key has %d bytes, want %d", len(b), len(k))
	}
	copy(k[:], b)
	return k, nil
}

// MustPublicKey is PublicKeyFromBytes for compile-time constant key material.
func MustPublicKey(b []byte) PublicKey {
	k, err := PublicKeyFromBytes(b)
	if err != nil {
		panic(err)
	}
	return k
}

// ParsePublicKey parses a 64-character hexadecimal key.
func ParsePublicKey(s string) (PublicKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return PublicKey{}, fmt.Errorf("netsec: parse public key: %w", err)
	}
	return PublicKeyFromBytes(b)
}

// IsZero reports whether the ID is all zeroes.
func (id BindingID) IsZero() bool {
	var zero BindingID
	return id == zero
}

// String returns the lower-case hexadecimal encoding.
func (id BindingID) String() string { return hex.EncodeToString(id[:]) }

// Bytes returns a copy of the ID.
func (id BindingID) Bytes() []byte {
	out := make([]byte, len(id))
	copy(out, id[:])
	return out
}

// IsZero reports whether the capability is all zeroes.
func (c Capability) IsZero() bool {
	var zero Capability
	return c == zero
}

// String returns the lower-case hexadecimal encoding.
func (c Capability) String() string { return hex.EncodeToString(c[:]) }

// IsZero reports whether the nonce is all zeroes.
func (n Nonce) IsZero() bool {
	var zero Nonce
	return n == zero
}

// String returns the lower-case hexadecimal encoding.
func (n Nonce) String() string { return hex.EncodeToString(n[:]) }

// TimestampFromTime encodes a timestamp using seconds followed by nanoseconds.
func TimestampFromTime(t time.Time) Timestamp {
	var ts Timestamp
	binary.BigEndian.PutUint64(ts[0:8], uint64(t.Unix()))
	binary.BigEndian.PutUint32(ts[8:12], uint32(t.Nanosecond()))
	return ts
}

// TimestampFromUint64 encodes a seconds-resolution timestamp. The low four
// bytes remain zero.
func TimestampFromUint64(v uint64) Timestamp {
	var ts Timestamp
	binary.BigEndian.PutUint64(ts[0:8], v)
	return ts
}

// TimestampFromBytes converts exactly 12 bytes into a Timestamp.
func TimestampFromBytes(b []byte) (Timestamp, error) {
	var ts Timestamp
	if len(b) != len(ts) {
		return ts, fmt.Errorf("netsec: timestamp has %d bytes, want %d", len(b), len(ts))
	}
	copy(ts[:], b)
	return ts, nil
}

// IsZero reports whether the timestamp is all zeroes.
func (ts Timestamp) IsZero() bool {
	var zero Timestamp
	return ts == zero
}

// Compare returns -1, 0, or 1 using timestamp byte order.
func (ts Timestamp) Compare(other Timestamp) int {
	for i := range ts {
		switch {
		case ts[i] < other[i]:
			return -1
		case ts[i] > other[i]:
			return 1
		}
	}
	return 0
}

// String returns the lower-case hexadecimal encoding.
func (ts Timestamp) String() string { return hex.EncodeToString(ts[:]) }

// FamilyOf returns the family of addr after removing an IPv4-in-IPv6 mapping.
func FamilyOf(addr netip.Addr) AddressFamily {
	if !addr.IsValid() {
		return AddressFamilyUnknown
	}
	if addr.Is4() || addr.Is4In6() {
		return AddressFamilyIPv4
	}
	return AddressFamilyIPv6
}

// String returns a stable address-family name.
func (f AddressFamily) String() string {
	switch f {
	case AddressFamilyIPv4:
		return "IPv4"
	case AddressFamilyIPv6:
		return "IPv6"
	default:
		return "unknown"
	}
}

// SourceAddress normalizes an endpoint so IPv4-in-IPv6 representation cannot
// bypass the source-family check.
func normalizeAddrPort(ap netip.AddrPort) netip.AddrPort {
	if !ap.IsValid() {
		return netip.AddrPort{}
	}
	addr := ap.Addr()
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	return netip.AddrPortFrom(addr, ap.Port())
}

func randomBytes(r io.Reader, n int) ([]byte, error) {
	if r == nil {
		r = rand.Reader
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, fmt.Errorf("netsec: generate random bytes: %w", err)
	}
	return b, nil
}

func randomNonce(r io.Reader) (Nonce, error) {
	var n Nonce
	b, err := randomBytes(r, len(n))
	if err != nil {
		return n, err
	}
	copy(n[:], b)
	return n, nil
}

func randomBindingID(r io.Reader) (BindingID, error) {
	var id BindingID
	b, err := randomBytes(r, len(id))
	if err != nil {
		return id, err
	}
	copy(id[:], b)
	return id, nil
}

func computeCapability(device DeviceID, peer PublicKey, generation uint64, ephemeral PublicKey, nonce Nonce, id BindingID) Capability {
	h := sha256.New()
	writeLengthPrefixed(h, []byte("netsec/binding-capability/v1"))
	writeLengthPrefixed(h, []byte(device))
	writeLengthPrefixed(h, peer[:])
	var gen [8]byte
	binary.BigEndian.PutUint64(gen[:], generation)
	writeLengthPrefixed(h, gen[:])
	writeLengthPrefixed(h, ephemeral[:])
	writeLengthPrefixed(h, nonce[:])
	writeLengthPrefixed(h, id[:])
	var out Capability
	copy(out[:], h.Sum(nil))
	return out
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeLengthPrefixed(w digestWriter, b []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(b)))
	_, _ = w.Write(n[:])
	_, _ = w.Write(b)
}
