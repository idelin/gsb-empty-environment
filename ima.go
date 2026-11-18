package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// DefaultXAttrName is the Linux IMA security attribute used for appraisal.
const DefaultXAttrName = "security.ima"

// The on-disk IMA signature attribute layout is:
//
//	type     uint8
//	version  uint8  (must be 2)
//	algo     uint8
//	keyid    uint32 (big endian)
//	siglen   uint16 (big endian)
//	signature[siglen]
//
// HeaderSize is the size of the fixed-size prefix.
const HeaderSize = 9

// HeaderVersion is the only signature attribute version accepted by this
// package.
const HeaderVersion = 2

// AttrType identifies the payload carried in security.ima.
type AttrType uint8

const (
	// TypeDigest is the legacy digest-only attribute format.
	TypeDigest AttrType = 0x01
	// TypeSignature is the IMA signature attribute format.
	TypeSignature AttrType = 0x03
	// TypeDigestNG is the newer digest-only attribute format.
	TypeDigestNG AttrType = 0x04
)

func (t AttrType) String() string {
	switch t {
	case TypeDigest:
		return "digest"
	case TypeSignature:
		return "signature"
	case TypeDigestNG:
		return "digest-ng"
	default:
		return fmt.Sprintf("unknown(0x%02x)", uint8(t))
	}
}

// Algorithm is the hash algorithm identifier stored in an IMA attribute.
//
// The values match the hash_algo identifiers used by IMA/Linux. A zero value
// is deliberately not considered valid.
type Algorithm uint8

const (
	AlgoMD5         Algorithm = 1
	AlgoSHA1        Algorithm = 2
	AlgoRipeMD160   Algorithm = 3
	AlgoSHA256      Algorithm = 4
	AlgoSHA384      Algorithm = 5
	AlgoSHA512      Algorithm = 6
	AlgoRipeMD320   Algorithm = 7
	AlgoWP256       Algorithm = 8
	AlgoWP384       Algorithm = 9
	AlgoWP512       Algorithm = 10
	AlgoTGR128      Algorithm = 11
	AlgoTGR160      Algorithm = 12
	AlgoTGR192      Algorithm = 13
	AlgoSM3         Algorithm = 14
	AlgoStreebog256 Algorithm = 15
	AlgoStreebog512 Algorithm = 16
	AlgoSHA3_256    Algorithm = 17
	AlgoSHA3_384    Algorithm = 18
	AlgoSHA3_512    Algorithm = 19
)

// Valid reports whether a is a known IMA hash algorithm.
func (a Algorithm) Valid() bool {
	_, ok := digestSizes[a]
	return ok
}

func (a Algorithm) String() string {
	switch a {
	case AlgoMD5:
		return "md5"
	case AlgoSHA1:
		return "sha1"
	case AlgoRipeMD160:
		return "rmd160"
	case AlgoSHA256:
		return "sha256"
	case AlgoSHA384:
		return "sha384"
	case AlgoSHA512:
		return "sha512"
	case AlgoRipeMD320:
		return "rmd320"
	case AlgoWP256:
		return "wp256"
	case AlgoWP384:
		return "wp384"
	case AlgoWP512:
		return "wp512"
	case AlgoTGR128:
		return "tgr128"
	case AlgoTGR160:
		return "tgr160"
	case AlgoTGR192:
		return "tgr192"
	case AlgoSM3:
		return "sm3"
	case AlgoStreebog256:
		return "streebog256"
	case AlgoStreebog512:
		return "streebog512"
	case AlgoSHA3_256:
		return "sha3-256"
	case AlgoSHA3_384:
		return "sha3-384"
	case AlgoSHA3_512:
		return "sha3-512"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(a))
	}
}

var digestSizes = map[Algorithm]int{
	AlgoMD5:         16,
	AlgoSHA1:        20,
	AlgoRipeMD160:   20,
	AlgoSHA256:      32,
	AlgoSHA384:      48,
	AlgoSHA512:      64,
	AlgoRipeMD320:   40,
	AlgoWP256:       32,
	AlgoWP384:       48,
	AlgoWP512:       64,
	AlgoTGR128:      16,
	AlgoTGR160:      20,
	AlgoTGR192:      24,
	AlgoSM3:         32,
	AlgoStreebog256: 32,
	AlgoStreebog512: 64,
	AlgoSHA3_256:    32,
	AlgoSHA3_384:    48,
	AlgoSHA3_512:    64,
}

var (
	// ErrNoAttribute means the file has no IMA attribute.
	ErrNoAttribute = errors.New("netsec: IMA attribute not found")
	// ErrMalformedAttribute means the attribute is too short or structurally
	// invalid.
	ErrMalformedAttribute = errors.New("netsec: malformed IMA attribute")
	// ErrUnknownAttributeType means the type byte is not a supported IMA
	// attribute type.
	ErrUnknownAttributeType = errors.New("netsec: unknown IMA attribute type")
	// ErrWrongAttributeType means a signature was required but the attribute
	// carries a digest instead.
	ErrWrongAttributeType = errors.New("netsec: IMA attribute is not a signature")
	// ErrUnsupportedVersion means a signature attribute does not use version 2.
	ErrUnsupportedVersion = errors.New("netsec: unsupported IMA attribute version")
	// ErrUnknownAlgorithm means the algorithm identifier is not known.
	ErrUnknownAlgorithm = errors.New("netsec: unknown IMA hash algorithm")
	// ErrLengthMismatch means the declared signature length does not exactly
	// match the bytes available in the attribute.
	ErrLengthMismatch = errors.New("netsec: IMA attribute length mismatch")
	// ErrEmptySignature means a signature attribute declares no signature
	// bytes.
	ErrEmptySignature = errors.New("netsec: empty IMA signature")
)

// Attribute is a decoded IMA security.ima value. Signature is populated for a
// signature attribute; Digest is populated for digest-only attributes.
type Attribute struct {
	Type            AttrType
	Version         uint8
	Algorithm       Algorithm
	KeyID           uint32
	SignatureLength uint16
	Signature       []byte
	Digest          []byte
}

// Parse decodes either a signature or a digest-only IMA attribute. It never
// slices beyond the supplied buffer.
func Parse(raw []byte) (Attribute, error) {
	if len(raw) == 0 {
		return Attribute{}, fmt.Errorf("%w: empty value", ErrMalformedAttribute)
	}

	switch AttrType(raw[0]) {
	case TypeSignature:
		return ParseSignature(raw)
	case TypeDigest:
		if len(raw) < 2 {
			return Attribute{Type: TypeDigest}, fmt.Errorf("%w: digest payload missing", ErrLengthMismatch)
		}
		want := digestSizes[AlgoSHA1]
		if len(raw)-1 != want {
			return Attribute{Type: TypeDigest, Algorithm: AlgoSHA1}, fmt.Errorf(
				"%w: digest is %d bytes, want %d", ErrLengthMismatch, len(raw)-1, want)
		}
		return Attribute{
			Type:      TypeDigest,
			Algorithm: AlgoSHA1,
			Digest:    append([]byte(nil), raw[1:]...),
		}, nil
	case TypeDigestNG:
		if len(raw) < 2 {
			return Attribute{Type: TypeDigestNG}, fmt.Errorf("%w: digest payload missing", ErrLengthMismatch)
		}
		algo := Algorithm(raw[1])
		want, ok := digestSizes[algo]
		if !ok {
			return Attribute{Type: TypeDigestNG, Algorithm: algo}, fmt.Errorf(
				"%w: %s", ErrUnknownAlgorithm, algo)
		}
		if len(raw)-2 != want {
			return Attribute{Type: TypeDigestNG, Algorithm: algo}, fmt.Errorf(
				"%w: digest is %d bytes, want %d", ErrLengthMismatch, len(raw)-2, want)
		}
		return Attribute{
			Type:      TypeDigestNG,
			Algorithm: algo,
			Digest:    append([]byte(nil), raw[2:]...),
		}, nil
	default:
		return Attribute{Type: AttrType(raw[0])}, fmt.Errorf(
			"%w: %s", ErrUnknownAttributeType, AttrType(raw[0]))
	}
}

// ParseSignature decodes a signature attribute and enforces every structural
// rule needed before the value can influence an execution or mapping decision.
func ParseSignature(raw []byte) (Attribute, error) {
	if len(raw) < HeaderSize {
		return Attribute{}, fmt.Errorf("%w: header is %d bytes, want at least %d",
			ErrLengthMismatch, len(raw), HeaderSize)
	}
	if AttrType(raw[0]) != TypeSignature {
		if attrType := AttrType(raw[0]); attrType != TypeDigest && attrType != TypeDigestNG {
			return Attribute{Type: attrType}, fmt.Errorf(
				"%w: %s", ErrUnknownAttributeType, attrType)
		}
		return Attribute{Type: AttrType(raw[0])}, fmt.Errorf(
			"%w: %s", ErrWrongAttributeType, AttrType(raw[0]))
	}

	version := raw[1]
	if version != HeaderVersion {
		return Attribute{Type: TypeSignature, Version: version}, fmt.Errorf(
			"%w: got %d, want %d", ErrUnsupportedVersion, version, HeaderVersion)
	}

	algo := Algorithm(raw[2])
	if !algo.Valid() {
		return Attribute{Type: TypeSignature, Version: version, Algorithm: algo}, fmt.Errorf(
			"%w: %s", ErrUnknownAlgorithm, algo)
	}

	keyID := binary.BigEndian.Uint32(raw[3:7])
	sigLen := binary.BigEndian.Uint16(raw[7:9])

	// Compare the declared length with the actual buffer length. Checking
	// equality, rather than only ensuring enough bytes, rejects both truncated
	// attributes and attributes carrying trailing data. The conversion is safe
	// because sigLen is uint16.
	want := HeaderSize + int(sigLen)
	if len(raw) != want {
		return Attribute{
				Type:            TypeSignature,
				Version:         version,
				Algorithm:       algo,
				KeyID:           keyID,
				SignatureLength: sigLen,
			}, fmt.Errorf("%w: declared %d bytes of signature, attribute has %d",
				ErrLengthMismatch, sigLen, len(raw)-HeaderSize)
	}
	if sigLen == 0 {
		return Attribute{
			Type:      TypeSignature,
			Version:   version,
			Algorithm: algo,
			KeyID:     keyID,
		}, ErrEmptySignature
	}

	return Attribute{
		Type:            TypeSignature,
		Version:         version,
		Algorithm:       algo,
		KeyID:           keyID,
		SignatureLength: sigLen,
		Signature:       append([]byte(nil), raw[HeaderSize:want]...),
	}, nil
}
