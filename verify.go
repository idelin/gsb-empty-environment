package impact

import (
	"errors"
	"fmt"
	"sort"
)

// Verifier independently checks analysis results by enumerating legal
// configurations and interpreting the summaries concretely. It also checks
// certificate provenance and structural call/return pairing.
type Verifier struct {
	// MaxConfigs bounds enumeration. The default is 1<<20, the full space for
	// the maximum of 20 options.
	MaxConfigs int
	// KeyRing, when set, is used for certificate verification. Otherwise the
	// snapshot key ring is used.
	KeyRing *KeyRing
}

// NewVerifier returns a verifier with full 20-variable enumeration enabled.
func NewVerifier() *Verifier {
	return &Verifier{MaxConfigs: 1 << MaxVariables}
}

// VerifySnapshot checks structural invariants, provenance identities, and
// call/return pairing.
func (v *Verifier) VerifySnapshot(snapshot *Snapshot) error {
	if snapshot == nil {
		return errors.New("impact: nil snapshot")
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	for key, ss := range snapshot.summaries {
		if key != ss.Summary.Key() {
			return fmt.Errorf("impact: summary key %q does not match provenance %q", key, ss.Summary.Key())
		}
		if ss.Certificate != nil && ss.Certificate.Provenance != ss.Summary.Provenance {
			return fmt.Errorf("impact: summary %q has a certificate for provenance %q", key, ss.Certificate.Provenance)
		}
		if err := verifySummaryPairing(snapshot, ss.Summary); err != nil {
			return err
		}
	}
	for symbol, bindings := range snapshot.bindings {
		for i, binding := range bindings {
			if !binding.Provenance.IsZero() && binding.EffectiveTarget() != binding.Provenance.Key() {
				return fmt.Errorf("impact: binding %q[%d] borrows provenance %q for target %q",
					symbol, i, binding.Provenance, binding.EffectiveTarget())
			}
			if !binding.Object.IsZero() && !binding.Provenance.IsZero() && binding.Object != binding.Provenance.ObjectID() {
				return fmt.Errorf("impact: binding %q[%d] object does not match provenance", symbol, i)
			}
		}
	}
	return nil
}

// VerifyCertificates performs strict certificate verification. Invalid or
// missing certificates are evidence errors for analysis, so they are not
// rejected by VerifySnapshot; callers that require a fully signed snapshot
// should call this method.
func (v *Verifier) VerifyCertificates(snapshot *Snapshot) error {
	if v == nil {
		v = NewVerifier()
	}
	if snapshot == nil {
		return errors.New("impact: nil snapshot")
	}
	return snapshot.VerifyCertificates(v.KeyRing)
}

func verifySummaryPairing(snapshot *Snapshot, summary Summary) error {
	callSites := make(map[SiteID]bool)
	returnSites := make(map[SiteID]bool)
	for _, event := range summary.Body {
		switch event.Kind {
		case EventCall:
			if event.Site == "" {
				return fmt.Errorf("impact: %s has a call without a site", summary.Provenance)
			}
			if callSites[event.Site] {
				return fmt.Errorf("impact: %s has duplicate call site %q", summary.Provenance, event.Site)
			}
			callSites[event.Site] = true
		case EventReturn:
			if event.Site != "" {
				if returnSites[event.Site] {
					return fmt.Errorf("impact: %s has duplicate return site %q", summary.Provenance, event.Site)
				}
				returnSites[event.Site] = true
			}
		case EventRegister:
			if event.ReturnTo != "" && event.ReturnTo != event.Site {
				return fmt.Errorf("impact: %s callback %q returns to %q instead of %q",
					summary.Provenance, event.Handle, event.ReturnTo, event.Site)
			}
		}
	}
	// A call may name the return site it expects in the callee. When possible,
	// verify every enabled target has that return site.
	for _, event := range summary.Body {
		if event.Kind != EventCall || event.ReturnTo == "" {
			continue
		}
		found := false
		anyTarget := false
		for _, binding := range snapshot.bindings[event.Target.Name] {
			target, ok := snapshot.summaries[binding.EffectiveTarget()]
			if !ok {
				continue
			}
			anyTarget = true
			for _, targetEvent := range target.Summary.Body {
				if targetEvent.Kind == EventReturn && targetEvent.Site == event.ReturnTo {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if anyTarget && !found {
			return fmt.Errorf("impact: %s call %q names unpaired return site %q",
				summary.Provenance, event.Site, event.ReturnTo)
		}
	}
	return nil
}

// VerifyAnalysis recomputes each entry by full configuration enumeration and
// compares the resulting sets with the analysis BDDs.
func (v *Verifier) VerifyAnalysis(snapshot *Snapshot, revoked []ObjectID, analysis *Analysis) error {
	if v == nil {
		v = NewVerifier()
	}
	if snapshot == nil || analysis == nil {
		return errors.New("impact: nil snapshot or analysis")
	}
	if err := v.VerifySnapshot(snapshot); err != nil {
		return err
	}
	if analysis.manager != snapshot.manager {
		return errors.New("impact: analysis and snapshot use different BDD managers")
	}
	if !analysis.valid.Equal(snapshot.valid) {
		return errors.New("impact: analysis valid constraint differs from snapshot")
	}
	revokedSet := newRevocations(revoked)
	entries := snapshot.Entries()
	if len(entries) != len(analysis.results) {
		return fmt.Errorf("impact: analysis has %d results for %d entries", len(analysis.results), len(entries))
	}
	seen := make(map[SummaryKey]bool)
	for _, entry := range entries {
		if seen[entry] {
			return fmt.Errorf("impact: duplicate entry %q", entry)
		}
		seen[entry] = true
		result, ok := analysis.Result(entry)
		if !ok {
			return fmt.Errorf("impact: missing result for entry %q", entry)
		}
		if result.Affected.m != snapshot.manager || result.Unknown.m != snapshot.manager {
			return fmt.Errorf("impact: entry %q result uses a different BDD manager", entry)
		}
		if err := result.Affected.IsCanonical(); err != nil {
			return fmt.Errorf("impact: entry %q affected BDD: %w", entry, err)
		}
		if err := result.Unknown.IsCanonical(); err != nil {
			return fmt.Errorf("impact: entry %q unknown BDD: %w", entry, err)
		}
		if !result.Affected.Leq(snapshot.valid) || !result.Unknown.Leq(snapshot.valid) {
			return fmt.Errorf("impact: entry %q result is not restricted to valid configurations", entry)
		}
		if !result.Affected.And(result.Unknown).IsFalse() {
			return fmt.Errorf("impact: entry %q Affected and Unknown overlap", entry)
		}
		affected, unknown, err := v.enumerateEntry(snapshot, revokedSet, entry)
		if err != nil {
			return err
		}
		if !result.Affected.Equal(affected) {
			return fmt.Errorf("impact: entry %q affected BDD mismatch: got %s want %s",
				entry, result.Affected.String(), affected.String())
		}
		if !result.Unknown.Equal(unknown) {
			return fmt.Errorf("impact: entry %q unknown BDD mismatch: got %s want %s",
				entry, result.Unknown.String(), unknown.String())
		}
	}
	for _, result := range analysis.results {
		if !seen[result.Entry] {
			return fmt.Errorf("impact: analysis contains an unexpected entry %q", result.Entry)
		}
	}
	return nil
}

func (v *Verifier) enumerateEntry(snapshot *Snapshot, revoked *revocations, entry SummaryKey) (BDD, BDD, error) {
	m := snapshot.manager
	if v.MaxConfigs <= 0 {
		v.MaxConfigs = 1 << MaxVariables
	}
	if snapshot.valid.SatCount() > uint64(v.MaxConfigs) {
		return BDD{}, BDD{}, fmt.Errorf("impact: verification requires %d configurations, limit is %d",
			snapshot.valid.SatCount(), v.MaxConfigs)
	}
	assignments := snapshot.valid.Enumerate()
	affected := m.False()
	unknown := m.False()
	for _, assignment := range assignments {
		a, u := concreteEntry(snapshot, revoked, entry, assignment.Values)
		cube := assignmentCube(m, assignment.Values)
		if a {
			affected = affected.Or(cube)
		}
		if u && !a {
			unknown = unknown.Or(cube)
		}
	}
	return affected, unknown, nil
}

func assignmentCube(m *Manager, values []bool) BDD {
	cube := m.True()
	for i := range m.vars {
		value := false
		if i < len(values) {
			value = values[i]
		}
		literal := BDD{m: m, id: m.mk(i, 0, 1)}
		if !value {
			literal = literal.Not()
		}
		cube = cube.And(literal)
	}
	return cube
}

// concreteEntry is a separate, boolean interpreter used as the oracle.
func concreteEntry(snapshot *Snapshot, revoked *revocations, entry SummaryKey, values []bool) (bool, bool) {
	rootObject := ObjectID{}
	if p, ok := parseProvenanceKey(entry); ok {
		rootObject = p.ObjectID()
	}
	_, hasRoot := snapshot.summaries[entry]
	certOK := hasRoot && concreteCertOK(snapshot, entry)
	if revoked.has(rootObject) {
		return true, false
	}
	if !hasRoot || !certOK {
		return false, true
	}

	// Least fixed point over all summaries. This is intentionally a separate
	// implementation from the symbolic evaluator.
	effects := make(map[SummaryKey]bool)
	unknowns := make(map[SummaryKey]bool)
	for {
		changed := false
		keys := make([]string, 0, len(snapshot.summaries))
		for key := range snapshot.summaries {
			keys = append(keys, string(key))
		}
		sort.Strings(keys)
		for _, raw := range keys {
			key := SummaryKey(raw)
			e, u := concreteEquation(snapshot, revoked, key, values, effects, unknowns)
			if e && !effects[key] {
				effects[key] = true
				changed = true
			}
			if u && !unknowns[key] {
				unknowns[key] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return effects[entry], unknowns[entry] && !effects[entry]
}

type concreteSelection struct {
	status  selectionStatus
	target  SummaryKey
	binding Binding
	object  ObjectID
}

func concreteResolve(snapshot *Snapshot, symbol string, values []bool) concreteSelection {
	bindings := snapshot.bindings[symbol]
	if len(bindings) == 0 {
		return concreteSelection{status: selectionMissingBinding}
	}
	strong := make(map[SummaryKey]Binding)
	weak := make(map[SummaryKey]Binding)
	for _, binding := range bindings {
		if !effectiveGuard(snapshot.manager, binding.Guard).Eval(values) {
			continue
		}
		target := binding.EffectiveTarget()
		if binding.Weak {
			if _, ok := weak[target]; !ok {
				weak[target] = binding
			}
		} else {
			if _, ok := strong[target]; !ok {
				strong[target] = binding
			}
		}
	}
	pick := func(table map[SummaryKey]Binding) (Binding, SummaryKey, bool, bool) {
		if len(table) == 0 {
			return Binding{}, "", false, false
		}
		keys := make([]string, 0, len(table))
		for key := range table {
			keys = append(keys, string(key))
		}
		sort.Strings(keys)
		if len(keys) > 1 {
			return Binding{}, "", false, true
		}
		key := SummaryKey(keys[0])
		return table[key], key, true, false
	}
	if binding, target, ok, ambiguous := pick(strong); ambiguous {
		return concreteSelection{status: selectionAmbiguous}
	} else if ok {
		return concreteSelection{status: selectionTarget, target: target, binding: binding, object: concreteBindingObject(snapshot, binding, target)}
	}
	if binding, target, ok, ambiguous := pick(weak); ambiguous {
		return concreteSelection{status: selectionAmbiguous}
	} else if ok {
		return concreteSelection{status: selectionTarget, target: target, binding: binding, object: concreteBindingObject(snapshot, binding, target)}
	}
	return concreteSelection{status: selectionDisabled}
}

func concreteBindingObject(snapshot *Snapshot, binding Binding, target SummaryKey) ObjectID {
	if !binding.Object.IsZero() {
		return binding.Object
	}
	if !binding.Provenance.IsZero() {
		return binding.Provenance.ObjectID()
	}
	if p, ok := parseProvenanceKey(target); ok {
		return p.ObjectID()
	}
	if ss, ok := snapshot.summaries[target]; ok {
		return ss.Summary.Provenance.ObjectID()
	}
	return ObjectID{}
}

func concreteEquation(snapshot *Snapshot, revoked *revocations, key SummaryKey, values []bool,
	effects, unknowns map[SummaryKey]bool) (bool, bool) {
	ss, ok := snapshot.summaries[key]
	if !ok {
		return false, false
	}
	effect := revoked.has(ss.Summary.Provenance.ObjectID())
	unknown := false

	type live struct {
		registered   bool
		unregistered bool
		origin       Provenance
		hasOrigin    bool
		symbol       string
	}
	handles := make(map[HandleID]*live)
	var order []HandleID
	for _, event := range ss.Summary.Body {
		switch event.Kind {
		case EventRegister:
			if !effectiveGuard(snapshot.manager, event.Guard).Eval(values) {
				continue
			}
			info := handles[event.Handle]
			if info == nil {
				origin := ss.Summary.Provenance
				if event.Origin != nil {
					origin = *event.Origin
				}
				info = &live{origin: origin, hasOrigin: event.Origin != nil, symbol: event.Target.Name}
				handles[event.Handle] = info
				order = append(order, event.Handle)
			}
			info.registered = true
		case EventUnregister:
			if !effectiveGuard(snapshot.manager, event.Guard).Eval(values) {
				continue
			}
			info := handles[event.Handle]
			if info == nil {
				info = &live{}
				handles[event.Handle] = info
				order = append(order, event.Handle)
			}
			info.unregistered = true
		}
	}

	for _, event := range ss.Summary.Body {
		if !effectiveGuard(snapshot.manager, event.Guard).Eval(values) {
			continue
		}
		switch event.Kind {
		case EventCall:
			selection := concreteResolve(snapshot, event.Target.Name, values)
			switch selection.status {
			case selectionMissingBinding, selectionAmbiguous:
				unknown = true
			case selectionDisabled:
			case selectionTarget:
				target, has := snapshot.summaries[selection.target]
				if !has || !concreteCertOK(snapshot, selection.target) ||
					!bindingMatchesSummary(selection.binding, target.Summary) {
					unknown = true
					continue
				}
				if !concreteAvailability(snapshot, selection.target, event.Target.Name, values) {
					continue
				}
				if revoked.has(selection.object) {
					effect = true
				}
				if effects[selection.target] {
					effect = true
				}
				if unknowns[selection.target] {
					unknown = true
				}
			}
		case EventRegister:
			info := handles[event.Handle]
			if info != nil && info.hasOrigin && revoked.has(info.origin.ObjectID()) {
				effect = true
			}
			if info != nil && !info.hasOrigin && info.symbol != "" {
				selection := concreteResolve(snapshot, info.symbol, values)
				if selection.status == selectionTarget && revoked.has(selection.object) {
					effect = true
				}
			}
		}
	}

	for _, handle := range order {
		info := handles[handle]
		if info == nil || !info.registered || info.unregistered {
			continue
		}
		originObject := info.origin.ObjectID()
		applyCallback := func(target SummaryKey, binding Binding, object ObjectID, symbol string) {
			targetSummary, has := snapshot.summaries[target]
			if !has || !concreteCertOK(snapshot, target) ||
				!bindingMatchesSummary(binding, targetSummary.Summary) {
				unknown = true
				return
			}
			if !concreteAvailability(snapshot, target, symbol, values) {
				return
			}
			if revoked.has(object) {
				effect = true
			}
			if effects[target] {
				effect = true
			}
			if unknowns[target] {
				unknown = true
			}
		}
		if info.symbol == "" {
			applyCallback(info.origin.Key(), Binding{}, originObject, "")
			continue
		}
		selection := concreteResolve(snapshot, info.symbol, values)
		switch selection.status {
		case selectionMissingBinding, selectionAmbiguous:
			unknown = true
		case selectionDisabled:
		case selectionTarget:
			if info.hasOrigin && selection.object.Key() != originObject.Key() {
				unknown = true
				continue
			}
			applyCallback(selection.target, selection.binding, selection.object, info.symbol)
		}
	}
	return effect, unknown
}

func concreteAvailability(snapshot *Snapshot, key SummaryKey, symbol string, values []bool) bool {
	if symbol == "" {
		return true
	}
	ss, ok := snapshot.summaries[key]
	if !ok {
		return true
	}
	found := false
	available := false
	for _, optional := range ss.Summary.OptionalSymbols {
		if optional.Name != symbol {
			continue
		}
		found = true
		if effectiveGuard(snapshot.manager, optional.Guard).Eval(values) {
			available = true
		}
	}
	return !found || available
}

func concreteCertOK(snapshot *Snapshot, key SummaryKey) bool {
	ss, ok := snapshot.summaries[key]
	if !ok || !certificatePresent(ss.Certificate, ss.Summary) {
		return false
	}
	if snapshot.keyRing != nil {
		return snapshot.keyRing.Verify(snapshot.manager, ss.Summary, ss.Certificate) == nil
	}
	digest, err := SummaryDigest(snapshot.manager, ss.Summary)
	return err == nil && digest == ss.Certificate.Digest
}
