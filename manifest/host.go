package manifest

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"time"
)

// Host is a boot-admission host. Its root set, thresholds and clock are pinned
// by the host integrator and never derived from manifests.
type Host struct {
	Model                string
	Roots                []RootKey
	RootThreshold        uint64
	MinFirmwareThreshold uint64
	Store                Store
	// Now returns the reference time; nil uses time.Now.
	Now func() time.Time
}

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Host) rootByID(id string) (RootKey, bool) {
	for _, r := range h.Roots {
		if r.ID == id {
			return r, true
		}
	}
	return RootKey{}, false
}

// verifyRootSignatures validates root signatures over the canonical encoding.
// Unknown root IDs never count; duplicate root IDs with differing signatures
// are an explicit conflict; a known root with a bad signature is a hard error.
func (h *Host) verifyRootSignatures(encoded []byte, sigs []RootSignature) error {
	seen := make(map[string][]byte, len(sigs))
	for _, s := range sigs {
		if len(s.Signature) == 0 {
			return fmt.Errorf("%w: root %q submitted empty signature", ErrInvalidRootSignature, s.RootID)
		}
		if prior, dup := seen[s.RootID]; dup {
			if !bytes.Equal(prior, s.Signature) {
				return fmt.Errorf("%w: root %q", ErrConflictingRootSignatures, s.RootID)
			}
			continue
		}
		seen[s.RootID] = s.Signature

		root, known := h.rootByID(s.RootID)
		if !known {
			// Unknown roots are ignored, not counted and not fatal.
			continue
		}
		if !ed25519.Verify(root.Public, encoded, s.Signature) {
			return fmt.Errorf("%w: root %q", ErrInvalidRootSignature, s.RootID)
		}
	}

	distinctKnown := 0
	for id := range seen {
		if _, known := h.rootByID(id); known {
			distinctKnown++
		}
	}
	if uint64(distinctKnown) < h.RootThreshold {
		return fmt.Errorf("%w: got %d distinct valid root(s), need %d",
			ErrRootThresholdNotMet, distinctKnown, h.RootThreshold)
	}
	return nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func keyByID(m Manifest, id string) (FirmwareKey, bool) {
	for _, k := range m.Keys {
		if k.KeyID == id {
			return k, true
		}
	}
	return FirmwareKey{}, false
}

// verifyTransition enforces state-continuity rules between the previously
// accepted manifest and a candidate of strictly higher generation.
func verifyTransition(previous, candidate Manifest) error {
	// Revocations can only grow.
	for _, revoked := range previous.RevokedKeyIDs {
		if !containsString(candidate.RevokedKeyIDs, revoked) {
			return fmt.Errorf("%w: keyID %q missing from new revocation set", ErrRevocationRollback, revoked)
		}
	}

	// A keyID that was still valid (not revoked) in the old manifest must keep
	// identical public key material if it still appears as a valid key in the
	// new manifest. A keyID revoked by the new manifest is no longer trusted
	// there, so rebinding its material is harmless (and revocation is already
	// enforced to be monotonic).
	for _, oldKey := range previous.Keys {
		if containsString(previous.RevokedKeyIDs, oldKey.KeyID) ||
			containsString(candidate.RevokedKeyIDs, oldKey.KeyID) {
			continue
		}
		newKey, present := keyByID(candidate, oldKey.KeyID)
		if !present {
			continue
		}
		if !bytes.Equal(oldKey.Public, newKey.Public) {
			return fmt.Errorf("%w: keyID %q", ErrKeyMaterialChanged, oldKey.KeyID)
		}
	}
	return nil
}

// Admit validates and atomically persists a signed manifest. Re-submitting the
// already accepted manifest (identical digest) is idempotent and returns the
// existing state.
func (h *Host) Admit(sm SignedManifest) (*State, error) {
	m := sm.Manifest
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}
	if m.DeviceModel != h.Model {
		return nil, fmt.Errorf("%w: manifest %q vs host %q", ErrModelMismatch, m.DeviceModel, h.Model)
	}

	now := h.now()
	if now.Before(m.NotBefore) || !now.Before(m.NotAfter) {
		return nil, fmt.Errorf("%w: now %s not in [%s, %s)",
			ErrOutsideValidityWindow, now.UTC().Format(time.RFC3339),
			m.NotBefore.UTC().Format(time.RFC3339), m.NotAfter.UTC().Format(time.RFC3339))
	}

	if m.FirmwareThreshold < h.MinFirmwareThreshold {
		return nil, fmt.Errorf("%w: %d < %d", ErrThresholdTooLow, m.FirmwareThreshold, h.MinFirmwareThreshold)
	}

	encoded, err := EncodeManifest(m)
	if err != nil {
		return nil, err
	}
	if err := h.verifyRootSignatures(encoded, sm.Signatures); err != nil {
		return nil, err
	}

	digest := sha256Sum(encoded)
	previous, err := h.Store.Load()
	switch {
	case err == nil:
		switch {
		case m.Generation < previous.Generation:
			return nil, fmt.Errorf("%w: submitted %d, accepted %d",
				ErrStaleGeneration, m.Generation, previous.Generation)
		case m.Generation == previous.Generation:
			if !bytes.Equal(digest, previous.Digest) {
				return nil, fmt.Errorf("%w: generation %d", ErrSameGenerationFork, m.Generation)
			}
			return previous, nil
		default:
			if err := verifyTransition(previous.Manifest, m); err != nil {
				return nil, err
			}
		}
	case err != nil && err != ErrNoState:
		return nil, err
	}

	state := &State{
		Generation: m.Generation,
		Digest:     digest,
		AcceptedAt: now,
		Manifest:   m,
	}
	if err := h.Store.Save(state); err != nil {
		return nil, err
	}
	return state, nil
}

// CurrentState returns the persisted trusted state or ErrNoState.
func (h *Host) CurrentState() (*State, error) {
	return h.Store.Load()
}

// eligibleFirmwareKeys returns the keys allowed to sign firmware for the
// accepted generation, indexed by public key fingerprint so alias keyIDs with
// identical material collapse to one vote.
func eligibleFirmwareKeys(m Manifest) map[string]FirmwareKey {
	out := make(map[string]FirmwareKey)
	for _, k := range m.Keys {
		if containsString(m.RevokedKeyIDs, k.KeyID) {
			continue
		}
		if !k.HasPurpose(PurposeFirmware) {
			continue
		}
		if !k.ValidAt(m.Generation) {
			continue
		}
		fp := string(PublicKeyFingerprint(k.Public))
		// Deterministic tie-break on duplicate fingerprints: keep the
		// lexicographically smallest keyID so behavior is stable.
		if existing, ok := out[fp]; ok && existing.KeyID < k.KeyID {
			continue
		}
		out[fp] = k
	}
	return out
}

// AdmitFirmware decides whether a firmware image may boot under the currently
// trusted manifest.
func (h *Host) AdmitFirmware(bundle FirmwareBundle) error {
	if bundle.Version == "" || len(bundle.Signatures) == 0 {
		return fmt.Errorf("%w: version and signatures are required", ErrInvalidFirmwareBundle)
	}
	state, err := h.Store.Load()
	if err == ErrNoState {
		return ErrNoTrustedManifest
	}
	if err != nil {
		return err
	}
	m := state.Manifest

	now := h.now()
	if now.Before(m.NotBefore) || !now.Before(m.NotAfter) {
		return fmt.Errorf("%w: trusted manifest expired", ErrOutsideValidityWindow)
	}
	if bundle.DeviceModel != h.Model || bundle.DeviceModel != m.DeviceModel {
		return fmt.Errorf("%w: bundle %q vs host %q", ErrModelMismatch, bundle.DeviceModel, h.Model)
	}
	if bundle.ManifestGeneration != state.Generation {
		return fmt.Errorf("%w: bundle %d, trusted %d",
			ErrFirmwareGenerationMismatch, bundle.ManifestGeneration, state.Generation)
	}

	imageDigest := bundle.ImageDigest
	if len(imageDigest) == 0 {
		imageDigest = ImageDigest(bundle.Image)
	} else if len(bundle.Image) > 0 && !bytes.Equal(imageDigest, ImageDigest(bundle.Image)) {
		return fmt.Errorf("%w: supplied digest does not match image", ErrInvalidFirmwareBundle)
	}
	if len(imageDigest) == 0 {
		return fmt.Errorf("%w: image digest is empty", ErrInvalidFirmwareBundle)
	}

	signingInput := EncodeFirmwareSigningInput(bundle.DeviceModel, imageDigest, bundle.Version, state.Generation)
	eligible := eligibleFirmwareKeys(m)
	eligibleByKeyID := make(map[string]FirmwareKey, len(eligible))
	for _, k := range eligible {
		eligibleByKeyID[k.KeyID] = k
	}

	// Count distinct public-key fingerprints behind valid signatures. A
	// keyID repeated with different signatures is a conflict and ignored as a
	// whole; an unknown/ineligible keyID never counts.
	signatureByKeyID := make(map[string][]byte)
	conflicting := make(map[string]struct{})
	for _, s := range bundle.Signatures {
		if prior, seen := signatureByKeyID[s.KeyID]; seen {
			if !bytes.Equal(prior, s.Signature) {
				conflicting[s.KeyID] = struct{}{}
			}
			continue
		}
		signatureByKeyID[s.KeyID] = s.Signature
	}

	votedFingerprints := make(map[string]struct{})
	for keyID, sig := range signatureByKeyID {
		if _, bad := conflicting[keyID]; bad {
			continue
		}
		key, ok := eligibleByKeyID[keyID]
		if !ok {
			continue
		}
		if !ed25519.Verify(key.Public, signingInput, sig) {
			continue
		}
		votedFingerprints[string(PublicKeyFingerprint(key.Public))] = struct{}{}
	}

	if uint64(len(votedFingerprints)) < m.FirmwareThreshold {
		return fmt.Errorf("%w: got %d distinct eligible key(s), need %d",
			ErrFirmwareThresholdNotMet, len(votedFingerprints), m.FirmwareThreshold)
	}
	return nil
}

func sha256Sum(b []byte) []byte {
	sum := PublicKeyFingerprint(b)
	return append([]byte(nil), sum...)
}
