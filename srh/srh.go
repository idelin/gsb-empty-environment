// Package srh implements admission control for SRv6 Segment Routing Header
// (SRH, routing type 4) packets crossing an SR domain boundary, following
// RFC 8754.
//
// A packet arriving from outside the domain that carries an SRH must be
// authenticated with an HMAC TLV so that an off-path attacker cannot steer
// traffic through an arbitrary segment list. The enforced policy is:
//
//   - an SRH from outside the domain must carry an HMAC TLV;
//   - the HMAC TLV Key ID must be non-zero;
//   - the HMAC-SHA-256 digest must verify against the key named by Key ID;
//   - the packet is dropped when Segments Left > Last Entry + 1.
//
// The authenticated input to HMAC-SHA-256 is the concatenation of:
//
//	source address (16 octets) || Last Entry (1 octet) || Flags (1 octet) ||
//	reserved (2 zero octets) || Key ID (4 octets, big-endian) ||
//	the whole segment list (each SID 16 octets, in packet order).
package srh

import (
	"crypto/sha256"
	"errors"
	"sync"
)

const (
	ipv6HeaderLen = 40

	nextHeaderRouting  = 43 // IPv6 Routing extension header
	nextHeaderHopByHop = 0
	nextHeaderFragment = 44
	nextHeaderDestOpts = 60
	routingTypeSRH     = 4
	srhFixedHeaderLen  = 8
	sidLen             = 16
	hmacFlagBit        = 0x01 // SRH Flags: HMAC TLV present
	tlvTypeHMAC        = 2    // RFC 8754 Section 2.2
	hmacTLVValueLen    = 38   // Reserved(2) + Key ID(4) + HMAC-SHA-256(32)
	hmacTLVTotalLen    = 2 + hmacTLVValueLen
	hmacSHA256Len      = sha256.Size
)

// Drop reasons returned by Admit. Use errors.Is to match them.
var (
	// ErrMalformed means the packet or its SRH is structurally invalid or
	// truncated.
	ErrMalformed = errors.New("srh: malformed packet or SRH")
	// ErrNoHMAC means an externally sourced SRH carries no HMAC TLV.
	ErrNoHMAC = errors.New("srh: SRH carries no HMAC TLV")
	// ErrZeroKeyID means the HMAC TLV uses the reserved Key ID zero.
	ErrZeroKeyID = errors.New("srh: HMAC TLV Key ID must be non-zero")
	// ErrUnknownKey means no configured key matches the TLV Key ID.
	ErrUnknownKey = errors.New("srh: unknown HMAC Key ID")
	// ErrBadMAC means the recomputed HMAC does not match the TLV digest.
	ErrBadMAC = errors.New("srh: HMAC verification failed")
	// ErrSegmentsLeft means Segments Left is greater than Last Entry + 1.
	ErrSegmentsLeft = errors.New("srh: Segments Left greater than Last Entry + 1")
	// ErrNotIPv6 means the buffer is not a parsable IPv6 packet.
	ErrNotIPv6 = errors.New("srh: not an IPv6 packet")
)

var errNoSRH = errors.New("srh: no SRH in packet")

// Keyring holds the shared keys accepted at the domain boundary, keyed by
// RFC 8754 Key ID. It is safe for concurrent use: verifiers may run while
// keys are provisioned or revoked.
type Keyring struct {
	mu   sync.RWMutex
	keys map[uint32][]byte
}

// NewKeyring returns an empty key ring.
func NewKeyring() *Keyring {
	return &Keyring{keys: make(map[uint32][]byte)}
}

// Add provisions key under id. Key ID zero is reserved and rejected, as is an
// empty key. The key bytes are copied.
func (k *Keyring) Add(id uint32, key []byte) error {
	if id == 0 {
		return ErrZeroKeyID
	}
	if len(key) == 0 {
		return errors.New("srh: empty HMAC key")
	}
	keyCopy := make([]byte, len(key))
	copy(keyCopy, key)

	k.mu.Lock()
	k.keys[id] = keyCopy
	k.mu.Unlock()
	return nil
}

// Delete revokes id. It is a no-op when id is absent.
func (k *Keyring) Delete(id uint32) {
	k.mu.Lock()
	delete(k.keys, id)
	k.mu.Unlock()
}

func (k *Keyring) lookup(id uint32) ([]byte, bool) {
	k.mu.RLock()
	key, ok := k.keys[id]
	k.mu.RUnlock()
	return key, ok
}

type hmacTLV struct {
	keyID uint32
	mac   []byte
}

type parsedSRH struct {
	lastEntry    byte
	flags        byte
	segmentsLeft byte
	segmentList  []byte // whole segment list, in packet order
	hmac         *hmacTLV
}

// Admit applies the SR domain ingress policy to a raw IPv6 packet.
//
// Packets without an SRH are accepted. Packets carrying an SRH are dropped
// (non-nil error) unless they present a structurally valid SRH with a
// verified HMAC TLV whose Key ID is known and non-zero. The returned error
// wraps one of the exported Err* sentinels.
func Admit(packet []byte, keys *Keyring) error {
	if keys == nil {
		return errors.New("srh: nil key ring")
	}
	src, off, length, err := locateSRH(packet)
	if errors.Is(err, errNoSRH) {
		return nil
	}
	if err != nil {
		return err
	}
	return verifySRH(src, packet[off:off+length], keys)
}
