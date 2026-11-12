package netsec

import "errors"

// Multicodec content-type codes used inside a CID.
const (
	codecRaw     = 0x55
	codecDagPB   = 0x70
	cidVersion1  = 1
	base32Prefix = byte(prefixBase32)
)

// ErrInvalidCID is returned when a string cannot be parsed as either a CIDv0
// (a bare base58btc multihash) or a multibase-encoded CIDv1.
var ErrInvalidCID = errors.New("netsec: invalid CID")

// CID is a content identifier. Only the version, codec and embedded multihash
// are retained: those are the fields needed for authorization and for
// rebuilding equivalent CID spellings.
type CID struct {
	version uint64
	codec   uint64
	hash    Multihash
}

// ParseCID parses a CID from any supported textual form:
//
//   - CIDv0: a bare base58btc string beginning with "Qm..." (the multihash
//     itself, always a sha2-256 dag-pb multihash);
//   - CIDv1: a multibase string (base16, base32, base36, base58btc, base64
//     and friends) whose decoded payload is version 1.
//
// Upper- and lower-case variants of the case-insensitive encodings are all
// accepted and decode to the same [CID], and therefore the same [Multihash].
func ParseCID(s string) (CID, error) {
	if s == "" {
		return CID{}, ErrInvalidCID
	}

	// CIDv0 is a bare base58btc multihash with no multibase prefix.
	if isCIDv0(s) {
		raw, err := base58Decode(s)
		if err != nil {
			return CID{}, ErrInvalidCID
		}
		hash, err := MultihashFromBytes(raw)
		if err != nil {
			return CID{}, ErrInvalidCID
		}
		code, err := hash.Code()
		if err != nil || code != MultihashSHA256 {
			return CID{}, ErrInvalidCID
		}
		return CID{version: 0, codec: codecDagPB, hash: hash}, nil
	}

	// Otherwise require an explicit multibase prefix and parse as CIDv1.
	raw, err := decodeMultibase(s)
	if err != nil {
		return CID{}, ErrInvalidCID
	}
	return parseCIDv1(raw)
}

func parseCIDv1(raw []byte) (CID, error) {
	version, n1, err := readUvarint(raw)
	if err != nil || version != cidVersion1 {
		return CID{}, ErrInvalidCID
	}
	codec, n2, err := readUvarint(raw[n1:])
	if err != nil || n2 == 0 {
		return CID{}, ErrInvalidCID
	}
	hash, err := MultihashFromBytes(raw[n1+n2:])
	if err != nil {
		return CID{}, ErrInvalidCID
	}
	return CID{version: cidVersion1, codec: codec, hash: hash}, nil
}

// Multihash returns the content multihash. This is the normalization step that
// makes every spelling of the same content compare equal on the blocklist.
func (c CID) Multihash() Multihash {
	return c.hash
}

// Version returns 0 or 1.
func (c CID) Version() uint64 {
	return c.version
}

// Codec returns the multicodec content-type code carried by the CID.
func (c CID) Codec() uint64 {
	return c.codec
}

// V1 returns the CIDv1 representation. A CIDv0 is upgraded in place by keeping
// its dag-pb codec and multihash; an existing CIDv1 is returned unchanged.
func (c CID) V1() CID {
	if c.version == cidVersion1 {
		return c
	}
	return CID{version: cidVersion1, codec: codecDagPB, hash: c.hash}
}

// bytes returns the binary encoding of the CID.
func (c CID) bytes() []byte {
	if c.version == 0 {
		return c.hash.raw
	}
	out := appendUvarint(nil, c.version)
	out = append(out, appendUvarint(nil, c.codec)...)
	out = append(out, c.hash.raw...)
	return out
}

// Encode renders the CID in the requested multibase (identified by its prefix
// character, for example 'b' for base32 or 'k' for base36). CIDv0 has no
// multibase form, so callers should convert with [CID.V1] first.
func (c CID) Encode(prefix byte) (string, error) {
	if c.version == 0 {
		return c.V1().Encode(prefix)
	}
	return encodeMultibase(prefix, c.bytes())
}

// String returns the canonical textual form: CIDv0 stays base58btc, CIDv1 is
// rendered as lower-case base32 with the "b" multibase prefix.
func (c CID) String() string {
	if c.version == 0 {
		return base58Encode(c.hash.raw)
	}
	s, err := c.Encode(base32Prefix)
	if err != nil {
		return ""
	}
	return s
}

// DNSLabel renders the CID as a lower-case DNS label for subdomain origin
// isolation. CIDv0 is first upgraded to CIDv1, then encoded as lower-case
// base32 (the encoding the IPFS gateway uses for CID labels).
func (c CID) DNSLabel() (string, error) {
	label, err := c.V1().Encode(base32Prefix)
	if err != nil {
		return "", err
	}
	return label, nil
}

// isCIDv0 matches a bare base58btc CIDv0: no multibase prefix, starts with the
// Qm pattern and only uses the base58 alphabet.
func isCIDv0(s string) bool {
	if len(s) < 2 || s[0] != 'Q' || s[1] != 'm' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if base58Index[s[i]] < 0 {
			return false
		}
	}
	return true
}
