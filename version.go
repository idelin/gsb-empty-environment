package impact

import (
	"errors"
	"fmt"
	"sort"
)

// SummaryChangeKind classifies a summary between two snapshots.
type SummaryChangeKind string

const (
	SummaryAdded    SummaryChangeKind = "added"
	SummaryRemoved  SummaryChangeKind = "removed"
	SummaryModified SummaryChangeKind = "modified"
)

// ChangeSet is a semantic diff between two snapshots. Object-level changes are
// derived from exact provenance, never from a symbol name.
type ChangeSet struct {
	Summaries      map[SummaryKey]SummaryChangeKind
	Bindings       []string
	Objects        []ObjectID
	ValidChanged   bool
	EntriesAdded   []SummaryKey
	EntriesRemoved []SummaryKey
}

// IsEmpty reports whether the snapshots have the same summaries, bindings, and
// entries.
func (c *ChangeSet) IsEmpty() bool {
	return c == nil || (len(c.Summaries) == 0 && len(c.Bindings) == 0 && !c.ValidChanged &&
		len(c.EntriesAdded) == 0 && len(c.EntriesRemoved) == 0)
}

// CompareSnapshots computes a semantic diff. Both snapshots must use the same
// fixed variable order; BDD node identities need not match.
func CompareSnapshots(old, new *Snapshot) (*ChangeSet, error) {
	if old == nil || new == nil {
		return nil, errors.New("impact: compare requires two snapshots")
	}
	if err := old.Validate(); err != nil {
		return nil, fmt.Errorf("impact: old snapshot: %w", err)
	}
	if err := new.Validate(); err != nil {
		return nil, fmt.Errorf("impact: new snapshot: %w", err)
	}
	if old.manager != new.manager {
		oldVars := old.manager.Variables()
		newVars := new.manager.Variables()
		if len(oldVars) != len(newVars) {
			return nil, errors.New("impact: snapshots use different build-option counts")
		}
		for i := range oldVars {
			if oldVars[i] != newVars[i] {
				return nil, errors.New("impact: snapshots use different build-option order")
			}
		}
	}
	change := &ChangeSet{Summaries: make(map[SummaryKey]SummaryChangeKind)}
	change.ValidChanged = old.valid.CanonicalKey() != new.valid.CanonicalKey()
	objects := make(map[string]ObjectID)
	keys := make(map[SummaryKey]bool)
	for key := range old.summaries {
		keys[key] = true
	}
	for key := range new.summaries {
		keys[key] = true
	}
	for key := range keys {
		oldSummary, inOld := old.summaries[key]
		newSummary, inNew := new.summaries[key]
		switch {
		case inOld && !inNew:
			change.Summaries[key] = SummaryRemoved
			objects[oldSummary.Summary.Provenance.ObjectID().Key()] = oldSummary.Summary.Provenance.ObjectID()
		case !inOld && inNew:
			change.Summaries[key] = SummaryAdded
			objects[newSummary.Summary.Provenance.ObjectID().Key()] = newSummary.Summary.Provenance.ObjectID()
		case inOld && inNew:
			same, err := sameSummary(old.manager, oldSummary.Summary, new.manager, newSummary.Summary)
			if err != nil {
				return nil, err
			}
			if !same {
				change.Summaries[key] = SummaryModified
				objects[newSummary.Summary.Provenance.ObjectID().Key()] = newSummary.Summary.Provenance.ObjectID()
			}
		}
	}
	symbols := make(map[string]bool)
	for symbol := range old.bindings {
		symbols[symbol] = true
	}
	for symbol := range new.bindings {
		symbols[symbol] = true
	}
	for symbol := range symbols {
		if !sameBindings(old.bindings[symbol], new.bindings[symbol]) {
			change.Bindings = append(change.Bindings, symbol)
			for _, binding := range old.bindings[symbol] {
				if object := binding.EffectiveObject(); !object.IsZero() {
					objects[object.Key()] = object
				}
			}
			for _, binding := range new.bindings[symbol] {
				if object := binding.EffectiveObject(); !object.IsZero() {
					objects[object.Key()] = object
				}
			}
		}
	}
	sort.Strings(change.Bindings)
	for _, object := range objects {
		change.Objects = append(change.Objects, object)
	}
	sort.Slice(change.Objects, func(i, j int) bool {
		return change.Objects[i].Key() < change.Objects[j].Key()
	})
	change.EntriesAdded = diffEntries(old.entries, new.entries)
	change.EntriesRemoved = diffEntries(new.entries, old.entries)
	return change, nil
}

func sameSummary(oldManager *Manager, oldSummary Summary, newManager *Manager, newSummary Summary) (bool, error) {
	if oldSummary.Provenance != newSummary.Provenance {
		return false, nil
	}
	oldDigest, err := SummaryDigest(oldManager, oldSummary)
	if err != nil {
		return false, err
	}
	newDigest, err := SummaryDigest(newManager, newSummary)
	if err != nil {
		return false, err
	}
	return oldDigest == newDigest, nil
}

func sameBindings(a, b []Binding) bool {
	if len(a) != len(b) {
		return false
	}
	keysA := canonicalBindings(a)
	keysB := canonicalBindings(b)
	for i := range keysA {
		if keysA[i] != keysB[i] {
			return false
		}
	}
	return true
}

func canonicalBindings(bindings []Binding) []string {
	out := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, fmt.Sprintf("%s|%s|%s|%t|%s",
			binding.EffectiveTarget(), binding.EffectiveObject().Key(),
			binding.Provenance.Key(), binding.Weak, canonicalGuardKey(binding.Guard)))
	}
	sort.Strings(out)
	return out
}

func diffEntries(a, b []SummaryKey) []SummaryKey {
	inB := make(map[SummaryKey]bool, len(b))
	for _, key := range b {
		inB[key] = true
	}
	var out []SummaryKey
	for _, key := range a {
		if !inB[key] {
			out = append(out, key)
		}
	}
	return out
}

// NewEngineFromVersions builds an engine over the new snapshot and returns the
// semantic diff from the old snapshot. Revocations are applied before the first
// analysis.
func NewEngineFromVersions(old, new *Snapshot, revoked []ObjectID) (*Engine, *ChangeSet, error) {
	change, err := CompareSnapshots(old, new)
	if err != nil {
		return nil, nil, err
	}
	engine, err := NewEngineWithRevocations(new, revoked...)
	if err != nil {
		return nil, nil, err
	}
	return engine, change, nil
}

// AnalyzeVersions compares two snapshots, analyzes the new version under the
// supplied revocations, and returns both the change set and impact results.
func AnalyzeVersions(old, new *Snapshot, revoked []ObjectID) (*Analysis, *ChangeSet, error) {
	engine, change, err := NewEngineFromVersions(old, new, revoked)
	if err != nil {
		return nil, nil, err
	}
	analysis, err := engine.Analyze()
	if err != nil {
		return nil, nil, err
	}
	return analysis, change, nil
}
