package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"math"
	"math/big"
)

const (
	coseHeaderAlg     = int64(1)
	coseHeaderX5Chain = int64(33)

	coseAlgES256 = int64(-7)
	coseAlgES384 = int64(-35)
	coseAlgES512 = int64(-36)
)

// coseSign1 is a parsed COSE_Sign1 structure (RFC 9052), as used for the
// issuerAuth element of an mdoc IssuerSigned structure.
type coseSign1 struct {
	protected   []byte
	unprotected map[any]any
	payload     []byte
	signature   []byte
	algorithm   int64
}

func parseCOSESign1(raw []byte) (*coseSign1, error) {
	v, err := decodeCBOR(raw)
	if err != nil {
		return nil, fmt.Errorf("cose: %w", err)
	}
	tag, ok := v.(cborTag)
	if !ok || tag.num != 18 {
		return nil, errors.New("cose: expected tag 18 (COSE_Sign1)")
	}
	arr, ok := tag.value.([]any)
	if !ok || len(arr) != 4 {
		return nil, errors.New("cose: COSE_Sign1 must be an array of 4 items")
	}
	protected, ok := arr[0].([]byte)
	if !ok {
		return nil, errors.New("cose: protected header must be a bstr")
	}
	unprotected, ok := arr[1].(map[any]any)
	if !ok {
		return nil, errors.New("cose: unprotected header must be a map")
	}
	payload, ok := arr[2].([]byte)
	if !ok {
		return nil, errors.New("cose: payload must be a bstr")
	}
	signature, ok := arr[3].([]byte)
	if !ok {
		return nil, errors.New("cose: signature must be a bstr")
	}
	c := &coseSign1{
		protected:   protected,
		unprotected: unprotected,
		payload:     payload,
		signature:   signature,
	}
	if len(protected) > 0 {
		pm, err := decodeCBOR(protected)
		if err != nil {
			return nil, fmt.Errorf("cose: decoding protected header: %w", err)
		}
		if m, ok := pm.(map[any]any); ok {
			if alg, ok := cborInt(cborMapGetInt(m, coseHeaderAlg)); ok {
				c.algorithm = alg
			}
		}
	}
	if c.algorithm == 0 {
		return nil, errors.New("cose: missing algorithm in protected header")
	}
	return c, nil
}

// cborInt normalizes a decoded CBOR integer (uint64 or int64) to int64.
func cborInt(v any) (int64, bool) {
	switch t := v.(type) {
	case uint64:
		if t > math.MaxInt64 {
			return 0, false
		}
		return int64(t), true
	case int64:
		return t, true
	default:
		return 0, false
	}
}

// cborMapGetInt looks up an integer map key regardless of whether it was
// decoded as uint64 (non-negative) or int64 (negative).
func cborMapGetInt(m map[any]any, key int64) any {
	if key >= 0 {
		if v, ok := m[uint64(key)]; ok {
			return v
		}
	}
	return m[key]
}

func coseAlgParams(alg int64) (crypto.Hash, elliptic.Curve, error) {
	switch alg {
	case coseAlgES256:
		return crypto.SHA256, elliptic.P256(), nil
	case coseAlgES384:
		return crypto.SHA384, elliptic.P384(), nil
	case coseAlgES512:
		return crypto.SHA512, elliptic.P521(), nil
	default:
		return 0, nil, fmt.Errorf("cose: unsupported algorithm %d", alg)
	}
}

// verify checks the COSE_Sign1 signature over the Sig_structure with the
// given issuer public key.
func (c *coseSign1) verify(key *ecdsa.PublicKey) error {
	hash, curve, err := coseAlgParams(c.algorithm)
	if err != nil {
		return err
	}
	if !hash.Available() {
		return fmt.Errorf("cose: hash for algorithm %d not available", c.algorithm)
	}
	if key.Curve != curve {
		return errors.New("cose: public key curve does not match algorithm")
	}
	size := (curve.Params().BitSize + 7) / 8
	if len(c.signature) != 2*size {
		return fmt.Errorf("cose: signature length %d, want %d", len(c.signature), 2*size)
	}
	sigStructure, err := encodeCBOR([]any{
		"Signature1",
		c.protected,
		[]byte{}, // external_aad: empty for mdoc issuerAuth
		c.payload,
	})
	if err != nil {
		return err
	}
	h := hash.New()
	h.Write(sigStructure)
	digest := h.Sum(nil)
	r := new(big.Int).SetBytes(c.signature[:size])
	s := new(big.Int).SetBytes(c.signature[size:])
	if !ecdsa.Verify(key, digest, r, s) {
		return errors.New("cose: signature verification failed")
	}
	return nil
}

// x5chain returns the certificate chain (each cert DER-encoded) from the
// unprotected header, or nil if absent.
func (c *coseSign1) x5chain() ([][]byte, error) {
	v := cborMapGetInt(c.unprotected, coseHeaderX5Chain)
	if v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case []byte:
		return [][]byte{t}, nil
	case []any:
		chain := make([][]byte, 0, len(t))
		for _, item := range t {
			der, ok := item.([]byte)
			if !ok {
				return nil, errors.New("cose: x5chain element is not a bstr")
			}
			chain = append(chain, der)
		}
		return chain, nil
	default:
		return nil, errors.New("cose: malformed x5chain header")
	}
}
