package netsec

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DNS RR type numbers used by the CDS/CDNSKEY update protocol.
const (
	TypeDS      uint16 = 43
	TypeRRSIG   uint16 = 46
	TypeDNSKEY  uint16 = 48
	TypeCDS     uint16 = 59
	TypeCDNSKEY uint16 = 60

	ClassINET uint16 = 1
)

// DS digest algorithm numbers.
const (
	DigestSHA1   uint8 = 1
	DigestSHA256 uint8 = 2
	DigestGOST   uint8 = 3
	DigestSHA384 uint8 = 4
)

// DNSSEC algorithm numbers supported by the built-in verifier.
const (
	AlgorithmRSASHA1         uint8 = 5
	AlgorithmRSASHA1NSEC3    uint8 = 7
	AlgorithmRSASHA256       uint8 = 8
	AlgorithmRSASHA512       uint8 = 10
	AlgorithmECDSAP256SHA256 uint8 = 13
	AlgorithmECDSAP384SHA384 uint8 = 14
	AlgorithmED25519         uint8 = 15
)

const (
	dnssecFlagZoneKey uint16 = 0x0100
	dnssecFlagSEP     uint16 = 0x0001
)

// DS is the RDATA of a DS record. CDS records have exactly the same RDATA
// format, so CDS is an alias of DS.
type DS struct {
	KeyTag     uint16
	Algorithm  uint8
	DigestType uint8
	Digest     []byte
}

// CDS is a child DNSKEY signal. Its RDATA format is identical to DS.
type CDS = DS

// DNSKEY is the RDATA of a DNSKEY record. CDNSKEY records have exactly the
// same RDATA format, so CDNSKEY is an alias of DNSKEY.
type DNSKEY struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	PublicKey []byte
}

// CDNSKEY is the child-side counterpart of a DNSKEY record.
type CDNSKEY = DNSKEY

// DeleteCDS is the special RFC 8078 CDS record that requests removal of the
// child's DS RRset.
func DeleteCDS() CDS {
	return CDS{Digest: []byte{0}}
}

// IsDelete reports whether d is exactly the CDS delete signal 0 0 0 00.
func (d DS) IsDelete() bool {
	return d.KeyTag == 0 &&
		d.Algorithm == 0 &&
		d.DigestType == 0 &&
		len(d.Digest) == 1 &&
		d.Digest[0] == 0
}

// Validate checks the invariants of a non-delete DS/CDS record.
func (d DS) Validate() error {
	if d.IsDelete() {
		return nil
	}
	if d.KeyTag == 0 {
		return errors.New("CDS: key tag 0 is reserved for the delete record")
	}
	if d.Algorithm == 0 {
		return errors.New("CDS: algorithm 0 is reserved for the delete record")
	}
	if d.DigestType == 0 {
		return errors.New("CDS: digest type 0 is reserved for the delete record")
	}
	want, ok := digestLength(d.DigestType)
	if !ok {
		return fmt.Errorf("CDS: unsupported digest type %d", d.DigestType)
	}
	if len(d.Digest) != want {
		return fmt.Errorf("CDS: digest type %d requires %d bytes, got %d", d.DigestType, want, len(d.Digest))
	}
	return nil
}

// String returns the presentation form of a DS/CDS record without an owner
// name or class.
func (d DS) String() string {
	if d.IsDelete() {
		return "0 0 0 00"
	}
	return fmt.Sprintf("%d %d %d %s", d.KeyTag, d.Algorithm, d.DigestType, strings.ToUpper(hex.EncodeToString(d.Digest)))
}

func (d DS) rdata() []byte {
	b := make([]byte, 4+len(d.Digest))
	binary.BigEndian.PutUint16(b[0:2], d.KeyTag)
	b[2] = d.Algorithm
	b[3] = d.DigestType
	copy(b[4:], d.Digest)
	return b
}

func (d DS) canonicalKey() string {
	return string(d.rdata())
}

// ParseDS parses a DS or CDS RDATA field:
//
//	<key-tag> <algorithm> <digest-type> <hex-digest>
func ParseDS(s string) (DS, error) {
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return DS{}, fmt.Errorf("DS: expected 4 fields, got %d", len(fields))
	}
	keyTag, err := strconv.ParseUint(fields[0], 10, 16)
	if err != nil {
		return DS{}, fmt.Errorf("DS: invalid key tag: %w", err)
	}
	algorithm, err := strconv.ParseUint(fields[1], 10, 8)
	if err != nil {
		return DS{}, fmt.Errorf("DS: invalid algorithm: %w", err)
	}
	digestType, err := strconv.ParseUint(fields[2], 10, 8)
	if err != nil {
		return DS{}, fmt.Errorf("DS: invalid digest type: %w", err)
	}
	digest, err := hex.DecodeString(fields[3])
	if err != nil {
		return DS{}, fmt.Errorf("DS: invalid digest: %w", err)
	}
	d := DS{
		KeyTag:     uint16(keyTag),
		Algorithm:  uint8(algorithm),
		DigestType: uint8(digestType),
		Digest:     digest,
	}
	if !d.IsDelete() {
		if err := d.Validate(); err != nil {
			return DS{}, err
		}
	}
	return d, nil
}

// ParseCDS is an alias of ParseDS.
func ParseCDS(s string) (CDS, error) { return ParseDS(s) }

// IsKSK reports whether k advertises itself as a secure entry point (KSK).
func (k DNSKEY) IsKSK() bool {
	return k.Protocol == 3 &&
		k.Flags&dnssecFlagZoneKey != 0 &&
		k.Flags&dnssecFlagSEP != 0
}

// IsZoneKey reports whether k has the DNS zone key flag and protocol 3.
func (k DNSKEY) IsZoneKey() bool {
	return k.Protocol == 3 && k.Flags&dnssecFlagZoneKey != 0
}

// IsDelete reports whether k is a CDNSKEY delete record. RFC 8078 defines the
// CDNSKEY delete record as flags 0, protocol 3, algorithm 0 and an empty or
// single-zero-octet public key.
func (k DNSKEY) IsDelete() bool {
	if k.Flags != 0 || k.Protocol != 3 || k.Algorithm != 0 {
		return false
	}
	return len(k.PublicKey) == 0 || (len(k.PublicKey) == 1 && k.PublicKey[0] == 0)
}

// Validate checks the invariants of a published DNSKEY/CDNSKEY record.
func (k DNSKEY) Validate() error {
	if k.IsDelete() {
		return nil
	}
	if k.Protocol != 3 {
		return fmt.Errorf("DNSKEY: protocol must be 3, got %d", k.Protocol)
	}
	if k.Algorithm == 0 {
		return errors.New("DNSKEY: algorithm 0 is reserved for the delete record")
	}
	if len(k.PublicKey) == 0 {
		return errors.New("DNSKEY: empty public key")
	}
	return nil
}

func (k DNSKEY) rdata() []byte {
	b := make([]byte, 4+len(k.PublicKey))
	binary.BigEndian.PutUint16(b[0:2], k.Flags)
	b[2] = k.Protocol
	b[3] = k.Algorithm
	copy(b[4:], k.PublicKey)
	return b
}

// KeyTag returns the RFC 4034 key tag of k.
func (k DNSKEY) KeyTag() uint16 {
	rdata := k.rdata()
	var ac uint32
	for i, b := range rdata {
		if i&1 == 1 {
			ac += uint32(b)
		} else {
			ac += uint32(b) << 8
		}
	}
	ac += (ac >> 16) & 0xffff
	return uint16(ac & 0xffff)
}

// String returns the presentation form of a DNSKEY/CDNSKEY record without an
// owner name or class.
func (k DNSKEY) String() string {
	return fmt.Sprintf("%d %d %d %s",
		k.Flags, k.Protocol, k.Algorithm, base64.StdEncoding.EncodeToString(k.PublicKey))
}

// ParseDNSKEY parses a DNSKEY or CDNSKEY RDATA field:
//
//	<flags> <protocol> <algorithm> <base64-public-key>
func ParseDNSKEY(s string) (DNSKEY, error) {
	fields := strings.Fields(s)
	if len(fields) != 4 {
		return DNSKEY{}, fmt.Errorf("DNSKEY: expected 4 fields, got %d", len(fields))
	}
	flags, err := strconv.ParseUint(fields[0], 10, 16)
	if err != nil {
		return DNSKEY{}, fmt.Errorf("DNSKEY: invalid flags: %w", err)
	}
	protocol, err := strconv.ParseUint(fields[1], 10, 8)
	if err != nil {
		return DNSKEY{}, fmt.Errorf("DNSKEY: invalid protocol: %w", err)
	}
	algorithm, err := strconv.ParseUint(fields[2], 10, 8)
	if err != nil {
		return DNSKEY{}, fmt.Errorf("DNSKEY: invalid algorithm: %w", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(fields[3])
	if err != nil {
		return DNSKEY{}, fmt.Errorf("DNSKEY: invalid public key: %w", err)
	}
	k := DNSKEY{
		Flags:     uint16(flags),
		Protocol:  uint8(protocol),
		Algorithm: uint8(algorithm),
		PublicKey: publicKey,
	}
	if !k.IsDelete() {
		if err := k.Validate(); err != nil {
			return DNSKEY{}, err
		}
	}
	return k, nil
}

// ParseCDNSKEY is an alias of ParseDNSKEY.
func ParseCDNSKEY(s string) (CDNSKEY, error) { return ParseDNSKEY(s) }

// ToDS builds the DS record for k under owner. owner is the zone apex at
// which the DNSKEY is published.
func (k DNSKEY) ToDS(owner string, digestType uint8) (DS, error) {
	if err := k.Validate(); err != nil {
		return DS{}, err
	}
	ownerWire, err := nameWire(owner)
	if err != nil {
		return DS{}, err
	}
	rdata := k.rdata()
	input := make([]byte, 0, len(ownerWire)+len(rdata))
	input = append(input, ownerWire...)
	input = append(input, rdata...)

	var digest []byte
	switch digestType {
	case DigestSHA1:
		sum := sha1.Sum(input)
		digest = sum[:]
	case DigestSHA256:
		sum := sha256.Sum256(input)
		digest = sum[:]
	case DigestSHA384:
		sum := sha512.Sum384(input)
		digest = sum[:]
	default:
		return DS{}, fmt.Errorf("DS: unsupported digest type %d", digestType)
	}
	return DS{
		KeyTag:     k.KeyTag(),
		Algorithm:  k.Algorithm,
		DigestType: digestType,
		Digest:     digest,
	}, nil
}

// Matches reports whether d is the DS record for k under owner.
func (d DS) Matches(owner string, k DNSKEY) bool {
	if d.IsDelete() || d.Algorithm != k.Algorithm || d.KeyTag != k.KeyTag() {
		return false
	}
	computed, err := k.ToDS(owner, d.DigestType)
	if err != nil {
		return false
	}
	return bytes.Equal(computed.Digest, d.Digest)
}

func digestLength(digestType uint8) (int, bool) {
	switch digestType {
	case DigestSHA1:
		return sha1.Size, true
	case DigestSHA256:
		return sha256.Size, true
	case DigestGOST:
		return 32, true
	case DigestSHA384:
		return sha512.Size384, true
	default:
		return 0, false
	}
}

func canonicalName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("empty DNS name")
	}
	if name == "." {
		return ".", nil
	}
	name = strings.TrimSuffix(name, ".")
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("invalid DNS name %q: empty label", name)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("invalid DNS name %q: label too long", name)
		}
	}
	if len(labels) > 127 || len(name)+2 > 255 {
		return "", fmt.Errorf("invalid DNS name %q: name too long", name)
	}
	return strings.ToLower(name) + ".", nil
}

func nameWire(name string) ([]byte, error) {
	canonical, err := canonicalName(name)
	if err != nil {
		return nil, err
	}
	if canonical == "." {
		return []byte{0}, nil
	}
	labels := strings.Split(strings.TrimSuffix(canonical, "."), ".")
	wire := make([]byte, 0, len(canonical)+1)
	for _, label := range labels {
		wire = append(wire, byte(len(label)))
		wire = append(wire, label...)
	}
	wire = append(wire, 0)
	return wire, nil
}

func labelCount(name string) (uint8, error) {
	canonical, err := canonicalName(name)
	if err != nil {
		return 0, err
	}
	if canonical == "." {
		return 0, nil
	}
	labels := strings.Split(strings.TrimSuffix(canonical, "."), ".")
	if len(labels) > 127 {
		return 0, fmt.Errorf("DNS name %q has too many labels", name)
	}
	return uint8(len(labels)), nil
}

func canonicalDSList(in []DS) ([]DS, error) {
	unique := make(map[string]DS, len(in))
	for _, d := range in {
		if err := d.Validate(); err != nil {
			return nil, err
		}
		digest := append([]byte(nil), d.Digest...)
		d.Digest = digest
		unique[d.canonicalKey()] = d
	}
	out := make([]DS, 0, len(unique))
	for _, d := range unique {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].rdata(), out[j].rdata()) < 0
	})
	return out, nil
}

func canonicalDNSKEYList(in []DNSKEY) ([]DNSKEY, error) {
	unique := make(map[string]DNSKEY, len(in))
	for _, k := range in {
		if err := k.Validate(); err != nil {
			return nil, err
		}
		publicKey := append([]byte(nil), k.PublicKey...)
		k.PublicKey = publicKey
		unique[string(k.rdata())] = k
	}
	out := make([]DNSKEY, 0, len(unique))
	for _, k := range unique {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].rdata(), out[j].rdata()) < 0
	})
	return out, nil
}

func dsListEqual(a, b []DS) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].rdata(), b[i].rdata()) {
			return false
		}
	}
	return true
}

func dnskeyListEqual(a, b []DNSKEY) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].rdata(), b[i].rdata()) {
			return false
		}
	}
	return true
}
