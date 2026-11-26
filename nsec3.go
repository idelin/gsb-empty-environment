package dnssec

import (
	"bytes"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	NSEC3HashSHA1         uint8  = 1
	NSEC3FlagOptOut       uint8  = 0x01
	NSEC3SHA1             uint8  = NSEC3HashSHA1
	NSEC3OptOut           uint8  = NSEC3FlagOptOut
	NSEC3HashLength       int    = 20
	ProtocolMaxIterations uint16 = 65535
	DefaultMaxIterations  uint16 = 250
	MaxNSEC3Iterations    uint16 = ProtocolMaxIterations
	NSEC3SaltMaxLength    int    = 255
)

// NSEC3 is the NSEC3 RDATA after the owner name.
type NSEC3 struct {
	HashAlgorithm uint8
	Flags         uint8
	Iterations    uint16
	Salt          []byte
	NextHash      []byte
	Types         []uint16
}

// NSEC3Parameters is the reusable parameter portion of an NSEC3 RR.
type NSEC3Parameters struct {
	HashAlgorithm uint8
	Flags         uint8
	Iterations    uint16
	Salt          []byte
}

// Params returns the NSEC3 parameter fields.
func (n NSEC3) Params() NSEC3Parameters {
	return NSEC3Parameters{
		HashAlgorithm: n.HashAlgorithm,
		Flags:         n.Flags,
		Iterations:    n.Iterations,
		Salt:          append([]byte(nil), n.Salt...),
	}
}

// ParseTypeBitmap parses an RFC 4034 type bitmap.
func ParseTypeBitmap(data []byte) ([]uint16, error) {
	var types []uint16
	off := 0
	lastWindow := -1
	for off < len(data) {
		if off+2 > len(data) {
			return nil, errors.New("dnssec: truncated type bitmap header")
		}
		window := int(data[off])
		length := int(data[off+1])
		off += 2
		if window <= lastWindow {
			return nil, errors.New("dnssec: type bitmap windows are not strictly increasing")
		}
		if length < 1 || length > 32 {
			return nil, fmt.Errorf("dnssec: invalid type bitmap window length %d", length)
		}
		if off+length > len(data) {
			return nil, errors.New("dnssec: truncated type bitmap window")
		}
		if data[off+length-1] == 0 {
			return nil, errors.New("dnssec: type bitmap window has a trailing zero octet")
		}
		for i := 0; i < length; i++ {
			b := data[off+i]
			for bit := 0; bit < 8; bit++ {
				if b&(0x80>>bit) != 0 {
					types = append(types, uint16(window*256+i*8+bit))
				}
			}
		}
		lastWindow = window
		off += length
	}
	return types, nil
}

// MarshalTypeBitmap encodes an RFC 4034 type bitmap from a type list.
func MarshalTypeBitmap(types []uint16) ([]byte, error) {
	if len(types) == 0 {
		return nil, nil
	}
	seen := make(map[uint16]struct{}, len(types))
	for _, t := range types {
		if t > 65535 {
			return nil, fmt.Errorf("dnssec: invalid DNS type %d", t)
		}
		seen[t] = struct{}{}
	}
	ordered := make([]uint16, 0, len(seen))
	for t := range seen {
		ordered = append(ordered, t)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	var out []byte
	for pos := 0; pos < len(ordered); {
		window := int(ordered[pos] / 256)
		end := pos
		maxOctet := 0
		for end < len(ordered) && int(ordered[end]/256) == window {
			octet := int(ordered[end]%256) / 8
			if octet > maxOctet {
				maxOctet = octet
			}
			end++
		}
		block := make([]byte, maxOctet+1)
		for i := pos; i < end; i++ {
			bit := int(ordered[i] % 256)
			block[bit/8] |= 0x80 >> uint(bit%8)
		}
		out = append(out, byte(window), byte(len(block)))
		out = append(out, block...)
		pos = end
	}
	return out, nil
}

func typeBitmapContains(types []uint16, typ uint16) bool {
	for _, t := range types {
		if t == typ {
			return true
		}
	}
	return false
}

// TypeBitmapContains reports whether the supplied NSEC3/NSEC type bitmap
// contains typ.
func TypeBitmapContains(types []uint16, typ uint16) bool {
	return typeBitmapContains(types, typ)
}

// RData returns the NSEC3 wire RDATA.
func (n NSEC3) RData() []byte {
	bitmap, _ := MarshalTypeBitmap(n.Types)
	out := make([]byte, 0, 5+len(n.Salt)+len(n.NextHash)+len(bitmap))
	out = append(out, n.HashAlgorithm, n.Flags)
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], n.Iterations)
	out = append(out, buf[:]...)
	out = append(out, byte(len(n.Salt)))
	out = append(out, n.Salt...)
	out = append(out, byte(len(n.NextHash)))
	out = append(out, n.NextHash...)
	out = append(out, bitmap...)
	return out
}

// ParseNSEC3 parses and structurally validates NSEC3 RDATA against the RFC
// 5155 protocol limits. Policy iteration limits are applied separately by
// ValidateNSEC3Parameters and NewNSEC3Set.
func ParseNSEC3(rdata []byte) (NSEC3, error) {
	if len(rdata) < 5 {
		return NSEC3{}, errors.New("dnssec: truncated NSEC3 RDATA")
	}
	n := NSEC3{
		HashAlgorithm: rdata[0],
		Flags:         rdata[1],
		Iterations:    binary.BigEndian.Uint16(rdata[2:4]),
	}
	off := 4
	saltLen := int(rdata[off])
	off++
	if off+saltLen > len(rdata) {
		return NSEC3{}, errors.New("dnssec: truncated NSEC3 salt")
	}
	n.Salt = append([]byte(nil), rdata[off:off+saltLen]...)
	off += saltLen
	if off >= len(rdata) {
		return NSEC3{}, errors.New("dnssec: truncated NSEC3 next hash length")
	}
	hashLen := int(rdata[off])
	off++
	if off+hashLen > len(rdata) {
		return NSEC3{}, errors.New("dnssec: truncated NSEC3 next hash")
	}
	n.NextHash = append([]byte(nil), rdata[off:off+hashLen]...)
	off += hashLen
	types, err := ParseTypeBitmap(rdata[off:])
	if err != nil {
		return NSEC3{}, err
	}
	n.Types = types
	if err := ValidateNSEC3Parameters(n, ProtocolMaxIterations); err != nil {
		return NSEC3{}, err
	}
	return n, nil
}

// ValidateNSEC3Parameters checks RFC 5155 parameters and the configured
// iteration limit. The limit defaults to DefaultMaxIterations; pass a limit to
// override it (65535 is the protocol maximum).
func ValidateNSEC3Parameters(n NSEC3, maxIterations ...uint16) error {
	limit := DefaultMaxIterations
	if len(maxIterations) > 0 {
		limit = maxIterations[0]
	}
	if n.HashAlgorithm != NSEC3HashSHA1 {
		return fmt.Errorf("dnssec: NSEC3 hash algorithm %d is not SHA-1 (1)", n.HashAlgorithm)
	}
	if n.Flags & ^NSEC3FlagOptOut != 0 {
		return fmt.Errorf("dnssec: NSEC3 has unsupported flag bits 0x%02x", n.Flags)
	}
	if n.Iterations > limit {
		return fmt.Errorf("dnssec: NSEC3 iteration count %d exceeds limit %d", n.Iterations, limit)
	}
	if len(n.Salt) > NSEC3SaltMaxLength {
		return fmt.Errorf("dnssec: NSEC3 salt is %d octets, maximum is 255", len(n.Salt))
	}
	if len(n.NextHash) != NSEC3HashLength {
		return fmt.Errorf("dnssec: NSEC3 next hash is %d octets, want 20 for SHA-1", len(n.NextHash))
	}
	if _, err := MarshalTypeBitmap(n.Types); err != nil {
		return err
	}
	return nil
}

// Validate validates NSEC3 parameters with the default resource limit.
func (n NSEC3) Validate(maxIterations ...uint16) error {
	return ValidateNSEC3Parameters(n, maxIterations...)
}

// EncodeBase32Hex encodes bytes using unpadded extended-hex Base32.
func EncodeBase32Hex(data []byte) string {
	return base32.HexEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
}

// Base32HexEncode is an alias for EncodeBase32Hex.
func Base32HexEncode(data []byte) string { return EncodeBase32Hex(data) }

// DecodeBase32Hex decodes unpadded extended-hex Base32. Padding characters
// are rejected rather than silently accepted.
func DecodeBase32Hex(s string) ([]byte, error) {
	if strings.ContainsAny(s, "= \t\r\n") {
		return nil, errors.New("dnssec: Base32hex input contains padding or whitespace")
	}
	decoded, err := base32.HexEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(s))
	if err != nil {
		return nil, fmt.Errorf("dnssec: invalid unpadded Base32hex: %w", err)
	}
	return decoded, nil
}

// Base32HexDecode is an alias for DecodeBase32Hex.
func Base32HexDecode(s string) ([]byte, error) { return DecodeBase32Hex(s) }

// EncodeSaltHex encodes NSEC3 salt in presentation form. Empty salt is
// represented by an empty string; ParseSaltHex also accepts "-".
func EncodeSaltHex(salt []byte) string { return strings.ToUpper(hex.EncodeToString(salt)) }

// SaltString returns the RFC 5155 presentation form, using "-" for no salt.
func (n NSEC3) SaltString() string {
	if len(n.Salt) == 0 {
		return "-"
	}
	return EncodeSaltHex(n.Salt)
}

// NextHashString returns the next hashed owner name in unpadded Base32hex.
func (n NSEC3) NextHashString() string { return EncodeBase32Hex(n.NextHash) }

// ParseSaltHex decodes NSEC3 presentation-format salt. "-" means empty salt.
func ParseSaltHex(s string) ([]byte, error) {
	if s == "-" || s == "" {
		return nil, nil
	}
	if len(s)%2 != 0 {
		return nil, errors.New("dnssec: NSEC3 salt has odd number of hex digits")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("dnssec: invalid NSEC3 salt: %w", err)
	}
	if len(b) > NSEC3SaltMaxLength {
		return nil, errors.New("dnssec: NSEC3 salt exceeds 255 octets")
	}
	return b, nil
}

// HashNSEC3 computes the RFC 5155 SHA-1 NSEC3 hash of name. The name must be
// a fully-qualified name; its canonical wire form is hashed.
func HashNSEC3(name Name, salt []byte, iterations uint16) ([]byte, error) {
	return HashNSEC3WithLimit(name, salt, iterations, ProtocolMaxIterations)
}

// HashNSEC3WithLimit is HashNSEC3 with a caller-supplied iteration limit.
func HashNSEC3WithLimit(name Name, salt []byte, iterations uint16, maxIterations uint16) ([]byte, error) {
	if iterations > maxIterations {
		return nil, fmt.Errorf("dnssec: NSEC3 iteration count %d exceeds limit %d", iterations, maxIterations)
	}
	nameWire, err := CanonicalNameWire(name)
	if err != nil {
		return nil, err
	}
	h := sha1.New()
	_, _ = h.Write(nameWire)
	_, _ = h.Write(salt)
	digest := h.Sum(nil)
	for i := uint16(0); i < iterations; i++ {
		h.Reset()
		_, _ = h.Write(digest)
		_, _ = h.Write(salt)
		digest = h.Sum(nil)
	}
	return digest, nil
}

// NSEC3Hash is an alias for HashNSEC3.
func NSEC3Hash(name Name, salt []byte, iterations uint16) ([]byte, error) {
	return HashNSEC3(name, salt, iterations)
}

// HashName is an alias for HashNSEC3.
func HashName(name Name, salt []byte, iterations uint16) ([]byte, error) {
	return HashNSEC3(name, salt, iterations)
}

// NSEC3Record combines the NSEC3 owner name with its RDATA.
type NSEC3Record struct {
	Owner Name
	Class uint16
	TTL   uint32
	NSEC3 NSEC3
}

// RData returns the NSEC3 RDATA.
func (r NSEC3Record) RData() []byte { return r.NSEC3.RData() }

// RR returns the NSEC3 record as a generic RR.
func (r NSEC3Record) RR() RR {
	class := r.Class
	if class == 0 {
		class = ClassINET
	}
	return RR{Name: r.Owner, Type: TypeNSEC3, Class: class, TTL: r.TTL, Data: r.NSEC3.RData()}
}

// VerifyNSEC3 verifies the RRSIGs over an NSEC3 owner's single-record RRset.
// Because NSEC3 owner names are hashed and never wildcard-expanded, a
// wildcard-indicated Labels count is rejected.
func VerifyNSEC3(record NSEC3Record, sigs []RRSIG, keys []DNSKEY, now time.Time, zone Name) error {
	rr := record.RR()
	sigIndex, _, err := VerifyAnyRRSIG([]RR{rr}, sigs, keys, now, zone)
	if err != nil {
		return err
	}
	if sigs[sigIndex].TypeCovered != TypeNSEC3 {
		return errors.New("dnssec: RRSIG does not cover an NSEC3 RRset")
	}
	if int(sigs[sigIndex].Labels) != record.Owner.LabelCount() {
		return fmt.Errorf("dnssec: NSEC3 RRSIG labels %d do not match owner label count %d", sigs[sigIndex].Labels, record.Owner.LabelCount())
	}
	return nil
}

// OwnerHash decodes the hashed-owner label from the NSEC3 owner name.
func (r NSEC3Record) OwnerHash() ([]byte, error) {
	if r.Owner.LabelCount() == 0 {
		return nil, errors.New("dnssec: NSEC3 owner is the root name")
	}
	hash, err := DecodeBase32Hex(r.Owner.Labels[0])
	if err != nil {
		return nil, err
	}
	if len(hash) != NSEC3HashLength {
		return nil, fmt.Errorf("dnssec: NSEC3 owner hash is %d octets, want 20", len(hash))
	}
	return hash, nil
}

// NSEC3Owner constructs an NSEC3 owner name for zone from a hash.
func NSEC3Owner(zone Name, hash []byte) Name {
	label := EncodeBase32Hex(hash)
	return Name{Labels: append([]string{strings.ToLower(label)}, zone.Labels...)}
}

// NSEC3CoversHash implements RFC 5155's circular interval check. The owner
// and next hashes are exclusive endpoints. A record whose next hash equals its
// owner hash covers every other hash in the wheel.
func NSEC3CoversHash(owner, next, target []byte) bool {
	cmpOwnerNext := bytes.Compare(owner, next)
	switch {
	case cmpOwnerNext < 0:
		return bytes.Compare(owner, target) < 0 && bytes.Compare(target, next) < 0
	case cmpOwnerNext > 0:
		return bytes.Compare(owner, target) < 0 || bytes.Compare(target, next) < 0
	default:
		return bytes.Compare(owner, target) != 0
	}
}

// CoversHash is an alias for NSEC3CoversHash.
func CoversHash(owner, next, target []byte) bool { return NSEC3CoversHash(owner, next, target) }

// NSEC3Set is a parameter-consistent collection of NSEC3 records for one zone.
type NSEC3Set struct {
	Zone          Name
	Records       []NSEC3Record
	MaxIterations uint16
}

// NewNSEC3Set validates and indexes records for zone. Records must all use
// the same NSEC3 parameters, as required by RFC 5155.
func NewNSEC3Set(zone Name, records []NSEC3Record, maxIterations ...uint16) (*NSEC3Set, error) {
	limit := DefaultMaxIterations
	if len(maxIterations) > 0 {
		limit = maxIterations[0]
	}
	if limit > ProtocolMaxIterations {
		limit = ProtocolMaxIterations
	}
	if err := validateName(zone); err != nil {
		return nil, err
	}
	s := &NSEC3Set{Zone: zone, MaxIterations: limit}
	if len(records) == 0 {
		return s, nil
	}

	salt := append([]byte(nil), records[0].NSEC3.Salt...)
	algorithm := records[0].NSEC3.HashAlgorithm
	iterations := records[0].NSEC3.Iterations
	seen := make(map[string]struct{}, len(records))
	for i, rec := range records {
		if rec.Class != 0 && rec.Class != ClassINET {
			return nil, fmt.Errorf("dnssec: NSEC3 record %d has non-IN class %d", i, rec.Class)
		}
		if err := ValidateNSEC3Parameters(rec.NSEC3, limit); err != nil {
			return nil, fmt.Errorf("dnssec: NSEC3 record %d: %w", i, err)
		}
		if rec.NSEC3.HashAlgorithm != algorithm || rec.NSEC3.Iterations != iterations || !bytes.Equal(rec.NSEC3.Salt, salt) {
			return nil, fmt.Errorf("dnssec: NSEC3 record %d has inconsistent zone parameters", i)
		}
		if rec.Owner.LabelCount() != zone.LabelCount()+1 || !IsSubdomainOf(rec.Owner, zone) {
			return nil, fmt.Errorf("dnssec: NSEC3 record %d owner %s is not in zone %s", i, rec.Owner, zone)
		}
		hash, err := rec.OwnerHash()
		if err != nil {
			return nil, fmt.Errorf("dnssec: NSEC3 record %d owner hash: %w", i, err)
		}
		key := string(hash)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("dnssec: duplicate NSEC3 owner hash %s", EncodeBase32Hex(hash))
		}
		seen[key] = struct{}{}
		s.Records = append(s.Records, NSEC3Record{
			Owner: Name{Labels: append([]string(nil), rec.Owner.Labels...)},
			Class: rec.Class,
			TTL:   rec.TTL,
			NSEC3: NSEC3{
				HashAlgorithm: rec.NSEC3.HashAlgorithm,
				Flags:         rec.NSEC3.Flags,
				Iterations:    rec.NSEC3.Iterations,
				Salt:          append([]byte(nil), rec.NSEC3.Salt...),
				NextHash:      append([]byte(nil), rec.NSEC3.NextHash...),
				Types:         append([]uint16(nil), rec.NSEC3.Types...),
			},
		})
	}
	sort.Slice(s.Records, func(i, j int) bool {
		hi, _ := s.Records[i].OwnerHash()
		hj, _ := s.Records[j].OwnerHash()
		return bytes.Compare(hi, hj) < 0
	})
	return s, nil
}

// Hash computes the NSEC3 hash of name using the set's zone parameters.
func (s *NSEC3Set) Hash(name Name) ([]byte, error) {
	if len(s.Records) == 0 {
		return nil, errors.New("dnssec: empty NSEC3 set")
	}
	p := s.Records[0].NSEC3
	return HashNSEC3WithLimit(name, p.Salt, p.Iterations, s.MaxIterations)
}

// FindExactHash finds the NSEC3 record whose owner hash equals hash.
func (s *NSEC3Set) FindExactHash(hash []byte) (*NSEC3Record, bool) {
	for i := range s.Records {
		ownerHash, err := s.Records[i].OwnerHash()
		if err != nil {
			continue
		}
		if bytes.Equal(ownerHash, hash) {
			return &s.Records[i], true
		}
	}
	return nil, false
}

// FindExactName hashes name and performs an exact NSEC3 owner match.
func (s *NSEC3Set) FindExactName(name Name) (*NSEC3Record, []byte, bool, error) {
	hash, err := s.Hash(name)
	if err != nil {
		return nil, nil, false, err
	}
	rec, ok := s.FindExactHash(hash)
	return rec, hash, ok, nil
}

// FindCoverage finds a record whose exclusive circular interval contains
// hash.
func (s *NSEC3Set) FindCoverage(hash []byte) (*NSEC3Record, bool) {
	for i := range s.Records {
		ownerHash, err := s.Records[i].OwnerHash()
		if err != nil {
			continue
		}
		if NSEC3CoversHash(ownerHash, s.Records[i].NSEC3.NextHash, hash) {
			return &s.Records[i], true
		}
	}
	return nil, false
}

// FindCoverageName hashes name and finds a covering NSEC3 record.
func (s *NSEC3Set) FindCoverageName(name Name) (*NSEC3Record, []byte, bool, error) {
	hash, err := s.Hash(name)
	if err != nil {
		return nil, nil, false, err
	}
	rec, ok := s.FindCoverage(hash)
	return rec, hash, ok, nil
}

// ClosestEncloser returns the longest existing ancestor of name, represented
// by an exact NSEC3 match. The queried name itself is considered.
func (s *NSEC3Set) ClosestEncloser(name Name) (Name, *NSEC3Record, error) {
	if !IsSubdomainOf(name, s.Zone) {
		return Name{}, nil, fmt.Errorf("dnssec: name %s is outside zone %s", name, s.Zone)
	}
	for count := name.LabelCount(); count >= s.Zone.LabelCount(); count-- {
		ancestor, err := name.Ancestor(count)
		if err != nil {
			return Name{}, nil, err
		}
		rec, _, ok, err := s.FindExactName(ancestor)
		if err != nil {
			return Name{}, nil, err
		}
		if ok {
			return ancestor, rec, nil
		}
	}
	return Name{}, nil, fmt.Errorf("dnssec: no closest encloser found for %s", name)
}

// NextCloser returns the first name below closestEncloser on the path from
// the zone toward qname.
func NextCloser(qname, closestEncloser Name) (Name, error) {
	if !IsProperSubdomainOf(qname, closestEncloser) {
		return Name{}, fmt.Errorf("dnssec: next closer requires %s to be below %s", qname, closestEncloser)
	}
	return qname.Ancestor(closestEncloser.LabelCount() + 1)
}
