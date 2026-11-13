package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
)

// COSE algorithm identifiers (RFC 8152 / RFC 9053).
const (
	AlgorithmES256 int64 = -7
	AlgorithmEdDSA int64 = -8
)

// COSE header parameter labels.
const headerLabelAlg int64 = 1

var (
	// ErrAlgNotProtected is returned when the alg header parameter is
	// missing from the protected header. alg is read exclusively from
	// the protected header; an alg carried only in the unprotected
	// header is never consulted and always causes rejection.
	ErrAlgNotProtected = errors.New("cose: alg not present in protected header")

	// ErrProtectedNotCanonical is returned when the protected header
	// bytes are not deterministic-length canonical CBOR, i.e. they
	// differ from their canonical re-encoding.
	ErrProtectedNotCanonical = errors.New("cose: protected header is not canonical CBOR")

	// ErrSignatureMismatch is returned when the signature does not
	// verify against the Sig_structure.
	ErrSignatureMismatch = errors.New("cose: signature verification failed")

	errMalformedMessage  = errors.New("cose: malformed Sign1 message")
	errUnsupportedAlg    = errors.New("cose: unsupported or mismatched algorithm")
	errUnprotectedNotMap = errors.New("cose: unprotected header is not a map")
)

// Sign1 is a parsed COSE_Sign1 message.
type Sign1 struct {
	// Protected is the raw byte content of the protected header bstr,
	// exactly as received. It is guaranteed to be canonical CBOR.
	Protected []byte
	// Unprotected is the decoded unprotected header. Its contents are
	// never used for security decisions.
	Unprotected Map
	// Payload is the detached-or-attached payload bytes.
	Payload []byte
	// Signature is the raw signature bytes.
	Signature []byte
}

// ParseSign1 decodes a COSE_Sign1 message and enforces the header policy:
//
//   - the protected header must be definite-length canonical CBOR whose
//     canonical re-encoding is byte-identical to the received bytes;
//   - the alg parameter (label 1) must be present in the protected header.
//
// The unprotected header is decoded but never consulted for alg.
func ParseSign1(data []byte) (*Sign1, error) {
	v, rest, err := decodeItem(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errMalformedMessage, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", errMalformedMessage)
	}
	arr, ok := v.([]any)
	if !ok || len(arr) != 4 {
		return nil, fmt.Errorf("%w: expected 4-element array", errMalformedMessage)
	}

	protected, ok := arr[0].([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: protected header is not a bstr", errMalformedMessage)
	}
	unprotected, ok := arr[1].(Map)
	if !ok {
		return nil, errUnprotectedNotMap
	}
	payload, ok := arr[2].([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: payload is not a bstr", errMalformedMessage)
	}
	signature, ok := arr[3].([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: signature is not a bstr", errMalformedMessage)
	}

	if err := checkProtectedCanonical(protected); err != nil {
		return nil, err
	}
	if _, err := protectedAlg(protected); err != nil {
		return nil, err
	}

	return &Sign1{
		Protected:   protected,
		Unprotected: unprotected,
		Payload:     payload,
		Signature:   signature,
	}, nil
}

// checkProtectedCanonical verifies that the protected header bytes are a
// single canonical CBOR item whose deterministic re-encoding is identical
// to the received bytes.
func checkProtectedCanonical(protected []byte) error {
	if len(protected) == 0 {
		// An empty protected header is encoded as the empty bstr and
		// carries no parameters; alg will be reported missing by the
		// caller.
		return nil
	}
	reenc, err := Canonicalize(protected)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrProtectedNotCanonical, err)
	}
	if !bytes.Equal(reenc, protected) {
		return ErrProtectedNotCanonical
	}
	return nil
}

// protectedAlg decodes the protected header and extracts the alg parameter.
// alg is read only from the protected header.
func protectedAlg(protected []byte) (int64, error) {
	if len(protected) == 0 {
		return 0, ErrAlgNotProtected
	}
	v, _, err := decodeItem(protected)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrProtectedNotCanonical, err)
	}
	m, ok := v.(Map)
	if !ok {
		return 0, fmt.Errorf("%w: protected header is not a map", ErrProtectedNotCanonical)
	}
	for _, e := range m {
		label, ok := cborInt(e.Key)
		if !ok || label != headerLabelAlg {
			continue
		}
		alg, ok := cborInt(e.Value)
		if !ok {
			return 0, fmt.Errorf("%w: alg value is not an integer", errUnsupportedAlg)
		}
		return alg, nil
	}
	return 0, ErrAlgNotProtected
}

func cborInt(v any) (int64, bool) {
	switch t := v.(type) {
	case uint64:
		if t > uint64(1<<63-1) {
			return 0, false
		}
		return int64(t), true
	case int64:
		return t, true
	}
	return 0, false
}

// sigStructure builds the CBOR-encoded Sig_structure for COSE_Sign1:
//
//	["Signature1", protected, external_aad, payload]
//
// It is encoded canonically with definite lengths.
func sigStructure(protected, externalAAD, payload []byte) []byte {
	var buf bytes.Buffer
	encodeCanonical(&buf, []any{
		"Signature1",
		protected,
		externalAAD,
		payload,
	})
	return buf.Bytes()
}

// VerifySign1 parses and verifies a COSE_Sign1 message. It returns the
// payload on success. key must match the algorithm declared in the
// protected header:
//
//   - EdDSA (-8): ed25519.PublicKey
//   - ES256 (-7): *ecdsa.PublicKey on the P-256 curve
//
// externalAAD is the externally supplied authenticated data, empty if none.
func VerifySign1(data []byte, key any, externalAAD []byte) ([]byte, error) {
	msg, err := ParseSign1(data)
	if err != nil {
		return nil, err
	}
	if err := msg.Verify(key, externalAAD); err != nil {
		return nil, err
	}
	return msg.Payload, nil
}

// Alg returns the algorithm identifier from the protected header. It never
// reads the unprotected header.
func (m *Sign1) Alg() (int64, error) {
	return protectedAlg(m.Protected)
}

// Verify checks the message signature against key and externalAAD.
func (m *Sign1) Verify(key any, externalAAD []byte) error {
	alg, err := protectedAlg(m.Protected)
	if err != nil {
		return err
	}
	toVerify := sigStructure(m.Protected, externalAAD, m.Payload)

	switch alg {
	case AlgorithmEdDSA:
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: EdDSA requires ed25519.PublicKey", errUnsupportedAlg)
		}
		if !ed25519.Verify(pub, toVerify, m.Signature) {
			return ErrSignatureMismatch
		}
		return nil
	case AlgorithmES256:
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic.P256() {
			return fmt.Errorf("%w: ES256 requires *ecdsa.PublicKey on P-256", errUnsupportedAlg)
		}
		if len(m.Signature) != 64 {
			return ErrSignatureMismatch
		}
		digest := sha256.Sum256(toVerify)
		r := new(big.Int).SetBytes(m.Signature[:32])
		s := new(big.Int).SetBytes(m.Signature[32:])
		if !ecdsa.Verify(pub, digest[:], r, s) {
			return ErrSignatureMismatch
		}
		return nil
	}
	return fmt.Errorf("%w: alg %d", errUnsupportedAlg, alg)
}
