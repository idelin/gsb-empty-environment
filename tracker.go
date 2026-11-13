package netsec

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
)

// Tracker is the dynamic code identity state machine for one sandbox host.
// It is safe for concurrent use.
type Tracker struct {
	mu sync.Mutex

	pages       map[PageID]*virtualPage
	physical    map[PhysicalID]*physicalPage
	nextPhysID  PhysicalID
	nextVirtual uint64

	rule       any
	ruleGen    uint64
	detections map[detectionKey]cachedDetection
	cacheStats CacheStats
}

// Compatibility aliases for hosts that use a different name for the engine.
type (
	CodeIdentity    = Tracker
	CodeTracker     = Tracker
	IdentityTracker = Tracker
	Engine          = Tracker
	Manager         = Tracker
)

type virtualPage struct {
	id       PageID
	gen      uint64
	perm     Perm
	phys     *physicalPage
	identity *Identity
}

type physicalPage struct {
	id       PhysicalID
	data     [PageSize]byte
	gen      uint64
	writeSeq uint64
	// nonXSeq is the last write sequence at which the physical page entered
	// a non-executable state. Execution identities include writes after it.
	nonXSeq uint64
	source  Source
	aliases map[PageID]*virtualPage
	writes  []WriteRecord
	retired bool
}

// NewTracker constructs an empty tracker.
func NewTracker() *Tracker {
	t := &Tracker{}
	t.initLocked()
	return t
}

// New is a shorter alias for NewTracker.
func New() *Tracker { return NewTracker() }

// NewCodeIdentity constructs an empty tracker.
func NewCodeIdentity() *Tracker { return NewTracker() }

// NewIdentityTracker is an alias for NewTracker.
func NewIdentityTracker() *Tracker { return NewTracker() }

func (t *Tracker) initLocked() {
	if t.pages == nil {
		t.pages = make(map[PageID]*virtualPage)
	}
	if t.physical == nil {
		t.physical = make(map[PhysicalID]*physicalPage)
	}
	if t.detections == nil {
		t.detections = make(map[detectionKey]cachedDetection)
	}
}

func (t *Tracker) nextGenerationLocked() uint64 {
	t.nextVirtual++
	if t.nextVirtual == 0 {
		t.nextVirtual++
	}
	return t.nextVirtual
}

func (t *Tracker) allocatePhysicalLocked(source Source) *physicalPage {
	t.nextPhysID++
	if t.nextPhysID == 0 {
		t.nextPhysID++
	}
	return &physicalPage{
		id:      t.nextPhysID,
		source:  source,
		aliases: make(map[PageID]*virtualPage),
	}
}

// Map creates or replaces the virtual mapping pageID over a fresh physical
// page. Every Map produces a new virtual generation for pageID. Source may be
// a Source, [32]byte, []byte, string, or fmt.Stringer.
func (t *Tracker) Map(pageID PageID, source any, perm Perm) error {
	return t.mapBytes(pageID, source, nil, perm)
}

// MapBytes is Map with initial bytes for the fresh physical page. Initial
// bytes are part of the mapped source and do not appear as Write provenance.
func (t *Tracker) MapBytes(pageID PageID, source any, initial []byte, perm Perm) error {
	return t.mapBytes(pageID, source, initial, perm)
}

func (t *Tracker) mapBytes(pageID PageID, source any, initial []byte, perm Perm) error {
	if err := checkPermissions(perm); err != nil {
		return err
	}
	if len(initial) > PageSize {
		return fmt.Errorf("%w: initial length %d", ErrOutOfRange, len(initial))
	}
	normalized, err := normalizeSource(source)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	if old := t.pages[pageID]; old != nil {
		t.detachLocked(old)
	}

	phys := t.allocatePhysicalLocked(normalized)
	copy(phys.data[:], initial)

	vp := &virtualPage{
		id:   pageID,
		gen:  t.nextGenerationLocked(),
		perm: perm,
		phys: phys,
	}
	t.pages[pageID] = vp
	phys.aliases[pageID] = vp
	t.physical[phys.id] = phys

	if perm.CanExecute() {
		t.captureIdentityLocked(vp)
	}
	return nil
}

// Write applies bytes to a writable virtual page. The write either applies
// completely or not at all. Any write advances the physical generation and
// invalidates every execution identity sharing that physical page.
func (t *Tracker) Write(pageID PageID, offset int, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		return fmt.Errorf("%w: %d", ErrNotMapped, pageID)
	}
	if !vp.perm.CanWrite() {
		return fmt.Errorf("%w: page %d has %s", ErrPermissionDenied, pageID, vp.perm)
	}
	if anyAliasHas(vp.phys, PermExecute) {
		return ErrWXConflict
	}
	if err := validateRange(offset, len(data)); err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}

	copy(vp.phys.data[offset:offset+len(data)], data)
	vp.phys.gen++
	if vp.phys.gen == 0 {
		vp.phys.gen++
	}
	vp.phys.writeSeq++

	rec := WriteRecord{
		Sequence:           vp.phys.writeSeq,
		PageID:             vp.id,
		VirtualGeneration:  vp.gen,
		Offset:             offset,
		Length:             len(data),
		Bytes:              append([]byte(nil), data...),
		PhysicalGeneration: vp.phys.gen,
		SourceDigest:       vp.phys.source.Digest,
		SourceName:         vp.phys.source.Name,
	}
	vp.phys.writes = append(vp.phys.writes, rec)

	for _, alias := range vp.phys.aliases {
		alias.identity = nil
	}
	return nil
}

// Protect changes the virtual page permissions and enforces W^X over the
// aggregate permissions of every alias of the physical page. Before a
// non-executable generation becomes executable, a fresh full-page identity is
// captured. Leaving executable state invalidates the local identity and starts
// a new write provenance epoch on the physical page.
func (t *Tracker) Protect(pageID PageID, perm Perm) error {
	if err := checkPermissions(perm); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		return fmt.Errorf("%w: %d", ErrNotMapped, pageID)
	}
	if err := ensureAggregatePerm(vp.phys, vp, perm); err != nil {
		return err
	}

	beforeX := anyAliasHas(vp.phys, PermExecute)
	oldX := vp.perm.CanExecute()
	newX := perm.CanExecute()

	vp.perm = perm
	afterX := anyAliasHas(vp.phys, PermExecute)

	switch {
	case !oldX && newX:
		t.captureIdentityLocked(vp)
	case oldX && !newX:
		vp.identity = nil
		if beforeX && !afterX {
			vp.phys.nonXSeq = vp.phys.writeSeq
		}
	case newX && vp.identity == nil:
		// Defensive: an executable page must never be usable without identity.
		t.captureIdentityLocked(vp)
	case !newX:
		vp.identity = nil
	}
	return nil
}

// Unmap removes a virtual page. The physical page remains available for audit
// and for RemapPhysical, but is retired when its last alias disappears.
func (t *Tracker) Unmap(pageID PageID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		return fmt.Errorf("%w: %d", ErrNotMapped, pageID)
	}
	t.detachLocked(vp)
	return nil
}

// Remap creates a new virtual generation for newPageID over an existing
// physical page. The target may be an active PageID or, when that PageID is
// not mapped, a PhysicalID. If perms is omitted, the target's current
// permissions are inherited. The aggregate W^X rule is applied to all aliases.
func (t *Tracker) Remap(newPageID, target PageID, perms ...Perm) error {
	if len(perms) > 1 {
		return fmt.Errorf("%w: at most one permission value", ErrInvalidPermission)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	var (
		targetPhys *physicalPage
		perm       Perm
	)
	if targetVP := t.pages[target]; targetVP != nil {
		if newPageID == target {
			return ErrSamePage
		}
		targetPhys = targetVP.phys
		perm = targetVP.perm
	} else if phys := t.physical[PhysicalID(target)]; phys != nil {
		targetPhys = phys
		perm = firstAliasPerm(phys)
	} else {
		return fmt.Errorf("%w: %d", ErrNotMapped, target)
	}
	if len(perms) == 1 {
		perm = perms[0]
	}
	if err := checkPermissions(perm); err != nil {
		return err
	}

	// If the new ID is currently mapped to the same physical page, it will be
	// detached before reattachment, so exclude it from the aggregate check.
	var exclude *virtualPage
	if old := t.pages[newPageID]; old != nil && old.phys == targetPhys {
		exclude = old
	}
	if err := ensureAggregatePerm(targetPhys, exclude, perm); err != nil {
		return err
	}

	old := t.pages[newPageID]
	preserveEpoch := old != nil && old.phys == targetPhys && old.perm.CanExecute() && perm.CanExecute()
	oldNonX := targetPhys.nonXSeq
	if old != nil {
		t.detachLocked(old)
	}
	if preserveEpoch && !anyAliasHas(targetPhys, PermExecute) {
		// The virtual mapping was replaced without the physical page ever
		// becoming non-executable, so keep its provenance epoch.
		targetPhys.nonXSeq = oldNonX
	}
	t.attachLocked(newPageID, targetPhys, perm)
	return nil
}

// RemapPhysical maps newPageID to an existing physical page identified
// directly by PhysicalID. Unlike Remap it can revive a retired physical page.
// If perms is omitted, an active alias's permissions are inherited, or
// PermNone is used for a retired page.
func (t *Tracker) RemapPhysical(newPageID PageID, physicalID PhysicalID, perms ...Perm) error {
	if len(perms) > 1 {
		return fmt.Errorf("%w: at most one permission value", ErrInvalidPermission)
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	phys := t.physical[physicalID]
	if phys == nil {
		return fmt.Errorf("%w: %d", ErrNoPhysical, physicalID)
	}

	perm := PermNone
	if len(perms) == 1 {
		perm = perms[0]
	} else {
		perm = firstAliasPerm(phys)
	}
	if err := checkPermissions(perm); err != nil {
		return err
	}

	var exclude *virtualPage
	if old := t.pages[newPageID]; old != nil && old.phys == phys {
		exclude = old
	}
	if err := ensureAggregatePerm(phys, exclude, perm); err != nil {
		return err
	}

	old := t.pages[newPageID]
	preserveEpoch := old != nil && old.phys == phys && old.perm.CanExecute() && perm.CanExecute()
	oldNonX := phys.nonXSeq
	if old != nil {
		t.detachLocked(old)
	}
	if preserveEpoch && !anyAliasHas(phys, PermExecute) {
		phys.nonXSeq = oldNonX
	}
	t.attachLocked(newPageID, phys, perm)
	return nil
}

func (t *Tracker) attachLocked(pageID PageID, phys *physicalPage, perm Perm) {
	vp := &virtualPage{
		id:   pageID,
		gen:  t.nextGenerationLocked(),
		perm: perm,
		phys: phys,
	}
	t.pages[pageID] = vp
	phys.aliases[pageID] = vp
	phys.retired = false
	if perm.CanExecute() {
		t.captureIdentityLocked(vp)
	}
}

func (t *Tracker) detachLocked(vp *virtualPage) {
	phys := vp.phys
	beforeX := anyAliasHas(phys, PermExecute)
	delete(t.pages, vp.id)
	delete(phys.aliases, vp.id)
	vp.identity = nil
	afterX := anyAliasHas(phys, PermExecute)
	if beforeX && !afterX {
		phys.nonXSeq = phys.writeSeq
	}
	if len(phys.aliases) == 0 {
		phys.retired = true
	}
	vp.phys = nil
}

func (t *Tracker) captureIdentityLocked(vp *virtualPage) {
	phys := vp.phys
	if phys == nil {
		vp.identity = nil
		return
	}
	snapshot := snapshotOf(&phys.data)
	digest := sha256.Sum256(snapshot)
	chain := make([]WriteRecord, 0)
	intervals := make([]WriteInterval, 0)
	for _, rec := range phys.writes {
		if rec.Sequence <= phys.nonXSeq {
			continue
		}
		copied := rec
		copied.Bytes = append([]byte(nil), rec.Bytes...)
		chain = append(chain, copied)
		intervals = append(intervals, WriteInterval{Offset: rec.Offset, Length: rec.Length})
	}
	vp.identity = &Identity{
		PhysicalID:         phys.id,
		PhysicalGeneration: phys.gen,
		PageID:             vp.id,
		VirtualGeneration:  vp.gen,
		Generation:         vp.gen,
		Digest:             digest,
		Snapshot:           snapshot,
		Source:             phys.source,
		SourceDigest:       phys.source.Digest,
		SourceName:         phys.source.Name,
		FileSource:         sourceDisplay(phys.source),
		WriteIntervals:     intervals,
		WriteChain:         chain,
	}
}

func sourceDisplay(source Source) string {
	return source.String()
}

func anyAliasHas(phys *physicalPage, bit Perm) bool {
	for _, alias := range phys.aliases {
		if alias.perm&bit != 0 {
			return true
		}
	}
	return false
}

func firstAliasPerm(phys *physicalPage) Perm {
	if len(phys.aliases) == 0 {
		return PermNone
	}
	ids := make([]PageID, 0, len(phys.aliases))
	for id := range phys.aliases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return phys.aliases[ids[0]].perm
}

func ensureAggregatePerm(phys *physicalPage, exclude *virtualPage, newPerm Perm) error {
	aggregate := newPerm
	for _, alias := range phys.aliases {
		if alias != exclude {
			aggregate |= alias.perm
		}
	}
	if aggregate&PermWrite != 0 && aggregate&PermExecute != 0 {
		return ErrWXConflict
	}
	return nil
}

// Execute returns the execution identity for the requested range. The call
// fails without evaluating detection rules unless the page is mapped, has X,
// is within range, has no writable alias, and still has a current identity.
func (t *Tracker) Execute(pageID PageID, offset, length int) (*Execution, error) {
	t.mu.Lock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("%w: %d", ErrNotMapped, pageID)
	}
	exec, key, needRule, rule, expected, err := t.prepareExecutionLocked(vp, offset, length)
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}

	if !needRule {
		return exec, nil
	}

	callbackSnapshot := append([]byte(nil), exec.Snapshot...)
	verdict, label, ruleErr := callRule(rule, exec.Digest, callbackSnapshot)

	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.executionStillValidLocked(expected) {
		return nil, ErrStaleIdentity
	}

	if ruleErr == nil && t.ruleGen == expected.ruleGen {
		t.detections[key] = cachedDetection{verdict: verdict, label: label}
	} else {
		if ruleErr != nil {
			ruleErr = fmt.Errorf("%w: %v", ErrRuleFailed, ruleErr)
		}
	}
	exec.Detection = Detection{
		Verdict:   verdict,
		Label:     label,
		Evaluated: ruleErr == nil,
		Cached:    false,
	}
	if ruleErr != nil {
		exec.Detection.Verdict = VerdictUnknown
	}
	exec.Verdict = exec.Detection.Verdict
	exec.RuleError = ruleErr
	return exec, nil
}

type expectedExecution struct {
	vp         *virtualPage
	phys       *physicalPage
	identity   *Identity
	virtualGen uint64
	ruleGen    uint64
	offset     int
	length     int
}

func (t *Tracker) prepareExecutionLocked(vp *virtualPage, offset, length int) (*Execution, detectionKey, bool, any, expectedExecution, error) {
	expected := expectedExecution{vp: vp, phys: vp.phys, identity: vp.identity, virtualGen: vp.gen, ruleGen: t.ruleGen, offset: offset, length: length}

	if !vp.perm.CanExecute() {
		return nil, detectionKey{}, false, nil, expected, fmt.Errorf("%w: page %d has %s", ErrPermissionDenied, vp.id, vp.perm)
	}
	if length <= 0 || offset < 0 || offset > PageSize || length > PageSize || offset+length > PageSize {
		return nil, detectionKey{}, false, nil, expected, fmt.Errorf("%w: [%d,%d)", ErrOutOfRange, offset, offset+length)
	}
	if anyAliasHas(vp.phys, PermWrite) {
		return nil, detectionKey{}, false, nil, expected, ErrWXConflict
	}

	id := vp.identity
	if id == nil {
		return nil, detectionKey{}, false, nil, expected, fmt.Errorf("%w: page %d", ErrNoIdentity, vp.id)
	}
	if id.PhysicalID != vp.phys.id ||
		id.PhysicalGeneration != vp.phys.gen ||
		id.VirtualGeneration != vp.gen ||
		id.PageID != vp.id {
		vp.identity = nil
		return nil, detectionKey{}, false, nil, expected, ErrStaleIdentity
	}
	digest := sha256.Sum256(vp.phys.data[:])
	if digest != id.Digest {
		vp.identity = nil
		return nil, detectionKey{}, false, nil, expected, ErrStaleIdentity
	}
	if len(id.Snapshot) != PageSize {
		vp.identity = nil
		return nil, detectionKey{}, false, nil, expected, ErrInvalidIdentity
	}

	cloned := cloneIdentity(*id)
	exec := &Execution{
		Identity: cloned,
		Offset:   offset,
		Length:   length,
		Code:     append([]byte(nil), cloned.Snapshot[offset:offset+length]...),
	}
	key := detectionKey{
		PhysicalID:         id.PhysicalID,
		PhysicalGeneration: id.PhysicalGeneration,
		Digest:             id.Digest,
	}
	exec.DetectionKey = key

	rule := t.rule
	if rule == nil {
		return exec, key, false, nil, expected, nil
	}
	if cached, ok := t.detections[key]; ok {
		t.cacheStats.Hits++
		exec.Detection = Detection{
			Verdict:   cached.verdict,
			Label:     cached.label,
			Evaluated: true,
			Cached:    true,
		}
		exec.Verdict = cached.verdict
		return exec, key, false, nil, expected, nil
	}
	t.cacheStats.Misses++
	return exec, key, true, rule, expected, nil
}

func (t *Tracker) executionStillValidLocked(exp expectedExecution) bool {
	if t.pages[exp.vp.id] != exp.vp || exp.vp.phys == nil || exp.vp.phys != exp.phys {
		return false
	}
	if exp.vp.identity != exp.identity || exp.vp.gen != exp.virtualGen {
		return false
	}
	if !exp.vp.perm.CanExecute() {
		return false
	}
	if exp.phys.gen != exp.identity.PhysicalGeneration || exp.phys.id != exp.identity.PhysicalID {
		return false
	}
	if anyAliasHas(exp.phys, PermWrite) {
		return false
	}
	digest := sha256.Sum256(exp.phys.data[:])
	if digest != exp.identity.Digest {
		return false
	}
	if exp.offset < 0 || exp.length <= 0 || exp.offset > PageSize || exp.length > PageSize || exp.offset+exp.length > PageSize {
		return false
	}
	return true
}

// Inspect returns a public view of a mapped virtual page.
func (t *Tracker) Inspect(pageID PageID) (PageState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		return PageState{}, false
	}
	return PageState{
		PageID:             vp.id,
		PhysicalID:         vp.phys.id,
		Permissions:        vp.perm,
		VirtualGeneration:  vp.gen,
		Generation:         vp.gen,
		PhysicalGeneration: vp.phys.gen,
		Source:             vp.phys.source,
		HasIdentity:        vp.identity != nil,
	}, true
}

// Snapshot returns an immutable copy of the current virtual page contents.
func (t *Tracker) Snapshot(pageID PageID) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil {
		return nil, false
	}
	return snapshotOf(&vp.phys.data), true
}

// PhysicalSnapshot returns an immutable copy of a physical page's contents,
// including a retired physical page retained for audit.
func (t *Tracker) PhysicalSnapshot(physicalID PhysicalID) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	phys := t.physical[physicalID]
	if phys == nil {
		return nil, false
	}
	return snapshotOf(&phys.data), true
}

// CurrentIdentity returns a copy of the virtual page's current identity, if
// that identity still matches the physical page generation.
func (t *Tracker) CurrentIdentity(pageID PageID) (Identity, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	vp := t.pages[pageID]
	if vp == nil || vp.identity == nil {
		return Identity{}, false
	}
	if vp.identity.PhysicalID != vp.phys.id ||
		vp.identity.PhysicalGeneration != vp.phys.gen ||
		vp.identity.VirtualGeneration != vp.gen {
		return Identity{}, false
	}
	if digest := sha256.Sum256(vp.phys.data[:]); digest != vp.identity.Digest {
		return Identity{}, false
	}
	return cloneIdentity(*vp.identity), true
}

// PhysicalAliases returns the currently mapped virtual pages sharing a
// physical page, sorted by PageID.
func (t *Tracker) PhysicalAliases(physicalID PhysicalID) []PageID {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initLocked()

	phys := t.physical[physicalID]
	if phys == nil {
		return nil
	}
	ids := make([]PageID, 0, len(phys.aliases))
	for id := range phys.aliases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
