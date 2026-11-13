package netsec

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
)

func validateRootTrust(roots []Root, threshold int) (map[string]ed25519.PublicKey, error) {
	if threshold < 1 {
		return nil, fmt.Errorf("%w: threshold must be positive", ErrInvalidRootTrust)
	}
	if threshold > len(roots) {
		return nil, fmt.Errorf("%w: threshold %d exceeds %d roots", ErrInvalidRootTrust, threshold, len(roots))
	}
	trusted := make(map[string]ed25519.PublicKey, len(roots))
	for i, root := range roots {
		if err := validateIdentifier("root ID", root.ID); err != nil {
			return nil, fmt.Errorf("%w: root %d: %v", ErrInvalidRootTrust, i, err)
		}
		if _, exists := trusted[root.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate root ID %q", ErrInvalidRootTrust, root.ID)
		}
		if len(root.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: root %q has invalid public key length", ErrInvalidRootTrust, root.ID)
		}
		trusted[root.ID] = append(ed25519.PublicKey(nil), root.PublicKey...)
	}
	return trusted, nil
}

// SignRoot signs the complete canonical manifest body with an Ed25519 root
// key. If the manifest already has a signature with rootID, it is replaced.
func (m *Manifest) SignRoot(rootID string, privateKey ed25519.PrivateKey) error {
	if err := validateIdentifier("root ID", rootID); err != nil {
		return err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidSigningKey, len(privateKey))
	}
	canonical, err := m.CanonicalEncoding()
	if err != nil {
		return err
	}
	signature := ed25519.Sign(privateKey, canonical)
	for i := range m.RootSignatures {
		if m.RootSignatures[i].RootID == rootID {
			m.RootSignatures[i].Signature = signature
			return nil
		}
	}
	m.RootSignatures = append(m.RootSignatures, RootSignature{
		RootID:    rootID,
		Signature: signature,
	})
	return nil
}

// VerifyRootSignatures checks root signatures over the complete manifest
// canonical body. Only distinct known root IDs count. Unknown roots do not
// count, and two signatures with the same root ID must be byte-identical.
//
// The returned IDs are sorted.
func VerifyRootSignatures(m Manifest, roots []Root, threshold int) ([]string, error) {
	trusted, err := validateRootTrust(roots, threshold)
	if err != nil {
		return nil, err
	}
	canonical, err := m.CanonicalEncoding()
	if err != nil {
		return nil, err
	}

	seen := make(map[string][]byte, len(m.RootSignatures))
	counted := make(map[string]struct{}, len(m.RootSignatures))
	for _, rootSignature := range m.RootSignatures {
		if rootSignature.RootID == "" {
			return nil, fmt.Errorf("%w: empty root ID", ErrRootSignatureConflict)
		}
		if previous, exists := seen[rootSignature.RootID]; exists {
			if !bytes.Equal(previous, rootSignature.Signature) {
				return nil, fmt.Errorf("%w: %q", ErrRootSignatureConflict, rootSignature.RootID)
			}
			continue
		}
		seen[rootSignature.RootID] = append([]byte(nil), rootSignature.Signature...)

		publicKey, known := trusted[rootSignature.RootID]
		if !known {
			continue
		}
		if ed25519.Verify(publicKey, canonical, rootSignature.Signature) {
			counted[rootSignature.RootID] = struct{}{}
		}
	}

	if len(counted) < threshold {
		return nil, fmt.Errorf("%w: have %d distinct trusted roots, need %d", ErrRootThreshold, len(counted), threshold)
	}
	signers := make([]string, 0, len(counted))
	for rootID := range counted {
		signers = append(signers, rootID)
	}
	sort.Strings(signers)
	return signers, nil
}

// VerifyRootSignatures is the method form of the package function.
func (m Manifest) VerifyRootSignatures(roots []Root, threshold int) ([]string, error) {
	return VerifyRootSignatures(m, roots, threshold)
}
