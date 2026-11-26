package dnssec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"time"
)

const (
	DNSKEYFlagZoneKey uint16 = 0x0100
	DNSKEYProtocol    uint8  = 3
)

// DNSKEY is the DNSSEC public key RDATA.
type DNSKEY struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	PublicKey []byte
}

// ParseDNSKEY parses DNSKEY RDATA.
func ParseDNSKEY(rdata []byte) (DNSKEY, error) {
	if len(rdata) < 4 {
		return DNSKEY{}, errors.New("dnssec: truncated DNSKEY RDATA")
	}
	return DNSKEY{
		Flags:     binary.BigEndian.Uint16(rdata[0:2]),
		Protocol:  rdata[2],
		Algorithm: rdata[3],
		PublicKey: append([]byte(nil), rdata[4:]...),
	}, nil
}

// RData returns the DNSKEY wire RDATA.
func (k DNSKEY) RData() []byte {
	out := make([]byte, 4+len(k.PublicKey))
	binary.BigEndian.PutUint16(out[0:2], k.Flags)
	out[2] = k.Protocol
	out[3] = k.Algorithm
	copy(out[4:], k.PublicKey)
	return out
}

// KeyTag implements the RFC 4034 appendix B key-tag calculation.
func (k DNSKEY) KeyTag() uint16 {
	rdata := k.RData()
	var sum uint32
	for i, b := range rdata {
		if i&1 == 0 {
			sum += uint32(b) << 8
		} else {
			sum += uint32(b)
		}
	}
	sum += (sum >> 16) & 0xffff
	return uint16(sum & 0xffff)
}

// PublicKeyP256 returns the ECDSA P-256 public key encoded by this DNSKEY.
func (k DNSKEY) PublicKeyP256() (*ecdsa.PublicKey, error) {
	if k.Protocol != DNSKEYProtocol {
		return nil, fmt.Errorf("dnssec: DNSKEY protocol is %d, want 3", k.Protocol)
	}
	if k.Algorithm != AlgECDSAP256 {
		return nil, fmt.Errorf("dnssec: DNSKEY algorithm is %d, want ECDSA-P256 (13)", k.Algorithm)
	}
	if k.Flags&DNSKEYFlagZoneKey == 0 {
		return nil, errors.New("dnssec: DNSKEY is not a zone key")
	}
	if len(k.PublicKey) != 64 {
		return nil, fmt.Errorf("dnssec: ECDSA-P256 public key length is %d, want 64", len(k.PublicKey))
	}
	x := new(big.Int).SetBytes(k.PublicKey[:32])
	y := new(big.Int).SetBytes(k.PublicKey[32:])
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	if !pub.Curve.IsOnCurve(x, y) {
		return nil, errors.New("dnssec: DNSKEY public point is not on P-256")
	}
	return pub, nil
}

// RRSIG is the parsed RRSIG RDATA.
type RRSIG struct {
	TypeCovered uint16
	Algorithm   uint8
	Labels      uint8
	OriginalTTL uint32
	Expiration  uint32
	Inception   uint32
	KeyTag      uint16
	SignerName  Name
	Signature   []byte
}

// ParseRRSIG parses RRSIG RDATA. The signer name in RRSIG RDATA is required
// to be uncompressed, as mandated by RFC 4034.
func ParseRRSIG(rdata []byte) (RRSIG, error) {
	if len(rdata) < 18 {
		return RRSIG{}, errors.New("dnssec: truncated RRSIG RDATA")
	}
	off := 18
	signer, off, err := ParseWireName(rdata, off)
	if err != nil {
		return RRSIG{}, fmt.Errorf("dnssec: RRSIG signer name: %w", err)
	}
	return RRSIG{
		TypeCovered: binary.BigEndian.Uint16(rdata[0:2]),
		Algorithm:   rdata[2],
		Labels:      rdata[3],
		OriginalTTL: binary.BigEndian.Uint32(rdata[4:8]),
		Expiration:  binary.BigEndian.Uint32(rdata[8:12]),
		Inception:   binary.BigEndian.Uint32(rdata[12:16]),
		KeyTag:      binary.BigEndian.Uint16(rdata[16:18]),
		SignerName:  signer,
		Signature:   append([]byte(nil), rdata[off:]...),
	}, nil
}

// RData returns the RRSIG wire RDATA.
func (r RRSIG) RData() []byte {
	prefix := r.Prefix()
	out := make([]byte, len(prefix)+len(r.Signature))
	copy(out, prefix)
	copy(out[len(prefix):], r.Signature)
	return out
}

// Prefix returns the RRSIG RDATA through the signer name; that is, the part
// that precedes the signature octets.
func (r RRSIG) Prefix() []byte {
	out := make([]byte, 18, 18+len(r.SignerName.Wire()))
	binary.BigEndian.PutUint16(out[0:2], r.TypeCovered)
	out[2] = r.Algorithm
	out[3] = r.Labels
	binary.BigEndian.PutUint32(out[4:8], r.OriginalTTL)
	binary.BigEndian.PutUint32(out[8:12], r.Expiration)
	binary.BigEndian.PutUint32(out[12:16], r.Inception)
	binary.BigEndian.PutUint16(out[16:18], r.KeyTag)
	out = append(out, r.SignerName.Wire()...)
	return out
}

// serialGE compares DNSSEC 32-bit serial times using RFC 1982 semantics.
func serialGE(a, b uint32) bool { return int32(a-b) >= 0 }

// ValidAt verifies the RRSIG inception/expiration interval.
func (r RRSIG) ValidAt(now time.Time) error {
	now32 := uint32(now.Unix())
	if !serialGE(now32, r.Inception) {
		return fmt.Errorf("dnssec: RRSIG inception %d is after current time %d", r.Inception, now32)
	}
	if !serialGE(r.Expiration, now32) {
		return fmt.Errorf("dnssec: RRSIG expiration %d is before current time %d", r.Expiration, now32)
	}
	if expirationBeforeInception(r.Inception, r.Expiration) {
		return errors.New("dnssec: RRSIG expiration precedes inception")
	}
	return nil
}

func expirationBeforeInception(inception, expiration uint32) bool {
	return int32(expiration-inception) < 0
}

// SignedOwner returns the owner name reconstructed from the RR owner and the
// RRSIG Labels field. For a wildcard-expanded RR, the leftmost label is
// replaced by "*". It also enforces that a wildcard RRSIG has exactly one
// additional label relative to its original owner name.
func (r RRSIG) SignedOwner(owner Name) (Name, error) {
	if len(owner.Labels) == 0 {
		if r.Labels != 0 {
			return Name{}, errors.New("dnssec: root owner cannot have nonzero RRSIG labels")
		}
		return Name{}, nil
	}
	if int(r.Labels) > len(owner.Labels) {
		return Name{}, fmt.Errorf("dnssec: RRSIG labels %d exceeds owner label count %d", r.Labels, len(owner.Labels))
	}
	if int(r.Labels) == len(owner.Labels) {
		return Name{Labels: append([]string(nil), owner.Labels...)}, nil
	}
	if int(r.Labels)+1 != len(owner.Labels) {
		return Name{}, fmt.Errorf("dnssec: wildcard RRSIG labels %d cannot expand to %d-label owner", r.Labels, len(owner.Labels))
	}
	suffix, err := owner.Ancestor(int(r.Labels))
	if err != nil {
		return Name{}, err
	}
	return suffix.Wildcard(), nil
}

// IsWildcardExpansion reports whether the Labels field indicates a
// wildcard-expanded owner.
func (r RRSIG) IsWildcardExpansion(owner Name) bool {
	return int(r.Labels)+1 == len(owner.Labels)
}

// DataToSign builds the RFC 4034 section 3.1.8.1 data covered by an RRSIG:
// RRSIG RDATA without the signature, followed by the canonical RRset using
// the RRSIG original TTL and the reconstructed wildcard owner when needed.
func DataToSign(rrset []RR, r RRSIG) ([]byte, error) {
	if len(rrset) == 0 {
		return nil, errors.New("dnssec: empty RRset")
	}
	owner := rrset[0].Name
	if err := validateName(owner); err != nil {
		return nil, err
	}
	for _, rr := range rrset {
		if !EqualNames(rr.Name, owner) {
			return nil, errors.New("dnssec: RRset has multiple owner names")
		}
		if rr.Type != r.TypeCovered {
			return nil, fmt.Errorf("dnssec: RRSIG covers type %d but RRset type is %d", r.TypeCovered, rr.Type)
		}
	}
	signedOwner, err := r.SignedOwner(owner)
	if err != nil {
		return nil, err
	}
	canonical, err := CanonicalizeRRset(rrset)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), r.Prefix()...)
	for _, rr := range canonical {
		wire, err := CanonicalRRWire(rr, signedOwner, r.OriginalTTL)
		if err != nil {
			return nil, err
		}
		out = append(out, wire...)
	}
	return out, nil
}

// VerifyRRSIG verifies an ECDSA-P256 RRSIG over rrset using key. If zone is
// supplied, the signer name must equal that zone (not merely be a suffix of
// the RR owner), preventing cross-zone signature substitution.
func VerifyRRSIG(rrset []RR, sig RRSIG, key DNSKEY, now time.Time, zone ...Name) error {
	if len(rrset) == 0 {
		return errors.New("dnssec: empty RRset")
	}
	if err := sig.ValidAt(now); err != nil {
		return err
	}
	if sig.Algorithm != AlgECDSAP256 || key.Algorithm != AlgECDSAP256 {
		return errors.New("dnssec: RRSIG and DNSKEY must use ECDSA-P256/SHA-256 algorithm 13")
	}
	if sig.KeyTag != key.KeyTag() {
		return fmt.Errorf("dnssec: RRSIG key tag %d does not match DNSKEY key tag %d", sig.KeyTag, key.KeyTag())
	}
	owner := rrset[0].Name
	for _, rr := range rrset {
		if !EqualNames(rr.Name, owner) {
			return errors.New("dnssec: RRset has multiple owner names")
		}
	}
	if !IsSubdomainOf(owner, sig.SignerName) {
		return fmt.Errorf("dnssec: RR owner %s is outside signer zone %s", owner, sig.SignerName)
	}
	for _, label := range sig.SignerName.Labels {
		if label == "*" {
			return errors.New("dnssec: wildcard label cannot appear in signer name")
		}
	}
	if len(zone) > 0 {
		if !EqualNames(sig.SignerName, zone[0]) {
			return fmt.Errorf("dnssec: signer %s is not the current zone %s", sig.SignerName, zone[0])
		}
		if !IsSubdomainOf(owner, zone[0]) {
			return fmt.Errorf("dnssec: RR owner %s is outside current zone %s", owner, zone[0])
		}
	}
	pub, err := key.PublicKeyP256()
	if err != nil {
		return err
	}
	if len(sig.Signature) != 64 {
		return fmt.Errorf("dnssec: ECDSA-P256 signature length is %d, want 64", len(sig.Signature))
	}
	data, err := DataToSign(rrset, sig)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	rv := new(big.Int).SetBytes(sig.Signature[:32])
	sv := new(big.Int).SetBytes(sig.Signature[32:])
	if rv.Sign() <= 0 || sv.Sign() <= 0 {
		return errors.New("dnssec: ECDSA-P256 signature has a zero scalar")
	}
	if !ecdsa.Verify(pub, digest[:], rv, sv) {
		return errors.New("dnssec: ECDSA-P256 RRSIG verification failed")
	}
	return nil
}

// VerifyRRSIGWithZone is an explicit-zone form of VerifyRRSIG.
func VerifyRRSIGWithZone(rrset []RR, sig RRSIG, key DNSKEY, zone Name, now time.Time) error {
	return VerifyRRSIG(rrset, sig, key, now, zone)
}

// VerifyAnyRRSIG verifies at least one signature in sigs with at least one
// matching key. A valid signature returns its index and key index.
func VerifyAnyRRSIG(rrset []RR, sigs []RRSIG, keys []DNSKEY, now time.Time, zone ...Name) (int, int, error) {
	if len(sigs) == 0 {
		return -1, -1, errors.New("dnssec: no RRSIG records")
	}
	if len(keys) == 0 {
		return -1, -1, errors.New("dnssec: no DNSKEY records")
	}
	var last error
	for i, sig := range sigs {
		for j, key := range keys {
			if sig.Algorithm != key.Algorithm || sig.KeyTag != key.KeyTag() {
				continue
			}
			if err := VerifyRRSIG(rrset, sig, key, now, zone...); err != nil {
				last = err
				continue
			}
			return i, j, nil
		}
	}
	if last == nil {
		last = errors.New("dnssec: no RRSIG matched a DNSKEY")
	}
	return -1, -1, last
}
