package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"time"
)

// Config pins the host's trust parameters. The root set and all thresholds are
// host-fixed and cannot be influenced by manifest contents.
type Config struct {
	// DeviceModel is the only model this host will admit.
	DeviceModel string
	// Roots is the pinned root trust set; map key must match Root.ID.
	Roots map[string]Root
	// RootThreshold is the minimum number of distinct known root signatures
	// required to accept a manifest.
	RootThreshold int
	// MinFirmwareThreshold is the lower bound for any manifest's firmware
	// signature threshold t.
	MinFirmwareThreshold uint64
	// Now supplies the reference clock for validity-window checks. Defaults
	// to time.Now when nil.
	Now func() time.Time
}

// Host is the boot-admission entry point. It is safe for concurrent use to the
// extent that the underlying Store is.
type Host struct {
	cfg   Config
	store Store
}

// NewHost validates the pinned configuration and returns a Host backed by
// store.
func NewHost(cfg Config, store Store) (*Host, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidConfig)
	}
	if cfg.DeviceModel == "" {
		return nil, fmt.Errorf("%w: empty device model", ErrInvalidConfig)
	}
	if len(cfg.Roots) == 0 {
		return nil, fmt.Errorf("%w: empty root set", ErrInvalidConfig)
	}
	if cfg.RootThreshold <= 0 || cfg.RootThreshold > len(cfg.Roots) {
		return nil, fmt.Errorf("%w: root threshold out of range", ErrInvalidConfig)
	}
	if cfg.MinFirmwareThreshold == 0 {
		return nil, fmt.Errorf("%w: zero minimum firmware threshold", ErrInvalidConfig)
	}
	for id, root := range cfg.Roots {
		if id == "" || id != root.ID {
			return nil, fmt.Errorf("%w: root key %q has mismatched ID", ErrInvalidConfig, id)
		}
		if len(root.PubKey) == 0 {
			return nil, fmt.Errorf("%w: root %q has empty public key", ErrInvalidConfig, id)
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Host{cfg: cfg, store: store}, nil
}

// Store exposes the backing trusted storage.
func (h *Host) Store() Store { return h.store }

// CurrentState loads trusted state and verifies its self-consistency. It
// returns ErrNoState before the first manifest is accepted.
func (h *Host) CurrentState() (*State, error) {
	return h.loadTrusted()
}

// Accept runs full admission on a signed manifest and, on success, durably and
// atomically updates trusted storage. Re-submitting the already-accepted
// manifest is a no-op success; lower generations are rejected as replays and
// same-generation manifests with a different digest are rejected as forks.
func (h *Host) Accept(sm *SignedManifest) error {
	if sm == nil || sm.Manifest == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	m := sm.Manifest

	if err := validate(m); err != nil {
		return err
	}
	if m.DeviceModel != h.cfg.DeviceModel {
		return fmt.Errorf("%w: manifest %q host %q", ErrModelMismatch, m.DeviceModel, h.cfg.DeviceModel)
	}
	if m.Threshold < h.cfg.MinFirmwareThreshold {
		return fmt.Errorf("%w: t=%d minimum=%d", ErrThresholdBelowMin, m.Threshold, h.cfg.MinFirmwareThreshold)
	}

	now := h.cfg.Now()
	if now.Before(m.NotBefore) || !now.Before(m.NotAfter) {
		return fmt.Errorf("%w: window [%s, %s) at %s",
			ErrOutsideValidity, m.NotBefore.UTC().Format(time.RFC3339),
			m.NotAfter.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}

	rootCount, err := verifyRootQuorum(sm, h.cfg.Roots)
	if err != nil {
		return err
	}
	if rootCount < h.cfg.RootThreshold {
		return fmt.Errorf("%w: %d of %d required roots", ErrRootQuorum, rootCount, h.cfg.RootThreshold)
	}

	digest := ManifestDigest(m)

	old, err := h.store.Load()
	switch {
	case err == nil:
		switch {
		case m.Generation < old.Generation:
			return fmt.Errorf("%w: gen %d < accepted %d", ErrReplayedManifest, m.Generation, old.Generation)
		case m.Generation == old.Generation:
			if !bytes.Equal(old.Digest, digest) {
				return fmt.Errorf("%w: gen %d", ErrForkedManifest, m.Generation)
			}
			// Identical re-submission: nothing to persist.
			return nil
		}
		if err := checkTransition(old.SignedManifest.Manifest, m); err != nil {
			return err
		}
	case errors.Is(err, ErrNoState):
		// First accepted manifest; no transition constraints apply.
	default:
		return err
	}

	newState := &State{
		Generation:     m.Generation,
		Digest:         digest,
		SignedManifest: sm,
	}
	if err := h.store.Commit(newState); err != nil {
		// Commit failure must leave the previous generation observable.
		return err
	}
	return nil
}

// BootCheck decides whether a firmware image may boot under the currently
// accepted manifest. The image generation must match the stored manifest
// generation, and the number of distinct eligible firmware keys signing the
// image must reach the manifest threshold t.
func (h *Host) BootCheck(img *FirmwareImage) error {
	if img == nil {
		return fmt.Errorf("%w: nil firmware image", ErrInvalidManifest)
	}
	state, err := h.loadTrusted()
	if err != nil {
		return err
	}
	return h.admitUnder(state, img)
}

// VerifyFirmware is the stateless variant of BootCheck: it admits img under an
// explicitly supplied signed manifest, after re-verifying its root quorum.
// It is useful when a caller already holds the accepted state.
func (h *Host) VerifyFirmware(sm *SignedManifest, img *FirmwareImage) error {
	if sm == nil || sm.Manifest == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	if img == nil {
		return fmt.Errorf("%w: nil firmware image", ErrInvalidManifest)
	}
	rootCount, err := verifyRootQuorum(sm, h.cfg.Roots)
	if err != nil {
		return err
	}
	if rootCount < h.cfg.RootThreshold {
		return fmt.Errorf("%w: %d of %d required roots", ErrRootQuorum, rootCount, h.cfg.RootThreshold)
	}
	if err := validate(sm.Manifest); err != nil {
		return err
	}
	return h.admitUnder(&State{
		Generation:     sm.Manifest.Generation,
		Digest:         ManifestDigest(sm.Manifest),
		SignedManifest: sm,
	}, img)
}

func (h *Host) admitUnder(state *State, img *FirmwareImage) error {
	m := state.SignedManifest.Manifest

	if img.DeviceModel != h.cfg.DeviceModel {
		return fmt.Errorf("%w: image %q host %q", ErrModelMismatch, img.DeviceModel, h.cfg.DeviceModel)
	}
	if img.DeviceModel != m.DeviceModel {
		return fmt.Errorf("%w: image %q manifest %q", ErrModelMismatch, img.DeviceModel, m.DeviceModel)
	}
	if img.Generation != state.Generation {
		return fmt.Errorf("%w: image gen %d accepted gen %d",
			ErrReplayedManifest, img.Generation, state.Generation)
	}

	votes, err := verifyFirmware(img, m)
	if err != nil {
		return err
	}
	if uint64(votes) < m.Threshold {
		return fmt.Errorf("%w: %d distinct key(s) of threshold %d",
			ErrFirmwareQuorum, votes, m.Threshold)
	}
	return nil
}

// loadTrusted returns the current state after re-checking that the stored
// digest still matches the stored manifest encoding. A mismatch indicates
// storage tampering or corruption.
func (h *Host) loadTrusted() (*State, error) {
	state, err := h.store.Load()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(state.Digest, ManifestDigest(state.SignedManifest.Manifest)) {
		return nil, fmt.Errorf("%w: gen %d", ErrDigestMismatch, state.Generation)
	}
	if state.Generation != state.SignedManifest.Manifest.Generation {
		return nil, fmt.Errorf("%w: stored generation field mismatch", ErrDigestMismatch)
	}
	return state, nil
}

// checkTransition enforces monotonic policy between two consecutive accepted
// manifests:
//   - the revocation set may only grow;
//   - a key ID that was active (present and unrevoked) in the old manifest
//     must keep identical public key material if it is still present;
//   - generations must strictly increase (callers guarantee this).
func checkTransition(old, new *Manifest) error {
	oldRevoked := make(map[string]struct{}, len(old.RevokedKeyIDs))
	for _, id := range old.RevokedKeyIDs {
		oldRevoked[id] = struct{}{}
	}
	newRevoked := make(map[string]struct{}, len(new.RevokedKeyIDs))
	for _, id := range new.RevokedKeyIDs {
		newRevoked[id] = struct{}{}
	}
	for id := range oldRevoked {
		if _, stillRevoked := newRevoked[id]; !stillRevoked {
			return fmt.Errorf("%w: key %q no longer revoked", ErrRevocationRollback, id)
		}
	}

	oldEntries := make(map[string]KeyEntry, len(old.Entries))
	for _, e := range old.Entries {
		oldEntries[e.KeyID] = e
	}
	for _, ne := range new.Entries {
		oe, existed := oldEntries[ne.KeyID]
		if !existed {
			continue
		}
		if _, wasRevoked := oldRevoked[ne.KeyID]; wasRevoked {
			// A revoked key ID may never be reintroduced; its presence in
			// new entries is itself a revocation rollback.
			return fmt.Errorf("%w: revoked key %q reintroduced as entry", ErrRevocationRollback, ne.KeyID)
		}
		if !bytes.Equal(oe.PubKey, ne.PubKey) {
			return fmt.Errorf("%w: key %q", ErrKeyMaterialChanged, ne.KeyID)
		}
	}
	return nil
}

// validate enforces well-formedness independent of host policy or storage.
func validate(m *Manifest) error {
	if m.DeviceModel == "" {
		return fmt.Errorf("%w: empty device model", ErrInvalidManifest)
	}
	if m.Generation == 0 {
		return fmt.Errorf("%w: generation must be positive", ErrInvalidManifest)
	}
	if !m.NotAfter.After(m.NotBefore) {
		return fmt.Errorf("%w: validity window must be half-open with NotAfter > NotBefore", ErrInvalidManifest)
	}
	if m.Threshold == 0 {
		return fmt.Errorf("%w: zero firmware threshold", ErrInvalidManifest)
	}

	seenEntries := make(map[string]struct{}, len(m.Entries))
	for _, e := range m.Entries {
		if e.KeyID == "" {
			return fmt.Errorf("%w: empty key ID", ErrInvalidManifest)
		}
		if _, dup := seenEntries[e.KeyID]; dup {
			return fmt.Errorf("%w: duplicate key entry %q", ErrInvalidManifest, e.KeyID)
		}
		seenEntries[e.KeyID] = struct{}{}
		if len(e.PubKey) == 0 {
			return fmt.Errorf("%w: key %q has empty public key", ErrInvalidManifest, e.KeyID)
		}
		if len(e.Purposes) == 0 {
			return fmt.Errorf("%w: key %q has no purposes", ErrInvalidManifest, e.KeyID)
		}
		if e.GenStop <= e.GenStart {
			return fmt.Errorf("%w: key %q has empty generation window", ErrInvalidManifest, e.KeyID)
		}
	}

	seenRevoked := make(map[string]struct{}, len(m.RevokedKeyIDs))
	for _, id := range m.RevokedKeyIDs {
		if id == "" {
			return fmt.Errorf("%w: empty revoked key ID", ErrInvalidManifest)
		}
		if _, dup := seenRevoked[id]; dup {
			return fmt.Errorf("%w: duplicate revoked key ID %q", ErrInvalidManifest, id)
		}
		seenRevoked[id] = struct{}{}
	}
	return nil
}
