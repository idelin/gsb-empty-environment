package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// HostConfig fixes the root trust set and admission policy of a boot host.
type HostConfig struct {
	// Model is the device model accepted by this host.
	Model string
	// Roots is the host's fixed root trust set.
	Roots []Root
	// RootThreshold is the minimum number of distinct trusted roots that must
	// sign a manifest.
	RootThreshold int
	// MinimumFirmwareThreshold is the lowest firmware threshold the host will
	// accept in a manifest.
	MinimumFirmwareThreshold int
	// Store is the trusted accepted-manifest store.
	Store TrustedStore
	// Now may be replaced in tests. It defaults to time.Now.
	Now func() time.Time
}

// Host verifies root-signed manifests, commits accepted state, and admits
// firmware using the accepted manifest.
type Host struct {
	model                string
	roots                []Root
	rootThreshold        int
	minFirmwareThreshold int
	store                TrustedStore
	now                  func() time.Time
	mu                   sync.Mutex
}

// NewHost validates and copies a host configuration.
func NewHost(config HostConfig) (*Host, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("%w: trusted store is required", ErrInvalidHostConfig)
	}
	if err := validateIdentifier("device model", config.Model); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidHostConfig, err)
	}
	if _, err := validateRootTrust(config.Roots, config.RootThreshold); err != nil {
		return nil, err
	}
	if config.MinimumFirmwareThreshold < 1 {
		return nil, fmt.Errorf("%w: minimum firmware threshold must be positive", ErrInvalidHostConfig)
	}
	if config.Now == nil {
		config.Now = time.Now
	}

	roots := make([]Root, len(config.Roots))
	for i, root := range config.Roots {
		roots[i] = Root{
			ID:        root.ID,
			PublicKey: append(ed25519.PublicKey(nil), root.PublicKey...),
		}
	}
	return &Host{
		model:                config.Model,
		roots:                roots,
		rootThreshold:        config.RootThreshold,
		minFirmwareThreshold: config.MinimumFirmwareThreshold,
		store:                config.Store,
		now:                  config.Now,
	}, nil
}

// ManifestAcceptance describes a successfully verified manifest.
type ManifestAcceptance struct {
	Generation      uint64
	Digest          [sha256.Size]byte
	RootSigners     []string
	AlreadyAccepted bool
}

// AcceptManifest verifies a manifest at the host's current time and commits it
// if it advances or exactly re-states accepted state.
func (h *Host) AcceptManifest(manifest Manifest) (ManifestAcceptance, error) {
	return h.AcceptManifestAt(manifest, h.now())
}

// AcceptManifestAt is AcceptManifest with an explicit admission time.
func (h *Host) AcceptManifestAt(manifest Manifest, now time.Time) (ManifestAcceptance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	canonical, err := manifest.CanonicalEncoding()
	if err != nil {
		return ManifestAcceptance{}, err
	}
	digest := sha256.Sum256(canonical)
	rootSigners, err := VerifyRootSignatures(manifest, h.roots, h.rootThreshold)
	if err != nil {
		return ManifestAcceptance{}, err
	}

	state, oldManifest, hasOld, err := h.loadState()
	if err != nil {
		return ManifestAcceptance{}, err
	}
	if hasOld {
		switch {
		case manifest.Generation < state.Generation:
			return ManifestAcceptance{}, fmt.Errorf("%w: manifest %d, accepted %d", ErrGenerationRollback, manifest.Generation, state.Generation)
		case manifest.Generation == state.Generation && digest != state.Digest:
			return ManifestAcceptance{}, fmt.Errorf("%w: generation %d", ErrGenerationFork, manifest.Generation)
		}
	}
	if manifest.Model != h.model {
		return ManifestAcceptance{}, fmt.Errorf("%w: manifest %q, host %q", ErrDeviceModel, manifest.Model, h.model)
	}
	if !manifest.ValidAt(now) {
		return ManifestAcceptance{}, fmt.Errorf("%w: now %s, window [%s, %s)", ErrManifestExpired, now.UTC().Format(time.RFC3339Nano), manifest.ValidFrom.UTC().Format(time.RFC3339Nano), manifest.ValidUntil.UTC().Format(time.RFC3339Nano))
	}
	if manifest.FirmwareThreshold < h.minFirmwareThreshold {
		return ManifestAcceptance{}, fmt.Errorf("%w: manifest %d, minimum %d", ErrFirmwareThresholdPolicy, manifest.FirmwareThreshold, h.minFirmwareThreshold)
	}

	result := ManifestAcceptance{
		Generation:  manifest.Generation,
		Digest:      digest,
		RootSigners: append([]string(nil), rootSigners...),
	}
	if hasOld {
		switch {
		case manifest.Generation == state.Generation:
			result.AlreadyAccepted = true
			return result, nil
		case manifest.Generation > state.Generation:
			if err := validateTransition(oldManifest, manifest); err != nil {
				return ManifestAcceptance{}, err
			}
		}
	}

	next := TrustedState{
		Generation: manifest.Generation,
		Digest:     digest,
		Manifest:   append([]byte(nil), canonical...),
	}
	if err := h.store.Commit(next); err != nil {
		return ManifestAcceptance{}, fmt.Errorf("%w: %v", ErrStore, err)
	}
	return result, nil
}

func (h *Host) loadState() (TrustedState, Manifest, bool, error) {
	state, err := h.store.Load()
	if err != nil {
		return TrustedState{}, Manifest{}, false, fmt.Errorf("%w: %v", ErrStore, err)
	}
	manifest, accepted, err := validateTrustedState(state)
	if err != nil {
		return TrustedState{}, Manifest{}, false, err
	}
	return state, manifest, accepted, nil
}

func validateTransition(oldManifest, next Manifest) error {
	nextRevoked := make(map[string]struct{}, len(next.RevokedKeyIDs))
	for _, keyID := range next.RevokedKeyIDs {
		nextRevoked[keyID] = struct{}{}
	}
	for _, keyID := range oldManifest.RevokedKeyIDs {
		if _, retained := nextRevoked[keyID]; !retained {
			return fmt.Errorf("%w: key ID %q was removed", ErrRevocationRollback, keyID)
		}
	}

	nextKeys := make(map[string]FirmwareKey, len(next.Keys))
	for _, key := range next.Keys {
		nextKeys[key.KeyID] = key
	}
	oldRevoked := make(map[string]struct{}, len(oldManifest.RevokedKeyIDs))
	for _, keyID := range oldManifest.RevokedKeyIDs {
		oldRevoked[keyID] = struct{}{}
	}
	for _, oldKey := range oldManifest.Keys {
		if !oldKey.ActiveAt(next.Generation) {
			continue
		}
		if _, isRevoked := oldRevoked[oldKey.KeyID]; isRevoked {
			continue
		}
		nextKey, stillPresent := nextKeys[oldKey.KeyID]
		if !stillPresent {
			continue
		}
		if !bytes.Equal(oldKey.PublicKey, nextKey.PublicKey) {
			return fmt.Errorf("%w: key ID %q", ErrKeyMaterialChanged, oldKey.KeyID)
		}
	}
	return nil
}

// AdmitFirmware checks firmware signatures against the currently accepted
// manifest at the host's current time.
func (h *Host) AdmitFirmware(attestation FirmwareAttestation) (FirmwareDecision, error) {
	return h.AdmitFirmwareAt(attestation, h.now())
}

// AdmitFirmwareAt is AdmitFirmware with an explicit boot time.
func (h *Host) AdmitFirmwareAt(attestation FirmwareAttestation, now time.Time) (FirmwareDecision, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	_, manifest, accepted, err := h.loadState()
	if err != nil {
		return FirmwareDecision{}, err
	}
	if !accepted {
		return FirmwareDecision{}, ErrNoAcceptedManifest
	}
	if manifest.Model != h.model {
		return FirmwareDecision{}, fmt.Errorf("%w: manifest %q, host %q", ErrDeviceModel, manifest.Model, h.model)
	}
	if !manifest.ValidAt(now) {
		return FirmwareDecision{}, fmt.Errorf("%w: now %s, window [%s, %s)", ErrManifestExpired, now.UTC().Format(time.RFC3339Nano), manifest.ValidFrom.UTC().Format(time.RFC3339Nano), manifest.ValidUntil.UTC().Format(time.RFC3339Nano))
	}
	if manifest.FirmwareThreshold < h.minFirmwareThreshold {
		return FirmwareDecision{}, fmt.Errorf("%w: stored manifest threshold %d, minimum %d", ErrFirmwareThresholdPolicy, manifest.FirmwareThreshold, h.minFirmwareThreshold)
	}
	return manifest.VerifyFirmware(attestation)
}

// CurrentManifest returns the accepted manifest body and whether one exists.
func (h *Host) CurrentManifest() (Manifest, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, manifest, accepted, err := h.loadState()
	return manifest, accepted, err
}
