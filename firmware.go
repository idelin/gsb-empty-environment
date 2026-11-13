package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sort"
)

const firmwareDomain = "netsec/firmware-statement/v1"

// FirmwareSignature is one firmware key ID's signature over the firmware
// statement.
type FirmwareSignature struct {
	KeyID     string
	Signature []byte
}

// FirmwareAttestation binds an image digest, version, and manifest generation
// to the host device model.
type FirmwareAttestation struct {
	Model              string
	ImageDigest        [sha256.Size]byte
	Version            string
	ManifestGeneration uint64
	Signatures         []FirmwareSignature
}

// CanonicalStatement returns the deterministic bytes covered by firmware
// signatures.
func (a FirmwareAttestation) CanonicalStatement() ([]byte, error) {
	if err := validateText("model", a.Model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFirmware, err)
	}
	if err := validateText("firmware version", a.Version); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFirmware, err)
	}
	if a.ManifestGeneration == 0 {
		return nil, fmt.Errorf("%w: manifest generation must be non-zero", ErrInvalidFirmware)
	}

	var buf bytes.Buffer
	writeString(&buf, firmwareDomain)
	writeString(&buf, a.Model)
	writeBytes(&buf, a.ImageDigest[:])
	writeString(&buf, a.Version)
	writeUint64(&buf, a.ManifestGeneration)
	return buf.Bytes(), nil
}

// Digest returns SHA-256 over the canonical firmware statement.
func (a FirmwareAttestation) Digest() ([sha256.Size]byte, error) {
	statement, err := a.CanonicalStatement()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(statement), nil
}

// Sign signs the canonical firmware statement. If keyID already has a
// signature, it is replaced.
func (a *FirmwareAttestation) Sign(keyID string, privateKey ed25519.PrivateKey) error {
	if err := validateIdentifier("firmware key ID", keyID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFirmware, err)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: got %d bytes", ErrInvalidSigningKey, len(privateKey))
	}
	statement, err := a.CanonicalStatement()
	if err != nil {
		return err
	}
	signature := ed25519.Sign(privateKey, statement)
	for i := range a.Signatures {
		if a.Signatures[i].KeyID == keyID {
			a.Signatures[i].Signature = signature
			return nil
		}
	}
	a.Signatures = append(a.Signatures, FirmwareSignature{
		KeyID:     keyID,
		Signature: signature,
	})
	return nil
}

// FirmwareDecision describes the votes that satisfied a manifest's firmware
// signature threshold.
type FirmwareDecision struct {
	Model              string
	ImageDigest        [sha256.Size]byte
	Version            string
	ManifestGeneration uint64
	Threshold          int
	CountedKeyIDs      []string
	UniqueSigners      int
}

// VerifyFirmware checks firmware signatures against this manifest. Only
// unrevoked key IDs whose entries are active for the manifest generation and
// include the firmware usage can vote. Public key aliases count once.
//
// The manifest's validity window is not checked here; callers admitting a
// boot should also call ValidAt.
func (m Manifest) VerifyFirmware(attestation FirmwareAttestation) (FirmwareDecision, error) {
	if err := m.Validate(); err != nil {
		return FirmwareDecision{}, err
	}
	statement, err := attestation.CanonicalStatement()
	if err != nil {
		return FirmwareDecision{}, err
	}
	if attestation.Model != m.Model {
		return FirmwareDecision{}, fmt.Errorf("%w: attestation %q, manifest %q", ErrDeviceModel, attestation.Model, m.Model)
	}
	if attestation.ManifestGeneration != m.Generation {
		return FirmwareDecision{}, fmt.Errorf("%w: attestation generation %d, manifest %d", ErrInvalidFirmware, attestation.ManifestGeneration, m.Generation)
	}

	revoked := make(map[string]struct{}, len(m.RevokedKeyIDs))
	for _, keyID := range m.RevokedKeyIDs {
		revoked[keyID] = struct{}{}
	}

	signatures := make(map[string][]byte, len(attestation.Signatures))
	for _, signature := range attestation.Signatures {
		if err := validateIdentifier("firmware key ID", signature.KeyID); err != nil {
			return FirmwareDecision{}, fmt.Errorf("%w: %v", ErrInvalidFirmware, err)
		}
		if previous, exists := signatures[signature.KeyID]; exists {
			if !bytes.Equal(previous, signature.Signature) {
				return FirmwareDecision{}, fmt.Errorf("%w: %q", ErrFirmwareSignatureConflict, signature.KeyID)
			}
			continue
		}
		signatures[signature.KeyID] = append([]byte(nil), signature.Signature...)
	}

	keys := append([]FirmwareKey(nil), m.Keys...)
	sort.Slice(keys, func(i, j int) bool { return keys[i].KeyID < keys[j].KeyID })

	byMaterial := make(map[string]string)
	for _, key := range keys {
		if _, isRevoked := revoked[key.KeyID]; isRevoked {
			continue
		}
		if !key.ActiveAt(m.Generation) || !key.HasUsage(UsageFirmware) {
			continue
		}
		signature, signed := signatures[key.KeyID]
		if !signed || !ed25519.Verify(key.PublicKey, statement, signature) {
			continue
		}
		material := string(key.PublicKey)
		if _, alreadyCounted := byMaterial[material]; alreadyCounted {
			continue
		}
		byMaterial[material] = key.KeyID
	}

	decision := FirmwareDecision{
		Model:              attestation.Model,
		ImageDigest:        attestation.ImageDigest,
		Version:            attestation.Version,
		ManifestGeneration: attestation.ManifestGeneration,
		Threshold:          m.FirmwareThreshold,
		UniqueSigners:      len(byMaterial),
	}
	for _, keyID := range byMaterial {
		decision.CountedKeyIDs = append(decision.CountedKeyIDs, keyID)
	}
	sort.Strings(decision.CountedKeyIDs)
	if decision.UniqueSigners < m.FirmwareThreshold {
		return decision, fmt.Errorf("%w: have %d unique firmware signers, need %d", ErrFirmwareThreshold, decision.UniqueSigners, m.FirmwareThreshold)
	}
	return decision, nil
}
