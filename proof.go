package credentialauth

import (
	"fmt"
	"sort"
)

// BuildProof normalizes a snapshot, checks its device chains and global
// consumption constraints, and returns the deterministic fixed proof.  If the
// snapshot contains a Request or Pending field, a permit is also issued.
func BuildProof(snapshot Snapshot) (*Proof, error) {
	return buildProof(snapshot, nil)
}

// Merge is an alias for BuildProof.
func Merge(snapshot Snapshot) (*Proof, error) {
	return BuildProof(snapshot)
}

// MergeAndProve is an alias for BuildProof.
func MergeAndProve(snapshot Snapshot) (*Proof, error) {
	return BuildProof(snapshot)
}

// VerifySnapshot checks the snapshot and returns its proof.  It does not issue
// a permit unless the snapshot itself contains a Request/Pending field.
func VerifySnapshot(snapshot Snapshot) (*Proof, error) {
	return BuildProof(snapshot)
}

// VerifyAndIssue checks the snapshot and issues one fixed consumption lock for
// request.  The result is deterministic for the exact normalized snapshot and
// request; callers that need stateful replay protection should use Verifier.
func VerifyAndIssue(snapshot Snapshot, request PermitRequest) (*Proof, error) {
	return buildProof(snapshot, &request)
}

// Analyze verifies the snapshot and returns proof data without issuing a new
// permit, even when the snapshot embeds a request.
func Analyze(snapshot Snapshot) (*Proof, error) {
	snapshot.Request = nil
	snapshot.Pending = nil
	return buildProof(snapshot, nil)
}

func buildProof(snapshot Snapshot, requestOverride *PermitRequest) (*Proof, error) {
	if requestOverride != nil {
		snapshot.Request = requestOverride
		snapshot.Pending = nil
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	plan, err := buildPlan(normalized)
	if err != nil {
		return nil, err
	}

	mode := SafetyFixedCompletion
	var request *PermitRequest
	var permit *Permit
	var candidate credentialKey
	candidateIndex := -1
	haveCandidate := false
	assignment := append([]int(nil), plan.assignment...)
	if normalized.request != nil {
		request = normalized.request
		mode = effectiveMode(*request)
		candidate, candidateIndex, err = selectPermitCandidate(plan, *request, mode)
		if err != nil {
			return nil, err
		}
		haveCandidate = true
		if candidateIndex >= 0 {
			without, ok := plan.graph().maximumMatching(candidateIndex, -1)
			if !ok {
				return nil, fmt.Errorf("%w: credential %q is consumed in every completion",
					ErrCandidatesExhausted, candidate.id)
			}
			assignment = without
		}
	}

	assignments := plan.batchAssignments(assignment)
	assignmentDigest, err := digestJSON(assignments)
	if err != nil {
		return nil, fmt.Errorf("hash assignment: %w", err)
	}
	statuses := plan.statuses(mode, assignment)

	proof := &Proof{
		Version:          ProofVersion,
		SnapshotDigest:   normalized.snapshotDigest,
		RequestDigest:    normalized.requestDigest,
		Safety:           mode,
		Request:          cloneRequest(request),
		Assignment:       assignments,
		AssignmentDigest: assignmentDigest,
		Consumed:         plan.assignmentRefs(assignment),
		Reserved:         reservationRefs(normalized.reservations),
		Statuses:         statuses,
	}
	if request == nil {
		proof.Safety = ""
	}
	for _, status := range statuses {
		if status.Unavoidable {
			proof.Unavoidable = append(proof.Unavoidable, status.Credential)
		}
		if status.ConditionallyUsed {
			proof.ConditionallyUsed = append(proof.ConditionallyUsed, status.Credential)
		}
		if status.UniversallyFree {
			proof.UniversallyFree = append(proof.UniversallyFree, status.Credential)
		}
		if status.Available {
			proof.Available = append(proof.Available, status.Credential)
		}
	}
	sortRefs(proof.Consumed)
	sortRefs(proof.Reserved)
	sortRefs(proof.Unavoidable)
	sortRefs(proof.ConditionallyUsed)
	sortRefs(proof.UniversallyFree)
	sortRefs(proof.Available)

	if request != nil {
		if !haveCandidate {
			return nil, fmt.Errorf("%w: permit candidate was not selected", ErrCandidatesExhausted)
		}
		ref := plan.credentialRef(candidate)
		permit = &Permit{
			SnapshotDigest:    normalized.snapshotDigest,
			RequestDigest:     normalized.requestDigest,
			AssignmentDigest:  assignmentDigest,
			RPID:              request.RPID,
			Credential:        ref,
			TransactionDigest: request.TransactionDigest,
			Purpose:           requestPurpose(*request),
		}
		permit.LockID = digestBytes(
			[]byte("credentialauth/consumption-lock/v1"),
			[]byte(normalized.snapshotDigest),
			[]byte(normalized.requestDigest),
			[]byte(assignmentDigest),
			[]byte(fmt.Sprintf("%d", candidateIndex)),
			[]byte(candidate.String()),
			[]byte(request.TransactionDigest),
			[]byte(requestPurpose(*request)),
		)
		proof.Permit = permit
	}
	lockedSet := make(map[credentialKey]struct{}, len(proof.Consumed)+len(proof.Reserved)+1)
	for _, ref := range proof.Consumed {
		key, _ := ref.key()
		lockedSet[key] = struct{}{}
	}
	for _, ref := range proof.Reserved {
		key, _ := ref.key()
		lockedSet[key] = struct{}{}
	}
	if proof.Permit != nil {
		key, _ := proof.Permit.Credential.key()
		lockedSet[key] = struct{}{}
	}
	proof.Locked = refsForKeys(sortedCredentialKeys(lockedSet))
	proof.Digest = computeProofDigest(proof)
	return proof, nil
}

func computeProofDigest(proof *Proof) string {
	if proof == nil {
		return ""
	}
	clone := *proof
	clone.Digest = ""
	digest, err := digestJSON(clone)
	if err != nil {
		return ""
	}
	return digest
}

func cloneRequest(request *PermitRequest) *PermitRequest {
	if request == nil {
		return nil
	}
	clone := *request
	clone.Candidates = cloneRefs(request.Candidates)
	clone.CredentialIDs = append([]string(nil), request.CredentialIDs...)
	return &clone
}

func selectPermitCandidate(plan *mergePlan, request PermitRequest, mode SafetyMode) (credentialKey, int, error) {
	candidates := append([]CredentialRef(nil), request.Candidates...)
	if len(candidates) == 0 {
		keys := make([]credentialKey, 0, len(plan.creds))
		for key := range plan.creds {
			if key.rpID == request.RPID {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].less(keys[j])
		})
		candidates = refsForKeys(keys)
	}

	graph := plan.graph()
	edgeCreds := plan.edgeCredentials()
	purpose := requestPurpose(request)
	for _, ref := range candidates {
		key, err := ref.key()
		if err != nil {
			return credentialKey{}, -1, err
		}
		if key.rpID != request.RPID {
			continue
		}
		if _, reserved := plan.reserved[key]; reserved {
			continue
		}
		if len(ref.AllowedPurposes) > 0 && !newPurposeSet(ref.AllowedPurposes).allows(purpose) {
			continue
		}
		info := plan.creds[key]
		if info != nil {
			if !info.usable || !info.purposes.allows(purpose) {
				continue
			}
		}
		index := plan.indexOf(key)
		if index >= 0 {
			if mode == SafetyUniversal {
				if _, used := edgeCreds[index]; used {
					continue
				}
			} else {
				if _, ok := graph.maximumMatching(index, -1); !ok {
					continue
				}
			}
		}
		return key, index, nil
	}
	return credentialKey{}, -1, fmt.Errorf("%w: no candidate under rpID %q is available in the requested safety mode",
		ErrCandidatesExhausted, request.RPID)
}

func (p *mergePlan) credentialRef(key credentialKey) CredentialRef {
	info := p.creds[key]
	if info == nil {
		return key.ref()
	}
	return canonicalRef(key, info.purposes, info.sources)
}

func reservationRefs(reservations []Reservation) []CredentialRef {
	result := make([]CredentialRef, 0, len(reservations))
	for _, reservation := range reservations {
		key, err := reservation.key()
		if err != nil {
			continue
		}
		result = append(result, key.ref())
	}
	sortRefs(result)
	return result
}

func sortRefs(refs []CredentialRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		left, leftErr := refs[i].key()
		right, rightErr := refs[j].key()
		if leftErr != nil || rightErr != nil {
			return false
		}
		return left.less(right)
	})
}
