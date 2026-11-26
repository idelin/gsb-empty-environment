package credentialauth

import (
	"fmt"
	"sort"
)

type credentialInfo struct {
	purposes purposeSet
	sources  []string
	usable   bool
}

type plannedBatch struct {
	id      string
	rpID    string
	count   int
	digests []string
	purpose string
	allowed []int
}

type plannedSlot struct {
	batchIndex int
	digest     string
	allowed    []int
}

type mergePlan struct {
	normalized *normalizedSnapshot
	creds      map[credentialKey]*credentialInfo
	credOrder  []credentialKey
	reserved   map[credentialKey]struct{}
	batches    []plannedBatch
	slots      []plannedSlot
	assignment []int
}

func buildPlan(normalized *normalizedSnapshot) (*mergePlan, error) {
	plan := &mergePlan{
		normalized: normalized,
		creds:      make(map[credentialKey]*credentialInfo),
		reserved:   make(map[credentialKey]struct{}, len(normalized.reservations)),
	}

	for _, receipt := range normalized.devices {
		for _, ref := range receipt.Credentials {
			key, err := ref.key()
			if err != nil {
				return nil, err
			}
			info := plan.creds[key]
			if info == nil {
				info = &credentialInfo{
					purposes: purposeSet{all: true},
					usable:   true,
				}
				plan.creds[key] = info
			}
			if info.purposes.all {
				info.purposes = ref.purposes()
			} else if !ref.purposes().all {
				info.purposes = info.purposes.intersect(ref.purposes())
			}
			info.sources = append(info.sources, receipt.DeviceID)
		}
	}
	for _, info := range plan.creds {
		info.sources = sortedUnique(info.sources)
		info.usable = !info.purposes.empty()
	}

	for _, reservation := range normalized.reservations {
		key, err := reservation.key()
		if err != nil {
			return nil, err
		}
		plan.reserved[key] = struct{}{}
	}

	// Assign credential indices in a stable order.  The index order is part of
	// the deterministic proof and oracle behavior.
	keySet := make(map[credentialKey]struct{}, len(plan.creds)+len(plan.reserved))
	for key := range plan.creds {
		keySet[key] = struct{}{}
	}
	for key := range plan.reserved {
		keySet[key] = struct{}{}
	}
	for _, batch := range normalized.batches {
		for _, ref := range batch.candidates() {
			key, err := plan.resolveCandidate(ref, batch.RPID)
			if err != nil {
				return nil, err
			}
			keySet[key] = struct{}{}
		}
	}
	for key := range keySet {
		plan.credOrder = append(plan.credOrder, key)
	}
	sort.Slice(plan.credOrder, func(i, j int) bool {
		return plan.credOrder[i].less(plan.credOrder[j])
	})
	credIndex := make(map[credentialKey]int, len(plan.credOrder))
	for index, key := range plan.credOrder {
		credIndex[key] = index
	}

	for _, batch := range normalized.batches {
		planned := plannedBatch{
			id:      batch.batchID(),
			rpID:    batch.RPID,
			count:   batch.transactionCount(),
			digests: append([]string(nil), batch.TransactionDigests...),
			purpose: batch.purpose(),
		}
		candidateSet := make(map[int]struct{}, len(batch.candidates()))
		for _, ref := range batch.candidates() {
			key, err := plan.resolveCandidate(ref, batch.RPID)
			if err != nil {
				return nil, err
			}
			info := plan.creds[key]
			if info == nil {
				return nil, fmt.Errorf("%w: batch %q references %q under rpID %q",
					ErrUnknownCredential, batch.batchID(), key.id, key.rpID)
			}
			if _, reserved := plan.reserved[key]; reserved {
				continue
			}
			if !info.usable || !info.purposes.allows(batch.purpose()) {
				continue
			}
			index := credIndex[key]
			candidateSet[index] = struct{}{}
		}
		for candidate := range candidateSet {
			planned.allowed = append(planned.allowed, candidate)
		}
		sort.Ints(planned.allowed)
		if planned.count > len(planned.allowed) {
			return nil, fmt.Errorf("%w: batch %q needs %d distinct credentials but only %d are available",
				ErrNoGlobalAssignment, planned.id, planned.count, len(planned.allowed))
		}
		batchIndex := len(plan.batches)
		plan.batches = append(plan.batches, planned)
		for ordinal := 0; ordinal < planned.count; ordinal++ {
			slot := plannedSlot{
				batchIndex: batchIndex,
				allowed:    append([]int(nil), planned.allowed...),
			}
			if ordinal < len(planned.digests) {
				slot.digest = planned.digests[ordinal]
			}
			plan.slots = append(plan.slots, slot)
		}
	}

	graph := plan.graph()
	assignment, ok := graph.maximumMatching(-1, -1)
	if !ok {
		return nil, fmt.Errorf("%w: %d confirmed transactions cannot be assigned to %d available credentials",
			ErrNoGlobalAssignment, len(plan.slots), graph.credentialCount())
	}
	plan.assignment = assignment
	return plan, nil
}

func (p *mergePlan) resolveCandidate(ref CredentialRef, fallbackRPID string) (credentialKey, error) {
	rpID := ref.RPID
	if rpID == "" {
		rpID = fallbackRPID
	}
	id := ref.CredentialIDValue()
	if id == "" {
		return credentialKey{}, invalidf("batch candidate has an empty credential ID")
	}
	if rpID != "" {
		return credentialKey{rpID: rpID, id: id}, nil
	}
	// An old, truncated summary may omit rpID.  It is safe to infer the
	// source only when exactly one signed receipt supplies that ID.
	var found []credentialKey
	for key := range p.creds {
		if key.id == id {
			found = append(found, key)
		}
	}
	if len(found) != 1 {
		return credentialKey{}, invalidf("cannot infer rpID for credential %q; %d sources match", id, len(found))
	}
	return found[0], nil
}

func (p *mergePlan) indexOf(key credentialKey) int {
	for index, candidate := range p.credOrder {
		if candidate == key {
			return index
		}
	}
	return -1
}

func (p *mergePlan) graph() *matchGraph {
	graph := &matchGraph{
		credentials: len(p.credOrder),
		adjacency:   make([][]int, len(p.slots)),
	}
	for slotIndex, slot := range p.slots {
		graph.adjacency[slotIndex] = append([]int(nil), slot.allowed...)
	}
	return graph
}

func (p *mergePlan) edgeCredentials() map[int]struct{} {
	result := make(map[int]struct{})
	for _, slot := range p.slots {
		for _, candidate := range slot.allowed {
			result[candidate] = struct{}{}
		}
	}
	return result
}

func (p *mergePlan) assignmentRefs(assignment []int) []CredentialRef {
	refs := make([]CredentialRef, 0, len(assignment))
	for _, credential := range assignment {
		if credential < 0 || credential >= len(p.credOrder) {
			continue
		}
		key := p.credOrder[credential]
		info := p.creds[key]
		var sources []string
		var purposes purposeSet
		if info != nil {
			sources = info.sources
			purposes = info.purposes
		} else {
			purposes = purposeSet{all: true}
		}
		refs = append(refs, canonicalRef(key, purposes, sources))
	}
	return refs
}

func (p *mergePlan) batchAssignments(assignment []int) []BatchAssignment {
	result := make([]BatchAssignment, 0, len(p.batches))
	for batchIndex, batch := range p.batches {
		entry := BatchAssignment{
			BatchID:            batch.id,
			TransactionCount:   batch.count,
			TransactionDigests: append([]string(nil), batch.digests...),
			Credentials:        make([]CredentialRef, 0, batch.count),
		}
		for slotIndex, slot := range p.slots {
			if slot.batchIndex != batchIndex {
				continue
			}
			credential := -1
			if slotIndex < len(assignment) {
				credential = assignment[slotIndex]
			}
			if credential >= 0 && credential < len(p.credOrder) {
				key := p.credOrder[credential]
				info := p.creds[key]
				var sources []string
				var purposes purposeSet
				if info != nil {
					sources = info.sources
					purposes = info.purposes
				} else {
					purposes = purposeSet{all: true}
				}
				entry.Credentials = append(entry.Credentials, canonicalRef(key, purposes, sources))
			}
		}
		result = append(result, entry)
	}
	return result
}

func (p *mergePlan) allStatusKeys() []credentialKey {
	seen := make(map[credentialKey]struct{}, len(p.credOrder)+len(p.reserved))
	for key := range p.creds {
		seen[key] = struct{}{}
	}
	for key := range p.reserved {
		seen[key] = struct{}{}
	}
	if p.normalized.request != nil {
		for _, ref := range p.normalized.request.Candidates {
			key, err := ref.key()
			if err == nil {
				seen[key] = struct{}{}
			}
		}
	}
	return sortedCredentialKeys(seen)
}

func (p *mergePlan) statuses(mode SafetyMode, assignment []int) []CredentialStatus {
	graph := p.graph()
	edgeCreds := p.edgeCredentials()
	consumed := make(map[int]struct{}, len(assignment))
	for _, credential := range assignment {
		consumed[credential] = struct{}{}
	}
	result := make([]CredentialStatus, 0, len(p.allStatusKeys()))
	for _, key := range p.allStatusKeys() {
		index := p.indexOf(key)
		_, reserved := p.reserved[key]
		info := p.creds[key]
		conditionallyUsed := false
		unavoidable := false
		if index >= 0 {
			if _, ok := edgeCreds[index]; ok {
				conditionallyUsed = true
				_, without := graph.maximumMatching(index, -1)
				unavoidable = !without
			}
		}
		universallyFree := !conditionallyUsed
		available := !reserved
		if available {
			if mode == SafetyUniversal {
				available = universallyFree
			} else {
				available = !unavoidable
			}
		}
		if info != nil && !info.usable {
			available = false
		}
		_, isConsumed := consumed[index]
		if index < 0 {
			isConsumed = false
		}
		var purposes purposeSet
		var sources []string
		if info != nil {
			purposes = info.purposes
			sources = info.sources
		} else {
			purposes = purposeSet{all: true}
		}
		result = append(result, CredentialStatus{
			Credential:        canonicalRef(key, purposes, sources),
			Reserved:          reserved,
			Consumed:          isConsumed,
			ConditionallyUsed: conditionallyUsed,
			Unavoidable:       unavoidable,
			UniversallyFree:   universallyFree,
			Available:         available,
		})
	}
	return result
}

func sortedCredentialKeys(set map[credentialKey]struct{}) []credentialKey {
	result := make([]credentialKey, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].less(result[j])
	})
	return result
}

func refsForKeys(keys []credentialKey) []CredentialRef {
	result := make([]CredentialRef, 0, len(keys))
	for _, key := range keys {
		result = append(result, key.ref())
	}
	return result
}
