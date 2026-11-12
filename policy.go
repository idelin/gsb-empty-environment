package netsec

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"hash"
)

// HashID is the FC-SP DH-CHAP hash function identifier carried in the
// Hash_Function TLV (FC-SW-6 table 76).
type HashID uint8

const (
	// HashMD5 and HashSHA1 are legacy weak algorithms kept only so that a
	// policy can explicitly recognise (and reject) them.
	HashMD5    HashID = 0x01
	HashSHA1   HashID = 0x02
	HashSHA256 HashID = 0x03
	HashSHA384 HashID = 0x04
	HashSHA512 HashID = 0x05
)

// hashStrength ranks identifiers from weakest to strongest. It is used to
// verify that policy lists are ordered strongest-first.
var hashStrength = map[HashID]int{
	HashMD5:    10,
	HashSHA1:   20,
	HashSHA256: 30,
	HashSHA384: 40,
	HashSHA512: 50,
}

// NewHash returns a fresh hash.Hash for an identifier. MD5 is deliberately
// unsupported: accepting it would let an attacker downgrade onto a broken
// primitive. All other listed hashes are FIPS-grade.
func (h HashID) NewHash() (hash.Hash, error) {
	switch h {
	case HashSHA1:
		return sha1.New(), nil
	case HashSHA256:
		return sha256.New(), nil
	case HashSHA384:
		return sha512.New384(), nil
	case HashSHA512:
		return sha512.New(), nil
	default:
		return nil, errors.New("dhchap: unsupported hash identifier")
	}
}

// String names the identifier for diagnostics.
func (h HashID) String() string {
	switch h {
	case HashMD5:
		return "MD5"
	case HashSHA1:
		return "SHA-1"
	case HashSHA256:
		return "SHA-256"
	case HashSHA384:
		return "SHA-384"
	case HashSHA512:
		return "SHA-512"
	default:
		return "unknown-hash"
	}
}

// DHGroupID is the FC-SP DH-CHAP DH group identifier carried in the
// DH_Group TLV (FC-SW-6 table 78). GroupNull is the "null DH group" which
// disables the Diffie-Hellman exchange and therefore all of its
// anti-replay/downgrade protection; it must never be selected implicitly.
type DHGroupID uint8

const (
	// DHGroupNull selects no Diffie-Hellman exchange (plain CHAP semantics).
	DHGroupNull DHGroupID = 0x00

	// DHGroup1024 and DHGroup2048 are the 1024-bit (RFC 2409 Oakley group 2)
	// and 2048-bit (RFC 3526 group 14) MODP groups defined by FC-SW-6.
	DHGroup1024 DHGroupID = 0x01
	DHGroup2048 DHGroupID = 0x02

	// DHGroup3072 and DHGroup4096 are the remaining FC-SW-6 MODP groups;
	// they are recognised on the wire but not provisioned in this build.
	DHGroup3072 DHGroupID = 0x04
	DHGroup4096 DHGroupID = 0x05
)

// groupStrength ranks identifiers from weakest to strongest. The null group
// ranks below every real group.
var groupStrength = map[DHGroupID]int{
	DHGroupNull: 0,
	DHGroup1024: 10,
	DHGroup2048: 20,
	DHGroup3072: 30,
	DHGroup4096: 40,
}

// String names the identifier for diagnostics.
func (g DHGroupID) String() string {
	switch g {
	case DHGroupNull:
		return "null-DH"
	case DHGroup1024:
		return "MODP-1024"
	case DHGroup2048:
		return "MODP-2048"
	case DHGroup3072:
		return "MODP-3072"
	case DHGroup4096:
		return "MODP-4096"
	default:
		return "unknown-dh-group"
	}
}

// Policy is the local switch port authentication policy. Both lists contain
// only acceptable identifiers and must be ordered strongest-first, so the
// first entry is the local preference. Anything not listed is forbidden even
// if the peer offers it.
type Policy struct {
	// Hashes lists acceptable hash algorithms strongest-first.
	Hashes []HashID

	// DHGroups lists acceptable DH groups strongest-first. It must not
	// contain DHGroupNull; use AllowNullDH for that.
	DHGroups []DHGroupID

	// AllowNullDH makes the null DH group selectable. It is the single
	// explicit gate for null DH: when false a null offer is rejected even if
	// every real group negotiation fails.
	AllowNullDH bool
}

// Validate checks internal consistency of a policy.
func (p Policy) Validate() error {
	if len(p.Hashes) == 0 {
		return ErrInvalidPolicy
	}
	seenHash := map[HashID]bool{}
	prevStrength := int(^uint(0) >> 1) // max int
	for _, id := range p.Hashes {
		strength, ok := hashStrength[id]
		if !ok {
			return ErrInvalidPolicy
		}
		if _, err := id.NewHash(); err != nil {
			// MD5 is recognised on the wire but must never be configured.
			return ErrInvalidPolicy
		}
		if seenHash[id] || strength > prevStrength {
			return ErrInvalidPolicy
		}
		seenHash[id] = true
		prevStrength = strength
	}

	if len(p.DHGroups) == 0 && !p.AllowNullDH {
		return ErrInvalidPolicy
	}
	seenGroup := map[DHGroupID]bool{}
	prevStrength = int(^uint(0) >> 1)
	for _, id := range p.DHGroups {
		if id == DHGroupNull {
			// Null DH is controlled exclusively by AllowNullDH.
			return ErrInvalidPolicy
		}
		strength, ok := groupStrength[id]
		if !ok || !groupProvisioned(id) {
			return ErrInvalidPolicy
		}
		if seenGroup[id] || strength > prevStrength {
			return ErrInvalidPolicy
		}
		seenGroup[id] = true
		prevStrength = strength
	}
	return nil
}

// NegotiateHash selects the strongest hash that appears in both the local
// policy and the peer offer. Local policy order decides preference; peer
// entries outside the policy are ignored rather than accepted.
func NegotiateHash(policy Policy, peerOffers []HashID) (HashID, error) {
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	offered := make(map[HashID]bool, len(peerOffers))
	for _, id := range peerOffers {
		offered[id] = true
	}
	for _, local := range policy.Hashes {
		if offered[local] {
			return local, nil
		}
	}
	return 0, ErrNoCommonHash
}

// NegotiateDHGroup selects the strongest provisioned DH group present in both
// the local policy and the peer offer. The null group is only chosen when
// AllowNullDH is true and no real group is mutually acceptable. A null-only
// offer against a policy that forbids null DH returns ErrNullDHNotAllowed so
// the attack is distinguishable from an ordinary parameter mismatch.
func NegotiateDHGroup(policy Policy, peerOffers []DHGroupID) (DHGroupID, error) {
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	offered := make(map[DHGroupID]bool, len(peerOffers))
	peerOfferedNull := false
	for _, id := range peerOffers {
		if id == DHGroupNull {
			peerOfferedNull = true
			continue
		}
		offered[id] = true
	}

	// Real groups always take precedence over null DH, regardless of the
	// order the peer chose.
	for _, local := range policy.DHGroups {
		if offered[local] && groupProvisioned(local) {
			return local, nil
		}
	}

	if peerOfferedNull {
		if policy.AllowNullDH {
			return DHGroupNull, nil
		}
		return 0, ErrNullDHNotAllowed
	}
	return 0, ErrNoCommonDHGroup
}
