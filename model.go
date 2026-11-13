package netsec

import (
	"fmt"
	"sort"
)

// ApplyHop applies the secure reference model to one role hop.
func ApplyHop(req HopRequest) (*HopResult, error) {
	return ApplyHopWithLimits(req, DefaultLimits)
}

// ApplyHopWithLimits is ApplyHop with custom byte limits.
func ApplyHopWithLimits(req HopRequest, lim Limits) (*HopResult, error) {
	n, err := normalizeRequest(req, lim)
	if err != nil {
		return nil, err
	}
	return applyNormalized(n)
}

// NextHop is an alias for ApplyHop.
func NextHop(req HopRequest) (*HopResult, error) { return ApplyHop(req) }

// BuildNextHop is an alias for ApplyHop.
func BuildNextHop(req HopRequest) (*HopResult, error) { return ApplyHop(req) }

// NextLabels returns only the next label values and transitive key set.
func NextLabels(req HopRequest) (map[string]string, []string, error) {
	res, err := ApplyHop(req)
	if err != nil {
		return nil, nil, err
	}
	return res.LabelMap(), res.TransitiveKeys(), nil
}

// SecureTransition adapts the reference model to the search callback shape.
func SecureTransition(s *Session, req HopRequest) (*Session, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: nil current session", ErrInvalidInput)
	}
	return s.Advance(req)
}

// ModelTransition is an alias for SecureTransition.
func ModelTransition(s *Session, req HopRequest) (*Session, error) {
	return SecureTransition(s, req)
}

func applyNormalized(n *normalizedRequest) (*HopResult, error) {
	if n.depth > MaxDepth {
		return nil, fmt.Errorf("%w: hop %d > %d", ErrDepthExceeded, n.depth, MaxDepth)
	}

	roles := append([]string(nil), n.roleIDs...)
	seenRoles := make(map[string]bool, len(roles))
	for _, r := range roles {
		if seenRoles[r] {
			return nil, fmt.Errorf("%w: existing chain repeats %q", ErrRoleCycle, r)
		}
		seenRoles[r] = true
	}
	if seenRoles[n.roleID] {
		return nil, fmt.Errorf("%w: role %q", ErrRoleCycle, n.roleID)
	}

	// Step 1: inherit only labels that are transitive in the current session.
	out := make(map[string]Label, len(n.current)+len(n.fixed)+len(n.requested))
	inherited := make(map[string]bool)
	for _, key := range sortedLabelKeys(n.current) {
		if !n.currentTransitive[key] {
			continue
		}
		l := n.current[key].clone()
		l.Key = key
		l.Fixed = false
		l.Transitive = true
		l.Origins = appendOrigin(l.Origins, Origin{
			RoleID: n.roleID,
			Hop:    n.depth,
			Kind:   OriginInherited,
			Value:  l.Value,
		})
		out[key] = l
		inherited[key] = true
	}

	// Step 2: target-owned fixed labels.  A fixed key conflicts with an
	// inherited key even when the value happens to be identical.
	fixedKeys := sortedKeys(n.fixed)
	for _, key := range fixedKeys {
		if inherited[key] {
			return nil, fmt.Errorf("%w: key %q is inherited and fixed", ErrFixedConflict, key)
		}
		if _, requested := n.requested[key]; requested {
			return nil, fmt.Errorf("%w: key %q is fixed and requested", ErrFixedConflict, key)
		}
		l := Label{
			Key:        key,
			Value:      n.fixed[key],
			Fixed:      true,
			Transitive: false,
			Origins: []Origin{{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginFixed,
				Value:  n.fixed[key],
			}},
		}
		out[key] = l
	}

	// Step 3: caller-requested labels.  Only exact keys in the override set
	// are permitted; no case folding is performed.
	for _, key := range sortedKeys(n.requested) {
		if _, isFixed := n.fixed[key]; isFixed {
			return nil, fmt.Errorf("%w: key %q", ErrFixedOverride, key)
		}
		if !n.overrides[key] {
			return nil, fmt.Errorf("%w: key %q", ErrNotOverridable, key)
		}
		value := n.requested[key]
		if old, ok := out[key]; ok {
			// Overriding an inherited label keeps the complete source chain;
			// this hop is appended as an override step.
			old.Value = value
			old.Origins = appendOrigin(old.Origins, Origin{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginOverride,
				Value:  value,
			})
			out[key] = old
			continue
		}
		if old, ok := n.current[key]; ok {
			// The original label was dropped because it was not transitive.
			// It may be re-added as a non-transitive label, but its provenance
			// is retained so this cannot launder an old non-transitive label
			// into a fresh, transitive one.
			old = old.clone()
			old.Key = key
			old.Fixed = false
			old.Transitive = false
			old.Value = value
			old.Origins = appendOrigin(old.Origins, Origin{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginOverride,
				Value:  value,
			})
			out[key] = old
			continue
		}
		out[key] = Label{
			Key:        key,
			Value:      value,
			Transitive: false,
			Origins: []Origin{{
				RoleID: n.roleID,
				Hop:    n.depth,
				Kind:   OriginNew,
				Value:  value,
			}},
		}
	}

	// Step 4: caller-requested transitive keys.  A key is eligible only if it
	// was already transitive on inheritance, or was genuinely new this hop and
	// the target permits it.  A key present in the current session but absent
	// from the current transitive set can never be upgraded.
	//
	// The next transitive set is an intersection with RequestedTransitive:
	// an inherited label that is not requested is retained, but becomes
	// non-transitive (and therefore permanently tainted).
	for key, l := range out {
		l.Transitive = false
		out[key] = l
	}
	for _, key := range n.requestedTransitive {
		l, ok := out[key]
		if !ok {
			// The next transitive set is an intersection: a requested key
			// that is not present contributes nothing.
			continue
		}
		if l.Fixed {
			if l.Transitive {
				continue
			}
			return nil, fmt.Errorf("%w: fixed key %q", ErrTransitiveUp, key)
		}
		if n.nonTransitive[key] {
			return nil, fmt.Errorf("%w: originally non-transitive key %q", ErrTransitiveUp, key)
		}
		if inherited[key] {
			l.Transitive = true
			out[key] = l
			continue
		}
		if n.targetTransDefined && !n.targetTransitive[key] {
			return nil, fmt.Errorf("%w: target does not permit key %q", ErrTransitiveUp, key)
		}
		l.Transitive = true
		out[key] = l
	}

	// Re-sort and materialize the result.
	keys := sortedLabelKeys(out)
	values := make(map[string]string, len(keys))
	proofs := make(map[string]Proof, len(keys))
	transitive := make([]string, 0)
	nonTransitive := make(map[string]bool, len(n.nonTransitive)+len(keys))
	for k, v := range n.nonTransitive {
		nonTransitive[k] = v
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

func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedLabelKeys(m map[string]Label) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
