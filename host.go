package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
)

// RollbackFuse is the trusted, monotonic rollback-index storage. Raise must
// only increase the stored value and must be durable before returning. The
// implementation may be a one-time-programmable fuse bank or a secure
// monotonic counter; it must not be backed by the ordinary StateStore.
type RollbackFuse interface {
	ReadRollbackIndex() (uint32, error)
	RaiseRollbackIndex(to uint32) error
}

// StateStore persists the ordinary State blob. Load returns ErrNoState when
// no state has been written. Save must atomically replace the previous blob.
// A process crash at any call boundary may leave either the old or the new
// blob, and an attacker may restore an older blob.
type StateStore interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// SlotImages writes and reads the slot payloads. ReadImage is used for the
// post-write digest verification and again before every attempted boot.
type SlotImages interface {
	WriteImage(slot SlotID, image []byte) error
	ReadImage(slot SlotID) ([]byte, error)
}

// Config fixes security policy for a host.
type Config struct {
	DeviceModel string

	// CurrentBootVersion is the bootloader version that will execute the
	// image. A manifest's MinBootVersion must not exceed it.
	CurrentBootVersion uint32

	// MaxAttempts is the number of times a candidate may be started without
	// confirmation before the candidate is abandoned. Zero selects 3.
	MaxAttempts uint32

	// AllowEqualIndexDifferentDigest permits two different images with the
	// same rollback index. The default is false.
	AllowEqualIndexDifferentDigest bool

	// InitialActive is used only when no ordinary state exists yet. It
	// defaults to SlotA.
	InitialActive SlotID

	// NonceReader supplies boot nonces. It defaults to crypto/rand.Reader.
	NonceReader io.Reader

	// VerifyManifest is mandatory. It authenticates the signed manifest.
	VerifyManifest ManifestVerifier
}

// InstallRequest is an authenticated image destined for the inactive slot.
type InstallRequest struct {
	Image     []byte
	Manifest  Manifest
	Signature []byte
}

// BootSession describes the image selected for this boot and the one-use nonce
// that the running image must return in BootConfirmation.
type BootSession struct {
	Slot              SlotID
	Manifest          Manifest
	ManifestSignature []byte
	ImageDigest       string
	Image             []byte
	Nonce             string
	Candidate         bool
}

// BootConfirmation is reported by the running image. Nonce, slot, and digest
// must all match the same BootSession.
type BootConfirmation struct {
	Nonce       string
	Slot        SlotID
	ImageDigest string
}

// RecoveryResult is the trusted view produced by Recover. It never lower the
// fuse. HasTarget is false exactly when RecoveryMode is true.
type RecoveryResult struct {
	State        State
	FuseIndex    uint32
	Target       SlotID
	HasTarget    bool
	RecoveryMode bool
}

type pendingBoot struct {
	session  BootSession
	consumed bool
}

// Host serializes all operations that can change the state machine.
type Host struct {
	cfg    Config
	fuse   RollbackFuse
	store  StateStore
	images SlotImages

	mu      sync.Mutex
	pending *pendingBoot
}

// New validates the host dependencies and returns a state machine. No
// persistent or fuse writes are performed during construction.
func New(cfg Config, fuse RollbackFuse, store StateStore, images SlotImages) (*Host, error) {
	if fuse == nil || store == nil || images == nil {
		return nil, fmt.Errorf("%w: fuse, state store, and image store are required", ErrInvalidConfig)
	}
	if cfg.VerifyManifest == nil {
		return nil, fmt.Errorf("%w: manifest verifier is required", ErrInvalidConfig)
	}
	if cfg.DeviceModel == "" {
		return nil, fmt.Errorf("%w: device model is required", ErrInvalidConfig)
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 3
	}
	if !cfg.InitialActive.Valid() {
		cfg.InitialActive = SlotA
	}
	if cfg.NonceReader == nil {
		cfg.NonceReader = rand.Reader
	}
	return &Host{
		cfg:    cfg,
		fuse:   fuse,
		store:  store,
		images: images,
	}, nil
}

// NewHost is an alias for New.
func NewHost(cfg Config, fuse RollbackFuse, store StateStore, images SlotImages) (*Host, error) {
	return New(cfg, fuse, store, images)
}

func (h *Host) clearPending() {
	if h.pending != nil {
		h.pending.consumed = true
		h.pending = nil
	}
}

func (h *Host) loadState() (State, error) {
	blob, err := h.store.Load()
	if err != nil {
		if errors.Is(err, ErrNoState) || errors.Is(err, io.EOF) || errors.Is(err, fs.ErrNotExist) {
			return emptyState(h.cfg.InitialActive), nil
		}
		return State{}, fmt.Errorf("%w: %w", ErrStateRead, err)
	}
	if len(blob) == 0 {
		return emptyState(h.cfg.InitialActive), nil
	}
	var state State
	if err := json.Unmarshal(blob, &state); err != nil {
		// A partially written or attacker-truncated snapshot must not be
		// mistaken for a valid success record.
		return State{}, fmt.Errorf("%w: %w", ErrInvalidState, err)
	}
	if state.SchemaVersion != stateSchemaVersion {
		return State{}, fmt.Errorf("%w: schema version %d", ErrInvalidState, state.SchemaVersion)
	}
	if !state.Active.Valid() {
		return State{}, fmt.Errorf("%w: invalid active slot", ErrInvalidState)
	}
	return state, nil
}

func (h *Host) saveState(state State) error {
	state.SchemaVersion = stateSchemaVersion
	blob, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStateWrite, err)
	}
	if err := h.store.Save(blob); err != nil {
		return fmt.Errorf("%w: %w", ErrStateWrite, err)
	}
	return nil
}

func (h *Host) readFuse() (uint32, error) {
	index, err := h.fuse.ReadRollbackIndex()
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrFuseRead, err)
	}
	return index, nil
}

// ensureFuseAt makes the trusted fuse at least target. It then reads the fuse
// back and refuses to continue unless the boot target still meets the fuse.
func (h *Host) ensureFuseAt(target uint32) (uint32, error) {
	current, err := h.readFuse()
	if err != nil {
		return 0, err
	}
	if target < current {
		return 0, fmt.Errorf("%w: image index %d is below fuse %d", ErrRollback, target, current)
	}
	if target > current {
		if err := h.fuse.RaiseRollbackIndex(target); err != nil {
			return 0, fmt.Errorf("%w: %w", ErrFuseRaise, err)
		}
		current, err = h.readFuse()
		if err != nil {
			return 0, err
		}
	}
	if current < target {
		return 0, fmt.Errorf("%w: fuse read back %d, need %d", ErrFuseVerify, current, target)
	}
	if current > target {
		return 0, fmt.Errorf("%w: image index %d is below fuse %d", ErrRollback, target, current)
	}
	return current, nil
}

func (h *Host) verifyManifest(manifest Manifest, signature, image []byte) (Manifest, error) {
	if manifest.DeviceModel != h.cfg.DeviceModel {
		return Manifest{}, fmt.Errorf("%w: got %q, want %q", ErrDeviceMismatch, manifest.DeviceModel, h.cfg.DeviceModel)
	}
	want, err := NormalizeDigest(manifest.ImageDigest)
	if err != nil {
		return Manifest{}, err
	}
	got := DigestImage(image)
	if !digestsEqual(want, got) {
		return Manifest{}, fmt.Errorf("%w: manifest %s, image %s", ErrImageDigestMismatch, want, got)
	}
	if manifest.MinBootVersion > h.cfg.CurrentBootVersion {
		return Manifest{}, fmt.Errorf("%w: need %d, host has %d",
			ErrMinBootVersion, manifest.MinBootVersion, h.cfg.CurrentBootVersion)
	}
	if len(signature) == 0 {
		return Manifest{}, fmt.Errorf("%w: empty signature", ErrManifestRejected)
	}
	if err := h.cfg.VerifyManifest(manifest, signature, image); err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrManifestRejected, err)
	}
	manifest.ImageDigest = want
	return manifest, nil
}

// Install validates an image, writes only the inactive slot, verifies the
// bytes by reading them back, and then atomically marks the slot as a
// candidate. A failure after the image write leaves no trusted candidate; a
// later install may overwrite the inactive slot again.
func (h *Host) Install(req InstallRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearPending()

	state, err := h.loadState()
	if err != nil {
		return err
	}
	fuseIndex, err := h.readFuse()
	if err != nil {
		return err
	}

	manifest, err := h.verifyManifest(req.Manifest, req.Signature, req.Image)
	if err != nil {
		return err
	}
	if !manifest.RollbackIndexValid(fuseIndex) {
		return fmt.Errorf("%w: manifest index %d is below fuse %d", ErrRollback, manifest.RollbackIndex, fuseIndex)
	}

	// Ordinary state may be stale, but it is the only record of which slot
	// should be treated as active while installing.
	if !state.Active.Valid() {
		return fmt.Errorf("%w: invalid active slot", ErrInvalidState)
	}
	target := state.Active.Other()
	for slot, ss := range state.Slots {
		if !slotHasManifest(ss) || ss.VersionIndex != manifest.RollbackIndex {
			continue
		}
		if !digestsEqual(ss.ImageDigest, manifest.ImageDigest) &&
			!h.cfg.AllowEqualIndexDifferentDigest {
			return fmt.Errorf("%w: index %d is already bound to a different image in slot %s",
				ErrSameIndexDigest, manifest.RollbackIndex, SlotID(slot))
		}
	}

	if err := h.images.WriteImage(target, req.Image); err != nil {
		return fmt.Errorf("%w: slot %s: %w", ErrImageWrite, target, err)
	}
	written, err := h.images.ReadImage(target)
	if err != nil {
		return fmt.Errorf("%w: slot %s: %w", ErrImageReadback, target, err)
	}
	if !digestsEqual(DigestImage(written), manifest.ImageDigest) {
		return fmt.Errorf("%w: slot %s read back %s, manifest %s",
			ErrImageDigestMismatch, target, DigestImage(written), manifest.ImageDigest)
	}

	state.Slots[target] = SlotState{
		ImageDigest:       manifest.ImageDigest,
		Manifest:          manifest,
		ManifestSignature: append([]byte(nil), req.Signature...),
		VersionIndex:      manifest.RollbackIndex,
		Candidate:         true,
		Success:           false,
		Attempts:          0,
	}
	if err := h.saveState(state); err != nil {
		return err
	}
	return nil
}

// InstallImage is a convenience wrapper for Install.
func (h *Host) InstallImage(image []byte, manifest Manifest, signature []byte) error {
	return h.Install(InstallRequest{Image: image, Manifest: manifest, Signature: signature})
}

// RollbackIndexValid reports whether index is at or above the trusted fuse.
func (m Manifest) RollbackIndexValid(fuseIndex uint32) bool {
	return m.RollbackIndex >= fuseIndex
}

func slotHasManifest(ss SlotState) bool {
	return ss.ImageDigest != "" || ss.Manifest.ImageDigest != ""
}

func (h *Host) slotAuthenticatedAt(slot SlotID, ss SlotState) ([]byte, Manifest, bool) {
	if !slotHasManifest(ss) {
		return nil, Manifest{}, false
	}
	if ss.Manifest.ImageDigest == "" || len(ss.ManifestSignature) == 0 {
		return nil, Manifest{}, false
	}
	if ss.VersionIndex != ss.Manifest.RollbackIndex {
		return nil, Manifest{}, false
	}
	if !digestsEqual(ss.ImageDigest, ss.Manifest.ImageDigest) {
		return nil, Manifest{}, false
	}
	image, err := h.images.ReadImage(slot)
	if err != nil {
		return nil, Manifest{}, false
	}
	if !digestsEqual(DigestImage(image), ss.Manifest.ImageDigest) {
		return nil, Manifest{}, false
	}
	manifest, err := h.verifyManifest(ss.Manifest, ss.ManifestSignature, image)
	if err != nil {
		return nil, Manifest{}, false
	}
	return image, manifest, true
}

// cleanState applies rollback-safe normalization:
//   - candidates below the trusted fuse are deleted;
//   - candidates whose signed evidence or slot bytes no longer authenticate
//     are deleted;
//   - records with an inconsistent version index are not trusted.
//
// Confirmed slots are retained even below the fuse for diagnostics, but they
// will never be selected for boot.
func (h *Host) cleanState(state State, fuseIndex uint32) (State, bool) {
	changed := false
	for slot := SlotID(0); slot < SlotCount; slot++ {
		ss := state.Slots[slot]
		if ss.Candidate {
			if ss.VersionIndex < fuseIndex || ss.VersionIndex != ss.Manifest.RollbackIndex {
				state.Slots[slot] = SlotState{}
				changed = true
				continue
			}
			if _, _, ok := h.slotAuthenticatedAt(slot, ss); !ok {
				state.Slots[slot] = SlotState{}
				changed = true
				continue
			}
		}
	}
	return state, changed
}

// Recover implements the ordinary-state recovery algorithm. It reads the fuse
// first, deletes low-index candidates, and exposes the highest confirmed slot
// whose index is at or above the fuse. It never lowers the fuse and never
// switches Active; a successful boot still has to report its nonce before
// Active changes.
func (h *Host) Recover() (RecoveryResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearPending()

	state, err := h.loadState()
	if err != nil {
		return RecoveryResult{}, err
	}
	fuseIndex, err := h.readFuse()
	if err != nil {
		return RecoveryResult{}, err
	}
	clean, changed := h.cleanState(state, fuseIndex)
	if changed {
		if err := h.saveState(clean); err != nil {
			return RecoveryResult{}, err
		}
	}

	result := RecoveryResult{
		State:     clean,
		FuseIndex: fuseIndex,
	}
	type recoveryTarget struct {
		slot   SlotID
		index  uint32
		digest string
	}
	var best []recoveryTarget
	ambiguous := false
	for slot := SlotID(0); slot < SlotCount; slot++ {
		ss := clean.Slots[slot]
		if !ss.Success || ss.Candidate || ss.VersionIndex < fuseIndex {
			continue
		}
		if ss.VersionIndex != ss.Manifest.RollbackIndex {
			continue
		}
		if _, _, ok := h.slotAuthenticatedAt(slot, ss); !ok {
			continue
		}
		record := recoveryTarget{slot: slot, index: ss.VersionIndex, digest: ss.ImageDigest}
		if len(best) == 0 || record.index > best[0].index {
			best = []recoveryTarget{record}
			ambiguous = false
			continue
		}
		if record.index == best[0].index &&
			!h.cfg.AllowEqualIndexDifferentDigest &&
			!digestsEqual(best[0].digest, record.digest) {
			ambiguous = true
			continue
		}
		best = append(best, record)
	}
	if len(best) > 0 && !ambiguous {
		result.Target = best[0].slot
		result.HasTarget = true
		// Prefer the already active slot only when it is one of the
		// authenticated candidates for the winning index.
		for _, record := range best {
			if record.slot == clean.Active {
				result.Target = clean.Active
				break
			}
		}
	} else {
		result.RecoveryMode = true
	}
	return result, nil
}

// BeginBoot selects and authenticates an image, consumes one candidate
// attempt, raises and re-reads the fuse before returning, and creates the
// one-use boot nonce. No image is selected if a candidate cannot get its fuse
// index committed.
func (h *Host) BeginBoot() (*BootSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clearPending()

	state, err := h.loadState()
	if err != nil {
		return nil, err
	}
	fuseIndex, err := h.readFuse()
	if err != nil {
		return nil, err
	}
	clean, changed := h.cleanState(state, fuseIndex)
	if changed {
		if err := h.saveState(clean); err != nil {
			return nil, err
		}
	}
	state = clean
	changed = false

	// A candidate that exhausted its attempts is abandoned before selecting
	// another slot. Removing it is a durable state transition.
	for slot := SlotID(0); slot < SlotCount; slot++ {
		ss := state.Slots[slot]
		if ss.Candidate && ss.Attempts >= h.cfg.MaxAttempts {
			state.Slots[slot] = SlotState{}
			changed = true
		}
	}
	if changed {
		if err := h.saveState(state); err != nil {
			return nil, err
		}
	}

	type target struct {
		slot      SlotID
		ss        SlotState
		image     []byte
		candidate bool
	}
	var candidateTarget *target
	var successTarget *target

	for slot := SlotID(0); slot < SlotCount; slot++ {
		ss := state.Slots[slot]
		if ss.VersionIndex < fuseIndex {
			continue
		}
		if ss.Candidate {
			if ss.Attempts >= h.cfg.MaxAttempts {
				continue
			}
			image, manifest, ok := h.slotAuthenticatedAt(slot, ss)
			if !ok {
				continue
			}
			ss.Manifest = manifest
			ss.ImageDigest = manifest.ImageDigest
			if candidateTarget == nil ||
				ss.VersionIndex > candidateTarget.ss.VersionIndex ||
				(ss.VersionIndex == candidateTarget.ss.VersionIndex && slot == state.Active) {
				candidateTarget = &target{slot: slot, ss: ss, image: image, candidate: true}
			}
			continue
		}
		if !ss.Success {
			continue
		}
		image, manifest, ok := h.slotAuthenticatedAt(slot, ss)
		if !ok {
			continue
		}
		ss.Manifest = manifest
		ss.ImageDigest = manifest.ImageDigest
		if successTarget == nil ||
			ss.VersionIndex > successTarget.ss.VersionIndex ||
			(ss.VersionIndex == successTarget.ss.VersionIndex && slot == state.Active) {
			successTarget = &target{slot: slot, ss: ss, image: image}
		}
	}

	var chosen *target
	switch {
	case candidateTarget != nil && successTarget == nil:
		chosen = candidateTarget
	case candidateTarget == nil && successTarget != nil:
		chosen = successTarget
	case candidateTarget != nil && successTarget != nil:
		if candidateTarget.ss.VersionIndex >= successTarget.ss.VersionIndex {
			chosen = candidateTarget
		} else {
			chosen = successTarget
		}
	default:
		return nil, ErrRecoveryMode
	}

	if !h.cfg.AllowEqualIndexDifferentDigest {
		for slot := SlotID(0); slot < SlotCount; slot++ {
			ss := state.Slots[slot]
			if !slotHasManifest(ss) || ss.VersionIndex != chosen.ss.VersionIndex || ss.VersionIndex < fuseIndex {
				continue
			}
			if ss.Candidate && ss.Attempts >= h.cfg.MaxAttempts {
				continue
			}
			if !ss.Candidate && !ss.Success {
				continue
			}
			if _, _, ok := h.slotAuthenticatedAt(slot, ss); !ok {
				continue
			}
			if !digestsEqual(ss.ImageDigest, chosen.ss.ImageDigest) {
				return nil, fmt.Errorf("%w: index %d has conflicting image digests",
					ErrSameIndexDigest, chosen.ss.VersionIndex)
			}
		}
	}

	// The attempt is committed before the image may execute. A crash here
	// costs an attempt but cannot authorize a lower image.
	if chosen.candidate {
		state.Slots[chosen.slot].Attempts++
		if err := h.saveState(state); err != nil {
			return nil, err
		}
		chosen.ss.Attempts = state.Slots[chosen.slot].Attempts
	}

	if _, err := h.ensureFuseAt(chosen.ss.VersionIndex); err != nil {
		return nil, err
	}

	nonceBytes := make([]byte, 32)
	if _, err := io.ReadFull(h.cfg.NonceReader, nonceBytes); err != nil {
		return nil, fmt.Errorf("netsec: boot nonce generation failed: %w", err)
	}
	nonce := hex.EncodeToString(nonceBytes)
	session := BootSession{
		Slot:              chosen.slot,
		Manifest:          chosen.ss.Manifest,
		ManifestSignature: append([]byte(nil), chosen.ss.ManifestSignature...),
		ImageDigest:       chosen.ss.ImageDigest,
		Image:             append([]byte(nil), chosen.image...),
		Nonce:             nonce,
		Candidate:         chosen.candidate,
	}
	h.pending = &pendingBoot{session: session}
	return &session, nil
}

// ConfirmBoot accepts the running image's report for exactly the most recent
// boot session. The nonce is one-use. On success, the candidate's success flag
// and the active slot are committed together in one ordinary-state write.
func (h *Host) ConfirmBoot(conf BootConfirmation) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.pending == nil {
		return ErrNoBootSession
	}
	expected := h.pending.session
	if subtle.ConstantTimeCompare([]byte(expected.Nonce), []byte(conf.Nonce)) != 1 {
		return ErrInvalidNonce
	}
	if conf.Slot != expected.Slot {
		return fmt.Errorf("%w: slot %s, expected %s", ErrConfirmation, conf.Slot, expected.Slot)
	}
	if !digestsEqual(conf.ImageDigest, expected.ImageDigest) {
		return fmt.Errorf("%w: image %s, expected %s", ErrConfirmation, conf.ImageDigest, expected.ImageDigest)
	}
	// Consume the nonce before any write: even a failed state write must not
	// leave the same boot session replayable as a successful confirmation.
	h.pending.consumed = true
	h.pending = nil

	state, err := h.loadState()
	if err != nil {
		return err
	}
	fuseIndex, err := h.readFuse()
	if err != nil {
		return err
	}
	if !expected.Manifest.RollbackIndexValid(fuseIndex) {
		return fmt.Errorf("%w: confirmed image index %d is below fuse %d",
			ErrRollback, expected.Manifest.RollbackIndex, fuseIndex)
	}
	// The boot path already raised the fuse; checking it again makes a
	// rollback of ordinary state unable to confirm a below-fuse image.
	verified, err := h.verifyManifest(expected.Manifest, expected.ManifestSignature, expected.Image)
	if err != nil {
		return err
	}
	expected.Manifest = verified

	ss := SlotState{
		ImageDigest:       expected.Manifest.ImageDigest,
		Manifest:          expected.Manifest,
		ManifestSignature: append([]byte(nil), expected.ManifestSignature...),
		VersionIndex:      expected.Manifest.RollbackIndex,
		Candidate:         false,
		Success:           true,
		Attempts:          0,
	}
	state.Slots[expected.Slot] = ss
	state.Active = expected.Slot
	if err := h.saveState(state); err != nil {
		return err
	}
	return nil
}

// Inspect returns a defensive copy of the ordinary state without applying
// recovery writes. It is intended for diagnostics and tests.
func (h *Host) Inspect() (State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := h.loadState()
	if err != nil {
		return State{}, err
	}
	return state, nil
}
