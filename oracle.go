package credentialauth

import (
	"errors"
	"fmt"
	"sort"
)

// DefaultOracleNodeLimit bounds enumeration work.  It is deliberately much
// larger than the small instances for which exhaustive enumeration is useful.
const DefaultOracleNodeLimit = 2_000_000

// OracleResult is the exhaustive answer for a small snapshot.
type OracleResult struct {
	Satisfiable         bool               `json:"satisfiable"`
	AssignmentCount     int                `json:"assignmentCount"`
	CanonicalAssignment []BatchAssignment  `json:"canonicalAssignment,omitempty"`
	Reserved            []CredentialRef    `json:"reserved"`
	ConditionallyUsed   []CredentialRef    `json:"conditionallyUsed"`
	Unavoidable         []CredentialRef    `json:"unavoidable"`
	UniversallyFree     []CredentialRef    `json:"universallyFree"`
	Avoidable           []CredentialRef    `json:"avoidable"`
	Statuses            []CredentialStatus `json:"statuses"`
}

// Oracle enumerates every compatible global assignment for a small snapshot.
// It is intended as an independent correctness check for the polynomial
// matching path, not as the production verifier.
func Oracle(snapshot Snapshot) (*OracleResult, error) {
	return OracleWithLimit(snapshot, DefaultOracleNodeLimit)
}

// BruteForce is an alias for Oracle.
func BruteForce(snapshot Snapshot) (*OracleResult, error) {
	return Oracle(snapshot)
}

// BruteForceOracle is an alias for Oracle.
func BruteForceOracle(snapshot Snapshot) (*OracleResult, error) {
	return Oracle(snapshot)
}

// OracleWithLimit is Oracle with an explicit recursion-node limit.
func OracleWithLimit(snapshot Snapshot, nodeLimit int) (*OracleResult, error) {
	if nodeLimit <= 0 {
		return nil, invalidf("oracle node limit must be positive")
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	plan, err := buildPlan(normalized)
	if err != nil {
		if errors.Is(err, ErrNoGlobalAssignment) {
			return &OracleResult{
				Satisfiable:     false,
				AssignmentCount: 0,
				Reserved:        reservationRefs(normalized.reservations),
			}, nil
		}
		return nil, err
	}
	assignments, err := enumeratePlanAssignments(plan, nodeLimit)
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return &OracleResult{
			Satisfiable:     false,
			AssignmentCount: 0,
			Reserved:        reservationRefs(normalized.reservations),
		}, nil
	}

	edgeCreds := plan.edgeCredentials()
	usedSome := make(map[int]bool, len(edgeCreds))
	usedEvery := make(map[int]bool, len(edgeCreds))
	for credential := range edgeCreds {
		usedEvery[credential] = true
	}
	for _, assignment := range assignments {
		usedHere := make(map[int]bool, len(assignment))
		for _, credential := range assignment {
			if credential >= 0 {
				usedHere[credential] = true
				usedSome[credential] = true
			}
		}
		for credential := range edgeCreds {
			if !usedHere[credential] {
				usedEvery[credential] = false
			}
		}
	}

	result := &OracleResult{
		Satisfiable:         true,
		AssignmentCount:     len(assignments),
		CanonicalAssignment: plan.batchAssignments(assignments[0]),
		Reserved:            reservationRefs(normalized.reservations),
	}
	for _, key := range plan.allStatusKeys() {
		index := plan.indexOf(key)
		_, reserved := plan.reserved[key]
		info := plan.creds[key]
		conditionallyUsed := index >= 0 && usedSome[index]
		unavoidable := index >= 0 && usedEvery[index]
		universallyFree := !conditionallyUsed
		avoidable := !unavoidable && !reserved
		available := !reserved && avoidable
		if info != nil && !info.usable {
			available = false
		}
		status := CredentialStatus{
			Credential:        plan.credentialRef(key),
			Reserved:          reserved,
			ConditionallyUsed: conditionallyUsed,
			Unavoidable:       unavoidable,
			UniversallyFree:   universallyFree,
			Available:         available,
		}
		result.Statuses = append(result.Statuses, status)
		if unavoidable {
			result.Unavoidable = append(result.Unavoidable, status.Credential)
		}
		if conditionallyUsed {
			result.ConditionallyUsed = append(result.ConditionallyUsed, status.Credential)
		}
		if universallyFree {
			result.UniversallyFree = append(result.UniversallyFree, status.Credential)
		}
		if avoidable {
			result.Avoidable = append(result.Avoidable, status.Credential)
		}
	}
	sortRefs(result.Reserved)
	sortRefs(result.ConditionallyUsed)
	sortRefs(result.Unavoidable)
	sortRefs(result.UniversallyFree)
	sortRefs(result.Avoidable)
	sort.SliceStable(result.Statuses, func(i, j int) bool {
		left, _ := result.Statuses[i].Credential.key()
		right, _ := result.Statuses[j].Credential.key()
		return left.less(right)
	})
	return result, nil
}

// EnumerateAssignments returns every compatible assignment for a small
// snapshot.  It is useful for cross-checking the production matching path.
func EnumerateAssignments(snapshot Snapshot) ([][]BatchAssignment, error) {
	return EnumerateAssignmentsWithLimit(snapshot, DefaultOracleNodeLimit)
}

// EnumerateAssignmentsWithLimit is EnumerateAssignments with an explicit
// recursion-node limit.
func EnumerateAssignmentsWithLimit(snapshot Snapshot, nodeLimit int) ([][]BatchAssignment, error) {
	if nodeLimit <= 0 {
		return nil, invalidf("oracle node limit must be positive")
	}
	normalized, err := normalizeSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	plan, err := buildPlan(normalized)
	if err != nil {
		if errors.Is(err, ErrNoGlobalAssignment) {
			return [][]BatchAssignment{}, nil
		}
		return nil, err
	}
	raw, err := enumeratePlanAssignments(plan, nodeLimit)
	if err != nil {
		return nil, err
	}
	result := make([][]BatchAssignment, 0, len(raw))
	for _, assignment := range raw {
		result = append(result, plan.batchAssignments(assignment))
	}
	return result, nil
}

func enumeratePlanAssignments(plan *mergePlan, nodeLimit int) ([][]int, error) {
	var (
		nodes     int
		result    [][]int
		used      = make([]bool, len(plan.credOrder))
		current   = make([]int, len(plan.slots))
		enumerate func(int) error
	)
	for i := range current {
		current[i] = -1
	}
	enumerate = func(slot int) error {
		nodes++
		if nodes > nodeLimit {
			return fmt.Errorf("%w: exceeded %d nodes", ErrOracleLimit, nodeLimit)
		}
		if slot == len(plan.slots) {
			result = append(result, append([]int(nil), current...))
			return nil
		}
		for _, credential := range plan.slots[slot].allowed {
			if credential < 0 || credential >= len(used) || used[credential] {
				continue
			}
			used[credential] = true
			current[slot] = credential
			if err := enumerate(slot + 1); err != nil {
				return err
			}
			current[slot] = -1
			used[credential] = false
		}
		return nil
	}
	if err := enumerate(0); err != nil {
		return nil, err
	}
	return result, nil
}
