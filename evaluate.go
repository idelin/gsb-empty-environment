package netsec

import (
	"sort"
	"time"
)

// EvaluationPolicy selects which security callbacks a model applies. The
// exported policies make differential tests explicit rather than hidden in
// test-only branches of the production host.
type EvaluationPolicy struct {
	DeduplicatePersons     bool
	DeduplicateKeys        bool
	EnforceEpoch           bool
	EnforceRequestBinding  bool
	EnforceContactValidity bool
	EnforceRevocation      bool
	EnforceSignature       bool
	EnforceDeadline        bool
}

var correctPolicy = EvaluationPolicy{
	DeduplicatePersons:     true,
	DeduplicateKeys:        true,
	EnforceEpoch:           true,
	EnforceRequestBinding:  true,
	EnforceContactValidity: true,
	EnforceRevocation:      true,
	EnforceSignature:       true,
	EnforceDeadline:        true,
}

var (
	// CorrectPolicy applies every callback required by the recovery model.
	// Host operations use an internal copy, so reassigning this variable only
	// affects callers that pass it explicitly to evaluation helpers.
	CorrectPolicy = correctPolicy

	// PendingDedupEpochPolicy models a pending implementation that forgot the
	// person/key deduplication and epoch callbacks while still checking
	// signatures, request binding, revocation, and deadlines.
	PendingDedupEpochPolicy = EvaluationPolicy{
		DeduplicatePersons:     false,
		DeduplicateKeys:        false,
		EnforceEpoch:           false,
		EnforceRequestBinding:  true,
		EnforceContactValidity: true,
		EnforceRevocation:      true,
		EnforceSignature:       true,
		EnforceDeadline:        true,
	}

	// PendingNoDedupPolicy isolates the missing person/key dedup callbacks.
	PendingNoDedupPolicy = EvaluationPolicy{
		DeduplicatePersons:     false,
		DeduplicateKeys:        false,
		EnforceEpoch:           true,
		EnforceRequestBinding:  true,
		EnforceContactValidity: true,
		EnforceRevocation:      true,
		EnforceSignature:       true,
		EnforceDeadline:        true,
	}

	// PendingNoEpochPolicy isolates the missing epoch callback.
	PendingNoEpochPolicy = EvaluationPolicy{
		DeduplicatePersons:     true,
		DeduplicateKeys:        true,
		EnforceEpoch:           false,
		EnforceRequestBinding:  true,
		EnforceContactValidity: true,
		EnforceRevocation:      true,
		EnforceSignature:       true,
		EnforceDeadline:        true,
	}
)

// Evaluation is the result of counting an approval set under one policy.
type Evaluation struct {
	AcceptedContacts []ContactID
	Weight           uint64
	DistinctPersons  uint32
	ThresholdMet     bool
	MinPersonsMet    bool
	Eligible         bool
	CoolingStartedAt time.Time
	CoolingEndsAt    time.Time
	CanComplete      bool
}

type contactUnion struct {
	parent []int
	rank   []int
}

func newContactUnion(size int) *contactUnion {
	u := &contactUnion{parent: make([]int, size), rank: make([]int, size)}
	for i := range u.parent {
		u.parent[i] = i
	}
	return u
}

func (u *contactUnion) find(i int) int {
	if u.parent[i] != i {
		u.parent[i] = u.find(u.parent[i])
	}
	return u.parent[i]
}

func (u *contactUnion) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

func approvalAccepted(cfg Config, rec *Recovery, approval Approval, policy EvaluationPolicy) (Contact, bool) {
	contact, ok := cfg.Contact(approval.ContactID)
	if !ok {
		return Contact{}, false
	}
	received := approval.ReceivedAt
	if received.IsZero() {
		received = rec.CreatedAt
	}
	if policy.EnforceDeadline && !rec.Deadline.IsZero() && received.After(rec.Deadline) {
		return Contact{}, false
	}
	if policy.EnforceSignature && !verifyApprovalSignature(contact, approval) {
		return Contact{}, false
	}
	if policy.EnforceRequestBinding {
		if approval.AccountID != rec.AccountID ||
			approval.RecoveryID != rec.ID ||
			approval.TargetDigest != rec.TargetDigest ||
			approval.DeviceDigest != rec.DeviceDigest ||
			!approval.Deadline.Equal(rec.Deadline) ||
			approval.ContactFingerprint != contact.Fingerprint {
			return Contact{}, false
		}
	}
	if policy.EnforceEpoch {
		if rec.Epoch != cfg.RecoveryEpoch || approval.Epoch != rec.Epoch {
			return Contact{}, false
		}
	}
	if policy.EnforceContactValidity {
		if contact.StartEpoch != 0 && rec.Epoch < contact.StartEpoch {
			return Contact{}, false
		}
		if contact.EndEpoch != 0 && rec.Epoch >= contact.EndEpoch {
			return Contact{}, false
		}
	}
	if policy.EnforceRevocation && contact.Revoked {
		return Contact{}, false
	}
	return contact, true
}

func approvalMetrics(cfg Config, accepted []Contact, policy EvaluationPolicy) (uint64, uint32) {
	if len(accepted) == 0 {
		return 0, 0
	}

	index := make(map[ContactID]int, len(cfg.Contacts))
	for i, contact := range cfg.Contacts {
		index[contact.ContactID] = i
	}

	union := newContactUnion(len(cfg.Contacts))
	if policy.DeduplicatePersons {
		firstByPerson := make(map[PersonID]int, len(cfg.Contacts))
		for i, contact := range cfg.Contacts {
			if first, ok := firstByPerson[contact.PersonID]; ok {
				union.union(first, i)
			} else {
				firstByPerson[contact.PersonID] = i
			}
		}
	}
	if policy.DeduplicateKeys {
		firstByKey := make(map[string]int, len(cfg.Contacts))
		for i, contact := range cfg.Contacts {
			key := string(contact.PublicKey)
			if first, ok := firstByKey[key]; ok {
				union.union(first, i)
			} else {
				firstByKey[key] = i
			}
		}
	}

	groupWeights := make(map[int]uint64, len(accepted))
	personsByRoot := make(map[int]struct{}, len(accepted))
	persons := make(map[PersonID]struct{}, len(accepted))
	for _, contact := range accepted {
		i, ok := index[contact.ContactID]
		if !ok {
			continue
		}
		root := union.find(i)
		if uint64(contact.Weight) > groupWeights[root] {
			groupWeights[root] = uint64(contact.Weight)
		}
		personsByRoot[root] = struct{}{}
		persons[contact.PersonID] = struct{}{}
	}

	var weight uint64
	for _, value := range groupWeights {
		weight += value
	}
	if policy.DeduplicatePersons || policy.DeduplicateKeys {
		return weight, uint32(len(personsByRoot))
	}
	return weight, uint32(len(persons))
}

// EvaluateApprovals counts approvals under policy, simulating the first moment
// at which the threshold and distinct-person minimum become satisfied.
func EvaluateApprovals(cfg Config, rec Recovery, approvals []Approval, now time.Time, policy EvaluationPolicy) Evaluation {
	cfg = cfg.clone()
	for i := range cfg.Contacts {
		if cfg.Contacts[i].Fingerprint.IsZero() {
			cfg.Contacts[i].Fingerprint = PublicKeyFingerprint(cfg.Contacts[i].PublicKey)
		}
	}
	ordered := append([]Approval(nil), approvals...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].ReceivedAt, ordered[j].ReceivedAt
		if left.IsZero() {
			left = rec.CreatedAt
		}
		if right.IsZero() {
			right = rec.CreatedAt
		}
		if left.Equal(right) {
			return ordered[i].ContactID < ordered[j].ContactID
		}
		return left.Before(right)
	})

	seen := make(map[ContactID]struct{}, len(ordered))
	accepted := make([]Contact, 0, len(ordered))
	evaluation := Evaluation{}
	coolingPeriod := cfg.CoolingPeriod
	if coolingPeriod <= 0 {
		coolingPeriod = DefaultCoolingPeriod
	}

	for _, approval := range ordered {
		if _, ok := seen[approval.ContactID]; ok {
			continue
		}
		contact, ok := approvalAccepted(cfg, &rec, approval, policy)
		if !ok {
			continue
		}
		seen[approval.ContactID] = struct{}{}
		accepted = append(accepted, contact)

		weight, persons := approvalMetrics(cfg, accepted, policy)
		evaluation.Weight = weight
		evaluation.DistinctPersons = persons
		evaluation.ThresholdMet = weight >= uint64(cfg.Threshold)
		evaluation.MinPersonsMet = persons >= cfg.MinDistinctPersons
		evaluation.Eligible = evaluation.ThresholdMet && evaluation.MinPersonsMet

		if evaluation.Eligible && evaluation.CoolingStartedAt.IsZero() {
			start := approval.ReceivedAt
			if start.IsZero() {
				start = rec.CreatedAt
			}
			evaluation.CoolingStartedAt = start
			evaluation.CoolingEndsAt = start.Add(coolingPeriod)
		}
	}

	evaluation.AcceptedContacts = make([]ContactID, 0, len(accepted))
	for _, contact := range accepted {
		evaluation.AcceptedContacts = append(evaluation.AcceptedContacts, contact.ContactID)
	}
	sort.Slice(evaluation.AcceptedContacts, func(i, j int) bool {
		return evaluation.AcceptedContacts[i] < evaluation.AcceptedContacts[j]
	})

	if evaluation.Eligible && !evaluation.CoolingStartedAt.IsZero() {
		evaluation.CanComplete = !now.Before(evaluation.CoolingEndsAt)
	}
	return evaluation
}
