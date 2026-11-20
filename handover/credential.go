package handover

import "sort"

// HandoverRequest asks the host to transfer the logical pages Requested from
// the window owned by WindowOwner to a new tenant.
type HandoverRequest struct {
	WindowOwner Identity
	Requested   IntervalSet
}

// Gap describes why a part of the request cannot be handed over yet.
type Gap struct {
	// Logical is the set of requested logical pages blocked by this gap.
	Logical IntervalSet
	// Physical is the physical image of those pages that remains exposed.
	Physical IntervalSet
	// Owner is the exact identity tuple whose lease survives.
	Owner Identity
	// Perm is the surviving permission bit (PermRead or PermWrite).
	Perm PermBits
	// Reason is a human-readable explanation of the missing evidence.
	Reason string
	// LeaseIDs are the leases contributing to the gap, sorted and unique.
	LeaseIDs []string
}

// Credential is an importable, serializable handover decision. A verifier only
// needs the credential plus the Manifest (window inventory and certificates):
// every set is stored structurally as aligned intervals and can be re-derived
// without expanding the 48-bit page space.
type Credential struct {
	// WindowOwner identifies the window the request was issued against.
	WindowOwner Identity
	// Requested is the normalized logical set asked to be handed over.
	Requested IntervalSet
	// SafeLogical is the maximal subset of Requested that may be transferred.
	SafeLogical IntervalSet
	// SafePhysical is the physical image of SafeLogical across all segments.
	SafePhysical IntervalSet
	// Gaps enumerates every blocked region with its owning tuple and permission.
	Gaps []Gap
	// LeaseIDs and ConfirmationIDs list the evidence considered, sorted and
	// unique. They are references for auditors; verification recomputes them.
	LeaseIDs        []string
	ConfirmationIDs []string
}

// Issue computes the maximal safely handoverable subset of a request.
//
// A physical page is safe only when every outstanding lease covering it has
// been invalidated by confirmations carrying the exact same identity tuple
// (device, boot generation, domain generation, queue) and permission.
// Confirmations from other tuples never count: distinct devices, reset
// generations, domains or queues cannot be stitched together. Each page is
// decided individually through interval-set arithmetic, so one safe page never
// stands in for an entire range.
func Issue(m *Manifest, req HandoverRequest) (*Credential, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	requested, err := Normalize(req.Requested)
	if err != nil {
		return nil, err
	}
	if requested.IsEmpty() {
		return nil, errf("handover request covers no logical pages")
	}
	if err := req.WindowOwner.Validate(); err != nil {
		return nil, err
	}
	window, err := m.findWindow(req.WindowOwner)
	if err != nil {
		return nil, err
	}
	if err := ensureCovered(window, requested); err != nil {
		return nil, err
	}
	return computeCredential(m, window, requested), nil
}

// Verify independently re-issues the credential from the manifest and compares
// every field, including gap attribution and evidence references. A nil error
// means the credential's coverage and gaps are fully supported by the window
// inventory and certificates alone.
func Verify(m *Manifest, c *Credential) error {
	if c == nil {
		return errf("cannot verify a nil credential")
	}
	if err := m.Validate(); err != nil {
		return err
	}
	requested, err := Normalize(c.Requested)
	if err != nil {
		return errf("credential requested range invalid: %w", err)
	}
	if requested.IsEmpty() {
		return errf("credential covers no logical pages")
	}
	window, err := m.findWindow(c.WindowOwner)
	if err != nil {
		return err
	}
	if err := ensureCovered(window, requested); err != nil {
		return err
	}
	want := computeCredential(m, window, requested)
	return compareCredentials(want, c)
}

func (m *Manifest) findWindow(owner Identity) (Window, error) {
	var found Window
	matches := 0
	for _, win := range m.Windows {
		if win.Owner == owner {
			found = win
			matches++
		}
	}
	switch matches {
	case 0:
		return Window{}, errf("no window found for device %q queue %q", owner.Device, owner.Queue)
	case 1:
		return found, nil
	default:
		return Window{}, errf("ambiguous manifest: %d windows share identity %q/%q", matches, owner.Device, owner.Queue)
	}
}

func ensureCovered(w Window, pages IntervalSet) error {
	var coverage []Interval
	for _, seg := range w.Segments {
		coverage = append(coverage, Interval{seg.LogBase, seg.LogBase + seg.Width()})
	}
	cov, err := Normalize(coverage)
	if err != nil {
		return err
	}
	if !ContainsSet(cov, pages) {
		return errf("requested logical pages are not fully contained in the target window")
	}
	return nil
}

type groupKey struct {
	owner Identity
	bit   PermBits
}

type groupState struct {
	leasePages IntervalSet
	leaseIDs   []string
}

func computeCredential(m *Manifest, w Window, requested IntervalSet) *Credential {
	// Physical image of the request, accumulated per segment.
	var requestedPhysical IntervalSet
	type segPart struct {
		seg     Segment
		logical IntervalSet
	}
	var parts []segPart
	for _, seg := range w.Segments {
		segRange := IntervalSet{{seg.LogBase, seg.LogBase + seg.Width()}}
		logical := Intersect(segRange, requested)
		if logical.IsEmpty() {
			continue
		}
		physical, err := segmentLogicalToPhysical(seg, logical)
		if err != nil {
			// Inputs were validated by Issue/Verify before reaching here.
			panic(err)
		}
		requestedPhysical = Union(requestedPhysical, physical)
		parts = append(parts, segPart{seg: seg, logical: logical})
	}

	// Group every surviving lease page by exact identity and permission bit.
	groups := map[groupKey]*groupState{}
	var groupOrder []groupKey
	relevantLeases := map[string]bool{}
	for _, lease := range m.Leases {
		hit := Intersect(lease.Pages, requestedPhysical)
		if hit.IsEmpty() {
			continue
		}
		relevantLeases[lease.ID] = true
		for _, bit := range []PermBits{PermRead, PermWrite} {
			if lease.Perm&bit == 0 {
				continue
			}
			key := groupKey{owner: lease.Owner, bit: bit}
			state, ok := groups[key]
			if !ok {
				state = &groupState{}
				groups[key] = state
				groupOrder = append(groupOrder, key)
			}
			state.leasePages = Union(state.leasePages, hit)
			state.leaseIDs = appendUnique(state.leaseIDs, lease.ID)
		}
	}

	relevantConfirmations := map[string]bool{}
	var blockedPhysical IntervalSet
	var gaps []Gap
	sortGroupKeys(groupOrder)
	for _, key := range groupOrder {
		state := groups[key]
		var confirmed IntervalSet
		for _, conf := range m.Confirmations {
			if conf.Owner != key.owner || conf.Perm&key.bit == 0 {
				continue
			}
			hit := Intersect(conf.Pages, state.leasePages)
			if hit.IsEmpty() {
				continue
			}
			relevantConfirmations[conf.ID] = true
			confirmed = Union(confirmed, hit)
		}
		block := Subtract(state.leasePages, confirmed)
		sort.Strings(state.leaseIDs)
		if block.IsEmpty() {
			continue
		}
		blockedPhysical = Union(blockedPhysical, block)
		var gapLogical, gapPhysical IntervalSet
		for _, part := range parts {
			segPhysRange := IntervalSet{{part.seg.PhysBase, part.seg.PhysBase + part.seg.Width()}}
			blockHere := Intersect(block, segPhysRange)
			if blockHere.IsEmpty() {
				continue
			}
			gapPhysical = Union(gapPhysical, blockHere)
			logicalHere, err := segmentPhysicalToLogical(part.seg, blockHere)
			if err != nil {
				panic(err)
			}
			gapLogical = Union(gapLogical, logicalHere)
		}
		gaps = append(gaps, Gap{
			Logical:  gapLogical,
			Physical: gapPhysical,
			Owner:    key.owner,
			Perm:     key.bit,
			Reason:   gapReason(key.bit),
			LeaseIDs: state.leaseIDs,
		})
	}

	safePhysical := Subtract(requestedPhysical, blockedPhysical)
	var safeLogical IntervalSet
	for _, part := range parts {
		physical, err := segmentLogicalToPhysical(part.seg, part.logical)
		if err != nil {
			panic(err)
		}
		safeHere := Intersect(physical, safePhysical)
		if safeHere.IsEmpty() {
			continue
		}
		logicalHere, err := segmentPhysicalToLogical(part.seg, safeHere)
		if err != nil {
			panic(err)
		}
		safeLogical = Union(safeLogical, logicalHere)
	}

	return &Credential{
		WindowOwner:     w.Owner,
		Requested:       requested,
		SafeLogical:     safeLogical,
		SafePhysical:    safePhysical,
		Gaps:            gaps,
		LeaseIDs:        sortedKeys(relevantLeases),
		ConfirmationIDs: sortedKeys(relevantConfirmations),
	}
}

func gapReason(bit PermBits) string {
	switch bit {
	case PermWrite:
		return "outstanding write lease lacks an exact-identity invalidation confirmation"
	case PermRead:
		return "outstanding read lease could observe the new tenant and lacks an exact-identity invalidation confirmation"
	default:
		return "outstanding lease lacks an exact-identity invalidation confirmation"
	}
}

func segmentLogicalToPhysical(seg Segment, logical IntervalSet) (IntervalSet, error) {
	local, err := seg.toLocal(logical)
	if err != nil {
		return nil, err
	}
	return seg.logicalImage(local)
}

func segmentPhysicalToLogical(seg Segment, physical IntervalSet) (IntervalSet, error) {
	local, err := seg.physicalToLocal(physical)
	if err != nil {
		return nil, err
	}
	inverseLocal, err := seg.Perm.Inverse().Image(local)
	if err != nil {
		return nil, err
	}
	return seg.localToLogical(inverseLocal)
}

func sortGroupKeys(keys []groupKey) {
	sort.Slice(keys, func(i, j int) bool {
		if identityOrder(keys[i].owner, keys[j].owner) {
			return true
		}
		if identityOrder(keys[j].owner, keys[i].owner) {
			return false
		}
		return keys[i].bit < keys[j].bit
	})
}

func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func compareCredentials(want, got *Credential) error {
	if want.WindowOwner != got.WindowOwner {
		return errf("window owner mismatch: want %+v, got %+v", want.WindowOwner, got.WindowOwner)
	}
	if !EqualSet(want.Requested, got.Requested) {
		return errf("requested range mismatch")
	}
	if !EqualSet(want.SafeLogical, got.SafeLogical) {
		return errf("safe logical coverage mismatch: want %v, got %v", want.SafeLogical, got.SafeLogical)
	}
	if !EqualSet(want.SafePhysical, got.SafePhysical) {
		return errf("safe physical coverage mismatch")
	}
	if len(want.Gaps) != len(got.Gaps) {
		return errf("gap count mismatch: want %d, got %d", len(want.Gaps), len(got.Gaps))
	}
	for idx := range want.Gaps {
		wg, gg := want.Gaps[idx], got.Gaps[idx]
		if wg.Owner != gg.Owner || wg.Perm != gg.Perm {
			return errf("gap %d identity/permission mismatch", idx)
		}
		if !EqualSet(wg.Logical, gg.Logical) || !EqualSet(wg.Physical, gg.Physical) {
			return errf("gap %d coverage mismatch", idx)
		}
		if !equalStrings(wg.LeaseIDs, gg.LeaseIDs) {
			return errf("gap %d lease references mismatch", idx)
		}
	}
	if !equalStrings(want.LeaseIDs, got.LeaseIDs) {
		return errf("lease evidence references mismatch")
	}
	if !equalStrings(want.ConfirmationIDs, got.ConfirmationIDs) {
		return errf("confirmation evidence references mismatch")
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}
