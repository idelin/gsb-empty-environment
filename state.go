package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Options controls optional model strictness. The zero value deliberately
// disables automatic reclamation so that callers can inspect and replay
// individual reclaim decisions; use DefaultOptions for normal execution.
type Options struct {
	// AutoReclaim marks every eligible retired generation as reclaimed after
	// each applied event.
	AutoReclaim bool

	// RequireActiveForLoadRoot additionally rejects LoadRoot while a thread
	// is inactive. The base model only states that Dereference requires an
	// active thread, so this is false by default.
	RequireActiveForLoadRoot bool

	// TreatQuiescentAsExit is a diagnostic mode for modelling a pending
	// implementation that incorrectly deactivates an active thread on
	// Quiescent. It is not part of the correct model and defaults to false.
	TreatQuiescentAsExit bool
}

// DefaultOptions returns the normal execution options.
func DefaultOptions() Options {
	return Options{AutoReclaim: true}
}

// ViolationKind classifies a safety violation found while applying an event.
type ViolationKind uint8

const (
	ViolationUseAfterFree ViolationKind = iota + 1
)

func (k ViolationKind) String() string {
	switch k {
	case ViolationUseAfterFree:
		return "use-after-free"
	default:
		return fmt.Sprintf("violation(%d)", uint8(k))
	}
}

// Violation describes a dereference of a reclaimed generation.
type Violation struct {
	Kind    ViolationKind
	Thread  ThreadID
	Ref     ObjectRef
	Epoch   Epoch
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s: thread %d dereferenced %s at %s",
		v.Kind, v.Thread, v.Ref, v.Epoch)
}

// Outcome reports the non-error effects of one applied event.
type Outcome struct {
	Advanced  bool
	Reclaimed []ObjectRef
	Violation *Violation
}

type threadState struct {
	active    bool
	published Epoch
	held      map[ObjectRef]struct{}
	lastHeld  *ObjectRef
	entered   bool
	quiescent bool
}

type objectState struct {
	ref              ObjectRef
	status           ObjectStatus
	retiredAt        Epoch
	retiredAdvance   uint64
	safeAdvances     uint8
	reclaimedAdvance uint64
}

// State is the concrete epoch-reclamation state. Its fields are private so
// that all transitions go through Apply and all inspection goes through the
// snapshot accessors.
type State struct {
	opts Options

	epoch    Epoch
	advances uint64
	root     *ObjectRef

	threads []threadState
	objects map[ObjectRef]*objectState
	latest  map[ObjectID]Generation
}

// NewState returns a three-thread state with normal execution options.
func NewState() *State {
	s, err := NewStateWithOptions(MaxThreads, DefaultOptions())
	if err != nil {
		panic(err)
	}
	return s
}

// NewStateWithOptions constructs a state with an explicit non-negative thread
// count (at most MaxThreads).
func NewStateWithOptions(threadCount int, opts Options) (*State, error) {
	if threadCount < 0 || threadCount > MaxThreads {
		return nil, fmt.Errorf("thread count %d outside [0,%d]", threadCount, MaxThreads)
	}
	s := &State{
		opts:    opts,
		threads: make([]threadState, threadCount),
		objects: make(map[ObjectRef]*objectState),
		latest:  make(map[ObjectID]Generation),
	}
	for i := range s.threads {
		s.threads[i].held = make(map[ObjectRef]struct{})
	}
	return s, nil
}

// Options returns the immutable options used by the state.
func (s *State) Options() Options {
	return s.opts
}

// Epoch returns the current global epoch.
func (s *State) Epoch() Epoch {
	return s.epoch
}

// Advances returns the number of successful TryAdvance events since the state
// was created.
func (s *State) Advances() uint64 {
	return s.advances
}

// Root returns a copy of the current root, or nil when there is no root.
func (s *State) Root() *ObjectRef {
	if s.root == nil {
		return nil
	}
	r := *s.root
	return &r
}

// ThreadCount returns the number of modelled threads.
func (s *State) ThreadCount() int {
	return len(s.threads)
}

// ThreadInfo is an immutable view of one thread.
type ThreadInfo struct {
	ID        ThreadID
	Active    bool
	Published Epoch
	Held      []ObjectRef
	LastHeld  *ObjectRef
	Entered   bool
	Quiescent bool
}

// Thread returns an immutable view of thread id.
func (s *State) Thread(id ThreadID) (ThreadInfo, bool) {
	if id < 0 || int(id) >= len(s.threads) {
		return ThreadInfo{}, false
	}
	t := &s.threads[id]
	info := ThreadInfo{
		ID:        id,
		Active:    t.active,
		Published: t.published,
		Held:      sortedRefs(t.held),
		Entered:   t.entered,
		Quiescent: t.quiescent,
	}
	if t.lastHeld != nil {
		r := *t.lastHeld
		info.LastHeld = &r
	}
	return info, true
}

// ObjectInfo is an immutable view of one address/generation pair.
type ObjectInfo struct {
	Ref                ObjectRef
	Status             ObjectStatus
	RetiredAt          Epoch
	RetiredAdvance     uint64
	SuccessfulAdvances uint64
	SafeAdvances       uint8
	ReclaimedAdvance   uint64
}

// Object returns an immutable view of ref.
func (s *State) Object(ref ObjectRef) (ObjectInfo, bool) {
	o, ok := s.objects[ref]
	if !ok {
		return ObjectInfo{}, false
	}
	return s.objectInfo(o), true
}

func (s *State) objectInfo(o *objectState) ObjectInfo {
	info := ObjectInfo{
		Ref:              o.ref,
		Status:           o.status,
		RetiredAt:        o.retiredAt,
		RetiredAdvance:   o.retiredAdvance,
		SafeAdvances:     o.safeAdvances,
		ReclaimedAdvance: o.reclaimedAdvance,
	}
	if s.advances >= o.retiredAdvance {
		info.SuccessfulAdvances = s.advances - o.retiredAdvance
	}
	return info
}

// Snapshot is a deterministic, immutable view of a state.
type Snapshot struct {
	Epoch    Epoch
	Advances uint64
	Root     *ObjectRef
	Threads  []ThreadInfo
	Objects  []ObjectInfo
}

// Snapshot returns a deterministic copy of the state.
func (s *State) Snapshot() Snapshot {
	snap := Snapshot{Epoch: s.epoch, Advances: s.advances, Root: s.Root()}
	snap.Threads = make([]ThreadInfo, 0, len(s.threads))
	for i := range s.threads {
		info, _ := s.Thread(ThreadID(i))
		snap.Threads = append(snap.Threads, info)
	}
	refs := make([]ObjectRef, 0, len(s.objects))
	for ref := range s.objects {
		refs = append(refs, ref)
	}
	sortRefs(refs)
	snap.Objects = make([]ObjectInfo, 0, len(refs))
	for _, ref := range refs {
		snap.Objects = append(snap.Objects, s.objectInfo(s.objects[ref]))
	}
	return snap
}

// Clone returns a deep copy of the state.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	c := &State{
		opts:     s.opts,
		epoch:    s.epoch,
		advances: s.advances,
		root:     s.Root(),
		threads:  make([]threadState, len(s.threads)),
		objects:  make(map[ObjectRef]*objectState, len(s.objects)),
		latest:   make(map[ObjectID]Generation, len(s.latest)),
	}
	for i := range s.threads {
		src := &s.threads[i]
		dst := &c.threads[i]
		dst.active = src.active
		dst.published = src.published
		dst.entered = src.entered
		dst.quiescent = src.quiescent
		dst.held = make(map[ObjectRef]struct{}, len(src.held))
		for ref := range src.held {
			dst.held[ref] = struct{}{}
		}
		if src.lastHeld != nil {
			r := *src.lastHeld
			dst.lastHeld = &r
		}
	}
	for ref, src := range s.objects {
		dst := *src
		c.objects[ref] = &dst
	}
	for id, gen := range s.latest {
		c.latest[id] = gen
	}
	return c
}

// Apply executes one event. For updater events the thread argument is ignored;
// callers may pass UpdaterThread. Invalid events return an error and leave the
// state unchanged. A dereference of a reclaimed generation is not an invalid
// event: it is applied and reported through Outcome.Violation.
func (s *State) Apply(thread ThreadID, ev Event) (Outcome, error) {
	if err := ev.validateFields(); err != nil {
		return Outcome{}, err
	}
	if ev.Kind.IsThreadEvent() {
		if thread < 0 || int(thread) >= len(s.threads) {
			return Outcome{}, fmt.Errorf("%s: invalid thread %d", ev.Kind, thread)
		}
	}

	var (
		out Outcome
		err error
	)
	switch ev.Kind {
	case EventEnter:
		err = s.enter(thread)
	case EventLoadRoot:
		err = s.loadRoot(thread)
	case EventDereference:
		out.Violation, err = s.dereference(thread, ev)
	case EventExit:
		err = s.exit(thread)
	case EventQuiescent:
		err = s.quiescent(thread)
	case EventReplaceRoot:
		err = s.replaceRoot(ev)
	case EventRetire:
		err = s.retire(ev)
	case EventTryAdvance:
		out.Advanced, err = s.tryAdvance()
	}
	if err != nil {
		return Outcome{}, err
	}
	if out.Violation == nil && s.opts.AutoReclaim {
		out.Reclaimed = s.settle()
	}
	return out, nil
}

func (s *State) enter(thread ThreadID) error {
	t := &s.threads[thread]
	if t.active {
		return fmt.Errorf("Enter: thread %d is already active", thread)
	}
	t.active = true
	t.published = s.epoch
	t.entered = true
	t.quiescent = false
	return nil
}

func (s *State) loadRoot(thread ThreadID) error {
	t := &s.threads[thread]
	if s.opts.RequireActiveForLoadRoot && !t.active {
		return fmt.Errorf("LoadRoot: thread %d is inactive", thread)
	}
	if s.root == nil {
		return fmt.Errorf("LoadRoot: no current root")
	}
	ref := *s.root
	o, ok := s.objects[ref]
	if !ok || o.status != ObjectLive {
		return fmt.Errorf("LoadRoot: root %s is not live", ref)
	}
	t.held[ref] = struct{}{}
	r := ref
	t.lastHeld = &r
	return nil
}

func (s *State) dereference(thread ThreadID, ev Event) (*Violation, error) {
	t := &s.threads[thread]
	if !t.active {
		return nil, fmt.Errorf("Dereference: thread %d is inactive", thread)
	}
	var ref ObjectRef
	switch {
	case ev.HasGeneration:
		ref = ObjectRef{ID: ev.Object, Gen: ev.Generation}
	case t.lastHeld != nil:
		ref = *t.lastHeld
	default:
		return nil, fmt.Errorf("Dereference: thread %d has not loaded a root", thread)
	}
	if _, ok := t.held[ref]; !ok {
		return nil, fmt.Errorf("Dereference: thread %d does not hold %s", thread, ref)
	}
	o, ok := s.objects[ref]
	if !ok {
		return nil, fmt.Errorf("Dereference: unknown object %s", ref)
	}
	if o.status == ObjectReclaimed {
		return &Violation{
			Kind:   ViolationUseAfterFree,
			Thread: thread,
			Ref:    ref,
			Epoch:  s.epoch,
			Message: fmt.Sprintf(
				"thread %d dereferenced %s after it was reclaimed", thread, ref),
		}, nil
	}
	return nil, nil
}

func (s *State) exit(thread ThreadID) error {
	t := &s.threads[thread]
	if !t.active {
		return fmt.Errorf("Exit: thread %d is inactive", thread)
	}
	t.active = false
	return nil
}

func (s *State) quiescent(thread ThreadID) error {
	t := &s.threads[thread]
	if t.active {
		if !s.opts.TreatQuiescentAsExit {
			return fmt.Errorf("Quiescent: thread %d is active; Quiescent cannot replace Exit", thread)
		}
		t.active = false
	}
	t.quiescent = true
	t.published = s.epoch
	return nil
}

func (s *State) replaceRoot(ev Event) error {
	if ev.HasGeneration {
		ref := ObjectRef{ID: ev.Object, Gen: ev.Generation}
		o, ok := s.objects[ref]
		if !ok {
			return fmt.Errorf("ReplaceRoot: unknown object %s", ref)
		}
		if o.status != ObjectLive {
			return fmt.Errorf("ReplaceRoot: object %s is %s, not live", ref, o.status)
		}
		r := ref
		s.root = &r
		return nil
	}

	id := ev.Object
	latest, seen := s.latest[id]
	if !seen {
		return s.allocateRoot(ObjectRef{ID: id, Gen: 0})
	}
	ref := ObjectRef{ID: id, Gen: latest}
	o := s.objects[ref]
	switch o.status {
	case ObjectLive:
		r := ref
		s.root = &r
		return nil
	case ObjectRetired:
		return fmt.Errorf("ReplaceRoot: address %d still has unreclaimed generation %d", id, latest)
	case ObjectReclaimed:
		return s.allocateRoot(ObjectRef{ID: id, Gen: latest + 1})
	default:
		return fmt.Errorf("ReplaceRoot: object %s has invalid status %d", ref, o.status)
	}
}

func (s *State) allocateRoot(ref ObjectRef) error {
	if _, exists := s.objects[ref]; exists {
		return fmt.Errorf("ReplaceRoot: object %s already exists", ref)
	}
	s.objects[ref] = &objectState{ref: ref, status: ObjectLive}
	s.latest[ref.ID] = ref.Gen
	r := ref
	s.root = &r
	return nil
}

func (s *State) retire(ev Event) error {
	ref, err := s.retireTarget(ev)
	if err != nil {
		return err
	}
	o, ok := s.objects[ref]
	if !ok {
		return fmt.Errorf("Retire: unknown object %s", ref)
	}
	if o.status != ObjectLive {
		return fmt.Errorf("Retire: object %s is %s, not live", ref, o.status)
	}
	o.status = ObjectRetired
	o.retiredAt = s.epoch
	o.retiredAdvance = s.advances
	o.safeAdvances = 0
	if s.root != nil && *s.root == ref {
		s.root = nil
	}
	return nil
}

func (s *State) retireTarget(ev Event) (ObjectRef, error) {
	if ev.HasGeneration {
		return ObjectRef{ID: ev.Object, Gen: ev.Generation}, nil
	}
	if ev.HasObject {
		id := ev.Object
		if s.root != nil && s.root.ID == id {
			return *s.root, nil
		}
		latest, ok := s.latest[id]
		if !ok {
			return ObjectRef{}, fmt.Errorf("Retire: unknown address %d", id)
		}
		return ObjectRef{ID: id, Gen: latest}, nil
	}
	if s.root == nil {
		return ObjectRef{}, fmt.Errorf("Retire: no current root")
	}
	return *s.root, nil
}

// tryAdvance advances only if every active thread published the current epoch.
// It does not update published epochs on behalf of active threads.
func (s *State) tryAdvance() (bool, error) {
	for i := range s.threads {
		t := &s.threads[i]
		if t.active && t.published != s.epoch {
			return false, nil
		}
	}

	s.advances++
	s.epoch = s.epoch.Next()
	for ref, o := range s.objects {
		if o.status != ObjectRetired {
			continue
		}
		if !s.activeHolds(ref) {
			if o.safeAdvances < 255 {
				o.safeAdvances++
			}
		}
	}
	return true, nil
}

func (s *State) activeHolds(ref ObjectRef) bool {
	for i := range s.threads {
		t := &s.threads[i]
		if !t.active {
			continue
		}
		if _, ok := t.held[ref]; ok {
			return true
		}
	}
	return false
}

// RetirementInfo describes the reclamation clock for one generation.
type RetirementInfo struct {
	Ref                ObjectRef
	RetiredAt          Epoch
	RetiredAdvance     uint64
	SuccessfulAdvances uint64
	SafeAdvances       uint8
	EpochDelta         Epoch
	Reclaimed          bool
}

// Retirement returns reclamation bookkeeping for ref.
func (s *State) Retirement(ref ObjectRef) (RetirementInfo, bool) {
	o, ok := s.objects[ref]
	if !ok {
		return RetirementInfo{}, false
	}
	info := RetirementInfo{
		Ref:            ref,
		RetiredAt:      o.retiredAt,
		RetiredAdvance: o.retiredAdvance,
		SafeAdvances:   o.safeAdvances,
		EpochDelta:     (s.epoch + EpochCount - o.retiredAt) % EpochCount,
		Reclaimed:      o.status == ObjectReclaimed,
	}
	if s.advances >= o.retiredAdvance {
		info.SuccessfulAdvances = s.advances - o.retiredAdvance
	}
	return info, true
}

// CanReclaim reports whether ref is currently safe to reclaim. The decision
// uses successful safe advances, not the numeric difference between the
// current and retirement epochs, so epoch wraparound cannot hide two completed
// grace periods.
func (s *State) CanReclaim(ref ObjectRef) bool {
	o, ok := s.objects[ref]
	if !ok || o.status != ObjectRetired {
		return false
	}
	if o.safeAdvances < 2 {
		return false
	}
	if s.root != nil && *s.root == ref {
		return false
	}
	return !s.activeHolds(ref)
}

// Reclaim explicitly reclaims ref when it is eligible. It is used when
// AutoReclaim is disabled, for example while replaying a proposed trace.
func (s *State) Reclaim(ref ObjectRef) error {
	if !s.CanReclaim(ref) {
		return fmt.Errorf("Reclaim: %s is not safe to reclaim", ref)
	}
	o := s.objects[ref]
	o.status = ObjectReclaimed
	o.reclaimedAdvance = s.advances
	return nil
}

// Settle reclaims every currently eligible generation and returns the refs in
// deterministic address/generation order.
func (s *State) Settle() []ObjectRef {
	return s.settle()
}

func (s *State) settle() []ObjectRef {
	var reclaimed []ObjectRef
	for ref, o := range s.objects {
		if o.status != ObjectRetired {
			continue
		}
		if s.CanReclaim(ref) {
			o.status = ObjectReclaimed
			o.reclaimedAdvance = s.advances
			reclaimed = append(reclaimed, ref)
		}
	}
	sortRefs(reclaimed)
	return reclaimed
}

func sortedRefs(set map[ObjectRef]struct{}) []ObjectRef {
	refs := make([]ObjectRef, 0, len(set))
	for ref := range set {
		refs = append(refs, ref)
	}
	sortRefs(refs)
	return refs
}

func sortRefs(refs []ObjectRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].ID != refs[j].ID {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Gen < refs[j].Gen
	})
}

func (s *State) key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "e=%d;a=%d;r=", s.epoch, s.advances)
	if s.root == nil {
		b.WriteByte('-')
	} else {
		fmt.Fprintf(&b, "%d.%d", s.root.ID, s.root.Gen)
	}
	for i := range s.threads {
		t := &s.threads[i]
		fmt.Fprintf(&b, ";t%d=%t,%d,", i, t.active, t.published)
		refs := sortedRefs(t.held)
		for _, ref := range refs {
			fmt.Fprintf(&b, "%d.%d,", ref.ID, ref.Gen)
		}
		b.WriteByte('|')
		if t.lastHeld == nil {
			b.WriteByte('-')
		} else {
			fmt.Fprintf(&b, "%d.%d", t.lastHeld.ID, t.lastHeld.Gen)
		}
	}
	refs := make([]ObjectRef, 0, len(s.objects))
	for ref := range s.objects {
		refs = append(refs, ref)
	}
	sortRefs(refs)
	for _, ref := range refs {
		o := s.objects[ref]
		fmt.Fprintf(&b, ";o%d.%d=%d,%d,%d,%d,%d",
			ref.ID, ref.Gen, o.status, o.retiredAt, o.retiredAdvance,
			o.safeAdvances, o.reclaimedAdvance)
	}
	return b.String()
}
