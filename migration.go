package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// MigrationState is the host-side protocol state.
type MigrationState string

const (
	MigrationIdle          MigrationState = "idle"
	MigrationCopying       MigrationState = "copying"
	MigrationFrozen        MigrationState = "frozen"
	MigrationAwaitingStart MigrationState = "awaiting-target-start"
	MigrationTargetStarted MigrationState = "target-started"
	MigrationTerminated    MigrationState = "terminated"
	MigrationAborted       MigrationState = "aborted"
)

// RoundResult summarizes one pre-copy round.
type RoundResult struct {
	Round          int   `json:"round"`
	Epoch          Epoch `json:"epoch"`
	Copied         int   `json:"copied"`
	Verified       int   `json:"verified"`
	Cleared        int   `json:"cleared"`
	RemainingDirty int   `json:"remaining_dirty"`
}

// PreCopyResult summarizes a bounded pre-copy phase.
type PreCopyResult struct {
	Rounds         int   `json:"rounds"`
	Epoch          Epoch `json:"epoch"`
	RemainingDirty int   `json:"remaining_dirty"`
	Converged      bool  `json:"converged"`
}

// MigrationHost is the preferred descriptive alias.
type MigrationHost = Migrator

type MigratorOption func(*Migrator)

// WithMigrationInjector installs one dispatcher on both the source and target
// as well as on the coordinator, producing a single ordered event stream.
func WithMigrationInjector(d *EventDispatcher) MigratorOption {
	return func(m *Migrator) {
		m.events = d
		m.src.events = d
		m.target.events = d
	}
}

// Migrator drives pre-copy rounds, the frozen cutover, receipt delivery and
// the one-way startup confirmation.
type Migrator struct {
	opMu sync.Mutex

	mu    sync.Mutex
	state MigrationState
	round int

	rounds  int
	freeze  *FreezePoint
	handoff *Handoff
	conf    *StartupConfirmation

	src    *Memory
	target *Target
	signer *Signer
	events *EventDispatcher
}

// NewMigrator wires a source, a target and the host signer.  The target is
// provisioned with the signer's public key and with the source instance
// identity; it will trust no other host key.
func NewMigrator(src *Memory, target *Target, signer *Signer, opts ...MigratorOption) (*Migrator, error) {
	if src == nil || target == nil {
		return nil, fmt.Errorf("%w: nil source or target", ErrInvalidState)
	}
	if signer == nil {
		return nil, fmt.Errorf("%w: nil host signer", ErrBadSignature)
	}
	if src.VM() == "" || target.VM() == "" {
		return nil, fmt.Errorf("%w: empty VM identity", ErrInvalidState)
	}
	if src.VM() != target.VM() {
		return nil, fmt.Errorf("%w: source VM %q, target VM %q", ErrVMNotOwner, src.VM(), target.VM())
	}
	if src.InstanceID() == "" || target.InstanceID() == "" {
		return nil, fmt.Errorf("%w: empty instance identity", ErrInvalidState)
	}
	if src.InstanceID() == target.InstanceID() {
		return nil, fmt.Errorf("%w: source and target instances are identical", ErrInvalidState)
	}

	m := &Migrator{
		src:    src,
		target: target,
		signer: signer,
		events: src.EventDispatcher(),
		state:  MigrationIdle,
	}
	if len(target.TrustedHostKey()) == 0 {
		target.TrustHost(signer.PublicKey())
	}
	if target.ExpectedSource() == "" {
		target.mu.Lock()
		target.expectedSource = src.InstanceID()
		target.mu.Unlock()
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	if m.events == nil {
		m.events = NewEventDispatcher(nil)
	}
	return m, nil
}

// NewMigrationHost is an alias for NewMigrator.
func NewMigrationHost(src *Memory, target *Target, signer *Signer, opts ...MigratorOption) (*Migrator, error) {
	return NewMigrator(src, target, signer, opts...)
}

// NewHost is an alias for NewMigrator.
func NewHost(src *Memory, target *Target, signer *Signer, opts ...MigratorOption) (*Migrator, error) {
	return NewMigrator(src, target, signer, opts...)
}

// State returns the host protocol state.
func (m *Migrator) State() MigrationState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Source returns the source memory image.
func (m *Migrator) Source() *Memory { return m.src }

// Target returns the target quarantine.
func (m *Migrator) Target() *Target { return m.target }

// Rounds returns the number of executed pre-copy rounds.
func (m *Migrator) Rounds() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rounds
}

// Handoff returns a deep copy of the current cutover handoff.
func (m *Migrator) Handoff() (*Handoff, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.handoff == nil {
		return nil, ErrNoHandoff
	}
	out := m.handoff.Clone()
	return &out, nil
}

// FreezePoint returns the captured frozen state, if cutover has begun.
func (m *Migrator) FreezePoint() (*FreezePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.freeze == nil {
		return nil, ErrNoHandoff
	}
	out := m.freeze.Clone()
	return &out, nil
}

// Round executes one pre-copy round:
//
//  1. atomically snapshot the global epoch, dirty set and per-page
//     generations/digests/bytes;
//  2. copy each page record to the target quarantine and verify the target's
//     readback digest;
//  3. clear the source dirty bit only when the current generation still
//     equals the copied generation.
func (m *Migrator) Round() (RoundResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.roundLocked()
}

func (m *Migrator) readyForCopy() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case MigrationIdle, MigrationCopying:
		return nil
	default:
		return fmt.Errorf("%w: cannot copy in state %s", ErrInvalidState, m.state)
	}
}

func (m *Migrator) copySnapshotPage(page PageSnapshot, epoch Epoch) (bool, bool, error) {
	rec := PageRecord{
		Page:       page.Page,
		VM:         page.VM,
		Generation: page.Generation,
		Digest:     page.Digest,
		Data:       append([]byte(nil), page.Data...),
	}
	if err := m.events.Emit(EventPageCopyBegin, EventPhaseBefore, EventFields{
		Page: page.Page, Generation: page.Generation, Epoch: epoch,
	}); err != nil {
		return false, false, err
	}
	if err := m.target.StagePage(rec); err != nil {
		return false, false, err
	}
	if err := m.events.Emit(EventPageCopyDone, EventPhaseAfter, EventFields{
		Page: page.Page, Generation: page.Generation, Digest: page.Digest,
	}); err != nil {
		return false, false, err
	}

	got, err := m.target.ReadbackDigest(page.Page)
	if err != nil {
		return false, false, err
	}
	if got != page.Digest {
		return false, false, &DigestMismatchError{
			Context: fmt.Sprintf("target readback page %d", page.Page),
			Got:     got,
			Want:    page.Digest,
		}
	}
	if err := m.events.Emit(EventPageReadback, EventPhaseAfter, EventFields{
		Page: page.Page, Generation: page.Generation, Digest: got,
	}); err != nil {
		return true, false, err
	}

	cleared, err := m.src.AcknowledgeCopied(page.Page, page.Generation)
	if err != nil {
		return true, cleared, err
	}
	return true, cleared, nil
}

// RunPreCopy performs at most maxRounds pre-copy rounds and stops as soon as
// the dirty set is empty.
func (m *Migrator) RunPreCopy(maxRounds int) (PreCopyResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if maxRounds < 1 {
		return PreCopyResult{}, fmt.Errorf("%w: maxRounds must be positive", ErrInvalidState)
	}
	res := PreCopyResult{Epoch: m.src.Epoch()}
	for i := 0; i < maxRounds; i++ {
		r, err := m.roundLocked()
		if err != nil {
			return res, err
		}
		res.Rounds++
		res.Epoch = r.Epoch
		res.RemainingDirty = r.RemainingDirty
		if r.RemainingDirty == 0 {
			res.Converged = true
			break
		}
	}
	return res, nil
}

// PreCopy is an alias for RunPreCopy.
func (m *Migrator) PreCopy(maxRounds int) (PreCopyResult, error) {
	return m.RunPreCopy(maxRounds)
}

// roundLocked is Round for callers already holding opMu.
func (m *Migrator) roundLocked() (RoundResult, error) {
	if err := m.readyForCopy(); err != nil {
		return RoundResult{}, err
	}
	m.mu.Lock()
	m.round++
	round := m.round
	m.rounds = m.round
	m.state = MigrationCopying
	m.mu.Unlock()

	snap, err := m.src.SnapshotDirty()
	if err != nil {
		return RoundResult{}, err
	}
	res := RoundResult{Round: round, Epoch: snap.Epoch, Copied: len(snap.Pages)}
	for _, page := range snap.Pages {
		copied, cleared, err := m.copySnapshotPage(page, snap.Epoch)
		if err != nil {
			if errors.Is(err, ErrStalePage) {
				continue
			}
			return res, err
		}
		if copied {
			res.Verified++
		}
		if cleared {
			res.Cleared++
		}
	}
	res.RemainingDirty = len(m.src.DirtyPages())
	return res, nil
}

// Cutover freezes all source writes, waits for in-flight writes to finish,
// captures the final dirty set and device state, copies and verifies the
// remaining pages, builds the all-page manifest and signs the startup receipt.
//
// On any failure after freezing, the source is thawed and the target
// quarantine is invalidated; the failure is still returned to the caller.
func (m *Migrator) Cutover(device DeviceState) (*Handoff, error) {
	return m.cutover(func() DeviceState { return device })
}

// CutoverWithDeviceState is Cutover with a device-state provider.  The
// provider is invoked only after the source is frozen and all in-flight writes
// have drained, so the returned DMA/policy/key state is a true quiescent
// snapshot.
func (m *Migrator) CutoverWithDeviceState(provider func() DeviceState) (*Handoff, error) {
	if provider == nil {
		return nil, fmt.Errorf("%w: nil device-state provider", ErrInvalidState)
	}
	return m.cutover(provider)
}

func (m *Migrator) cutover(deviceProvider func() DeviceState) (handoff *Handoff, err error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if m.handoff != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: cutover already performed", ErrInvalidState)
	}
	state := m.state
	m.mu.Unlock()
	switch state {
	case MigrationIdle, MigrationCopying:
	default:
		return nil, fmt.Errorf("%w: cutover in state %s", ErrInvalidState, state)
	}

	frozen := false
	defer func() {
		if err == nil {
			return
		}
		if targetStarted := m.target.Started(); targetStarted {
			// Never thaw a source after the target may have become live.
			return
		}
		if invErr := m.target.Invalidate("cutover failure: " + err.Error()); invErr != nil {
			// Fail closed: if the target cannot be proven aborted, keep the
			// source frozen.
			m.mu.Lock()
			m.state = MigrationAborted
			m.freeze = nil
			m.mu.Unlock()
			return
		}
		if frozen || m.src.Frozen() {
			_ = m.src.thawForAbort()
		}
		m.mu.Lock()
		m.state = MigrationAborted
		m.freeze = nil
		m.mu.Unlock()
	}()

	fp, freezeErr := m.src.Freeze()
	if freezeErr != nil {
		return nil, freezeErr
	}
	// Seal immediately, before the final page copy and manifest work.  This
	// prevents a concurrent direct Thaw from reopening the source while the
	// cutover image is being built.
	if err := m.src.sealForCutover(); err != nil {
		return nil, err
	}
	frozen = true
	m.mu.Lock()
	m.freeze = &fp
	m.state = MigrationFrozen
	m.mu.Unlock()

	if err := m.events.Emit(EventFinalPageCopy, EventPhaseBefore, EventFields{
		Epoch: fp.Epoch, Copied: len(fp.Dirty), RemainingDirty: len(fp.Dirty),
	}); err != nil {
		return nil, err
	}
	for _, page := range fp.Dirty {
		if _, _, err := m.copyFinalPage(page, fp.Epoch); err != nil {
			return nil, err
		}
	}

	device := deviceProvider().Clone()
	deviceDigest := device.Digest()
	if err := m.events.Emit(EventDeviceState, EventPhaseAfter, EventFields{
		Epoch: fp.Epoch, Digest: deviceDigest,
	}); err != nil {
		return nil, err
	}

	manifest := Manifest{
		VM:             fp.VM,
		SourceInstance: m.src.InstanceID(),
		TargetInstance: m.target.InstanceID(),
		Epoch:          fp.Epoch,
		Pages:          fp.ManifestEntries(),
		DeviceDigest:   deviceDigest,
		DeviceState:    &device,
	}
	if err := m.events.Emit(EventManifestBuild, EventPhaseBefore, EventFields{
		Epoch: manifest.Epoch, Digest: manifest.Root(),
	}); err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if err := m.target.CheckManifest(manifest); err != nil {
		return nil, err
	}
	root := manifest.Root()
	if err := m.events.Emit(EventManifestBuild, EventPhaseAfter, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: root,
	}); err != nil {
		return nil, err
	}

	if err := m.events.Emit(EventReceiptSign, EventPhaseBefore, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: root,
	}); err != nil {
		return nil, err
	}
	receipt, err := m.signer.BuildReceipt(manifest)
	if err != nil {
		return nil, err
	}
	if err := m.events.Emit(EventReceiptSign, EventPhaseAfter, EventFields{
		Epoch: manifest.Epoch, ManifestRoot: root, ReceiptDigest: receipt.Digest(),
	}); err != nil {
		return nil, err
	}

	h := &Handoff{
		Manifest:     manifest,
		ManifestRoot: root,
		Receipt:      receipt,
		FreezePoint:  fp,
	}
	m.mu.Lock()
	m.handoff = h
	m.state = MigrationAwaitingStart
	m.mu.Unlock()
	out := h.Clone()
	return &out, nil
}

func (m *Migrator) copyFinalPage(page PageSnapshot, epoch Epoch) (bool, bool, error) {
	rec := PageRecord{Page: page.Page, VM: page.VM, Generation: page.Generation, Digest: page.Digest, Data: append([]byte(nil), page.Data...)}
	if err := m.events.Emit(EventPageCopyBegin, EventPhaseBefore, EventFields{
		Page: page.Page, Generation: page.Generation, Epoch: epoch,
	}); err != nil {
		return false, false, err
	}
	if err := m.target.StagePage(rec); err != nil {
		return false, false, err
	}
	got, err := m.target.ReadbackDigest(page.Page)
	if err != nil {
		return false, false, err
	}
	if got != page.Digest {
		return false, false, &DigestMismatchError{Context: "final readback", Got: got, Want: page.Digest}
	}
	cleared, err := m.src.AcknowledgeCopied(page.Page, page.Generation)
	if err != nil {
		return true, cleared, err
	}
	if err := m.events.Emit(EventFinalPageCopy, EventPhaseAfter, EventFields{
		Page: page.Page, Generation: page.Generation, Digest: got, Cleared: cleared,
	}); err != nil {
		return true, cleared, err
	}
	return true, cleared, nil
}

// StartTarget delivers the signed receipt to the target.  A lost receipt
// leaves the source frozen and the quarantine hidden; the host may retry or
// abort.
func (m *Migrator) StartTarget() (*StartupConfirmation, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.startTargetLocked()
}

func (m *Migrator) startTargetLocked() (*StartupConfirmation, error) {
	m.mu.Lock()
	switch m.state {
	case MigrationAwaitingStart:
	case MigrationTargetStarted:
		if m.conf != nil {
			c := m.conf.Clone()
			m.mu.Unlock()
			return &c, nil
		}
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: target started without confirmation", ErrInvalidState)
	default:
		state := m.state
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: target start in state %s", ErrInvalidState, state)
	}
	h := m.handoff.Clone()
	m.mu.Unlock()

	if err := m.events.Emit(EventReceiptDelivery, EventPhaseBefore, EventFields{
		Epoch: h.Manifest.Epoch, ManifestRoot: h.ManifestRoot, ReceiptDigest: h.Receipt.Digest(),
	}); err != nil {
		return nil, err
	}
	conf, err := m.target.Start(h.Receipt, h.Manifest)
	if conf != nil {
		m.mu.Lock()
		m.conf = conf
		m.state = MigrationTargetStarted
		m.mu.Unlock()
	}
	if err != nil {
		return conf, err
	}
	if err := m.events.Emit(EventReceiptDelivery, EventPhaseAfter, EventFields{
		Epoch: h.Manifest.Epoch, ManifestRoot: h.ManifestRoot, ReceiptDigest: h.Receipt.Digest(),
	}); err != nil {
		return conf, err
	}
	if err := m.events.Emit(EventStartupConfirmation, EventPhaseAfter, EventFields{
		VM: h.Manifest.VM, Epoch: h.Manifest.Epoch, ManifestRoot: h.ManifestRoot, ReceiptDigest: conf.ReceiptDigest,
	}); err != nil {
		return conf, err
	}
	return conf, nil
}

// ReconcileStartup recovers the lost-confirmation case.  If the target is
// already live, it returns its durable confirmation and terminates the source.
func (m *Migrator) ReconcileStartup() (*StartupConfirmation, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	conf, err := m.target.Confirmation()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.state == MigrationTerminated {
		already := m.conf != nil && m.conf.ReceiptDigest == conf.ReceiptDigest
		m.mu.Unlock()
		if already {
			return conf, nil
		}
		return nil, ErrTerminated
	}
	m.conf = conf
	if m.state != MigrationTargetStarted {
		m.state = MigrationTargetStarted
	}
	m.mu.Unlock()
	return conf, m.confirmLocked(conf)
}

// ConfirmStartup validates the target confirmation and permanently terminates
// the frozen source.  Validation binds the entire receipt: VM, both instance
// identities, final epoch, all 64 page generations, device digest and manifest
// root.
func (m *Migrator) ConfirmStartup(conf *StartupConfirmation) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.confirmLocked(conf)
}

func (m *Migrator) confirmLocked(conf *StartupConfirmation) error {
	if conf == nil {
		return fmt.Errorf("%w: nil startup confirmation", ErrInvalidReceipt)
	}
	m.mu.Lock()
	switch m.state {
	case MigrationAwaitingStart, MigrationTargetStarted:
	case MigrationTerminated:
		if m.conf != nil && m.conf.ReceiptDigest == conf.ReceiptDigest {
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
		return ErrTerminated
	default:
		state := m.state
		m.mu.Unlock()
		return fmt.Errorf("%w: confirmation in state %s", ErrInvalidState, state)
	}
	if m.handoff == nil {
		m.mu.Unlock()
		return ErrNoHandoff
	}
	h := m.handoff.Clone()
	m.mu.Unlock()

	manifestRoot := h.Manifest.Root()
	if err := ValidateConfirmation(h.Receipt, h.Manifest, *conf); err != nil {
		return err
	}
	actual, err := m.target.Confirmation()
	if err != nil {
		return fmt.Errorf("%w: target has not produced a startup confirmation: %v", ErrInvalidReceipt, err)
	}
	if *actual != *conf {
		return fmt.Errorf("%w: confirmation does not match the live target", ErrInvalidReceipt)
	}
	if err := m.events.Emit(EventSourceConfirm, EventPhaseBefore, EventFields{
		VM: conf.VM, Epoch: conf.FinalEpoch, ManifestRoot: manifestRoot, ReceiptDigest: conf.ReceiptDigest,
	}); err != nil {
		return err
	}
	if err := m.src.markTerminated(); err != nil {
		return err
	}
	m.mu.Lock()
	confirmed := conf.Clone()
	m.conf = &confirmed
	m.state = MigrationTerminated
	m.mu.Unlock()
	if err := m.events.Emit(EventSourceTerminated, EventPhaseAfter, EventFields{
		VM: conf.VM, Epoch: conf.FinalEpoch, ManifestRoot: manifestRoot,
	}); err != nil {
		return err
	}
	return nil
}

// ValidateConfirmation performs the source-side receipt/confirmation binding
// check.  It is exported so tests and auditors can validate a confirmation
// without terminating memory.
func ValidateConfirmation(receipt Receipt, manifest Manifest, conf StartupConfirmation) error {
	root := manifest.Root()
	if conf.VM != receipt.VM || conf.VM != manifest.VM {
		return fmt.Errorf("%w: confirmation VM mismatch", ErrInvalidReceipt)
	}
	if conf.SourceInstance != receipt.SourceInstance || conf.SourceInstance != manifest.SourceInstance {
		return fmt.Errorf("%w: confirmation source instance mismatch", ErrInvalidReceipt)
	}
	if conf.TargetInstance != receipt.TargetInstance || conf.TargetInstance != manifest.TargetInstance {
		return fmt.Errorf("%w: confirmation target instance mismatch", ErrInvalidReceipt)
	}
	if conf.FinalEpoch != receipt.FinalEpoch || conf.FinalEpoch != manifest.Epoch {
		return fmt.Errorf("%w: confirmation epoch mismatch", ErrInvalidReceipt)
	}
	if conf.Pages != receipt.Pages || conf.Pages != manifest.PageGenerations() {
		return fmt.Errorf("%w: confirmation page generation mismatch", ErrInvalidReceipt)
	}
	if conf.DeviceDigest != receipt.DeviceDigest || conf.DeviceDigest != manifest.DeviceDigest {
		return fmt.Errorf("%w: confirmation device digest mismatch", ErrInvalidReceipt)
	}
	if conf.ManifestRoot != receipt.ManifestRoot || conf.ManifestRoot != root {
		return fmt.Errorf("%w: confirmation manifest root mismatch", ErrInvalidReceipt)
	}
	if conf.ReceiptDigest != receipt.Digest() {
		return fmt.Errorf("%w: confirmation receipt digest mismatch", ErrInvalidReceipt)
	}
	return nil
}

// Abort rolls back a hand-off that has not produced a live target.  It thawed
// the source so normal execution can continue and irreversibly discards the
// target quarantine.  After target start, this method refuses: the correct
// recovery is ReconcileStartup so two writable copies cannot exist.
func (m *Migrator) Abort(reason string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if m.state == MigrationTerminated {
		m.mu.Unlock()
		return ErrTerminated
	}
	m.mu.Unlock()
	if m.target.Started() {
		return ErrTargetStarted
	}

	if err := m.events.Emit(EventAbort, EventPhaseBefore, EventFields{Reason: reason}); err != nil {
		// Fail closed regarding the source: do not thaw if the abort path was
		// interrupted before it could invalidate the target.
		return err
	}

	// Invalidate performs the "not live" check and quarantine discard under
	// the target lock.  Only after that atomic transition is it safe to thaw
	// the source.  This ordering prevents a concurrent Start from creating two
	// writable copies.
	if err := m.target.Invalidate(reason); err != nil {
		if errors.Is(err, ErrAlreadyStarted) {
			return ErrTargetStarted
		}
		return err
	}
	var errs []error
	if m.src.Frozen() {
		if err := m.src.thawForAbort(); err != nil && !errors.Is(err, ErrNotFrozen) {
			errs = append(errs, err)
		}
	}
	m.mu.Lock()
	m.handoff = nil
	m.freeze = nil
	m.conf = nil
	m.state = MigrationAborted
	m.mu.Unlock()
	if err := m.events.Emit(EventAbort, EventPhaseAfter, EventFields{Reason: reason}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ReleaseMemory discards source memory after permanent termination.  Before
// confirmation the call is refused, enforcing "frozen and not reusable".
func (m *Migrator) ReleaseMemory() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	state := m.state
	m.mu.Unlock()
	if state != MigrationTerminated {
		return ErrNotTerminated
	}
	if err := m.events.Emit(EventSourceReleased, EventPhaseBefore, EventFields{}); err != nil {
		return err
	}
	if err := m.src.Release(); err != nil {
		return err
	}
	return m.events.Emit(EventSourceReleased, EventPhaseAfter, EventFields{})
}

// Migrate runs bounded pre-copy, cutover, target start and source confirmation
// in one call.  Fault injection at individual protocol events still applies.
func (m *Migrator) Migrate(device DeviceState, maxRounds int) (*StartupConfirmation, error) {
	if _, err := m.RunPreCopy(maxRounds); err != nil {
		return nil, err
	}
	if _, err := m.Cutover(device); err != nil {
		return nil, err
	}
	conf, err := m.StartTarget()
	if err != nil {
		if conf == nil {
			return nil, err
		}
		// The target may have started and only the confirmation was dropped.
		if _, recErr := m.ReconcileStartup(); recErr != nil {
			return conf, errors.Join(err, recErr)
		}
		return conf, nil
	}
	if err := m.ConfirmStartup(conf); err != nil {
		return conf, err
	}
	return conf, nil
}

// WitnessFromError extracts a stale-startup witness if present.
func WitnessFromError(err error) (*StaleStartupError, bool) {
	var stale *StaleStartupError
	if errors.As(err, &stale) {
		return stale, true
	}
	return nil, false
}
