package webauthn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"
	"math/big"
)

// WebAuthn authenticatorData flag bits (W3C WebAuthn, section 6.1).
const (
	flagUP byte = 1 << 0 // User Present
	flagUV byte = 1 << 2 // User Verified
	flagBE byte = 1 << 3 // Backup Eligible
	flagBS byte = 1 << 4 // Backup State
	flagAT byte = 1 << 5 // Attested Credential Data included
	flagED byte = 1 << 6 // Extension Data included
)

// reservedFlagMask covers bit 1 and bit 7, which are reserved in WebAuthn L2/L3
// and must never be set in a well-formed registration response.
const reservedFlagMask byte = (1 << 1) | (1 << 7)

const (
	rpIDHashLength    = 32
	flagsLength       = 1
	signCountLength   = 4
	aaguidLength      = 16
	credIDLenLength   = 2
	minAuthDataLength = rpIDHashLength + flagsLength + signCountLength
)

// AAGUID is the 16-byte authenticator attestation GUID.
type AAGUID [aaguidLength]byte

// AuthenticatorData is the parsed content of the raw authenticatorData byte
// string defined in W3C WebAuthn section 6.1.
type AuthenticatorData struct {
	RPIDHash            []byte
	Flags               byte
	UP                  bool
	UV                  bool
	BE                  bool
	BS                  bool
	AT                  bool
	ED                  bool
	SignCount           uint32
	AAGUID              AAGUID
	CredentialID        []byte
	CredentialPublicKey *cborNode
	Extensions          *cborNode
}

// parseAuthenticatorData performs the strict binary parse of an
// authenticatorData byte stream. It does not apply policy; flag policy and
// rpIdHash comparison are enforced by the top-level verifier.
func parseAuthenticatorData(raw []byte) (*AuthenticatorData, error) {
	if len(raw) < minAuthDataLength {
		return nil, fmt.Errorf("%w: authenticatorData shorter than %d bytes", ErrMalformed, minAuthDataLength)
	}

	data := &AuthenticatorData{
		RPIDHash: append([]byte(nil), raw[:rpIDHashLength]...),
		Flags:    raw[rpIDHashLength],
		SignCount: uint32(raw[rpIDHashLength+flagsLength])<<24 |
			uint32(raw[rpIDHashLength+flagsLength+1])<<16 |
			uint32(raw[rpIDHashLength+flagsLength+2])<<8 |
			uint32(raw[rpIDHashLength+flagsLength+3]),
	}
	flags := data.Flags
	data.UP = flags&flagUP != 0
	data.UV = flags&flagUV != 0
	data.BE = flags&flagBE != 0
	data.BS = flags&flagBS != 0
	data.AT = flags&flagAT != 0
	data.ED = flags&flagED != 0

	if flags&reservedFlagMask != 0 {
		return nil, fmt.Errorf("%w: reserved flag bit is set (flags=0x%02x)", ErrFlags, flags)
	}

	// FIDO authenticator model: a credential can only be backed up (BS) when
	// it is backup eligible (BE). BS=1 with BE=0 is an illegal state.
	if !data.BE && data.BS {
		return nil, fmt.Errorf("%w: BS=1 requires BE=1 (flags=0x%02x)", ErrFlags, flags)
	}

	offset := minAuthDataLength
	if data.AT {
		// attestedCredentialData: AAGUID (16) || credIdLength (2) || credId || COSE key.
		if len(raw) < offset+aaguidLength+credIDLenLength {
			return nil, fmt.Errorf("%w: truncated attested credential data", ErrMalformed)
		}
		copy(data.AAGUID[:], raw[offset:offset+aaguidLength])
		offset += aaguidLength

		credIDLength := int(raw[offset])<<8 | int(raw[offset+1])
		offset += credIDLenLength
		if len(raw) < offset+credIDLength {
			return nil, fmt.Errorf("%w: credential ID extends past authenticatorData", ErrMalformed)
		}
		data.CredentialID = append([]byte(nil), raw[offset:offset+credIDLength]...)
		offset += credIDLength
		if len(data.CredentialID) == 0 {
			return nil, fmt.Errorf("%w: attested credential ID is empty", ErrMalformed)
		}

		keyDecoder := &cborDecoder{data: raw[offset:]}
		keyNode, err := keyDecoder.readNode()
		if err != nil {
			return nil, fmt.Errorf("%w: credential public key is not valid CBOR: %v", ErrMalformed, err)
		}
		data.CredentialPublicKey = keyNode
		offset += keyDecoder.pos
	}

	if data.ED {
		if offset >= len(raw) {
			return nil, fmt.Errorf("%w: ED flag set but extension data missing", ErrMalformed)
		}
		extDecoder := &cborDecoder{data: raw[offset:]}
		extNode, err := extDecoder.readNode()
		if err != nil {
			return nil, fmt.Errorf("%w: extension data is not a valid CBOR map: %v", ErrMalformed, err)
		}
		if extNode.major != cborMap || extDecoder.pos != len(raw)-offset {
			return nil, fmt.Errorf("%w: extension data must be a single CBOR map", ErrMalformed)
		}
		data.Extensions = extNode
		offset += extDecoder.pos
	}

	if offset != len(raw) {
		return nil, fmt.Errorf("%w: %d unparsed bytes remain in authenticatorData", ErrMalformed, len(raw)-offset)
	}
	return data, nil
}

// COSE key labels (RFC 8152 section 13 / RFC 9053).
const (
	coseKty = 1
	coseAlg = 3
	coseCrv = -1
	coseX   = -2
	coseY   = -3
)

// COSE key type values.
const (
	coseKtyEC2 = 2
)

// COSE algorithm identifiers used by WebAuthn authenticators.
const (
	algES256 = -7
	algES384 = -35
	algES512 = -36
)

// COSE elliptic curve identifiers.
const (
	coseCrvP256 = 1
	coseCrvP384 = 2
	coseCrvP521 = 3
)

// parseCOSEPublicKey decodes an ES256/ES384/ES512 EC2 COSE public key and
// returns the corresponding *ecdsa.PublicKey together with its COSE algorithm.
// Only the two-coordinate uncompressed EC2 form is accepted.
func parseCOSEPublicKey(node *cborNode) (*ecdsa.PublicKey, int, error) {
	if node == nil || node.major != cborMap {
		return nil, 0, errCBORType("COSE public key", "map", node)
	}

	ktyNode := node.mapIndex(coseKty)
	kty, ok := ktyNode.asInt64()
	if !ok {
		return nil, 0, errCBORType("kty", "integer", ktyNode)
	}
	if kty != coseKtyEC2 {
		return nil, 0, fmt.Errorf("%w: unsupported COSE key type %d (only EC2=2)", ErrMalformed, kty)
	}

	algNode := node.mapIndex(coseAlg)
	alg, ok := algNode.asInt64()
	if !ok {
		return nil, 0, errCBORType("alg", "integer", algNode)
	}

	crvNode := node.mapIndex(coseCrv)
	crv, ok := crvNode.asInt64()
	if !ok {
		return nil, 0, errCBORType("crv", "integer", crvNode)
	}

	xNode := node.mapIndex(coseX)
	xBytes, ok := xNode.asBytes()
	if !ok {
		return nil, 0, errCBORType("x", "byte string", xNode)
	}
	yNode := node.mapIndex(coseY)
	yBytes, ok := yNode.asBytes()
	if !ok {
		return nil, 0, errCBORType("y", "byte string", yNode)
	}

	var curve elliptic.Curve
	var expectedCoordLength int
	switch crv {
	case coseCrvP256:
		curve = elliptic.P256()
		expectedCoordLength = 32
	case coseCrvP384:
		curve = elliptic.P384()
		expectedCoordLength = 48
	case coseCrvP521:
		curve = elliptic.P521()
		expectedCoordLength = 66
	default:
		return nil, 0, fmt.Errorf("%w: unsupported COSE curve %d", ErrMalformed, crv)
	}

	// The algorithm must match the curve (ES256/P-256, ES384/P-384, ES512/P-521).
	if (crv == coseCrvP256 && alg != algES256) ||
		(crv == coseCrvP384 && alg != algES384) ||
		(crv == coseCrvP521 && alg != algES512) {
		return nil, 0, fmt.Errorf("%w: COSE alg %d does not match curve %d", ErrMalformed, alg, crv)
	}
	if len(xBytes) != expectedCoordLength || len(yBytes) != expectedCoordLength {
		return nil, 0, fmt.Errorf("%w: EC coordinates must each be %d bytes on this curve", ErrMalformed, expectedCoordLength)
	}

	publicKey := &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return nil, 0, fmt.Errorf("%w: EC public key point is not on the declared curve", ErrMalformed)
	}
	return publicKey, int(alg), nil
}
