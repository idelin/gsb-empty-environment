package impact

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// revocations is a set of object identities.
type revocations struct {
	objects map[string]ObjectID
}

func newRevocations(objects []ObjectID) *revocations {
	r := &revocations{objects: make(map[string]ObjectID, len(objects))}
	for _, obj := range objects {
		if !obj.IsZero() {
			r.objects[obj.Key()] = obj
		}
	}
	return r
}

func (r *revocations) has(obj ObjectID) bool {
	if r == nil || obj.IsZero() {
		return false
	}
	if _, ok := r.objects[obj.Key()]; ok {
		return true
	}
	for _, revoked := range r.objects {
		if objectMatches(revoked, obj) {
			return true
		}
	}
	return false
}

// objectMatches treats empty fields in pattern as wildcards. This lets a
// release service revoke by object path without knowing every build ID (or by
// version when the patch produced a fresh build ID).
func objectMatches(pattern, candidate ObjectID) bool {
	fields := []struct {
		want string
		got  string
	}{
		{pattern.Module, candidate.Module},
		{pattern.Version, candidate.Version},
		{pattern.Package, candidate.Package},
		{pattern.Object, candidate.Object},
		{pattern.BuildID, candidate.BuildID},
	}
	for _, field := range fields {
		if field.want != "" && field.want != field.got {
			return false
		}
	}
	return true
}

type dependencies struct {
	summaries map[SummaryKey]struct{}
	symbols   map[string]struct{}
	objects   map[string]ObjectID
}

func newDependencies() *dependencies {
	return &dependencies{
		summaries: make(map[SummaryKey]struct{}),
		symbols:   make(map[string]struct{}),
		objects:   make(map[string]ObjectID),
	}
}

func (d *dependencies) summary(key SummaryKey) {
	if key != "" {
		d.summaries[key] = struct{}{}
	}
}

func (d *dependencies) symbol(name string) {
	if name != "" {
		d.symbols[name] = struct{}{}
	}
}

func (d *dependencies) object(obj ObjectID) {
	if !obj.IsZero() {
		d.objects[obj.Key()] = obj
	}
}

type selectionStatus uint8

const (
	selectionTarget selectionStatus = iota
	selectionMissingBinding
	selectionAmbiguous
	selectionDisabled
)

type selection struct {
	guard   BDD
	target  SummaryKey
	binding Binding
	status  selectionStatus
}

type bindingTargetInfo struct {
	guard   BDD
	binding Binding
}

// handleLive records the symbolic condition under which a registration handle
// is still installed. A handle is live when some registration guard holds and
// no unregistration guard holds.
type handleLive struct {
	registered   BDD
	unregistered BDD
	origin       Provenance
	hasOrigin    bool
	symbol       string
}

// evaluator computes least fixed points of per-summary may-effect equations.
// Summaries are declarative: each guarded call or callback is an alternative
// behavior. Guards conjoin along a call chain, which is what makes two
// incompatible edges unable to support one path.
type evaluator struct {
	snapshot *Snapshot
	revoked  *revocations
	deps     *dependencies

	effects  map[SummaryKey]BDD
	unknowns map[SummaryKey]BDD
	known    map[SummaryKey]bool

	certCache  map[SummaryKey]bool
	iterations int
	stateCount int
}

func newEvaluator(snapshot *Snapshot, revoked *revocations) *evaluator {
	return &evaluator{
		snapshot:  snapshot,
		revoked:   revoked,
		deps:      newDependencies(),
		effects:   make(map[SummaryKey]BDD),
		unknowns:  make(map[SummaryKey]BDD),
		known:     make(map[SummaryKey]bool),
		certCache: make(map[SummaryKey]bool),
	}
}

func (ev *evaluator) rootObject(entry SummaryKey) ObjectID {
	if p, ok := parseProvenanceKey(entry); ok {
		return p.ObjectID()
	}
	return ObjectID{}
}

func (ev *evaluator) true() BDD  { return ev.snapshot.manager.True() }
func (ev *evaluator) false() BDD { return ev.snapshot.manager.False() }

func (ev *evaluator) effectOf(key SummaryKey) BDD {
	if value, ok := ev.effects[key]; ok {
		return value
	}
	return ev.false()
}

func (ev *evaluator) unknownOf(key SummaryKey) BDD {
	if value, ok := ev.unknowns[key]; ok {
		return value
	}
	return ev.false()
}

// certEvidence decides whether a summary carries usable signed evidence. With
// a key ring installed the full HMAC signature is verified. Without a key ring
// the certificate must still bind the exact provenance and digest; this is
// useful for fixtures while absent or stale certificates remain missing
// evidence.
func (ev *evaluator) certEvidence(key SummaryKey) bool {
	if cached, ok := ev.certCache[key]; ok {
		return cached
	}
	ev.deps.summary(key)
	ss, ok := ev.snapshot.summaries[key]
	valid := false
	if ok && certificatePresent(ss.Certificate, ss.Summary) {
		cert := ss.Certificate
		if ev.snapshot.keyRing != nil {
			valid = ev.snapshot.keyRing.Verify(ev.snapshot.manager, ss.Summary, cert) == nil
		} else {
			digest, err := SummaryDigest(ev.snapshot.manager, ss.Summary)
			valid = err == nil && digest == cert.Digest
		}
	}
	ev.certCache[key] = valid
	return valid
}

// summaryEquation computes one summary's current may-effect and may-unknown
// sets and reports summary keys referenced by feasible calls.
func (ev *evaluator) summaryEquation(key SummaryKey) (BDD, BDD, []SummaryKey, error) {
	m := ev.snapshot.manager
	effect := ev.false()
	unknown := ev.false()
	ss, ok := ev.snapshot.summaries[key]
	if !ok {
		return effect, unknown, nil, nil
	}
	ev.deps.summary(key)
	object := ss.Summary.Provenance.ObjectID()
	ev.deps.object(object)
	if ev.revoked.has(object) {
		effect = m.True()
	}

	// Compute handle liveness from all registration/unregistration guards in
	// this summary. Distinct handles are independent, so unregistering one
	// handle cannot suppress a second registration of the same callback.
	live := make(map[HandleID]*handleLive)
	var handleOrder []HandleID
	for _, event := range ss.Summary.Body {
		switch event.Kind {
		case EventRegister:
			guard := effectiveGuard(m, event.Guard)
			if guard.IsFalse() {
				continue
			}
			info := live[event.Handle]
			if info == nil {
				origin := ss.Summary.Provenance
				if event.Origin != nil {
					origin = *event.Origin
				}
				info = &handleLive{
					registered:   m.False(),
					unregistered: m.False(),
					origin:       origin,
					hasOrigin:    event.Origin != nil,
					symbol:       event.Target.Name,
				}
				live[event.Handle] = info
				handleOrder = append(handleOrder, event.Handle)
			}
			info.registered = info.registered.Or(guard)
		case EventUnregister:
			guard := effectiveGuard(m, event.Guard)
			if guard.IsFalse() {
				continue
			}
			info := live[event.Handle]
			if info == nil {
				info = &handleLive{registered: m.False(), unregistered: m.False()}
				live[event.Handle] = info
				handleOrder = append(handleOrder, event.Handle)
			}
			info.unregistered = info.unregistered.Or(guard)
		}
	}

	seenTargets := make(map[SummaryKey]bool)
	var targets []SummaryKey
	addTarget := func(target SummaryKey) {
		if target == "" || seenTargets[target] {
			return
		}
		seenTargets[target] = true
		targets = append(targets, target)
	}

	for _, event := range ss.Summary.Body {
		guard := effectiveGuard(m, event.Guard)
		if guard.IsFalse() {
			continue
		}
		switch event.Kind {
		case EventCall:
			ev.deps.symbol(event.Target.Name)
			for _, choice := range ev.resolve(event.Target.Name, guard) {
				if choice.status != selectionTarget {
					unknown = unknown.Or(choice.guard)
					continue
				}
				obj := ev.selectionObject(choice)
				ev.deps.object(obj)
				ev.deps.summary(choice.target)
				target, has := ev.snapshot.summaries[choice.target]
				if !has {
					unknown = unknown.Or(choice.guard)
					continue
				}
				if !ev.certEvidence(choice.target) {
					unknown = unknown.Or(choice.guard)
					continue
				}
				if !bindingMatchesSummary(choice.binding, target.Summary) {
					unknown = unknown.Or(choice.guard)
					continue
				}
				optional := ev.availability(choice.target, event.Target.Name)
				selected := choice.guard.And(optional)
				if selected.IsFalse() {
					continue
				}
				if ev.revoked.has(obj) {
					effect = effect.Or(selected)
				}
				addTarget(choice.target)
				effect = effect.Or(selected.And(ev.effectOf(choice.target)))
				unknown = unknown.Or(selected.And(ev.unknownOf(choice.target)))
			}
		case EventRegister:
			info := live[event.Handle]
			if info == nil {
				continue
			}
			originObject := info.origin.ObjectID()
			ev.deps.object(originObject)
			// Making a handle bound to a revoked object already touches that
			// object, even if the handle is later unregistered.
			if info.hasOrigin && ev.revoked.has(originObject) {
				effect = effect.Or(info.registered)
			}
			if !info.hasOrigin && info.symbol != "" {
				for _, choice := range ev.resolve(info.symbol, info.registered) {
					if choice.status != selectionTarget {
						continue
					}
					obj := ev.selectionObject(choice)
					ev.deps.object(obj)
					if ev.revoked.has(obj) {
						effect = effect.Or(choice.guard)
					}
				}
			}
		case EventUnregister:
			// Liveness was folded in above.
		case EventReturn:
			// Return evidence does not gate may-reachability in a declarative
			// summary. Call/return pairing is checked by the verifier.
		}
	}

	for _, handle := range handleOrder {
		info := live[handle]
		if info == nil {
			continue
		}
		liveGuard := info.registered.And(info.unregistered.Not())
		if liveGuard.IsFalse() {
			continue
		}
		originObject := info.origin.ObjectID()
		ev.deps.object(originObject)
		if info.symbol == "" {
			targetKey := info.origin.Key()
			ev.deps.summary(targetKey)
			ev.addCallbackEffect(&effect, &unknown, liveGuard, targetKey, Binding{}, originObject, "", addTarget)
			continue
		}
		ev.deps.symbol(info.symbol)
		for _, choice := range ev.resolve(info.symbol, liveGuard) {
			if choice.status != selectionTarget {
				unknown = unknown.Or(choice.guard)
				continue
			}
			obj := ev.selectionObject(choice)
			ev.deps.object(obj)
			ev.deps.summary(choice.target)
			if info.hasOrigin && obj.Key() != originObject.Key() {
				// The handle is bound to one exact object provenance.
				unknown = unknown.Or(choice.guard)
				continue
			}
			ev.addCallbackEffect(&effect, &unknown, choice.guard, choice.target, choice.binding, obj, info.symbol, addTarget)
		}
	}
	return effect, unknown, targets, nil
}

func (ev *evaluator) addCallbackEffect(effect, unknown *BDD, guard BDD, key SummaryKey, binding Binding, object ObjectID, symbol string, addTarget func(SummaryKey)) {
	ev.deps.summary(key)
	ev.deps.object(object)
	target, has := ev.snapshot.summaries[key]
	if !has {
		*unknown = unknown.Or(guard)
		return
	}
	if !ev.certEvidence(key) {
		*unknown = unknown.Or(guard)
		return
	}
	if binding.Target != "" || !binding.Provenance.IsZero() {
		if !bindingMatchesSummary(binding, target.Summary) {
			*unknown = unknown.Or(guard)
			return
		}
	}
	optional := ev.availability(key, symbol)
	selected := guard.And(optional)
	if selected.IsFalse() {
		return
	}
	if ev.revoked.has(object) {
		*effect = effect.Or(selected)
	}
	addTarget(key)
	*effect = effect.Or(selected.And(ev.effectOf(key)))
	*unknown = unknown.Or(selected.And(ev.unknownOf(key)))
}

func (ev *evaluator) availability(key SummaryKey, symbol string) BDD {
	ss, ok := ev.snapshot.summaries[key]
	if !ok || symbol == "" {
		return ev.true()
	}
	ev.deps.summary(key)
	availability := ev.false()
	found := false
	for _, optional := range ss.Summary.OptionalSymbols {
		if optional.Name != symbol {
			continue
		}
		found = true
		availability = availability.Or(effectiveGuard(ev.snapshot.manager, optional.Guard))
	}
	if !found {
		return ev.true()
	}
	return availability
}

func (ev *evaluator) selectionObject(choice selection) ObjectID {
	if !choice.binding.Object.IsZero() {
		return choice.binding.Object
	}
	if !choice.binding.Provenance.IsZero() {
		return choice.binding.Provenance.ObjectID()
	}
	if p, ok := parseProvenanceKey(choice.target); ok {
		return p.ObjectID()
	}
	if ss, ok := ev.snapshot.summaries[choice.target]; ok {
		return ss.Summary.Provenance.ObjectID()
	}
	return ObjectID{}
}

func bindingMatchesSummary(binding Binding, summary Summary) bool {
	if !binding.Provenance.IsZero() && binding.Provenance != summary.Provenance {
		return false
	}
	if !binding.Object.IsZero() && binding.Object != summary.Provenance.ObjectID() {
		return false
	}
	if binding.Target != "" && binding.Target != summary.Key() {
		return false
	}
	return true
}

// resolve partitions configurations by the exact binding selected for a
// symbol. Strong bindings win over weak bindings. Two distinct enabled
// targets of the same strength are ambiguous evidence.
func (ev *evaluator) resolve(symbol string, base BDD) []selection {
	m := ev.snapshot.manager
	bindings := ev.snapshot.bindings[symbol]
	if len(bindings) == 0 {
		return []selection{{guard: base, status: selectionMissingBinding}}
	}

	strong := make(map[SummaryKey]*bindingTargetInfo)
	weak := make(map[SummaryKey]*bindingTargetInfo)
	for _, binding := range bindings {
		binding := binding
		target := binding.EffectiveTarget()
		enabled := base.And(effectiveGuard(m, binding.Guard))
		if enabled.IsFalse() {
			continue
		}
		table := strong
		if binding.Weak {
			table = weak
		}
		if existing, ok := table[target]; ok {
			existing.guard = existing.guard.Or(enabled)
		} else {
			table[target] = &bindingTargetInfo{guard: enabled, binding: binding}
		}
	}
	strongAny := m.False()
	for _, info := range strong {
		strongAny = strongAny.Or(info.guard)
	}
	strongAmbig := m.False()
	strongTargets := sortedTargets(strong)
	for i := 0; i < len(strongTargets); i++ {
		for j := i + 1; j < len(strongTargets); j++ {
			strongAmbig = strongAmbig.Or(strong[strongTargets[i]].guard.And(strong[strongTargets[j]].guard))
		}
	}
	weakAmbig := m.False()
	weakTargets := sortedTargets(weak)
	for i := 0; i < len(weakTargets); i++ {
		for j := i + 1; j < len(weakTargets); j++ {
			weakAmbig = weakAmbig.Or(weak[weakTargets[i]].guard.And(weak[weakTargets[j]].guard))
		}
	}

	var out []selection
	for _, target := range strongTargets {
		guard := strong[target].guard.And(strongAmbig.Not())
		if guard.IsFalse() {
			continue
		}
		out = append(out, selection{
			guard:   guard,
			target:  target,
			binding: strong[target].binding,
			status:  selectionTarget,
		})
	}
	for _, target := range weakTargets {
		guard := weak[target].guard.And(strongAny.Not()).And(weakAmbig.Not())
		if guard.IsFalse() {
			continue
		}
		out = append(out, selection{
			guard:   guard,
			target:  target,
			binding: weak[target].binding,
			status:  selectionTarget,
		})
	}
	ambiguous := strongAmbig.Or(strongAny.Not().And(weakAmbig))
	if !ambiguous.IsFalse() {
		out = append(out, selection{guard: ambiguous, status: selectionAmbiguous})
	}
	return out
}

func sortedTargets(table map[SummaryKey]*bindingTargetInfo) []SummaryKey {
	keys := make([]SummaryKey, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// analyzeEntry runs the least fixed point for one entry.
func analyzeEntry(snapshot *Snapshot, revoked *revocations, entry SummaryKey) (Result, *dependencies, int, int, error) {
	m := snapshot.manager
	ev := newEvaluator(snapshot, revoked)
	rootObject := ev.rootObject(entry)
	ev.deps.summary(entry)
	ev.deps.object(rootObject)

	_, hasSummary := snapshot.summaries[entry]
	certOK := hasSummary && ev.certEvidence(entry)
	affected := m.False()
	unknown := m.False()

	if ev.revoked.has(rootObject) {
		affected = m.True()
	}
	if !hasSummary {
		if !affected.IsTrue() {
			unknown = m.True()
		}
	} else if !certOK {
		if !affected.IsTrue() {
			unknown = m.True()
		}
	} else {
		ev.known[entry] = true
		for {
			if ev.iterations > 100000 {
				return Result{}, ev.deps, ev.stateCount, ev.iterations,
					fmt.Errorf("impact: analysis did not converge")
			}
			ev.iterations++
			changed := false
			keys := sortedSummaryKeys(ev.known)
			for _, key := range keys {
				effect, unknownSet, targets, err := ev.summaryEquation(key)
				if err != nil {
					return Result{}, ev.deps, ev.stateCount, ev.iterations, err
				}
				if old, ok := ev.effects[key]; !ok {
					ev.effects[key] = effect
					ev.unknowns[key] = unknownSet
					changed = true
				} else {
					mergedEffect := old.Or(effect)
					mergedUnknown := ev.unknowns[key].Or(unknownSet)
					if !mergedEffect.Equal(old) || !mergedUnknown.Equal(ev.unknowns[key]) {
						ev.effects[key] = mergedEffect
						ev.unknowns[key] = mergedUnknown
						changed = true
					}
				}
				for _, target := range targets {
					if _, ok := snapshot.summaries[target]; !ok {
						continue
					}
					if !ev.certEvidence(target) {
						continue
					}
					if !ev.known[target] {
						ev.known[target] = true
						changed = true
					}
				}
				ev.stateCount++
			}
			if !changed {
				break
			}
		}
		affected = affected.Or(ev.effectOf(entry))
		unknown = unknown.Or(ev.unknownOf(entry))
	}
	affected = affected.And(snapshot.valid)
	unknown = unknown.And(snapshot.valid).And(affected.Not())
	if err := affected.IsCanonical(); err != nil {
		return Result{}, ev.deps, ev.stateCount, ev.iterations, err
	}
	if err := unknown.IsCanonical(); err != nil {
		return Result{}, ev.deps, ev.stateCount, ev.iterations, err
	}
	return Result{Entry: entry, Affected: affected, Unknown: unknown}, ev.deps, ev.stateCount, ev.iterations, nil
}

func sortedSummaryKeys(keys map[SummaryKey]bool) []SummaryKey {
	out := make([]SummaryKey, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Analyze computes impact for every entry without an incremental cache.
func Analyze(snapshot *Snapshot, revoked []ObjectID) (*Analysis, error) {
	if snapshot == nil {
		return nil, errors.New("impact: nil snapshot")
	}
	engine, err := NewEngine(snapshot)
	if err != nil {
		return nil, err
	}
	engine.RevokeObjects(revoked...)
	return engine.Analyze()
}

// AnalyzeSnapshot is an alias for Analyze.
func AnalyzeSnapshot(snapshot *Snapshot, revoked []ObjectID) (*Analysis, error) {
	return Analyze(snapshot, revoked)
}

func newAnalysis(m *Manager, valid BDD, results []Result) *Analysis {
	byEntry := make(map[SummaryKey]Result, len(results))
	for _, r := range results {
		byEntry[r.Entry] = r
	}
	return &Analysis{
		manager: m,
		valid:   valid,
		results: append([]Result(nil), results...),
		byEntry: byEntry,
	}
}

// parseProvenanceKey reverses Provenance.Key.
func parseProvenanceKey(key SummaryKey) (Provenance, bool) {
	parts, ok := splitKey(string(key), 6)
	if !ok {
		return Provenance{}, false
	}
	return Provenance{
		Module:  parts[0],
		Version: parts[1],
		Package: parts[2],
		Object:  parts[3],
		Symbol:  parts[4],
		BuildID: parts[5],
	}, true
}

func splitKey(s string, count int) ([]string, bool) {
	parts := make([]string, 0, count)
	pos := 0
	for len(parts) < count {
		colon := strings.IndexByte(s[pos:], ':')
		if colon <= 0 {
			return nil, false
		}
		length := 0
		for _, c := range s[pos : pos+colon] {
			if c < '0' || c > '9' {
				return nil, false
			}
			length = length*10 + int(c-'0')
		}
		start := pos + colon + 1
		if length < 0 || start+length > len(s) {
			return nil, false
		}
		parts = append(parts, s[start:start+length])
		pos = start + length
	}
	return parts, pos == len(s)
}

// ProvenanceForKey returns the provenance encoded in a summary key.
func ProvenanceForKey(key SummaryKey) (Provenance, bool) {
	return parseProvenanceKey(key)
}
