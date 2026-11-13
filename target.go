package netsec

import (
	"crypto/ed25519"
	"fmt"
	"sync"
)

// TargetState is the target quarantine lifecycle.
type TargetState string

const (
	TargetEmpty       TargetState = "empty"
	TargetQuarantined TargetState = "quarantined"
	TargetLive        TargetState = "live"
	TargetAborted     TargetState = "aborted"
)

// TargetOption configures a migration target.
type TargetOption func(*Target)

// WithHostKey provisions the only host public key the target trusts.
func WithHostKey(pub ed25519.PublicKey) TargetOption {
	return func(t *Target) {
		t.hostKey = append(ed25519.PublicKey(nil), pub...)
	}
}

// WithExpectedSource binds the target to a specific source incarnation.
func WithExpectedSource(id InstanceID) TargetOption {
	return func(t *Target) {
		t.expectedSource = id
	}
}

// WithTargetInjector installs the target event/fault dispatcher.
func WithTargetInjector(d *EventDispatcher) TargetOption {
	return func(t *Target) {
		t.events = d
	}
}

type stagedPage struct {
	record PageRecord
}

// Target holds an invisible quarantine of verified pages.  No page bytes are
// readable until Start validates the signed receipt and atomically exposes
// the complete image.
type Target struct {
	mu sync.Mutex

	vm             VMID
	instance       InstanceID
	expectedSource InstanceID
	hostKey        ed25519.PublicKey

	quarantine map[PageID]stagedPage
	started    bool
	aborted    bool
	visible    bool

	manifestRoot  Digest
	receiptDigest Digest
	confirmation  *StartupConfirmation

	events *EventDispatcher
}

// NewTarget constructs an isolated target instance.  An empty instance ID is
// replaced with "target:<vm>".
func NewTarget(vm VMID, instance InstanceID, opts ...TargetOption) *Target {
	if instance == "" {
		instance = InstanceID("target:" + string(vm))
	}
	t := &Target{
		vm:         vm,
		instance:   instance,
		quarantine: make(map[PageID]stagedPage),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	if t.events == nil {
		t.events = NewEventDispatcher(nil)
	}
	return t
}

// VM returns the target VM identity.
func (t *Target) VM() VMID { return t.vm }

// InstanceID returns the target instance identity.
func (t *Target) InstanceID() InstanceID { return t.instance }

// TrustHost provisions the host public key used for receipt verification.
func (t *Target) TrustHost(pub ed25519.PublicKey) {
	t.mu.Lock()
	t.hostKey = append(ed25519.PublicKey(nil), pub...)
	t.mu.Unlock()
}

// TrustedHostKey returns a copy of the provisioned host public key.
func (t *Target) TrustedHostKey() ed25519.PublicKey {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append(ed25519.PublicKey(nil), t.hostKey...)
}

// ExpectedSource returns the provisioned source instance, if any.
func (t *Target) ExpectedSource() InstanceID {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expectedSource
}

// EventDispatcher returns the target's dispatcher.
func (t *Target) EventDispatcher() *EventDispatcher {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.events
}

// State returns the quarantine lifecycle state.
func (t *Target) State() TargetState {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.aborted:
		return TargetAborted
	case t.visible:
		return TargetLive
	case len(t.quarantine) > 0:
		return TargetQuarantined
	default:
		return TargetEmpty
	}
}

// Started reports whether the target is live.
func (t *Target) Started() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

// Visible reports whether target memory can be read by the guest.
func (t *Target) Visible() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.visible
}

// QuarantineCount returns the number of staged pages.  It deliberately exposes
// only metadata, never quarantined bytes.
func (t *Target) QuarantineCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.quarantine)
}

// StagedGeneration returns the generation of a staged page.  It is metadata
// used by the host to audit progress; the bytes remain invisible.
func (t *Target) StagedGeneration(page PageID) (Generation, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.quarantine[page]
	if !ok {
		return 0, false
	}
	return p.record.Generation, true
}

// StagePage verifies and quarantines one page record.  Records are monotonic:
// an older generation never overwrites a newer quarantined generation, and
// equal generations must have identical content.
func (t *Target) StagePage(rec PageRecord) error {
	if int(rec.Page) >= MaxPages {
		return fmt.Errorf("%w: %d", ErrPageOutOfRange, rec.Page)
	}
	if err := t.event(EventPageStage, EventPhaseBefore, EventFields{
		Page: rec.Page, Generation: rec.Generation, Digest: rec.Digest,
	}); err != nil {
		return err
	}
	if rec.VM == "" {
		rec.VM = t.vm
	}
	if rec.VM != t.vm {
		return fmt.Errorf("%w: record VM %q, target VM %q", ErrVMNotOwner, rec.VM, t.vm)
	}
	if rec.Generation == 0 {
		return fmt.Errorf("%w: zero generation", ErrInvalidManifest)
	}
	if err := rec.VerifyDigest(); err != nil {
		return err
	}

	t.mu.Lock()
	if t.aborted {
		t.mu.Unlock()
		return ErrTargetAborted
	}
	if t.started {
		t.mu.Unlock()
		return ErrAlreadyStarted
	}
	if old, ok := t.quarantine[rec.Page]; ok {
		switch {
		case rec.Generation < old.record.Generation:
			t.mu.Unlock()
			return fmt.Errorf("%w: page %d got generation %d, already has %d",
				ErrStalePage, rec.Page, rec.Generation, old.record.Generation)
		case rec.Generation == old.record.Generation && rec.Digest != old.record.Digest:
			t.mu.Unlock()
			return fmt.Errorf("%w: page %d generation %d has two digests",
				ErrConflictingPage, rec.Page, rec.Generation)
		case rec.Generation == old.record.Generation:
			// Idempotent duplicate round/copy.  Keep the existing bytes.
			t.mu.Unlock()
			if err := t.event(EventPageStage, EventPhaseAfter, EventFields{
				Page: rec.Page, Generation: rec.Generation, Digest: rec.Digest, Copied: 1,
			}); err != nil {
				return err
			}
			return nil
		}
	}
	t.quarantine[rec.Page] = stagedPage{record: rec.Clone()}
	t.mu.Unlock()

	if err := t.event(EventPageStage, EventPhaseAfter, EventFields{
		Page: rec.Page, Generation: rec.Generation, Digest: rec.Digest, Copied: 1,
	}); err != nil {
		return err
	}
	return nil
}

// ReadbackDigest recomputes the digest from the quarantined bytes and returns
// it to the host.  The target never trusts the digest supplied on the wire.
func (t *Target) ReadbackDigest(page PageID) (Digest, error) {
	if int(page) >= MaxPages {
		return Digest{}, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	if err := t.event(EventPageReadback, EventPhaseBefore, EventFields{Page: page}); err != nil {
		return Digest{}, err
	}
	t.mu.Lock()
	if t.aborted {
		t.mu.Unlock()
		return Digest{}, ErrTargetAborted
	}
	p, ok := t.quarantine[page]
	if !ok {
		t.mu.Unlock()
		return Digest{}, ErrNotStaged
	}
	got := DigestBytes(p.record.Data)
	want := p.record.Digest
	t.mu.Unlock()
	if err := t.event(EventPageReadback, EventPhaseAfter, EventFields{
		Page: page, Generation: p.record.Generation, Digest: got,
	}); err != nil {
		return got, err
	}
	if got != want {
		return got, &DigestMismatchError{Context: fmt.Sprintf("target readback page %d", page), Got: got, Want: want}
	}
	return got, nil
}

// Start verifies a host-signed receipt and the complete all-page manifest,
// then atomically makes the quarantine visible.  Any missing or older target
// page causes a StaleStartupError; no partial start is possible.
func (t *Target) Start(receipt Receipt, manifest Manifest) (*StartupConfirmation, error) {
	if err := t.event(EventReceiptVerify, EventPhaseBefore, EventFields{
		Epoch: receipt.FinalEpoch, ManifestRoot: receipt.ManifestRoot, ReceiptDigest: receipt.Digest(),
	}); err != nil {
		return nil, err
	}

	t.mu.Lock()
	hostKey := append(ed25519.PublicKey(nil), t.hostKey...)
	alreadyStarted := t.started
	existing := t.confirmation
	t.mu.Unlock()

	if len(hostKey) == 0 {
		return nil, ErrUntrustedHost
	}
	if err := VerifyReceipt(hostKey, receipt, manifest); err != nil {
		return nil, err
	}
	if receipt.VM != t.vm {
		return nil, fmt.Errorf("%w: receipt VM %q, target VM %q", ErrInvalidReceipt, receipt.VM, t.vm)
	}
	if receipt.TargetInstance != t.instance {
		return nil, fmt.Errorf("%w: receipt target %q, target instance %q", ErrInvalidReceipt, receipt.TargetInstance, t.instance)
	}
	t.mu.Lock()
	expectedSource := t.expectedSource
	t.mu.Unlock()
	if expectedSource != "" && receipt.SourceInstance != expectedSource {
		return nil, fmt.Errorf("%w: receipt source %q, expected %q", ErrInvalidReceipt, receipt.SourceInstance, expectedSource)
	}
	if err := t.event(EventReceiptVerify, EventPhaseAfter, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: manifest.Root(), ReceiptDigest: receipt.Digest(),
	}); err != nil {
		return nil, err
	}
	if err := t.event(EventTargetStartCheck, EventPhaseBefore, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: manifest.Root(),
	}); err != nil {
		return nil, err
	}
	if err := t.event(EventTargetStart, EventPhaseBefore, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: manifest.Root(),
	}); err != nil {
		return nil, err
	}

	if alreadyStarted {
		if existing != nil && existing.ManifestRoot == manifest.Root() &&
			existing.ReceiptDigest == receipt.Digest() {
			out := existing.Clone()
			return &out, nil
		}
		return nil, ErrAlreadyStarted
	}

	t.mu.Lock()
	if t.aborted {
		t.mu.Unlock()
		return nil, ErrTargetAborted
	}
	if t.started {
		started := t.confirmation != nil && t.confirmation.ManifestRoot == manifest.Root() &&
			t.confirmation.ReceiptDigest == receipt.Digest()
		out := StartupConfirmation{}
		if t.confirmation != nil {
			out = t.confirmation.Clone()
		}
		t.mu.Unlock()
		if started {
			return &out, nil
		}
		return nil, ErrAlreadyStarted
	}

	root := manifest.Root()
	mismatches := t.collectMismatchesLocked(manifest)
	if len(mismatches) > 0 {
		t.mu.Unlock()
		err := &StaleStartupError{
			VM:             t.vm,
			TargetInstance: t.instance,
			FinalEpoch:     manifest.Epoch,
			ManifestRoot:   root,
			Mismatches:     mismatches,
		}
		witness := err.ShortestWitness()
		if emitErr := t.event(EventTargetStartCheck, EventPhaseAfter, EventFields{
			Epoch: manifest.Epoch, ManifestRoot: root,
			Page: witness.Page, TargetGeneration: witness.TargetGeneration,
			ExpectedGeneration: witness.ExpectedGeneration, Reason: err.Error(),
		}); emitErr != nil {
			return nil, emitErr
		}
		return nil, err
	}

	// Complete validation and mutation under one lock so staging cannot race
	// the transition to visible.
	t.started = true
	t.visible = true
	t.manifestRoot = root
	t.receiptDigest = receipt.Digest()
	conf := &StartupConfirmation{
		VM:             t.vm,
		SourceInstance: receipt.SourceInstance,
		TargetInstance: t.instance,
		FinalEpoch:     manifest.Epoch,
		Pages:          manifest.PageGenerations(),
		DeviceDigest:   manifest.DeviceDigest,
		ManifestRoot:   root,
		ReceiptDigest:  t.receiptDigest,
	}
	t.confirmation = conf
	out := conf.Clone()
	t.mu.Unlock()

	if err := t.event(EventTargetStart, EventPhaseAfter, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: root, ReceiptDigest: receipt.Digest(),
	}); err != nil {
		return &out, err
	}
	if err := t.event(EventTargetVisible, EventPhaseAfter, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: root,
	}); err != nil {
		return &out, err
	}
	return &out, nil
}

// StartHandoff is a convenience wrapper around Start.
func (t *Target) StartHandoff(h Handoff) (*StartupConfirmation, error) {
	return t.Start(h.Receipt, h.Manifest)
}

func (t *Target) collectMismatchesLocked(manifest Manifest) []PageMismatch {
	var out []PageMismatch
	for i := 0; i < MaxPages; i++ {
		want := manifest.Pages[i]
		staged, ok := t.quarantine[PageID(i)]
		switch {
		case want.Generation == 0:
			if ok && staged.record.Generation > 0 {
				out = append(out, PageMismatch{
					Page: PageID(i), TargetGeneration: staged.record.Generation,
					ExpectedGeneration: 0, TargetDigest: staged.record.Digest, ExpectedDigest: Digest{},
				})
			}
		case !ok:
			out = append(out, PageMismatch{
				Page: PageID(i), TargetGeneration: 0, ExpectedGeneration: want.Generation,
				ExpectedDigest: want.Digest, Missing: true,
			})
		case staged.record.Generation != want.Generation || staged.record.Digest != want.Digest:
			out = append(out, PageMismatch{
				Page: PageID(i), TargetGeneration: staged.record.Generation,
				ExpectedGeneration: want.Generation, TargetDigest: staged.record.Digest,
				ExpectedDigest: want.Digest,
			})
		}
	}
	return out
}

// CheckManifest compares the current quarantine with a complete manifest
// without starting the target.  The host uses it before signing a receipt so
// it never authorizes an image that is already known to be stale.
func (t *Target) CheckManifest(manifest Manifest) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if manifest.VM != t.vm {
		return fmt.Errorf("%w: manifest VM %q, target VM %q", ErrVMNotOwner, manifest.VM, t.vm)
	}
	if manifest.TargetInstance != t.instance {
		return fmt.Errorf("%w: manifest target %q, target instance %q", ErrInvalidManifest, manifest.TargetInstance, t.instance)
	}
	t.mu.Lock()
	if t.aborted {
		t.mu.Unlock()
		return ErrTargetAborted
	}
	if t.expectedSource != "" && manifest.SourceInstance != t.expectedSource {
		t.mu.Unlock()
		return fmt.Errorf("%w: manifest source %q, expected %q", ErrInvalidManifest, manifest.SourceInstance, t.expectedSource)
	}
	mismatches := t.collectMismatchesLocked(manifest)
	t.mu.Unlock()
	if len(mismatches) == 0 {
		return nil
	}
	return &StaleStartupError{
		VM:             t.vm,
		TargetInstance: t.instance,
		FinalEpoch:     manifest.Epoch,
		ManifestRoot:   manifest.Root(),
		Mismatches:     mismatches,
	}
}

// ReadPage reads live, visible target memory.  Before startup or after abort
// the quarantine is inaccessible.
func (t *Target) ReadPage(page PageID) ([]byte, error) {
	if int(page) >= MaxPages {
		return nil, fmt.Errorf("%w: %d", ErrPageOutOfRange, page)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.aborted {
		return nil, ErrTargetAborted
	}
	if !t.started || !t.visible {
		return nil, ErrQuarantineHidden
	}
	p, ok := t.quarantine[page]
	if !ok {
		return nil, ErrPageNotPresent
	}
	return append([]byte(nil), p.record.Data...), nil
}

// Confirmation returns the target's startup confirmation if it is live.
func (t *Target) Confirmation() (*StartupConfirmation, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.aborted {
		return nil, ErrTargetAborted
	}
	if !t.started || t.confirmation == nil {
		return nil, ErrNotStarted
	}
	out := t.confirmation.Clone()
	return &out, nil
}

// Invalidate discards the quarantine after a failed hand-off.  It is
// impossible once the target is live; an already-started target must be
// reconciled, never rolled back into a second running copy of the source.
func (t *Target) Invalidate(reason string) error {
	if err := t.event(EventAbort, EventPhaseBefore, EventFields{Reason: reason}); err != nil {
		return err
	}
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return ErrAlreadyStarted
	}
	if t.aborted {
		t.mu.Unlock()
		return nil
	}
	for i := 0; i < MaxPages; i++ {
		delete(t.quarantine, PageID(i))
	}
	t.aborted = true
	t.visible = false
	confirmation := t.confirmation != nil
	t.confirmation = nil
	t.manifestRoot = Digest{}
	t.receiptDigest = Digest{}
	t.mu.Unlock()

	if confirmation {
		return fmt.Errorf("%w: target confirmation unexpectedly existed", ErrInvalidState)
	}
	if err := t.event(EventQuarantineInvalidated, EventPhaseAfter, EventFields{Reason: reason}); err != nil {
		return err
	}
	return nil
}

func (t *Target) event(name EventName, phase EventPhase, fields EventFields) error {
	t.mu.Lock()
	d := t.events
	t.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.Emit(name, phase, fields)
}
