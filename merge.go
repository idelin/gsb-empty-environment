package netsec

import (
	"fmt"
	"sort"
)

// MergeSessions merges two chains conservatively.  A duplicate key is only
// acceptable when both branches agree exactly and neither branch treats it as
// fixed.  Transitivity is intersected for shared labels, so a merge cannot
// upgrade a non-transitive label.
func MergeSessions(left, right *Session) (*Session, error) {
	return MergeSessionsWithConfig(left, right, AuditConfig{})
}

// MergeAudited merges two chains with the default audited merge flaws.
func MergeAudited(left, right *Session) (*Session, error) {
	return MergeSessionsWithConfig(left, right, DefaultAuditConfig())
}

// MergeSessionsWithConfig is the configurable merge used by tests and audit
// tooling.
func MergeSessionsWithConfig(left, right *Session, cfg AuditConfig) (*Session, error) {
	if left == nil || right == nil {
		return nil, fmt.Errorf("%w: nil merge input", ErrInvalidInput)
	}
	out := &Session{
		Labels:        make(map[string]Label, len(left.Labels)+len(right.Labels)),
		Transitive:    make(map[string]bool),
		NonTransitive: make(map[string]bool),
		Depth:         maxInt(left.Depth, right.Depth),
		RoleIDs:       mergeRoles(left.RoleIDs, right.RoleIDs),
		Proofs:        make(map[string]Proof),
	}
	for k, v := range left.NonTransitive {
		out.NonTransitive[k] = v
	}
	for k, v := range right.NonTransitive {
		out.NonTransitive[k] = v
	}
	for k, l := range left.Labels {
		if !l.Transitive {
			out.NonTransitive[k] = true
		}
	}
	for k, l := range right.Labels {
		if !l.Transitive {
			out.NonTransitive[k] = true
		}
	}

	type sideLabel struct {
		label Label
		side  string
	}
	combined := make(map[string][]sideLabel)
	for _, key := range sortedLabelKeys(left.Labels) {
		combined[key] = append(combined[key], sideLabel{label: left.Labels[key].clone(), side: "left"})
	}
	for _, rawKey := range sortedLabelKeys(right.Labels) {
		key := rawKey
		if cfg.has(FlawCaseFoldKeys) {
			key = auditKey(cfg, rawKey)
		}
		combined[key] = append(combined[key], sideLabel{label: right.Labels[rawKey].clone(), side: "right"})
	}

	keys := make([]string, 0, len(combined))
	for key := range combined {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		entries := combined[key]
		if len(entries) == 1 {
			l := entries[0].label
			l.Key = key
			out.Labels[key] = l
			if l.Transitive {
				out.Transitive[key] = true
				if cfg.has(FlawMergeTransitiveUnion) {
					delete(out.NonTransitive, key)
				}
			} else {
				out.NonTransitive[key] = true
			}
			continue
		}

		// Duplicate key.
		lastWins := cfg.has(FlawMergeLastWins)
		if !lastWins {
			for _, e := range entries {
				if e.label.Fixed {
					return nil, fmt.Errorf("%w: merged key %q is fixed", ErrFixedConflict, key)
				}
			}
			first := entries[0].label
			for _, e := range entries[1:] {
				if e.label.Value != first.Value {
					return nil, fmt.Errorf("%w: merged key %q has values %q and %q", ErrLabelConflict, key, first.Value, e.label.Value)
				}
			}
		}

		chosen := entries[len(entries)-1].label
		l := chosen.clone()
		l.Key = key
		if !lastWins {
			l.Fixed = false
			l.Transitive = true
		}
		var origins []Origin
		for _, e := range entries {
			origins = appendUniqueOrigins(origins, e.label.Origins...)
		}
		origins = append(origins, Origin{
			RoleID: "",
			Hop:    out.Depth,
			Kind:   OriginMerge,
			Value:  l.Value,
		})
		l.Origins = origins

		// Shared-label transitivity is conservative unless the audited union
		// flaw is enabled.
		if cfg.has(FlawMergeTransitiveUnion) {
			l.Transitive = false
			for _, e := range entries {
				if e.label.Transitive {
					l.Transitive = true
				}
			}
		} else {
			l.Transitive = true
			for _, e := range entries {
				if !e.label.Transitive {
					l.Transitive = false
				}
			}
		}
		out.Labels[key] = l
		if l.Transitive {
			out.Transitive[key] = true
			if cfg.has(FlawMergeTransitiveUnion) {
				delete(out.NonTransitive, key)
			}
		} else {
			out.NonTransitive[key] = true
		}
	}

	for key, l := range out.Labels {
		out.Proofs[key] = makeProof(l)
	}
	return out, nil
}

func mergeRoles(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	seen := make(map[string]bool)
	for _, list := range [][]string{a, b} {
		for _, r := range list {
			if r == "" || seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func appendUniqueOrigins(dst []Origin, src ...Origin) []Origin {
	seen := make(map[Origin]bool, len(dst)+len(src))
	for _, o := range dst {
		seen[o] = true
	}
	for _, o := range src {
		if seen[o] {
			continue
		}
		seen[o] = true
		dst = append(dst, o)
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
