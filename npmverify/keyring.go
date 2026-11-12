package npmverify

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
)

// ECDSAKeyring is a ready-made SignatureVerifier holding the trusted
// registry signing keys, keyed by the Signature.KeyID. A proxy that manages
// its own key material can use it directly or implement SignatureVerifier
// for other schemes (e.g. Ed25519 or an HSM-backed signer).
type ECDSAKeyring struct {
	keys map[string]*ecdsa.PublicKey
}

// NewECDSAKeyring builds a keyring from PEM-encoded PKIX public keys
// ("-----BEGIN PUBLIC KEY-----") associated with their key ids.
func NewECDSAKeyring(keys map[string][]byte) (*ECDSAKeyring, error) {
	ring := &ECDSAKeyring{keys: make(map[string]*ecdsa.PublicKey, len(keys))}
	for keyID, material := range keys {
		block, _ := pem.Decode(material)
		if block == nil || block.Type != "PUBLIC KEY" {
			return nil, fmt.Errorf("npmverify: key %q is not a PEM-encoded PUBLIC KEY", keyID)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("npmverify: key %q: %w", keyID, err)
		}
		pub, ok := parsed.(*ecdsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("npmverify: key %q is not an ECDSA public key", keyID)
		}
		ring.keys[keyID] = pub
	}
	return ring, nil
}

// Verify implements SignatureVerifier. Unknown key ids and invalid signatures
// produce a non-nil error so the corresponding signature is rejected.
func (k *ECDSAKeyring) Verify(ctx context.Context, message string, sig Signature) error {
	pub, ok := k.keys[sig.KeyID]
	if !ok {
		return fmt.Errorf("npmverify: unknown signing key %q", sig.KeyID)
	}

	raw, err := base64.StdEncoding.DecodeString(sig.Sig)
	if err != nil {
		return fmt.Errorf("npmverify: signature is not valid base64: %w", err)
	}

	hasher := ecdsaHash(pub)
	if hasher == nil {
		return errors.New("npmverify: unsupported ECDSA curve")
	}
	hasher.Write([]byte(message))
	digest := hasher.Sum(nil)

	if !ecdsa.VerifyASN1(pub, digest, raw) {
		return errors.New("npmverify: ECDSA signature does not verify")
	}
	return nil
}

func ecdsaHash(pub *ecdsa.PublicKey) hash.Hash {
	switch pub.Params().Name {
	case "P-224", "P-256":
		return sha256.New()
	case "P-384":
		return sha512.New384()
	case "P-521":
		return sha512.New()
	default:
		return nil
	}
}
