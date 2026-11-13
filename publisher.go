package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	// ErrPublicationInProgress reports a second concurrent staged publication.
	ErrPublicationInProgress = errors.New("netsec: publication already in progress")
	// ErrNoPublication reports an operation on a missing or completed pending
	// publication.
	ErrNoPublication = errors.New("netsec: no pending publication")
	// ErrManifestUnchanged reports an attempt to publish the current manifest.
	ErrManifestUnchanged = errors.New("netsec: manifest is already current")
	// ErrManifestAlreadyKnown reports re-publication of a known manifest root.
	ErrManifestAlreadyKnown = errors.New("netsec: manifest root already known")
	// ErrManifestCurrent reports reclamation of the currently published
	// manifest.
	ErrManifestCurrent = errors.New("netsec: cannot reclaim current manifest")
	// ErrManifestNotRetired reports reclamation of a manifest that was never
	// retired by a later publication.
	ErrManifestNotRetired = errors.New("netsec: manifest is not retired")
	// ErrManifestReclaimed reports a pin or reclaim attempt on already
	// reclaimed content.
	ErrManifestReclaimed = errors.New("netsec: manifest already reclaimed")
	// ErrUnknownManifest reports a pin against a manifest not owned by this
	// publisher.
	ErrUnknownManifest = errors.New("netsec: unknown manifest")
	// ErrLeaseOutstanding reports a reclaim blocked by live frame, callback or
	// sampler leases.
	ErrLeaseOutstanding = errors.New("netsec: outstanding leases")
	// ErrBlockedInPatch reports a barrier report whose PC or return address is
	// in a replaced range without an old-generation lease.
	ErrBlockedInPatch = errors.New("netsec: thread is in a replaced code range without an old lease")
	// ErrThreadsChanged reports that the thread set changed between the barrier
	// and commit; the barrier must be taken again.
	ErrThreadsChanged = errors.New("netsec: thread set changed during barrier")
	// ErrBarrierUnstable reports an inability to obtain a stable barrier after
	// repeated thread registrations.
	ErrBarrierUnstable = errors.New("netsec: barrier did not stabilize")
	// ErrFrameExited reports metadata access or exit on an exited frame.
	ErrFrameExited = errors.New("netsec: frame already exited")
	// ErrUnknownUnit reports an entry-table miss.
	ErrUnknownUnit = errors.New("netsec: unit not present in manifest")
)

// LeaseKind identifies why a manifest is pinned.
type LeaseKind uint8

const (
	// LeaseFrame pins a manifest for an active activation.
	LeaseFrame LeaseKind = iota
	// LeaseCallback pins a manifest for an in-flight callback.
	LeaseCallback
	// LeaseSampler pins a manifest for a sampler walk.
	LeaseSampler
)

func (k LeaseKind) String() string {
	switch k {
	case LeaseFrame:
		return "frame"
	case LeaseCallback:
		return "callback"
	case LeaseSampler:
		return "sampler"
	default:
		return fmt.Sprintf("lease(%d)", k)
	}
}

// LeaseCounts is the set of live leases on a manifest.
type LeaseCounts struct {
	Frames    int
	Callbacks int
	Samplers  int
}

// Total returns the sum of all lease classes.
func (c LeaseCounts) Total() int { return c.Frames + c.Callbacks + c.Samplers }

// PublicationState is the lifecycle state of a staged publication.
type PublicationState uint8

const (
	PublicationPending PublicationState = iota
	PublicationBarrier
	PublicationBarrierDone
	PublicationCommitted
	PublicationAborted
)

func (s PublicationState) String() string {
	switch s {
	case PublicationPending:
		return "pending"
	case PublicationBarrier:
		return "barrier"
	case PublicationBarrierDone:
		return "barrier-done"
	case PublicationCommitted:
		return "committed"
	case PublicationAborted:
		return "aborted"
	default:
		return fmt.Sprintf("publication-state(%d)", s)
	}
}

// PatchRange is a replaceable address interval belonging to a unit.
type PatchRange struct {
	UnitID string
	Start  uint64
	End    uint64
}

// Contains reports whether pc is in the patch range.
func (r PatchRange) Contains(pc uint64) bool {
	return r.Start <= pc && pc < r.End
}

// BarrierRequest is sent to every registered thread before a manifest swap.
type BarrierRequest struct {
	PublicationID uint64
	OldRoot       Digest
	NewRoot       Digest
	Replaced      []PatchRange
}

// BarrierReport is a thread's safepoint reply. If PC or ReturnPC lies in a
// replaced range, Lease must pin OldRoot.
type BarrierReport struct {
	PC       uint64
	ReturnPC uint64
	Parked   bool
	Lease    *Lease
}

// Thread participates in the global code barrier. Probe must execute at a
// safepoint; a parked thread must not move until ReleaseBarrier is called.
type Thread interface {
	ID() string
	Probe(context.Context, BarrierRequest) (BarrierReport, error)
}

// BarrierReleaser is implemented by parked threads that need an explicit
// release after commit or abort.
type BarrierReleaser interface {
	ReleaseBarrier(publicationID uint64)
}

// BarrierResult is a stable snapshot proving every registered thread is
// outside the replaced ranges or pinned to the old manifest.
type BarrierResult struct {
	PublicationID uint64
	OldRoot       Digest
	NewRoot       Digest
	Replaced      []PatchRange
	Reports       map[string]BarrierReport
	ThreadEpoch   uint64
}

// Pending is a staged publication. Content is not visible to new calls until
// Commit atomically swaps the entry table.
type Pending struct {
	publisher *Publisher
	id        uint64
	manifest  *Manifest
	state     PublicationState
	staged    []Digest
	reports   map[string]BarrierReport
	err       error
}

// ID returns the publication identifier.
func (p *Pending) ID() uint64 { return p.id }

// Manifest returns the complete manifest being staged.
func (p *Pending) Manifest() *Manifest { return p.manifest }

// State returns the current lifecycle state.
func (p *Pending) State() PublicationState {
	p.publisher.mu.Lock()
	defer p.publisher.mu.Unlock()
	return p.state
}

// StagedRoots returns the content roots staged by this publication.
func (p *Pending) StagedRoots() []Digest { return append([]Digest(nil), p.staged...) }

type manifestState struct {
	manifest     *Manifest
	publishedRef int
	leases       LeaseCounts
	retired      bool
	reclaimed    bool
}

func (s *manifestState) totalRefs() int {
	if s == nil {
		return 0
	}
	return s.publishedRef + s.leases.Total()
}

// Lease is one frame, callback or sampler pin on a manifest.
type Lease struct {
	publisher *Publisher
	manifest  *Manifest
	kind      LeaseKind
	released  atomic.Bool
}

// Manifest returns the pinned manifest.
func (l *Lease) Manifest() *Manifest { return l.manifest }

// ManifestRoot returns the pinned manifest root.
func (l *Lease) ManifestRoot() Digest { return l.manifest.Root() }

// Kind returns the lease class.
func (l *Lease) Kind() LeaseKind { return l.kind }

// Released reports whether the lease has been released.
func (l *Lease) Released() bool { return l.released.Load() }

// Release drops the lease. It is idempotent.
func (l *Lease) Release() {
	if l == nil || !l.released.CompareAndSwap(false, true) {
		return
	}
	p := l.publisher
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[l.manifest.root]
	if st == nil || st.reclaimed {
		return
	}
	switch l.kind {
	case LeaseFrame:
		if st.leases.Frames > 0 {
			st.leases.Frames--
		}
	case LeaseCallback:
		if st.leases.Callbacks > 0 {
			st.leases.Callbacks--
		}
	case LeaseSampler:
		if st.leases.Samplers > 0 {
			st.leases.Samplers--
		}
	}
}

// Publisher serializes manifest publications. One staged publication may be
// pending at a time; the current entry table is swapped atomically only after
// a stable global code barrier.
type Publisher struct {
	mu    sync.Mutex
	gate  sync.RWMutex
	state atomic.Pointer[Manifest]

	states    map[Digest]*manifestState
	reclaimed map[Digest]bool
	threads   map[string]Thread
	epoch     uint64

	pending         *Pending
	nextPublication uint64

	store *ContentStore
}

// NewPublisher creates a publisher whose initial entry table is initial. A nil
// initial manifest creates the empty manifest.
func NewPublisher(initial *Manifest) (*Publisher, error) {
	return NewPublisherWithStore(initial, NewContentStore())
}

// NewPublisherWithStore is NewPublisher with an explicit content store.
func NewPublisherWithStore(initial *Manifest, store *ContentStore) (*Publisher, error) {
	if store == nil {
		store = NewContentStore()
	}
	if initial == nil {
		empty, err := BuildManifest(nil, nil, Validators{})
		if err != nil {
			return nil, err
		}
		initial = empty
	}
	if err := initial.Verify(Validators{}); err != nil {
		return nil, err
	}
	p := &Publisher{
		states:    make(map[Digest]*manifestState),
		reclaimed: make(map[Digest]bool),
		threads:   make(map[string]Thread),
		store:     store,
	}
	st := &manifestState{manifest: initial, publishedRef: 1}
	p.states[initial.root] = st
	p.state.Store(initial)
	for _, u := range initial.units {
		store.retain(u)
	}
	return p, nil
}

// CurrentManifest returns the atomically published entry table.
func (p *Publisher) CurrentManifest() *Manifest { return p.state.Load() }

// ContentStore returns the publisher's content store.
func (p *Publisher) ContentStore() *ContentStore { return p.store }

// Pending returns the currently staged publication, if any.
func (p *Publisher) Pending() *Pending {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

// RegisterThread adds a barrier participant. If a publication is already
// pending, the next barrier pass will probe the new thread.
func (p *Publisher) RegisterThread(t Thread) error {
	if t == nil || t.ID() == "" {
		return fmt.Errorf("%w: thread has no ID", ErrInvalidManifest)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.threads[t.ID()]; exists {
		return fmt.Errorf("netsec: thread %q already registered", t.ID())
	}
	p.threads[t.ID()] = t
	p.epoch++
	return nil
}

// UnregisterThread removes a barrier participant.
func (p *Publisher) UnregisterThread(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.threads[id]; !exists {
		return fmt.Errorf("netsec: thread %q is not registered", id)
	}
	delete(p.threads, id)
	p.epoch++
	return nil
}

// Begin registers a complete manifest as Pending and stages its content. The
// entry table is not changed.
func (p *Publisher) Begin(next *Manifest) (*Pending, error) {
	if next == nil {
		return nil, fmt.Errorf("%w: nil manifest", ErrInvalidManifest)
	}
	if err := next.Verify(Validators{}); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending != nil {
		return nil, ErrPublicationInProgress
	}
	old := p.state.Load()
	if old.root == next.root {
		return nil, ErrManifestUnchanged
	}
	if st := p.states[next.root]; st != nil {
		if st.reclaimed {
			return nil, fmt.Errorf("%w: manifest root %s", ErrManifestReclaimed, next.root)
		}
		return nil, fmt.Errorf("%w: manifest root %s", ErrManifestAlreadyKnown, next.root)
	}
	p.nextPublication++
	pend := &Pending{
		publisher: p,
		id:        p.nextPublication,
		manifest:  next,
		state:     PublicationPending,
	}
	for _, u := range next.units {
		pend.staged = append(pend.staged, p.store.stage(u))
	}
	p.states[next.root] = &manifestState{manifest: next}
	p.pending = pend
	return pend, nil
}

// Barrier takes a stable global code barrier for pend. On any probe failure or
// incomplete quiescence the publication is aborted and staged content becomes
// reclaimable.
func (p *Publisher) Barrier(ctx context.Context, pend *Pending) (*BarrierResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	if p.pending != pend || pend == nil {
		p.mu.Unlock()
		return nil, ErrNoPublication
	}
	if pend.state != PublicationPending && pend.state != PublicationBarrierDone {
		p.mu.Unlock()
		return nil, fmt.Errorf("netsec: publication %d is %s", pend.id, pend.state)
	}
	old := p.state.Load()
	replaced := replacedRanges(old, pend.manifest)
	req := BarrierRequest{
		PublicationID: pend.id,
		OldRoot:       old.root,
		NewRoot:       pend.manifest.root,
		Replaced:      replaced,
	}
	pend.state = PublicationBarrier
	pend.reports = make(map[string]BarrierReport)
	p.mu.Unlock()

	reports := make(map[string]BarrierReport)
	probed := make(map[string]Thread)
	for attempts := 0; ; attempts++ {
		if attempts > 10000 {
			p.abortWith(pend, ErrBarrierUnstable, reports)
			return nil, ErrBarrierUnstable
		}
		p.mu.Lock()
		ids := make([]string, 0, len(p.threads))
		for id := range p.threads {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		epoch := p.epoch
		threads := make(map[string]Thread, len(ids))
		for _, id := range ids {
			threads[id] = p.threads[id]
		}
		p.mu.Unlock()

		p.mu.Lock()
		for id, t := range p.threads {
			if previous, ok := probed[id]; ok && previous != t {
				delete(reports, id)
				delete(probed, id)
			}
		}
		p.mu.Unlock()

		for _, id := range ids {
			if _, ok := reports[id]; ok {
				continue
			}
			t := threads[id]
			probeReq := req
			probeReq.Replaced = append([]PatchRange(nil), req.Replaced...)
			report, err := t.Probe(ctx, probeReq)
			if err != nil {
				p.abortWith(pend, err, reports)
				return nil, fmt.Errorf("netsec: barrier probe %s: %w", id, err)
			}
			reports[id] = report
			probed[id] = t
			if err := validateBarrierReport(report, old, replaced); err != nil {
				p.abortWith(pend, err, reports)
				return nil, fmt.Errorf("netsec: barrier probe %s: %w", id, err)
			}
		}

		p.mu.Lock()
		if p.pending != pend {
			p.mu.Unlock()
			return nil, ErrNoPublication
		}
		stable := epoch == p.epoch
		if stable {
			for id := range p.threads {
				if _, ok := reports[id]; !ok {
					stable = false
					break
				}
			}
		}
		p.mu.Unlock()
		if !stable {
			continue
		}

		result := &BarrierResult{
			PublicationID: pend.id,
			OldRoot:       old.root,
			NewRoot:       pend.manifest.root,
			Replaced:      append([]PatchRange(nil), replaced...),
			Reports:       cloneReports(reports),
			ThreadEpoch:   epoch,
		}
		p.mu.Lock()
		if p.pending != pend {
			p.mu.Unlock()
			return nil, ErrNoPublication
		}
		pend.state = PublicationBarrierDone
		pend.reports = cloneReports(reports)
		p.mu.Unlock()
		return result, nil
	}
}

func validateBarrierReport(report BarrierReport, old *Manifest, replaced []PatchRange) error {
	if report.Lease != nil && report.Lease.Released() {
		return fmt.Errorf("%w: lease already released", ErrBlockedInPatch)
	}
	needsLease := false
	for _, pc := range []uint64{report.PC, report.ReturnPC} {
		if pc == 0 {
			continue
		}
		for _, r := range replaced {
			if r.Contains(pc) {
				needsLease = true
				break
			}
		}
		if needsLease {
			break
		}
	}
	if !needsLease {
		return nil
	}
	if report.Lease == nil || report.Lease.ManifestRoot() != old.root {
		return ErrBlockedInPatch
	}
	return nil
}

// Commit atomically swaps the entry table from the old manifest to the staged
// manifest. It fails with ErrThreadsChanged if threads registered or
// unregistered after the barrier; take the barrier again in that case.
func (p *Publisher) Commit(pend *Pending, result *BarrierResult) error {
	if pend == nil || result == nil || result.PublicationID != pend.id {
		return ErrNoPublication
	}
	p.gate.Lock()
	defer p.gate.Unlock()

	p.mu.Lock()
	if p.pending != pend {
		p.mu.Unlock()
		return ErrNoPublication
	}
	if pend.state != PublicationBarrierDone {
		p.mu.Unlock()
		return fmt.Errorf("netsec: publication %d is %s", pend.id, pend.state)
	}
	if result.ThreadEpoch != p.epoch {
		pend.state = PublicationBarrierDone
		reports := cloneReports(pend.reports)
		p.mu.Unlock()
		releaseParked(p, result.PublicationID, reports)
		return ErrThreadsChanged
	}
	old := p.state.Load()
	if old.root != result.OldRoot || pend.manifest.root != result.NewRoot {
		p.mu.Unlock()
		return fmt.Errorf("%w: barrier result does not match publication", ErrInvalidManifest)
	}
	replaced := replacedRanges(old, pend.manifest)
	for id, report := range pend.reports {
		if err := validateBarrierReport(report, old, replaced); err != nil {
			p.mu.Unlock()
			return fmt.Errorf("netsec: barrier probe %s is stale: %w", id, err)
		}
	}
	oldState := p.states[old.root]
	newState := p.states[pend.manifest.root]
	if oldState == nil || newState == nil {
		p.mu.Unlock()
		return ErrUnknownManifest
	}

	for _, u := range pend.manifest.units {
		p.store.retain(u)
	}
	for _, root := range pend.staged {
		p.store.release(root)
	}

	p.state.Store(pend.manifest)
	newState.publishedRef++
	oldState.retired = true
	if oldState.publishedRef > 0 {
		oldState.publishedRef--
	}
	pend.state = PublicationCommitted
	reports := cloneReports(pend.reports)
	p.pending = nil
	p.mu.Unlock()

	releaseParked(p, result.PublicationID, reports)
	return nil
}

// Abort discards a staged publication. Its staged content becomes orphaned
// and is reclaimed immediately when no live manifest references it.
func (p *Publisher) Abort(pend *Pending, reason error) error {
	if pend == nil {
		return ErrNoPublication
	}
	return p.abortWith(pend, reason, nil)
}

func (p *Publisher) abortWith(pend *Pending, reason error, reports map[string]BarrierReport) error {
	p.mu.Lock()
	if p.pending != pend {
		p.mu.Unlock()
		return ErrNoPublication
	}
	if pend.state == PublicationCommitted || pend.state == PublicationAborted {
		p.mu.Unlock()
		return ErrNoPublication
	}
	staged := append([]Digest(nil), pend.staged...)
	if len(reports) == 0 {
		reports = cloneReports(pend.reports)
	}
	pend.state = PublicationAborted
	pend.err = reason
	delete(p.states, pend.manifest.root)
	p.pending = nil
	p.mu.Unlock()

	for _, root := range staged {
		p.store.release(root)
	}
	p.store.ReclaimOrphans()
	releaseParked(p, pend.id, reports)
	return nil
}

// Publish is the convenience path: Begin, Barrier, Commit. If a thread joins
// between barrier and commit, the barrier is retaken.
func (p *Publisher) Publish(ctx context.Context, next *Manifest) (*Manifest, error) {
	pend, err := p.Begin(next)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 64; attempt++ {
		result, err := p.Barrier(ctx, pend)
		if err != nil {
			return nil, err
		}
		err = p.Commit(pend, result)
		if err == nil {
			return next, nil
		}
		if errors.Is(err, ErrThreadsChanged) {
			continue
		}
		_ = p.Abort(pend, err)
		return nil, err
	}
	_ = p.Abort(pend, ErrBarrierUnstable)
	return nil, ErrBarrierUnstable
}

// PinFrame pins manifest for an activation.
func (p *Publisher) PinFrame(m *Manifest) (*Lease, error) {
	return p.pin(m, LeaseFrame)
}

// PinCallback pins manifest for an in-flight callback.
func (p *Publisher) PinCallback(m *Manifest) (*Lease, error) {
	return p.pin(m, LeaseCallback)
}

// PinSampler pins manifest for a sampler walk. A late sampler may pin a
// retired manifest until it is reclaimed; afterwards it receives
// ErrManifestReclaimed.
func (p *Publisher) PinSampler(m *Manifest) (*Lease, error) {
	return p.pin(m, LeaseSampler)
}

func (p *Publisher) pin(m *Manifest, kind LeaseKind) (*Lease, error) {
	if m == nil {
		return nil, ErrUnknownManifest
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[m.root]
	if st == nil {
		if p.reclaimed[m.root] {
			return nil, fmt.Errorf("%w: %s", ErrManifestReclaimed, m.root)
		}
		return nil, ErrUnknownManifest
	}
	if st.reclaimed {
		return nil, fmt.Errorf("%w: %s", ErrManifestReclaimed, m.root)
	}
	switch kind {
	case LeaseFrame:
		st.leases.Frames++
	case LeaseCallback:
		st.leases.Callbacks++
	case LeaseSampler:
		st.leases.Samplers++
	default:
		return nil, fmt.Errorf("netsec: unknown lease kind %d", kind)
	}
	return &Lease{publisher: p, manifest: m, kind: kind}, nil
}

// LeaseCounts returns the live counts for a manifest.
func (p *Publisher) LeaseCounts(m *Manifest) LeaseCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.states[m.root]; st != nil && !st.reclaimed {
		return st.leases
	}
	return LeaseCounts{}
}

// TryReclaim reclaims a retired manifest only when every frame, callback and
// sampler lease has been dropped.
func (p *Publisher) TryReclaim(m *Manifest) error {
	if m == nil {
		return ErrUnknownManifest
	}
	p.mu.Lock()
	st := p.states[m.root]
	if st == nil {
		if p.reclaimed[m.root] {
			return nil
		}
		p.mu.Unlock()
		return ErrUnknownManifest
	}
	if st.reclaimed {
		p.mu.Unlock()
		return nil
	}
	if p.state.Load().root == m.root {
		p.mu.Unlock()
		return ErrManifestCurrent
	}
	if !st.retired {
		p.mu.Unlock()
		return ErrManifestNotRetired
	}
	if st.leases.Total() != 0 {
		counts := st.leases
		p.mu.Unlock()
		return fmt.Errorf("%w: frames=%d callbacks=%d samplers=%d",
			ErrLeaseOutstanding, counts.Frames, counts.Callbacks, counts.Samplers)
	}
	st.reclaimed = true
	p.reclaimed[m.root] = true
	units := append([]*UnitVersion(nil), m.units...)
	p.mu.Unlock()

	for _, u := range units {
		p.store.release(u.ContentRoot())
	}
	p.store.ReclaimOrphans()
	return nil
}

// Collect reclaims every retired manifest with zero outstanding leases.
func (p *Publisher) Collect() {
	p.mu.Lock()
	roots := make([]*Manifest, 0)
	for _, st := range p.states {
		if st.retired && !st.reclaimed && st.leases.Total() == 0 {
			roots = append(roots, st.manifest)
		}
	}
	p.mu.Unlock()
	for _, m := range roots {
		_ = p.TryReclaim(m)
	}
	p.store.ReclaimOrphans()
}

// Recover installs a verified complete manifest after a crash. The thread
// table and pending publication are discarded; only content reachable from
// live survives.
func (p *Publisher) Recover(live *Manifest, validators Validators) error {
	if live == nil {
		empty, err := BuildManifest(nil, nil, validators)
		if err != nil {
			return err
		}
		live = empty
	}
	if err := live.Verify(validators); err != nil {
		return err
	}
	p.gate.Lock()
	p.mu.Lock()
	if p.pending != nil {
		p.pending.state = PublicationAborted
		p.pending.err = errors.New("netsec: publication interrupted by recovery")
	}
	p.pending = nil
	p.states = map[Digest]*manifestState{
		live.root: {manifest: live, publishedRef: 1},
	}
	p.reclaimed = make(map[Digest]bool)
	p.threads = make(map[string]Thread)
	p.epoch++
	p.state.Store(live)
	p.mu.Unlock()
	p.gate.Unlock()
	p.store.Recover(live)
	return nil
}

func replacedRanges(old, next *Manifest) []PatchRange {
	var out []PatchRange
	for _, u := range old.units {
		nu, ok := next.Resolve(u.UnitID())
		if ok && nu.ContentRoot() == u.ContentRoot() {
			continue
		}
		for _, r := range u.patchRanges {
			out = append(out, PatchRange{UnitID: u.UnitID(), Start: r.Start, End: r.End})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UnitID != out[j].UnitID {
			return out[i].UnitID < out[j].UnitID
		}
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

func cloneReports(in map[string]BarrierReport) map[string]BarrierReport {
	out := make(map[string]BarrierReport, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func releaseParked(p *Publisher, publicationID uint64, reports map[string]BarrierReport) {
	p.mu.Lock()
	threads := make(map[string]Thread, len(p.threads))
	for id, t := range p.threads {
		threads[id] = t
	}
	p.mu.Unlock()
	for id, report := range reports {
		if !report.Parked {
			continue
		}
		if r, ok := threads[id].(BarrierReleaser); ok {
			r.ReleaseBarrier(publicationID)
		}
	}
}
