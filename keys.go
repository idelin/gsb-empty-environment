package netsec

import (
	"bytes"
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	_ "crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
)

var (
	// ErrUnsupportedKey indicates that a public key or signature algorithm is
	// not supported by this package.
	ErrUnsupportedKey = errors.New("unsupported SSH key algorithm")
	// ErrInvalidKey indicates that an SSH public key is malformed.
	ErrInvalidKey = errors.New("invalid SSH public key")
	// ErrInvalidSignature indicates that an SSH signature is malformed or
	// does not verify.
	ErrInvalidSignature = errors.New("invalid SSH signature")
)

// HostKey is a complete SSH public key blob and its SSH key type.
type HostKey struct {
	Type string
	Blob []byte
}

// Fingerprint returns the SHA-256 fingerprint in OpenSSH's SHA256:... form.
func (k HostKey) Fingerprint() string {
	sum := sha256.Sum256(k.Blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// ParseHostKey validates an SSH wire-encoded public key.
func ParseHostKey(blob []byte) (HostKey, error) {
	parsed, err := parsePublicKey(blob)
	if err != nil {
		return HostKey{}, err
	}
	return parsed.hostKey, nil
}

// MarshalPublicKey encodes a public key in SSH wire format.
func MarshalPublicKey(publicKey crypto.PublicKey) (HostKey, error) {
	switch key := publicKey.(type) {
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return HostKey{}, fmt.Errorf("%w: invalid Ed25519 public key length %d", ErrInvalidKey, len(key))
		}
		blob := appendString(nil, []byte("ssh-ed25519"))
		blob = appendString(blob, key)
		return HostKey{Type: "ssh-ed25519", Blob: blob}, nil
	case *ed25519.PublicKey:
		if key == nil {
			return HostKey{}, fmt.Errorf("%w: nil Ed25519 public key", ErrInvalidKey)
		}
		return MarshalPublicKey(ed25519.PublicKey(*key))
	case rsa.PublicKey:
		return marshalRSAPublicKey(&key)
	case *rsa.PublicKey:
		return marshalRSAPublicKey(key)
	case ecdsa.PublicKey:
		return marshalECDSAPublicKey(&key)
	case *ecdsa.PublicKey:
		return marshalECDSAPublicKey(key)
	case dsa.PublicKey:
		return marshalDSAPublicKey(&key)
	case *dsa.PublicKey:
		return marshalDSAPublicKey(key)
	default:
		return HostKey{}, fmt.Errorf("%w: %T", ErrUnsupportedKey, publicKey)
	}
}

// NewHostKey is an alias for MarshalPublicKey.
func NewHostKey(publicKey crypto.PublicKey) (HostKey, error) {
	return MarshalPublicKey(publicKey)
}

func marshalRSAPublicKey(key *rsa.PublicKey) (HostKey, error) {
	if key == nil || key.N == nil || key.N.Sign() <= 0 || key.E <= 1 {
		return HostKey{}, fmt.Errorf("%w: invalid RSA public key", ErrInvalidKey)
	}
	blob := appendString(nil, []byte("ssh-rsa"))
	blob = appendString(blob, marshalMPInt(big.NewInt(int64(key.E))))
	blob = appendString(blob, marshalMPInt(key.N))
	return HostKey{Type: "ssh-rsa", Blob: blob}, nil
}

func marshalECDSAPublicKey(key *ecdsa.PublicKey) (HostKey, error) {
	if key == nil || key.Curve == nil || key.X == nil || key.Y == nil ||
		!key.Curve.IsOnCurve(key.X, key.Y) {
		return HostKey{}, fmt.Errorf("%w: invalid ECDSA public key", ErrInvalidKey)
	}
	keyType, curveName, err := ecdsaTypeAndCurveName(key.Curve)
	if err != nil {
		return HostKey{}, err
	}
	point := elliptic.Marshal(key.Curve, key.X, key.Y)
	blob := appendString(nil, []byte(keyType))
	blob = appendString(blob, []byte(curveName))
	blob = appendString(blob, point)
	return HostKey{Type: keyType, Blob: blob}, nil
}

func marshalDSAPublicKey(key *dsa.PublicKey) (HostKey, error) {
	if key == nil || key.P == nil || key.Q == nil || key.G == nil || key.Y == nil ||
		key.P.Sign() <= 0 || key.Q.Sign() <= 0 || key.G.Sign() <= 0 || key.Y.Sign() <= 0 {
		return HostKey{}, fmt.Errorf("%w: invalid DSA public key", ErrInvalidKey)
	}
	blob := appendString(nil, []byte("ssh-dss"))
	blob = appendString(blob, marshalMPInt(key.P))
	blob = appendString(blob, marshalMPInt(key.Q))
	blob = appendString(blob, marshalMPInt(key.G))
	blob = appendString(blob, marshalMPInt(key.Y))
	return HostKey{Type: "ssh-dss", Blob: blob}, nil
}

func marshalMPInt(value *big.Int) []byte {
	if value == nil || value.Sign() == 0 {
		return nil
	}
	magnitude := value.Bytes()
	if magnitude[0]&0x80 != 0 {
		return append([]byte{0}, magnitude...)
	}
	return magnitude
}

// Verify checks an SSH signature blob against data using this public key.
func (k HostKey) Verify(data, signature []byte) error {
	parsed, err := parsePublicKey(k.Blob)
	if err != nil {
		return err
	}
	if k.Type != "" && k.Type != parsed.hostKey.Type {
		return fmt.Errorf("%w: key type %q does not match blob type %q", ErrInvalidKey, k.Type, parsed.hostKey.Type)
	}

	format, signatureBlob, err := parseSignature(signature)
	if err != nil {
		return err
	}
	if err := parsed.verify(format, signatureBlob, data); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

type parsedHostKey struct {
	hostKey HostKey

	ed25519 ed25519.PublicKey
	rsa     *rsa.PublicKey
	ecdsa   *ecdsa.PublicKey
	dsa     *dsa.PublicKey
}

func parsePublicKey(blob []byte) (parsedHostKey, error) {
	offset := 0
	typeBytes, err := consumeString(blob, &offset)
	if err != nil {
		return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	keyType := string(typeBytes)
	parsed := parsedHostKey{
		hostKey: HostKey{Type: keyType, Blob: append([]byte(nil), blob...)},
	}

	switch keyType {
	case "ssh-ed25519":
		key, err := consumeString(blob, &offset)
		if err != nil {
			return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		if len(key) != ed25519.PublicKeySize {
			return parsedHostKey{}, fmt.Errorf("%w: invalid Ed25519 key length %d", ErrInvalidKey, len(key))
		}
		parsed.ed25519 = append(ed25519.PublicKey(nil), key...)
	case "ssh-rsa":
		exponentBytes, err := consumeString(blob, &offset)
		if err != nil {
			return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		modulusBytes, err := consumeString(blob, &offset)
		if err != nil {
			return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		exponent, err := parseMPInt(exponentBytes)
		if err != nil || !exponent.IsInt64() || exponent.Int64() <= 1 || exponent.Int64() > int64(^uint(0)>>1) {
			return parsedHostKey{}, fmt.Errorf("%w: invalid RSA exponent", ErrInvalidKey)
		}
		modulus, err := parseMPInt(modulusBytes)
		if err != nil || modulus.Sign() <= 0 {
			return parsedHostKey{}, fmt.Errorf("%w: invalid RSA modulus", ErrInvalidKey)
		}
		parsed.rsa = &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		curveNameBytes, err := consumeString(blob, &offset)
		if err != nil {
			return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		curve, expectedName, err := ecdsaCurveForKeyType(keyType)
		if err != nil {
			return parsedHostKey{}, err
		}
		if string(curveNameBytes) != expectedName {
			return parsedHostKey{}, fmt.Errorf("%w: curve %q does not match key type %q", ErrInvalidKey, curveNameBytes, keyType)
		}
		pointBytes, err := consumeString(blob, &offset)
		if err != nil {
			return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
		}
		x, y := elliptic.Unmarshal(curve, pointBytes)
		if x == nil {
			x, y = elliptic.UnmarshalCompressed(curve, pointBytes)
		}
		if x == nil || y == nil || !curve.IsOnCurve(x, y) {
			return parsedHostKey{}, fmt.Errorf("%w: invalid ECDSA point", ErrInvalidKey)
		}
		parsed.ecdsa = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	case "ssh-dss":
		values := make([]*big.Int, 4)
		for index := range values {
			raw, err := consumeString(blob, &offset)
			if err != nil {
				return parsedHostKey{}, fmt.Errorf("%w: %v", ErrInvalidKey, err)
			}
			value, err := parseMPInt(raw)
			if err != nil || value.Sign() <= 0 {
				return parsedHostKey{}, fmt.Errorf("%w: invalid DSA parameter", ErrInvalidKey)
			}
			values[index] = value
		}
		parsed.dsa = &dsa.PublicKey{
			Parameters: dsa.Parameters{P: values[0], Q: values[1], G: values[2]},
			Y:          values[3],
		}
	default:
		return parsedHostKey{}, fmt.Errorf("%w: %q", ErrUnsupportedKey, keyType)
	}

	if offset != len(blob) {
		return parsedHostKey{}, fmt.Errorf("%w: trailing data", ErrInvalidKey)
	}
	return parsed, nil
}

func parsePublicKeyType(blob []byte) (string, error) {
	offset := 0
	keyType, err := consumeString(blob, &offset)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	if len(keyType) == 0 || offset == len(blob) {
		return "", fmt.Errorf("%w: incomplete key blob", ErrInvalidKey)
	}
	return string(keyType), nil
}

func parseMPInt(raw []byte) (*big.Int, error) {
	if len(raw) == 0 {
		return new(big.Int), nil
	}
	if raw[0]&0x80 != 0 {
		return nil, errors.New("negative SSH mpint")
	}
	return new(big.Int).SetBytes(raw), nil
}

func parseSignature(signature []byte) (string, []byte, error) {
	offset := 0
	format, err := consumeString(signature, &offset)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	blob, err := consumeString(signature, &offset)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if offset != len(signature) {
		return "", nil, fmt.Errorf("%w: trailing data", ErrInvalidSignature)
	}
	return string(format), blob, nil
}

func (k parsedHostKey) verify(format string, signature, data []byte) error {
	switch {
	case k.ed25519 != nil:
		if format != "ssh-ed25519" {
			return fmt.Errorf("signature format %q does not match Ed25519 key", format)
		}
		if len(signature) != ed25519.SignatureSize || !ed25519.Verify(k.ed25519, data, signature) {
			return errors.New("Ed25519 signature verification failed")
		}
		return nil
	case k.rsa != nil:
		var hash crypto.Hash
		switch format {
		case "ssh-rsa":
			hash = crypto.SHA1
		case "rsa-sha2-256":
			hash = crypto.SHA256
		case "rsa-sha2-512":
			hash = crypto.SHA512
		default:
			return fmt.Errorf("signature format %q does not match RSA key", format)
		}
		digest := hashData(hash, data)
		if err := rsa.VerifyPKCS1v15(k.rsa, hash, digest, signature); err != nil {
			return fmt.Errorf("RSA signature verification failed: %w", err)
		}
		return nil
	case k.ecdsa != nil:
		expectedFormat, hash, err := ecdsaSignatureParameters(k.ecdsa.Curve)
		if err != nil {
			return err
		}
		if format != expectedFormat {
			return fmt.Errorf("signature format %q does not match ECDSA key", format)
		}
		r, s, err := parseECDSASignature(signature)
		if err != nil {
			return err
		}
		if !ecdsa.Verify(k.ecdsa, hashData(hash, data), r, s) {
			return errors.New("ECDSA signature verification failed")
		}
		return nil
	case k.dsa != nil:
		if format != "ssh-dss" {
			return fmt.Errorf("signature format %q does not match DSA key", format)
		}
		if len(signature) != 40 {
			return errors.New("invalid DSA signature length")
		}
		digest := sha1.Sum(data)
		r := new(big.Int).SetBytes(signature[:20])
		s := new(big.Int).SetBytes(signature[20:])
		if !dsa.Verify(k.dsa, digest[:], r, s) {
			return errors.New("DSA signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("%w: no verifier for key", ErrUnsupportedKey)
	}
}

func parseECDSASignature(signature []byte) (*big.Int, *big.Int, error) {
	offset := 0
	rBytes, err := consumeString(signature, &offset)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	sBytes, err := consumeString(signature, &offset)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	if offset != len(signature) {
		return nil, nil, fmt.Errorf("%w: trailing ECDSA data", ErrInvalidSignature)
	}
	r, err := parseMPInt(rBytes)
	if err != nil || r.Sign() <= 0 {
		return nil, nil, fmt.Errorf("%w: invalid ECDSA r", ErrInvalidSignature)
	}
	s, err := parseMPInt(sBytes)
	if err != nil || s.Sign() <= 0 {
		return nil, nil, fmt.Errorf("%w: invalid ECDSA s", ErrInvalidSignature)
	}
	return r, s, nil
}

func hashData(hash crypto.Hash, data []byte) []byte {
	hasher := hash.New()
	_, _ = hasher.Write(data)
	return hasher.Sum(nil)
}

func ecdsaCurveForKeyType(keyType string) (elliptic.Curve, string, error) {
	switch keyType {
	case "ecdsa-sha2-nistp256":
		return elliptic.P256(), "nistp256", nil
	case "ecdsa-sha2-nistp384":
		return elliptic.P384(), "nistp384", nil
	case "ecdsa-sha2-nistp521":
		return elliptic.P521(), "nistp521", nil
	default:
		return nil, "", fmt.Errorf("%w: %q", ErrUnsupportedKey, keyType)
	}
}

func ecdsaTypeAndCurveName(curve elliptic.Curve) (string, string, error) {
	switch curve {
	case elliptic.P256():
		return "ecdsa-sha2-nistp256", "nistp256", nil
	case elliptic.P384():
		return "ecdsa-sha2-nistp384", "nistp384", nil
	case elliptic.P521():
		return "ecdsa-sha2-nistp521", "nistp521", nil
	default:
		return "", "", fmt.Errorf("%w: ECDSA curve %s", ErrUnsupportedKey, curve.Params().Name)
	}
}

func ecdsaSignatureParameters(curve elliptic.Curve) (string, crypto.Hash, error) {
	switch curve {
	case elliptic.P256():
		return "ecdsa-sha2-nistp256", crypto.SHA256, nil
	case elliptic.P384():
		return "ecdsa-sha2-nistp384", crypto.SHA384, nil
	case elliptic.P521():
		return "ecdsa-sha2-nistp521", crypto.SHA512, nil
	default:
		return "", 0, fmt.Errorf("%w: ECDSA curve %s", ErrUnsupportedKey, curve.Params().Name)
	}
}

// SignProof creates the SSH signature returned by a hostkeys-prove-00 request.
// It is primarily useful for servers, integration tests, and test fixtures.
func SignProof(privateKey crypto.PrivateKey, sessionID, keyBlob []byte) ([]byte, error) {
	if len(sessionID) == 0 {
		return nil, errors.New("session identifier is empty")
	}
	parsed, err := parsePublicKey(keyBlob)
	if err != nil {
		return nil, err
	}
	data := ProofData(sessionID, keyBlob)

	var publicKey crypto.PublicKey
	switch key := privateKey.(type) {
	case ed25519.PrivateKey:
		if len(key) != ed25519.PrivateKeySize {
			return nil, errors.New("invalid Ed25519 private key")
		}
		publicKey = key.Public()
	case *ed25519.PrivateKey:
		if key == nil || len(*key) != ed25519.PrivateKeySize {
			return nil, errors.New("nil Ed25519 private key")
		}
		publicKey = key.Public()
	case *rsa.PrivateKey:
		if key == nil {
			return nil, errors.New("nil RSA private key")
		}
		publicKey = &key.PublicKey
	case *ecdsa.PrivateKey:
		if key == nil || key.D == nil || key.Curve == nil || key.X == nil || key.Y == nil ||
			!key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, errors.New("invalid ECDSA private key")
		}
		publicKey = &key.PublicKey
	case *dsa.PrivateKey:
		if key == nil || key.X == nil || key.P == nil || key.Q == nil || key.G == nil || key.Y == nil {
			return nil, errors.New("invalid DSA private key")
		}
		publicKey = &key.PublicKey
	default:
		return nil, fmt.Errorf("%w: private key %T", ErrUnsupportedKey, privateKey)
	}

	signerKey, err := MarshalPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(signerKey.Blob, keyBlob) {
		return nil, fmt.Errorf("%w: private key does not match public key", ErrInvalidKey)
	}

	var format string
	var signature []byte
	switch key := privateKey.(type) {
	case ed25519.PrivateKey:
		format = "ssh-ed25519"
		signature = ed25519.Sign(key, data)
	case *ed25519.PrivateKey:
		format = "ssh-ed25519"
		signature = ed25519.Sign(*key, data)
	case *rsa.PrivateKey:
		format = "rsa-sha2-512"
		signature, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA512, hashData(crypto.SHA512, data))
	case *ecdsa.PrivateKey:
		format, _, err = ecdsaSignatureParameters(key.Curve)
		if err == nil {
			var hash crypto.Hash
			_, hash, _ = ecdsaSignatureParameters(key.Curve)
			var r, s *big.Int
			r, s, err = ecdsa.Sign(rand.Reader, key, hashData(hash, data))
			if err == nil {
				signature = appendString(nil, marshalMPInt(r))
				signature = appendString(signature, marshalMPInt(s))
			}
		}
	case *dsa.PrivateKey:
		format = "ssh-dss"
		digest := sha1.Sum(data)
		var r, s *big.Int
		r, s, err = dsa.Sign(rand.Reader, key, digest[:])
		if err == nil {
			signature = make([]byte, 40)
			r.FillBytes(signature[:20])
			s.FillBytes(signature[20:])
		}
	}
	if err != nil {
		return nil, err
	}
	if parsed.hostKey.Type == "ssh-rsa" {
		format = "rsa-sha2-512"
	}
	if format == "" || signature == nil {
		return nil, fmt.Errorf("%w: cannot sign with %T", ErrUnsupportedKey, privateKey)
	}

	out := appendString(nil, []byte(format))
	out = appendString(out, signature)
	return out, nil
}
