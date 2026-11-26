package rpki

import (
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"

	"crypto/x509/pkix"
)

var (
	oidPEIPAddrBlocks    = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 7}
	oidPEASIdentifiers   = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 8}
	oidSubjectInfoAccess = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 1, 11}
	oidADCARepository    = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 5}
	oidKPSignedObject    = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 10}
	oidKPTimeStamping    = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
	oidContentType       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidData              = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidSHA256            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidECDSAWithSHA256   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidManifest          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 26}
	oidROA               = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 24}
	oidASPA              = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 49}
)

var (
	ErrExtension = errors.New("rpki: extension parse error")
)

type asn1IPBlock struct {
	AddressFamily []byte
	Choice        asn1.RawValue
}

// ParseResourceExtensions extracts RFC 3779 resource choices from a certificate.
func ParseResourceExtensions(exts []pkix.Extension) (ResourceClaims, bool, error) {
	var claims ResourceClaims
	found := false
	for _, ext := range exts {
		switch {
		case ext.Id.Equal(oidPEIPAddrBlocks):
			found = true
			if err := parseIPAddrBlocks(ext.Value, &claims); err != nil {
				return ResourceClaims{}, true, err
			}
		case ext.Id.Equal(oidPEASIdentifiers):
			found = true
			if err := parseASIdentifiers(ext.Value, &claims); err != nil {
				return ResourceClaims{}, true, err
			}
		}
	}
	return claims, found, nil
}

func parseIPAddrBlocks(der []byte, claims *ResourceClaims) error {
	var blocks []asn1IPBlock
	rest, err := asn1.Unmarshal(der, &blocks)
	if err != nil {
		return fmt.Errorf("%w: IPAddrBlocks: %v", ErrExtension, err)
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: trailing IPAddrBlocks bytes", ErrExtension)
	}
	for _, block := range blocks {
		family, err := afiToFamily(block.AddressFamily)
		if err != nil {
			return err
		}
		choice, err := parseIPChoice(block.Choice, family)
		if err != nil {
			return err
		}
		switch family {
		case IPv4:
			if claims.IPv4 != nil {
				return fmt.Errorf("%w: duplicate IPv4 block", ErrExtension)
			}
			claims.IPv4 = choice
		case IPv6:
			if claims.IPv6 != nil {
				return fmt.Errorf("%w: duplicate IPv6 block", ErrExtension)
			}
			claims.IPv6 = choice
		}
	}
	return nil
}

func afiToFamily(afi []byte) (IPFamily, error) {
	if len(afi) < 2 {
		return 0, fmt.Errorf("%w: short AFI %x", ErrExtension, afi)
	}
	switch uint16(afi[0])<<8 | uint16(afi[1]) {
	case 1:
		return IPv4, nil
	case 2:
		return IPv6, nil
	default:
		return 0, fmt.Errorf("%w: unsupported AFI %x", ErrExtension, afi[:2])
	}
}

func parseIPChoice(raw asn1.RawValue, family IPFamily) (*IPChoice, error) {
	if raw.Class == 0 && raw.Tag == asn1.TagNull {
		return &IPChoice{Inherit: true}, nil
	}
	if raw.Class != 0 || raw.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: invalid IPAddressChoice tag %d", ErrExtension, raw.Tag)
	}
	out := &IPChoice{}
	entries, err := unmarshalChildren(raw.Bytes)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Class != asn1.ClassContextSpecific {
			return nil, fmt.Errorf("%w: expected context IP choice, got class %d tag %d", ErrExtension, entry.Class, entry.Tag)
		}
		switch entry.Tag {
		case 0:
			bits, err := decodeMaybeExplicitBitString(entry)
			if err != nil {
				return nil, err
			}
			prefix, err := bitStringToPrefix(bits, family)
			if err != nil {
				return nil, err
			}
			out.Ranges = append(out.Ranges, prefix.Range())
		case 1:
			r, err := decodeIPRange(entry, family)
			if err != nil {
				return nil, err
			}
			out.Ranges = append(out.Ranges, r)
		default:
			return nil, fmt.Errorf("%w: unknown IPAddressOrRange tag %d", ErrExtension, entry.Tag)
		}
	}
	return out, nil
}

func unmarshalChildren(content []byte) ([]asn1.RawValue, error) {
	var out []asn1.RawValue
	for len(content) > 0 {
		var child asn1.RawValue
		rest, err := asn1.Unmarshal(content, &child)
		if err != nil {
			return nil, fmt.Errorf("%w: child element: %v", ErrExtension, err)
		}
		out = append(out, child)
		content = rest
	}
	return out, nil
}

func decodeMaybeExplicitBitString(raw asn1.RawValue) (asn1.BitString, error) {
	if looksLikeDERBitString(raw.Bytes) {
		var bs asn1.BitString
		rest, err := asn1.Unmarshal(raw.Bytes, &bs)
		if err == nil && len(rest) == 0 {
			return bs, nil
		}
	}
	if len(raw.Bytes) == 0 {
		return asn1.BitString{}, fmt.Errorf("%w: empty implicit bit string", ErrExtension)
	}
	unused := int(raw.Bytes[0])
	payload := raw.Bytes[1:]
	if unused > 7 || len(payload) == 0 && unused != 0 {
		return asn1.BitString{}, fmt.Errorf("%w: invalid bit string unused count %d", ErrExtension, unused)
	}
	bitLen := len(payload)*8 - unused
	if bitLen < 0 {
		return asn1.BitString{}, fmt.Errorf("%w: invalid bit string length", ErrExtension)
	}
	return asn1.BitString{Bytes: append([]byte(nil), payload...), BitLength: bitLen}, nil
}

func looksLikeDERBitString(content []byte) bool {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(content, &raw)
	return err == nil && len(rest) == 0 && raw.Tag == asn1.TagBitString && raw.Class == asn1.ClassUniversal
}

func bitStringToPrefix(bs asn1.BitString, family IPFamily) (IPPrefix, error) {
	if bs.BitLength < 0 || bs.BitLength > family.bitLen() {
		return IPPrefix{}, fmt.Errorf("%w: prefix length %d outside %s", ErrExtension, bs.BitLength, familyName(family))
	}
	value := bitStringValue(bs)
	if value.BitLen() > bs.BitLength {
		return IPPrefix{}, fmt.Errorf("%w: prefix bits exceed declared length", ErrExtension)
	}
	value.Lsh(value, uint(family.bitLen()-bs.BitLength))
	return IPPrefix{Family: family, Bits: bs.BitLength, Prefix: value}, nil
}

func bitStringValue(bs asn1.BitString) *big.Int {
	return new(big.Int).SetBytes(bs.Bytes)
}

func decodeIPRange(raw asn1.RawValue, family IPFamily) (IPRange, error) {
	content := raw.Bytes
	children, err := unmarshalChildren(content)
	if err != nil || len(children) != 2 {
		var seq asn1.RawValue
		rest, uerr := asn1.Unmarshal(raw.Bytes, &seq)
		if uerr != nil || len(rest) != 0 {
			return IPRange{}, fmt.Errorf("%w: IP range must contain two addresses", ErrExtension)
		}
		children, err = unmarshalChildren(seq.Bytes)
		if err != nil || len(children) != 2 {
			return IPRange{}, fmt.Errorf("%w: IP range has invalid endpoints", ErrExtension)
		}
	}
	minBS, err := rawToBitString(children[0])
	if err != nil {
		return IPRange{}, err
	}
	maxBS, err := rawToBitString(children[1])
	if err != nil {
		return IPRange{}, err
	}
	min, err := endpointInt(minBS, family, false)
	if err != nil {
		return IPRange{}, err
	}
	max, err := endpointInt(maxBS, family, true)
	if err != nil {
		return IPRange{}, err
	}
	if min.Cmp(max) > 0 {
		return IPRange{}, fmt.Errorf("%w: IP range min > max", ErrExtension)
	}
	return IPRange{Family: family, Start: min, End: max}, nil
}

func rawToBitString(raw asn1.RawValue) (asn1.BitString, error) {
	var bs asn1.BitString
	if raw.Class == asn1.ClassUniversal && raw.Tag == asn1.TagBitString {
		rest, err := asn1.Unmarshal(raw.FullBytes, &bs)
		if err != nil {
			return asn1.BitString{}, fmt.Errorf("%w: bit string: %v", ErrExtension, err)
		}
		if len(rest) != 0 {
			return asn1.BitString{}, fmt.Errorf("%w: trailing bit string bytes", ErrExtension)
		}
		return bs, nil
	}
	return decodeMaybeExplicitBitString(raw)
}

// endpointInt handles both full-width RFC 3779 endpoints and compact endpoint
// encodings. Missing low bits of a minimum are zero; missing low bits of a
// maximum are one.
func endpointInt(bs asn1.BitString, family IPFamily, maximum bool) (*big.Int, error) {
	width := family.bitLen()
	if bs.BitLength > width {
		return nil, fmt.Errorf("%w: address endpoint width %d", ErrExtension, bs.BitLength)
	}
	value := bitStringValue(bs)
	shift := uint(width - bs.BitLength)
	value.Lsh(value, shift)
	if maximum && shift > 0 {
		low := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), shift), big.NewInt(1))
		value.Or(value, low)
	}
	return value, nil
}

func parseASIdentifiers(der []byte, claims *ResourceClaims) error {
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(der, &seq)
	if err != nil {
		return fmt.Errorf("%w: ASIdentifiers: %v", ErrExtension, err)
	}
	if len(rest) != 0 || seq.Tag != asn1.TagSequence {
		return fmt.Errorf("%w: malformed ASIdentifiers", ErrExtension)
	}
	children, err := unmarshalChildren(seq.Bytes)
	if err != nil {
		return err
	}
	for _, child := range children {
		if child.Class != asn1.ClassContextSpecific || !child.IsCompound {
			return fmt.Errorf("%w: expected explicit AS choice wrapper", ErrExtension)
		}
		if child.Tag != 0 && child.Tag != 1 {
			continue
		}
		var choice asn1.RawValue
		rest, err := asn1.Unmarshal(child.Bytes, &choice)
		if err != nil || len(rest) != 0 {
			return fmt.Errorf("%w: AS choice wrapper: %v", ErrExtension, err)
		}
		parsed, err := parseASChoice(choice)
		if err != nil {
			return err
		}
		if child.Tag == 0 {
			if claims.ASNs != nil {
				return fmt.Errorf("%w: duplicate asnum choice", ErrExtension)
			}
			claims.ASNs = parsed
		} else {
			if claims.RDIs != nil {
				return fmt.Errorf("%w: duplicate rdi choice", ErrExtension)
			}
			claims.RDIs = parsed
		}
	}
	return nil
}

func parseASChoice(choice asn1.RawValue) (*ASChoice, error) {
	if choice.Class == 0 && choice.Tag == asn1.TagNull {
		return &ASChoice{Inherit: true}, nil
	}
	if choice.Class != 0 || choice.Tag != asn1.TagSequence {
		return nil, fmt.Errorf("%w: invalid ASIdentifierChoice", ErrExtension)
	}
	out := &ASChoice{}
	entries, err := unmarshalChildren(choice.Bytes)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Class != asn1.ClassContextSpecific {
			return nil, fmt.Errorf("%w: expected context AS entry", ErrExtension)
		}
		switch entry.Tag {
		case 0:
			asn, err := decodeASN(entry)
			if err != nil {
				return nil, err
			}
			out.Ranges = append(out.Ranges, ASRange{Start: asn, End: asn})
		case 1:
			r, err := decodeASRange(entry)
			if err != nil {
				return nil, err
			}
			out.Ranges = append(out.Ranges, r)
		default:
			return nil, fmt.Errorf("%w: unknown AS entry tag %d", ErrExtension, entry.Tag)
		}
	}
	return out, nil
}

func decodeASN(raw asn1.RawValue) (uint64, error) {
	if looksLikeDERInteger(raw.Bytes) {
		var n uint64
		rest, err := asn1.Unmarshal(raw.Bytes, &n)
		if err == nil && len(rest) == 0 {
			return n, nil
		}
	}
	if len(raw.Bytes) == 0 {
		return 0, fmt.Errorf("%w: empty ASN", ErrExtension)
	}
	n := new(big.Int).SetBytes(raw.Bytes)
	if n.Sign() < 0 {
		return 0, fmt.Errorf("%w: negative ASN", ErrExtension)
	}
	return n.Uint64(), nil
}

func looksLikeDERInteger(content []byte) bool {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(content, &raw)
	return err == nil && len(rest) == 0 && raw.Tag == asn1.TagInteger
}

func decodeASRange(raw asn1.RawValue) (ASRange, error) {
	children, err := unmarshalChildren(raw.Bytes)
	if err != nil || len(children) != 2 {
		var seq asn1.RawValue
		rest, uerr := asn1.Unmarshal(raw.Bytes, &seq)
		if uerr != nil || len(rest) != 0 {
			return ASRange{}, fmt.Errorf("%w: AS range needs two integers", ErrExtension)
		}
		children, err = unmarshalChildren(seq.Bytes)
		if err != nil || len(children) != 2 {
			return ASRange{}, fmt.Errorf("%w: invalid AS range", ErrExtension)
		}
	}
	start, err := rawASInt(children[0])
	if err != nil {
		return ASRange{}, err
	}
	end, err := rawASInt(children[1])
	if err != nil {
		return ASRange{}, err
	}
	if start > end {
		return ASRange{}, fmt.Errorf("%w: AS range start > end", ErrExtension)
	}
	return ASRange{Start: start, End: end}, nil
}

func rawASInt(raw asn1.RawValue) (uint64, error) {
	if raw.Class == asn1.ClassUniversal && raw.Tag == asn1.TagInteger {
		var n uint64
		rest, err := asn1.Unmarshal(raw.FullBytes, &n)
		if err == nil && len(rest) == 0 {
			return n, nil
		}
	}
	return decodeASN(raw)
}

// MarshalResourceExtensions encodes RFC 3779 claims for use in test/local CA
// certificate generation. The returned bytes are the extension value DER.
func MarshalResourceExtensions(claims ResourceClaims) (ipBlocks []byte, asIDs []byte, err error) {
	ipBlocks, err = marshalIPAddrBlocks(claims)
	if err != nil {
		return nil, nil, err
	}
	asIDs, err = marshalASIdentifiers(claims)
	if err != nil {
		return nil, nil, err
	}
	return ipBlocks, asIDs, nil
}

func marshalIPAddrBlocks(claims ResourceClaims) ([]byte, error) {
	var blocks []asn1IPBlock
	add := func(choice *IPChoice, family IPFamily, afi []byte) error {
		if choice == nil {
			return nil
		}
		wireChoice, err := marshalIPChoice(*choice, family)
		if err != nil {
			return err
		}
		blocks = append(blocks, asn1IPBlock{AddressFamily: afi, Choice: wireChoice})
		return nil
	}
	if err := add(claims.IPv4, IPv4, []byte{0, 1}); err != nil {
		return nil, err
	}
	if err := add(claims.IPv6, IPv6, []byte{0, 2}); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	return asn1.Marshal(blocks)
}

func marshalIPChoice(choice IPChoice, family IPFamily) (asn1.RawValue, error) {
	if choice.Inherit {
		return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagNull}, nil
	}
	var members []asn1.RawValue
	for _, r := range choice.Ranges {
		if r.Family != family {
			return asn1.RawValue{}, fmt.Errorf("%w: range family mismatch", ErrInvalidResource)
		}
		member, err := marshalIPRangeOrPrefix(r, family)
		if err != nil {
			return asn1.RawValue{}, err
		}
		members = append(members, member)
	}
	seqBytes, err := asn1.Marshal(members)
	if err != nil {
		return asn1.RawValue{}, err
	}
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(seqBytes, &seq); err != nil {
		return asn1.RawValue{}, err
	}
	return seq, nil
}

func marshalIPRangeOrPrefix(r IPRange, family IPFamily) (asn1.RawValue, error) {
	// Exact CIDR-aligned intervals are encoded as prefixes; all other intervals
	// use RFC 3779 min/max range encoding at full address width.
	if bits, ok := rangeToPrefixLength(r, family); ok {
		bs, err := prefixToBitString(r.Start, bits, family)
		if err != nil {
			return asn1.RawValue{}, err
		}
		raw, err := bitStringRaw(bs)
		if err != nil {
			return asn1.RawValue{}, err
		}
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: false, Bytes: raw.Bytes}, nil
	}
	minBS, err := fullAddressBitString(r.Start, family)
	if err != nil {
		return asn1.RawValue{}, err
	}
	maxBS, err := fullAddressBitString(r.End, family)
	if err != nil {
		return asn1.RawValue{}, err
	}
	seqBytes, err := asn1.Marshal(struct {
		Min asn1.BitString
		Max asn1.BitString
	}{Min: minBS, Max: maxBS})
	if err != nil {
		return asn1.RawValue{}, err
	}
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(seqBytes, &seq); err != nil {
		return asn1.RawValue{}, err
	}
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: seq.Bytes}, nil
}

func rangeToPrefixLength(r IPRange, family IPFamily) (int, bool) {
	width := family.bitLen()
	if r.Start == nil || r.End == nil {
		return 0, false
	}
	size := new(big.Int).Sub(new(big.Int).Add(r.End, big.NewInt(1)), r.Start)
	if size.Sign() <= 0 || size.And(size, new(big.Int).Sub(size, big.NewInt(1))).Sign() != 0 {
		return 0, false
	}
	bits := width - size.BitLen() + 1
	if bits < 0 || bits > width {
		return 0, false
	}
	blockSize := new(big.Int).Lsh(big.NewInt(1), uint(width-bits))
	if new(big.Int).Mod(r.Start, blockSize).Sign() != 0 {
		return 0, false
	}
	return bits, true
}

func prefixToBitString(start *big.Int, bits int, family IPFamily) (asn1.BitString, error) {
	width := family.bitLen()
	if bits < 0 || bits > width {
		return asn1.BitString{}, fmt.Errorf("%w: prefix bits %d", ErrInvalidResource, bits)
	}
	full := BigIntToIP(family, start)
	if full == nil {
		return asn1.BitString{}, fmt.Errorf("%w: prefix integer out of range", ErrInvalidResource)
	}
	byteLen := (bits + 7) / 8
	raw := append([]byte(nil), full[:byteLen]...)
	if bits%8 != 0 && len(raw) > 0 {
		raw[len(raw)-1] &= byte(0xff << (8 - bits%8))
	}
	return asn1.BitString{Bytes: raw, BitLength: bits}, nil
}

func fullAddressBitString(v *big.Int, family IPFamily) (asn1.BitString, error) {
	ip := BigIntToIP(family, v)
	if ip == nil {
		return asn1.BitString{}, fmt.Errorf("%w: address out of family range", ErrInvalidResource)
	}
	return asn1.BitString{Bytes: append([]byte(nil), ip...), BitLength: family.bitLen()}, nil
}

func bitStringRaw(bs asn1.BitString) (asn1.RawValue, error) {
	der, err := asn1.Marshal(bs)
	if err != nil {
		return asn1.RawValue{}, err
	}
	var raw asn1.RawValue
	_, err = asn1.Unmarshal(der, &raw)
	return raw, err
}

func marshalASIdentifiers(claims ResourceClaims) ([]byte, error) {
	var wrappers []asn1.RawValue
	if claims.ASNs != nil {
		full, err := marshalASChoice(*claims.ASNs)
		if err != nil {
			return nil, err
		}
		wrappers = append(wrappers, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: full})
	}
	if claims.RDIs != nil {
		full, err := marshalASChoice(*claims.RDIs)
		if err != nil {
			return nil, err
		}
		wrappers = append(wrappers, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: full})
	}
	if len(wrappers) == 0 {
		return nil, nil
	}
	content, err := asn1.Marshal(wrappers)
	if err != nil {
		return nil, err
	}
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(content, &seq); err != nil {
		return nil, err
	}
	return asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: seq.Bytes})
}

func marshalASChoice(choice ASChoice) ([]byte, error) {
	if choice.Inherit {
		return asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagNull})
	}
	var entries []asn1.RawValue
	for _, r := range choice.Ranges {
		entry, err := marshalASRangeOrASN(r)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return asn1.Marshal(entries)
}

func marshalASRangeOrASN(r ASRange) (asn1.RawValue, error) {
	if r.Start == r.End {
		intDER, err := asn1.Marshal(new(big.Int).SetUint64(r.Start))
		if err != nil {
			return asn1.RawValue{}, err
		}
		var intRaw asn1.RawValue
		if _, err := asn1.Unmarshal(intDER, &intRaw); err != nil {
			return asn1.RawValue{}, err
		}
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, Bytes: intRaw.Bytes}, nil
	}
	seqDER, err := asn1.Marshal(struct {
		Min *big.Int
		Max *big.Int
	}{Min: new(big.Int).SetUint64(r.Start), Max: new(big.Int).SetUint64(r.End)})
	if err != nil {
		return asn1.RawValue{}, err
	}
	var seq asn1.RawValue
	if _, err := asn1.Unmarshal(seqDER, &seq); err != nil {
		return asn1.RawValue{}, err
	}
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, IsCompound: true, Bytes: seq.Bytes}, nil
}

type accessDescriptionWire struct {
	AccessMethod   asn1.ObjectIdentifier
	AccessLocation asn1.RawValue
}

// ParseCARepositoryURIs extracts id-ad-caRepository SIA URIs.
func ParseCARepositoryURIs(value []byte) ([]string, error) {
	var ads []accessDescriptionWire
	rest, err := asn1.Unmarshal(value, &ads)
	if err != nil {
		return nil, fmt.Errorf("%w: SIA: %v", ErrExtension, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing SIA bytes", ErrExtension)
	}
	var uris []string
	for _, ad := range ads {
		if !ad.AccessMethod.Equal(oidADCARepository) {
			continue
		}
		if ad.AccessLocation.Class != asn1.ClassContextSpecific || ad.AccessLocation.Tag != 6 {
			return nil, fmt.Errorf("%w: caRepository is not URI", ErrExtension)
		}
		uris = append(uris, string(ad.AccessLocation.Bytes))
	}
	return uris, nil
}

// MarshalSubjectInfoAccess encodes one or more caRepository URIs.
func MarshalSubjectInfoAccess(uris ...string) ([]byte, error) {
	ads := make([]accessDescriptionWire, 0, len(uris))
	for _, uri := range uris {
		if uri == "" {
			return nil, fmt.Errorf("%w: empty SIA URI", ErrInvalidResource)
		}
		ads = append(ads, accessDescriptionWire{
			AccessMethod: oidADCARepository,
			AccessLocation: asn1.RawValue{
				Class: asn1.ClassContextSpecific,
				Tag:   6,
				Bytes: []byte(uri),
			},
		})
	}
	return asn1.Marshal(ads)
}
