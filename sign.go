package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const signedLevelDomain = "netsec.security-level.v1"

// SignLevel signs a version's security level with an Ed25519 private key.
func SignLevel(version VersionID, level SecurityLevel, keyID string, privateKey ed25519.PrivateKey) SignedLevel {
	return SignedLevel{
		Level:     level,
		KeyID:     keyID,
		Signature: ed25519.Sign(privateKey, levelDigest(version, level)),
	}
}

// Ed25519Verifier returns a LevelVerifier that checks signatures against a
// key ring indexed by key ID.
func Ed25519Verifier(keys map[string]ed25519.PublicKey) LevelVerifier {
	keyRing := make(map[string]ed25519.PublicKey, len(keys))
	for keyID, key := range keys {
		keyRing[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return func(version VersionID, level SignedLevel) error {
		if level.KeyID == "" {
			return errors.New("security level has no key ID")
		}
		publicKey, ok := keyRing[level.KeyID]
		if !ok {
			return fmt.Errorf("unknown signing key %q", level.KeyID)
		}
		if len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("signing key %q has invalid size %d", level.KeyID, len(publicKey))
		}
		if len(level.Signature) != ed25519.SignatureSize {
			return fmt.Errorf("signature for version %q has invalid size %d", version, len(level.Signature))
		}
		if !ed25519.Verify(publicKey, levelDigest(version, level.Level), level.Signature) {
			return fmt.Errorf("signature for version %q is invalid", version)
		}
		return nil
	}
}

func levelDigest(version VersionID, level SecurityLevel) []byte {
	payload := make([]byte, 0, len(signedLevelDomain)+len(version)+11)
	payload = append(payload, signedLevelDomain...)
	payload = append(payload, 0)
	payload = append(payload, version...)
	payload = append(payload, 0)
	payload = binary.BigEndian.AppendUint64(payload, level)
	digest := sha256.Sum256(payload)
	return digest[:]
}
