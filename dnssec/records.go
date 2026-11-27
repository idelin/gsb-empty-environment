package dnssec

import (
	"encoding/binary"
	"errors"
)

var ErrRdataLen = errors.New("dnssec: malformed RDATA length")

// RRSIG is the parsed form of an RRSIG RDATA (RFC 4034 section 3).
type RRSIG struct {
	TypeCovered uint16
	Algorithm   uint8
	Labels      uint8
	OrigTTL     uint32
	Expiration  uint32
	Inception   uint32
	KeyTag      uint16
	Signer      Name
	Signature   []byte
}

// ParseRRSIG parses RRSIG RDATA.
func ParseRRSIG(b []byte) (*RRSIG, error) {
	// Fixed part is 18 octets before the signer name.
	if len(b) < 18 {
		return nil, ErrRdataLen
	}
	r := &RRSIG{
		TypeCovered: binary.BigEndian.Uint16(b[0:2]),
		Algorithm:   b[2],
		Labels:      b[3],
		OrigTTL:     binary.BigEndian.Uint32(b[4:8]),
		Expiration:  binary.BigEndian.Uint32(b[8:12]),
		Inception:   binary.BigEndian.Uint32(b[12:16]),
		KeyTag:      binary.BigEndian.Uint16(b[16:18]),
	}
	signer, n, err := readName(b, 18)
	if err != nil {
		return nil, err
	}
	r.Signer = signer
	r.Signature = append([]byte(nil), b[n:]...)
	return r, nil
}

// DNSKEY flags (RFC 4034 section 2.1.1).
const (
	DNSKEYZoneFlag    = 0x0100 // SEP / zone key bit
	DNSKEYRevokeFlag  = 0x0080
	DNSKEYSEPOffset   = 15
	DNSKEYRevokeShift = 8
)

// DNSKEY is the parsed form of a DNSKEY RDATA (RFC 4034 section 2).
type DNSKEY struct {
	Flags     uint16
	Protocol  uint8
	Algorithm uint8
	PublicKey []byte
}

// ParseDNSKEY parses DNSKEY RDATA.
func ParseDNSKEY(b []byte) (*DNSKEY, error) {
	if len(b) < 4 {
		return nil, ErrRdataLen
	}
	return &DNSKEY{
		Flags:     binary.BigEndian.Uint16(b[0:2]),
		Protocol:  b[2],
		Algorithm: b[3],
		PublicKey: append([]byte(nil), b[4:]...),
	}, nil
}

// IsZoneKey reports the ZONE flag (bit 7).
func (k *DNSKEY) IsZoneKey() bool { return k.Flags&DNSKEYZoneFlag != 0 }

// IsRevoked reports the REVOKE flag (bit 8).
func (k *DNSKEY) IsRevoked() bool { return k.Flags&DNSKEYRevokeFlag != 0 }

// DS is the parsed form of a DS RDATA (RFC 4034 section 5).
type DS struct {
	KeyTag     uint16
	Algorithm  uint8
	DigestType uint8
	Digest     []byte
}

// ParseDS parses DS RDATA.
func ParseDS(b []byte) (*DS, error) {
	if len(b) < 4 {
		return nil, ErrRdataLen
	}
	return &DS{
		KeyTag:     binary.BigEndian.Uint16(b[0:2]),
		Algorithm:  b[2],
		DigestType: b[3],
		Digest:     append([]byte(nil), b[4:]...),
	}, nil
}

// NSEC is the parsed form of an NSEC RDATA (RFC 4034 section 4).
type NSEC struct {
	NextName Name
	Types    []uint16
}

// ParseNSEC parses NSEC RDATA.
func ParseNSEC(b []byte) (*NSEC, error) {
	next, n, err := readName(b, 0)
	if err != nil {
		return nil, err
	}
	types, err := parseTypeBitmap(b[n:])
	if err != nil {
		return nil, err
	}
	return &NSEC{NextName: next, Types: types}, nil
}

// HasType reports whether the type bitmap contains typ.
func (s *NSEC) HasType(typ uint16) bool {
	for _, t := range s.Types {
		if t == typ {
			return true
		}
	}
	return false
}

// NSEC3 hash algorithm and flags (RFC 5155 section 3).
const (
	NSEC3SHA1   = 1
	NSEC3OptOut = 0x01
)

// NSEC3 is the parsed form of an NSEC3 RDATA (RFC 5155 section 3.2).
type NSEC3 struct {
	HashAlg    uint8
	Flags      uint8
	Iterations uint16
	Salt       []byte
	NextHashed []byte
	Types      []uint16
}

// OptOut reports the Opt-Out flag.
func (n *NSEC3) OptOut() bool { return n.Flags&NSEC3OptOut != 0 }

// ParseNSEC3 parses NSEC3 RDATA.
func ParseNSEC3(b []byte) (*NSEC3, error) {
	// HashAlg(1) Flags(1) Iterations(2) SaltLen(1) Salt HashLen(1) Hash
	if len(b) < 5 {
		return nil, ErrRdataLen
	}
	n := &NSEC3{
		HashAlg:    b[0],
		Flags:      b[1],
		Iterations: binary.BigEndian.Uint16(b[2:4]),
	}
	saltLen := int(b[4])
	off := 5
	if off+saltLen+1 > len(b) {
		return nil, ErrRdataLen
	}
	n.Salt = append([]byte(nil), b[off:off+saltLen]...)
	off += saltLen
	hashLen := int(b[off])
	off++
	if off+hashLen > len(b) {
		return nil, ErrRdataLen
	}
	n.NextHashed = append([]byte(nil), b[off:off+hashLen]...)
	off += hashLen
	types, err := parseTypeBitmap(b[off:])
	if err != nil {
		return nil, err
	}
	n.Types = types
	return n, nil
}

// HasType reports whether the type bitmap contains typ.
func (n *NSEC3) HasType(typ uint16) bool {
	for _, t := range n.Types {
		if t == typ {
			return true
		}
	}
	return false
}

// parseTypeBitmap decodes the NSEC/NSEC3 type bitmap (RFC 4034 4.1.2 /
// RFC 5155 3.2.1). The returned types are sorted and de-duplicated.
func parseTypeBitmap(b []byte) ([]uint16, error) {
	var types []uint16
	seen := map[uint16]bool{}
	off := 0
	for off < len(b) {
		if off+2 > len(b) {
			return nil, ErrRdataLen
		}
		window := uint16(b[off])
		bitmapLen := int(b[off+1])
		off += 2
		if bitmapLen < 1 || bitmapLen > 32 || off+bitmapLen > len(b) {
			return nil, ErrRdataLen
		}
		for i := 0; i < bitmapLen; i++ {
			octet := b[off+i]
			for bit := 0; bit < 8; bit++ {
				if octet&(0x80>>bit) != 0 {
					typ := window*256 + uint16(i*8+bit)
					if !seen[typ] {
						seen[typ] = true
						types = append(types, typ)
					}
				}
			}
		}
		off += bitmapLen
	}
	return types, nil
}

// encodeTypeBitmap is the canonical encoder for the type bitmap.
func encodeTypeBitmap(types []uint16) []byte {
	if len(types) == 0 {
		return nil
	}
	// Group into 256-type windows, keeping the highest set octet only.
	windows := map[uint16]map[uint16]bool{}
	var order []uint16
	for _, t := range types {
		w := t / 256
		if _, ok := windows[w]; !ok {
			windows[w] = map[uint16]bool{}
			order = append(order, w)
		}
		windows[w][t%256] = true
	}
	// Stable canonical order for deterministic output.
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j-1] > order[j]; j-- {
			order[j-1], order[j] = order[j], order[j-1]
		}
	}
	var out []byte
	for _, w := range order {
		bits := windows[w]
		maxBit := 0
		for bit := range bits {
			if int(bit) > maxBit {
				maxBit = int(bit)
			}
		}
		bitmapLen := maxBit/8 + 1
		out = append(out, byte(w), byte(bitmapLen))
		bitmap := make([]byte, bitmapLen)
		for bit := range bits {
			bitmap[bit/8] |= 0x80 >> (bit % 8)
		}
		out = append(out, bitmap...)
	}
	return out
}
