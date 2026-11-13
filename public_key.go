package netsec

import (
	"bytes"
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

// Key is an OpenPGP public key or public subkey.
type Key struct {
	Version      uint8
	CreationTime time.Time
	Algorithm    PublicKeyAlgorithm

	// Fingerprint is the complete OpenPGP fingerprint.  Security decisions
	// must use this value rather than the shorter KeyID.
	Fingerprint []byte

	// KeyID is the 64-bit OpenPGP key ID.  It is retained for interoperability
	// and diagnostics only; it must not be used to authorize a revocation.
	KeyID []byte

	// PublicKey is one of *rsa.PublicKey, *dsa.PublicKey, *ecdsa.PublicKey, or
	// ed25519.PublicKey.
	PublicKey crypto.PublicKey

	// Body is the complete public key packet body.  It is the input to the
	// OpenPGP key fingerprint and to signatures made over the key.
	Body []byte
}

// FingerprintHex returns the fingerprint as lower-case hexadecimal.
func (k *Key) FingerprintHex() string {
	return hex.EncodeToString(k.Fingerprint)
}

// KeyIDHex returns the 64-bit key ID as lower-case hexadecimal.
func (k *Key) KeyIDHex() string {
	return hex.EncodeToString(k.KeyID)
}

func parsePublicKeyPacket(tag uint8, body []byte) (*Key, error) {
	if tag != 6 && tag != 14 {
		return nil, fmt.Errorf("%w: packet tag %d is not a public key", ErrInvalidPacket, tag)
	}
	if len(body) < 6 {
		return nil, fmt.Errorf("%w: short public key packet", ErrInvalidPacket)
	}

	version := body[0]
	var material []byte
	switch version {
	case 4:
		material = body[6:]
	case 5, 6:
		if len(body) < 10 {
			return nil, fmt.Errorf("%w: short v%d public key packet", ErrInvalidPacket, version)
		}
		n := binary.BigEndian.Uint32(body[6:10])
		if uint64(n) > uint64(len(body)-10) {
			return nil, fmt.Errorf("%w: truncated v%d key material", ErrInvalidPacket, version)
		}
		material = body[10 : 10+int(n)]
	case 3:
		return nil, fmt.Errorf("%w: v3 public keys are not supported", ErrUnsupportedAlgorithm)
	default:
		return nil, fmt.Errorf("%w: unsupported public key version %d", ErrUnsupportedAlgorithm, version)
	}

	algorithm := PublicKeyAlgorithm(body[5])
	publicKey, err := parseKeyMaterial(algorithm, material)
	if err != nil {
		return nil, err
	}

	key := &Key{
		Version:      version,
		CreationTime: time.Unix(int64(binary.BigEndian.Uint32(body[1:5])), 0).UTC(),
		Algorithm:    algorithm,
		PublicKey:    publicKey,
		Body:         append([]byte(nil), body...),
	}
	switch version {
	case 4:
		hash := sha1.Sum(canonicalKeyPrefix(version, body))
		key.Fingerprint = hash[:]
	case 5, 6:
		hash := sha256.Sum256(canonicalKeyPrefix(version, body))
		key.Fingerprint = hash[:]
	}

	switch version {
	case 5, 6:
		// V5 and V6 key IDs are the high-order 64 bits of the fingerprint.
		key.KeyID = append([]byte(nil), key.Fingerprint[:8]...)
	default:
		key.KeyID = append([]byte(nil), key.Fingerprint[len(key.Fingerprint)-8:]...)
	}
	return key, nil
}

func canonicalKeyPrefix(version uint8, body []byte) []byte {
	var prefix []byte
	switch version {
	case 5:
		prefix = []byte{0x9a}
		prefix = append(prefix, byte(len(body)>>24), byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	case 6:
		prefix = []byte{0x9b}
		prefix = append(prefix, byte(len(body)>>24), byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	default:
		prefix = []byte{0x99, byte(len(body) >> 8), byte(len(body))}
	}
	return append(prefix, body...)
}

func parseKeyMaterial(algorithm PublicKeyAlgorithm, material []byte) (crypto.PublicKey, error) {
	rd := newMaterialReader(material)
	switch algorithm {
	case PubKeyRSAEncryptSign, PubKeyRSAEncryptOnly, PubKeyRSASignOnly:
		n, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		e, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		if n.Sign() <= 0 || e.Sign() <= 0 {
			return nil, fmt.Errorf("%w: invalid RSA public key", ErrInvalidPacket)
		}
		if !e.IsInt64() || e.Int64() > int64(^uint(0)>>1) {
			return nil, fmt.Errorf("%w: RSA exponent is too large", ErrInvalidPacket)
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case PubKeyDSA:
		p, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		q, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		g, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		y, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		return &dsa.PublicKey{Parameters: dsa.Parameters{P: p, Q: q, G: g}, Y: y}, nil
	case PubKeyECDSA:
		oid, err := rd.oid()
		if err != nil {
			return nil, err
		}
		point, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		curve, err := curveForOID(oid)
		if err != nil {
			return nil, err
		}
		x, y, err := unmarshalCurvePoint(curve, point)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case PubKeyEdDSA, PubKeyEd25519:
		oid, err := rd.oid()
		if err != nil {
			return nil, err
		}
		point, err := rd.mpi()
		if err != nil {
			return nil, err
		}
		if !isEd25519OID(oid) {
			return nil, fmt.Errorf("%w: unsupported EdDSA curve", ErrUnsupportedAlgorithm)
		}
		raw, err := ed25519PointBytes(point)
		if err != nil {
			return nil, err
		}
		return ed25519.PublicKey(raw), nil
	case PubKeyECDH, PubKeyX25519, PubKeyX448:
		// These keys cannot issue signatures, so recovery of their public
		// material is unnecessary for revocation verification.
		return nil, fmt.Errorf("%w: non-signing public key algorithm %s", ErrUnsupportedAlgorithm, algorithm)
	default:
		return nil, fmt.Errorf("%w: public-key algorithm %d", ErrUnsupportedAlgorithm, algorithm)
	}
}

type materialReader struct {
	data []byte
	off  int
}

func newMaterialReader(data []byte) *materialReader {
	return &materialReader{data: data}
}

func (r *materialReader) read(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.data) {
		return nil, fmt.Errorf("%w: truncated key material", ErrInvalidPacket)
	}
	out := r.data[r.off : r.off+n]
	r.off += n
	return out, nil
}

func (r *materialReader) mpi() (*big.Int, error) {
	bitsBytes, err := r.read(2)
	if err != nil {
		return nil, err
	}
	bits := binary.BigEndian.Uint16(bitsBytes)
	n := (int(bits) + 7) / 8
	value, err := r.read(n)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(value), nil
}

func (r *materialReader) oid() ([]byte, error) {
	n, err := r.read(1)
	if err != nil {
		return nil, err
	}
	return r.read(int(n[0]))
}

var curveOIDMap = map[string]elliptic.Curve{
	"2a8648ce3d030107": elliptic.P256(),
	"2b81040022":       elliptic.P384(),
	"2b81040023":       elliptic.P521(),
}

func curveForOID(oid []byte) (elliptic.Curve, error) {
	curve, ok := curveOIDMap[hex.EncodeToString(oid)]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported curve OID %s", ErrUnsupportedAlgorithm, hex.EncodeToString(oid))
	}
	return curve, nil
}

func unmarshalCurvePoint(curve elliptic.Curve, point *big.Int) (*big.Int, *big.Int, error) {
	byteLen := (curve.Params().BitSize + 7) / 8
	expected := 1 + 2*byteLen
	compressedLen := 1 + byteLen
	raw := point.Bytes()
	if len(raw) == expected {
		x, y := elliptic.Unmarshal(curve, raw)
		if x == nil || y == nil {
			return nil, nil, fmt.Errorf("%w: invalid ECDSA point", ErrInvalidPacket)
		}
		return x, y, nil
	}
	if len(raw) == compressedLen && (raw[0] == 2 || raw[0] == 3) {
		x, y := elliptic.UnmarshalCompressed(curve, raw)
		if x == nil || y == nil {
			return nil, nil, fmt.Errorf("%w: invalid compressed ECDSA point", ErrInvalidPacket)
		}
		return x, y, nil
	}
	if len(raw) < expected {
		padded := append(bytes.Repeat([]byte{0}, expected-len(raw)), raw...)
		x, y := elliptic.Unmarshal(curve, padded)
		if x == nil || y == nil {
			return nil, nil, fmt.Errorf("%w: invalid ECDSA point", ErrInvalidPacket)
		}
		return x, y, nil
	}
	if len(raw) != expected {
		return nil, nil, fmt.Errorf("%w: invalid ECDSA point length", ErrInvalidPacket)
	}
	return nil, nil, fmt.Errorf("%w: invalid ECDSA point", ErrInvalidPacket)
}

func isEd25519OID(oid []byte) bool {
	encoded := hex.EncodeToString(oid)
	return encoded == "2b06010401da470f01" || encoded == "2b6570"
}

func ed25519PointBytes(point *big.Int) ([]byte, error) {
	raw := point.Bytes()
	if len(raw) == 33 && raw[0] == 0x40 {
		raw = raw[1:]
	}
	if len(raw) > ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid Ed25519 point", ErrInvalidPacket)
	}
	if len(raw) < ed25519.PublicKeySize {
		raw = append(bytes.Repeat([]byte{0}, ed25519.PublicKeySize-len(raw)), raw...)
	}
	return append([]byte(nil), raw...), nil
}
