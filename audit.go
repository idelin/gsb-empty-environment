package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Flaw identifies one deliberately supported bug class in the audited
// implementation.  The pristine audited implementation follows the spec; a
// caller can enable the individual flaws to model a legacy deployment and let
// the differential search produce a concrete witness.
type Flaw uint32

const (
	FlawNone Flaw = 0

	// FlawCaseFoldKeys collapses keys with ToLower before protection and
	// override checks.  This makes "Project" capable of replacing "project".
	FlawCaseFoldKeys Flaw = 1 << 0
	// FlawAllowFixedOverride lets a caller request a target-fixed key.
	FlawAllowFixedOverride Flaw = 1 << 1
	// FlawIgnoreFixedConflict lets a fixed label silently replace an
	// inherited label with the same key.
	FlawIgnoreFixedConflict Flaw = 1 << 2
	// FlawAllowTransitiveUpgrade puts any requested, present key in the next
	// transitive set, including fixed and previously non-transitive labels.
	FlawAllowTransitiveUpgrade Flaw = 1 << 3
	// FlawSkipRoleCycle permits the same role ID to appear more than once.
	FlawSkipRoleCycle Flaw = 1 << 4
	// FlawResetSourcesOnOverride drops the inherited provenance when a value
	// is overridden.
	FlawResetSourcesOnOverride Flaw = 1 << 5
	// FlawMergeLastWins makes a two-chain merge silently prefer the right
	// chain instead of rejecting conflicts.
	FlawMergeLastWins Flaw = 1 << 6
	// FlawMergeTransitiveUnion unions transitive sets during a merge instead
	// of intersecting them.
	FlawMergeTransitiveUnion Flaw = 1 << 7
)

// AuditConfig selects the bugs present in the audited implementation.
type AuditConfig struct {
	Flaws  Flaw
	Limits Limits
}

// DefaultAuditConfig models the full set of known legacy label-propagation
// defects.  Use AuditConfig{Flaws: FlawNone} to obtain a spec-conforming
// baseline.
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		Flaws: FlawCaseFoldKeys |
			FlawAllowFixedOverride |
			FlawIgnoreFixedConflict |
			FlawAllowTransitiveUpgrade |
			FlawSkipRoleCycle |
			FlawResetSourcesOnOverride |
			FlawMergeLastWins |
			FlawMergeTransitiveUnion,
	}
}

func (c AuditConfig) has(f Flaw) bool { return c.Flaws&f != 0 }

// AuditedHop applies the default audited implementation.
func AuditedHop(req HopRequest) (*HopResult, error) {
	return AuditedHopWithConfig(req, DefaultAuditConfig())
}

// AuditedHopWithConfig applies the audited implementation with selected
// flaws.
func AuditedHopWithConfig(req HopRequest, cfg AuditConfig) (*HopResult, error) {
	n, err := normalizeRequest(req, cfg.Limits)
	if err != nil {
		return nil, err
	}
	return applyAudited(n, cfg)
}

// AuditedTransition adapts the default audited implementation to the search
// callback shape.
func AuditedTransition(s *Session, req HopRequest) (*Session, error) {
	return auditedTransition(s, req, DefaultAuditConfig())
}

// AuditedTransitionWithConfig adapts an audited implementation with selected
// flaws.
func AuditedTransitionWithConfig(cfg AuditConfig) TransitionFunc {
	return func(s *Session, req HopRequest) (*Session, error) {
		return auditedTransition(s, req, cfg)
	}
}

func auditedTransition(s *Session, req HopRequest, cfg AuditConfig) (*Session, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: nil current session", ErrInvalidInput)
	}
	req.CurrentLabelSet = cloneLabelMap(s.Labels)
	req.CurrentLabels = nil
	req.CurrentPairs = nil
	req.CurrentTransitive = nil
	req.CurrentTransitiveSet = cloneBoolMap(s.Transitive)
	req.CurrentRoleIDs = append([]string(nil), s.RoleIDs...)
	req.NonTransitiveKeys = nil
	req.TaintedKeys = nil
	req.NonTransitiveSet = cloneBoolMap(s.NonTransitive)
	req.TaintedSet = nil
	req.Depth = s.Depth + 1
	res, err := AuditedHopWithConfig(req, cfg)
	if err != nil {
		return nil, err
	}
	return res.Session, nil
}

func auditKey(cfg AuditConfig, key string) string {
	if cfg.has(FlawCaseFoldKeys) {
		return strings.ToLower(key)
	}
	return key
}

func auditKeySet(cfg AuditConfig, set map[string]bool) map[string]bool {
	out := make(map[string]bool, len(set))
	for k, v := range set {
		out[auditKey(cfg, k)] = v
	}
	return out
}

func applyAudited(n *normalizedRequest, cfg AuditConfig) (*HopResult, error) {
	if n.depth > MaxDepth {
		return nil, fmt.Errorf("%w: hop %d > %d", ErrDepthExceeded, n.depth, MaxDepth)
	}

	roles := append([]string(nil), n.roleIDs...)
	if !cfg.has(FlawSkipRoleCycle) {
		seen := make(map[string]bool, len(roles))
		for _, r := range roles {
			if seen[r] {
				return nil, fmt.Errorf("%w: existing chain repeats %q", ErrRoleCycle, r)
			}
			seen[r] = true
		}
		if seen[n.roleID] {
			return nil, fmt.Errorf("%w: role %q", ErrRoleCycle, n.roleID)
		}
	}

	overrides := n.overrides
	targetTransitive := n.targetTransitive
	if cfg.has(FlawCaseFoldKeys) {
		overrides = auditKeySet(cfg, overrides)
		targetTransitive = auditKeySet(cfg, targetTransitive)
	}

	out := make(map[string]Label, len(n.current)+len(n.fixed)+len(n.requested))
	inherited := make(map[string]bool)

	// Inherit current transitive labels.
	for _, key := range sortedLabelKeys(n.current) {
		if !n.currentTransitive[key] {
			continue
		}
		ak := auditKey(cfg, key)
		l := n.current[key].clone()
		l.Key = ak
		l.Fixed = false
		l.Transitive = true
		l.Origins = appendOrigin(l.Origins, Origin{
			RoleID: n.roleID,
			Hop:    n.depth,
			Kind:   OriginInherited,
			Value:  l.Value,
		})
		out[ak] = l
		inherited[ak] = true
	}

	// Fixed labels.
	fixedKeys := sortedKeys(n.fixed)
	for _, rawKey := range fixedKeys {
		key := auditKey(cfg, rawKey)
		if inherited[key] && !cfg.has(FlawIgnoreFixedConflict) {
			return nil, fmt.Errorf("%w: key %q is inherited and fixed", ErrFixedConflict, rawKey)
		}
		if _, requested := n.requested[rawKey]; requested && !cfg.has(FlawAllowFixedOverride) {
			return nil, fmt.Errorf("%w: key %q is fixed and requested", ErrFixedConflict, rawKey)
		}
		out[key] = Label{
			Key:        key,
			Value:      n.fixed[rawKey],
			Fixed:      true,
			Transitive: false,
			Origins: []Origin{{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginFixed,
				Value:  n.fixed[rawKey],
			}},
		}
	}

	// Caller requested labels.
	for _, rawKey := range sortedKeys(n.requested) {
		key := auditKey(cfg, rawKey)
		if old, exists := out[key]; exists && old.Fixed {
			if !cfg.has(FlawAllowFixedOverride) {
				return nil, fmt.Errorf("%w: key %q", ErrFixedOverride, rawKey)
			}
		}
		if !overrides[key] {
			// A key that exactly names a fixed label is also accepted by the
			// legacy fixed-override bug.
			if !(cfg.has(FlawAllowFixedOverride) && out[key].Fixed) {
				return nil, fmt.Errorf("%w: key %q", ErrNotOverridable, rawKey)
			}
		}
		value := n.requested[rawKey]
		if old, ok := out[key]; ok {
			old.Value = value
			old.Fixed = false
			if cfg.has(FlawResetSourcesOnOverride) {
				old.Origins = []Origin{{
					RoleID: n.roleID,
					Hop:    n.depth,
					Kind:   OriginOverride,
					Value:  value,
				}}
			} else {
				old.Origins = appendOrigin(old.Origins, Origin{
					RoleID: n.roleID,
					Hop:    n.depth,
					Kind:   OriginOverride,
					Value:  value,
				})
			}
			out[key] = old
			continue
		}
		if old, ok := n.current[rawKey]; ok {
			old = old.clone()
			old.Key = key
			old.Fixed = false
			old.Transitive = false
			old.Value = value
			if cfg.has(FlawResetSourcesOnOverride) {
				old.Origins = []Origin{{
					RoleID: n.roleID,
					Hop:    n.depth,
					Kind:   OriginOverride,
					Value:  value,
				}}
			} else {
				old.Origins = appendOrigin(old.Origins, Origin{
					RoleID: n.roleID,
					Hop:    n.depth,
					Kind:   OriginOverride,
					Value:  value,
				})
			}
			out[key] = old
			continue
		}
		out[key] = Label{
			Key:   key,
			Value: value,
			Origins: []Origin{{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginNew,
				Value:  value,
			}},
		}
	}

	// Transitivity.
	if cfg.has(FlawAllowTransitiveUpgrade) {
		for _, rawKey := range n.requestedTransitive {
			key := auditKey(cfg, rawKey)
			l, ok := out[key]
			if !ok {
				continue
			}
			l.Transitive = true
			out[key] = l
		}
	} else {
		for key, l := range out {
			l.Transitive = false
			out[key] = l
		}
		oldNonTransitive := make(map[string]bool)
		for key := range n.nonTransitive {
			oldNonTransitive[auditKey(cfg, key)] = true
		}
		for _, rawKey := range n.requestedTransitive {
			key := auditKey(cfg, rawKey)
			l, ok := out[key]
			if !ok {
				continue
			}
			if l.Fixed {
				if l.Transitive {
					continue
				}
				return nil, fmt.Errorf("%w: fixed key %q", ErrTransitiveUp, rawKey)
			}
			if oldNonTransitive[key] {
				return nil, fmt.Errorf("%w: originally non-transitive key %q", ErrTransitiveUp, rawKey)
			}
			if !inherited[key] && n.targetTransDefined && !targetTransitive[key] {
				return nil, fmt.Errorf("%w: target does not permit key %q", ErrTransitiveUp, rawKey)
			}
			l.Transitive = true
			out[key] = l
		}
	}

	keys := sortedLabelKeys(out)
	values := make(map[string]string, len(keys))
	proofs := make(map[string]Proof, len(keys))
	transitive := make([]string, 0)
	nonTransitive := make(map[string]bool, len(n.nonTransitive)+len(keys))
	for k, v := range n.nonTransitive {
		nonTransitive[auditKey(cfg, k)] = v
	}
	for _, key := range keys {
		l := out[key]
		l.Key = key
		values[key] = l.Value
		proofs[key] = makeProof(l)
		if l.Transitive {
			transitive = append(transitive, key)
		} else {
			nonTransitive[key] = true
		}
		out[key] = l
	}
	sort.Strings(transitive)
	tainted := sortedBoolKeys(nonTransitive)
	roles = append(roles, n.roleID)

	sess := &Session{
		Labels:        cloneLabelMap(out),
		Transitive:    make(map[string]bool, len(transitive)),
		NonTransitive: make(map[string]bool, len(nonTransitive)),
		Depth:         n.depth,
		RoleIDs:       append([]string(nil), roles...),
		Proofs:        make(map[string]Proof, len(proofs)),
	}
	for _, key := range transitive {
		sess.Transitive[key] = true
	}
	for k, v := range nonTransitive {
		sess.NonTransitive[k] = v
	}
	for k, p := range proofs {
		sess.Proofs[k] = cloneProof(p)
	}
	return &HopResult{
		Labels:        out,
		Values:        values,
		Transitive:    transitive,
		TransitiveSet: cloneBoolMap(sess.Transitive),
		NonTransitive: tainted,
		TaintedKeys:   append([]string(nil), tainted...),
		Depth:         n.depth,
		RoleIDs:       append([]string(nil), roles...),
		Proofs:        proofs,
		Session:       sess,
	}, nil
}
