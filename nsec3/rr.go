package nsec3

import "bytes"

// DNS type numbers referenced by the verification logic (RFC 1035/RFC 4034).
const (
	TypeA      = uint16(1)
	TypeNS     = uint16(2)
	TypeSOA    = uint16(6)
	TypeCNAME  = uint16(5)
	TypeDS     = uint16(43)
	TypeDNSKEY = uint16(48)
)

// optOutFlag is the only defined NSEC3 flags bit (RFC 5155 section 3.2).
const optOutFlag = uint8(0x01)

// maxSaltLen and maxHashLen are the one-octet length field limits.
const (
	maxSaltLen = 255
	maxHashLen = 255
)

// Params identifies a single NSEC3 hash chain. Every record belonging to
// the same proof must share identical parameters; mixing parameters means
// mixing zones (or an attacker splicing chains).
type Params struct {
	Algorithm  uint8
	Iterations uint16
	Salt       []byte
}

// Equal reports whether two parameter sets describe the same hash chain.
func (p Params) Equal(other Params) bool {
	return p.Algorithm == other.Algorithm &&
		p.Iterations == other.Iterations &&
		bytes.Equal(p.Salt, other.Salt)
}

// Record is a parsed NSEC3 RDATA set together with its type bitmap.
type Record struct {
	Algorithm  uint8
	Flags      uint8
	Iterations uint16
	Salt       []byte
	// NextHash is the raw binary Next Hashed Owner Name (20 bytes for
	// SHA-1), i.e. the ring successor of the record's owner.
	NextHash []byte
	// OwnerHash is the raw binary hashed owner name of this record. It
	// is not part of the RDATA; callers populate it from the RR owner
	// name (see OwnerHashFromLabel).
	OwnerHash []byte
	Types     map[uint16]bool
}

// Params returns the hash-chain parameters of the record.
func (r *Record) Params() Params {
	return Params{Algorithm: r.Algorithm, Iterations: r.Iterations, Salt: r.Salt}
}

// OptOut reports whether the Opt-Out flag is set.
func (r *Record) OptOut() bool { return r.Flags&optOutFlag != 0 }

// HasType reports whether t is present in the type bitmap.
func (r *Record) HasType(t uint16) bool { return r.Types[t] }

// Validate checks the record against RFC 5155 field constraints and the
// local iteration policy.
func (r *Record) Validate() error {
	if r.Algorithm != HashSHA1 {
		return ErrUnknownAlgorithm
	}
	if int(r.Iterations) > MaxIterations {
		return ErrIterationsTooLarge
	}
	if len(r.Salt) > maxSaltLen {
		return ErrInvalidRecord
	}
	if len(r.NextHash) == 0 || len(r.NextHash) > maxHashLen {
		return ErrInvalidRecord
	}
	if len(r.OwnerHash) > maxHashLen {
		return ErrInvalidRecord
	}
	if r.Types == nil {
		return ErrInvalidRecord
	}
	return nil
}

// ParseRData parses the NSEC3 RDATA (the wire format following the owner
// name, type, class and TTL) into a Record. The owner hash is not part of
// the RDATA and must be attached separately (see OwnerHashFromLabel).
func ParseRData(rdata []byte) (*Record, error) {
	if len(rdata) < 5 {
		return nil, ErrInvalidRecord
	}
	r := &Record{
		Algorithm:  rdata[0],
		Flags:      rdata[1],
		Iterations: uint16(rdata[2])<<8 | uint16(rdata[3]),
	}
	saltLen := int(rdata[4])
	pos := 5
	if saltLen > len(rdata)-pos {
		return nil, ErrInvalidRecord
	}
	if saltLen > 0 {
		r.Salt = append([]byte(nil), rdata[pos:pos+saltLen]...)
	}
	pos += saltLen
	if pos >= len(rdata) {
		return nil, ErrInvalidRecord
	}
	hashLen := int(rdata[pos])
	pos++
	if hashLen == 0 || hashLen > len(rdata)-pos {
		return nil, ErrInvalidRecord
	}
	r.NextHash = append([]byte(nil), rdata[pos:pos+hashLen]...)
	pos += hashLen
	types, err := ParseTypeBitmap(rdata[pos:])
	if err != nil {
		return nil, err
	}
	r.Types = types
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// MarshalRData renders the record back to wire RDATA. The bitmap is
// re-canonicalized (sorted windows, no empty windows).
func (r *Record) MarshalRData() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	types := make([]uint16, 0, len(r.Types))
	for t := range r.Types {
		types = append(types, t)
	}
	bitmap := EncodeTypeBitmap(types)
	out := make([]byte, 0, 6+len(r.Salt)+len(r.NextHash)+len(bitmap))
	out = append(out, r.Algorithm, r.Flags,
		byte(r.Iterations>>8), byte(r.Iterations), byte(len(r.Salt)))
	out = append(out, r.Salt...)
	out = append(out, byte(len(r.NextHash)))
	out = append(out, r.NextHash...)
	out = append(out, bitmap...)
	return out, nil
}

// OwnerHashFromLabel decodes the base32hex label of a hashed owner name
// (the left-most label of the NSEC3 RR owner, e.g. "0p9mh..." in
// "0p9mh....example") into raw hash bytes.
func OwnerHashFromLabel(label string) ([]byte, error) {
	raw, err := Base32HexDecode(label)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > maxHashLen {
		return nil, ErrInvalidRecord
	}
	return raw, nil
}

// OwnerLabel renders raw hash bytes as the base32hex owner label.
func OwnerLabel(hash []byte) string {
	return Base32HexEncode(hash)
}
