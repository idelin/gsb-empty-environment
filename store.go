package diag

import "sort"

// Store aggregates diagnostics contributed by multiple analysis
// phases. Each phase owns its contribution set; refreshing a phase
// replaces exactly that set and leaves every other phase untouched.
// Merging is by identity (Key), never by current-text range, so stale
// responses bound to older analysis versions stay distinct from fresh
// ones even when their ranges coincide after mapping.
type Store struct {
	phases map[string]map[Key]Diagnostic
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{phases: make(map[string]map[Key]Diagnostic)}
}

// Refresh replaces all contributions of phase with diags, which the
// phase computed against file version version. Duplicate identities
// within one response collapse to a single contribution keeping the
// maximum severity. Refreshing with an empty slice revokes the phase's
// contributions entirely. A refresh at a newer file version replaces
// the phase's older-version identities wholesale.
func (s *Store) Refresh(phase string, version FileVersion, diags []Diagnostic) {
	set := make(map[Key]Diagnostic, len(diags))
	for _, d := range diags {
		k := Key{Version: version, RuleID: d.RuleID, Range: d.Range}
		if prev, ok := set[k]; !ok || d.Severity > prev.Severity {
			set[k] = d
		}
	}
	if len(set) == 0 {
		delete(s.phases, phase)
		return
	}
	s.phases[phase] = set
}

// Snapshot merges every phase's contributions by identity and maps the
// surviving ranges to doc's current version. Contributions whose
// original range intersects an edit since their analysis version are
// dropped; the rest keep their identity. The result is sorted
// deterministically by (range, rule id, origin version).
func (s *Store) Snapshot(doc *Document) []Merged {
	names := make([]string, 0, len(s.phases))
	for name := range s.phases {
		names = append(names, name)
	}
	sort.Strings(names)

	acc := make(map[Key]*Merged)
	for _, name := range names {
		for k, d := range s.phases[name] {
			mapped, ok := doc.MapRange(k.Version, k.Range)
			if !ok {
				continue
			}
			m, ok := acc[k]
			if !ok {
				m = &Merged{
					RuleID:   k.RuleID,
					Range:    mapped,
					Severity: d.Severity,
					Message:  d.Message,
					Origin:   k,
				}
				acc[k] = m
			} else if d.Severity > m.Severity {
				m.Severity = d.Severity
				m.Message = d.Message
			}
			m.Phases = append(m.Phases, name)
		}
	}

	out := make([]Merged, 0, len(acc))
	for _, m := range acc {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return mergedLess(out[i], out[j]) })
	return out
}

func mergedLess(a, b Merged) bool {
	if a.Range.Start != b.Range.Start {
		return a.Range.Start < b.Range.Start
	}
	if a.Range.End != b.Range.End {
		return a.Range.End < b.Range.End
	}
	if a.RuleID != b.RuleID {
		return a.RuleID < b.RuleID
	}
	return a.Origin.Version < b.Origin.Version
}
