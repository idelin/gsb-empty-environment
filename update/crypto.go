package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

const ed25519PublicKeySize = ed25519.PublicKeySize

// Signer is an Ed25519 private key together with the root alias under which
// its public half is listed.
type Signer struct {
	Alias      string
	PrivateKey ed25519.PrivateKey
}

// GenerateSigner creates a fresh Ed25519 signer for the given root alias.
func GenerateSigner(alias string) (*Signer, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Signer{Alias: alias, PrivateKey: private}, nil
}

// PublicKey returns the root-compatible public key descriptor.
func (s *Signer) PublicKey() PublicKey {
	public := s.PrivateKey.Public().(ed25519.PublicKey)
	return PublicKey{
		Alias:     s.Alias,
		Algorithm: Ed25519,
		Public:    append(HexBytes(nil), public...),
	}
}

// Sign produces the Signature structure over the given canonical payload.
func (s *Signer) Sign(payload []byte) Signature {
	return Signature{
		KeyID:     s.Alias,
		Algorithm: Ed25519,
		Signature: ed25519.Sign(s.PrivateKey, payload),
	}
}

// countValidSignatures verifies an envelope against role and reports how many
// distinct authorized key aliases produced a valid signature.
//
// The key alias is the unit of trust: multiple Signature entries referencing
// the same alias never count more than once, even if both are individually
// valid. Unknown aliases, unsupported algorithms and bad signatures are
// ignored rather than counted.
func countValidSignatures(root *RootFile, roleName string, envelope *Envelope) (int, error) {
	role, ok := root.Roles[roleName]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingRole, roleName)
	}
	authorized := make(map[string]PublicKey, len(role.KeyAliases))
	for _, alias := range role.KeyAliases {
		authorized[alias] = root.Keys[alias]
	}

	payload := envelope.SignedBytes()
	counted := make(map[string]struct{}, len(envelope.Signatures))
	for _, signature := range envelope.Signatures {
		if _, alreadyCounted := counted[signature.KeyID]; alreadyCounted {
			// Same alias appearing again must not get a second vote.
			continue
		}
		key, ok := authorized[signature.KeyID]
		if !ok || signature.Algorithm != Ed25519 || key.Algorithm != Ed25519 {
			continue
		}
		if len(signature.Signature) != ed25519.SignatureSize {
			continue
		}
		if !ed25519.Verify(ed25519.PublicKey(key.Public), payload, signature.Signature) {
			continue
		}
		counted[signature.KeyID] = struct{}{}
	}
	return len(counted), nil
}

// verifyRole enforces the role threshold with distinct-alias counting.
func verifyRole(root *RootFile, roleName string, envelope *Envelope) error {
	count, err := countValidSignatures(root, roleName, envelope)
	if err != nil {
		return err
	}
	if count < root.Roles[roleName].Threshold {
		return fmt.Errorf("%w: role %s has %d valid distinct signatures, needs %d",
			ErrThresholdNotMet, roleName, count, root.Roles[roleName].Threshold)
	}
	return nil
}
