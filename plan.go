package tokenmigrate

import (
	"fmt"
	"sort"
	"time"
)

// Plan is the result of planning a migration. It is immutable from the
// caller's perspective; Materialize never modifies either the plan or its
// input.
type Plan struct {
	At           time.Time
	InputDigest  string
	PlanDigest   string
	Assignments  []Assignment
	Reissues     []Reissue
	TargetScopes []Scope
	Certificate  Certificate

	table *ReadTable
}

// ReissueCount returns the number of old records whose token must change.
func (p *Plan) ReissueCount() int {
	return len(p.Reissues)
}

// Lookup redeems an old, scoped token through the compatibility table.
func (p *Plan) Lookup(req ReadRequest) (ReadResult, error) {
	if p == nil || p.table == nil {
		return ReadResult{}, ErrNotFound
	}
	if req.At.IsZero() {
		req.At = p.At
	}
	return p.table.lookup(req)
}

// LookupUnique redeems token without requiring the caller to supply its
// origin. It succeeds only when the tenant has exactly one matching old read.
// Every failure, including ambiguity, is ErrNotFound.
func (p *Plan) LookupUnique(tenant DomainID, token Token, at time.Time) (ReadResult, error) {
	if p == nil || p.table == nil {
		return ReadResult{}, ErrNotFound
	}
	if at.IsZero() {
		at = p.At
	}
	return p.table.lookupUnique(tenant, token, at)
}

type placement struct {
	assignment Assignment
	change     *Change
}

type classBucket struct {
	class   Commitment
	records []int
	reissue []RecordID
	weight  int
}

// Build validates in, computes commitment equality classes with verified
// proofs, selects an optimal reissue set, and constructs the compatibility
// table. It never mutates in.
func Build(in Input, opts Options) (*Plan, error) {
	at := opts.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	validated, err := validateInput(in, at, opts.Verifier)
	if err != nil {
		return nil, err
	}

	placements, activeTargets, err := computePlacements(validated)
	if err != nil {
		return nil, err
	}
	if err := applyReissues(placements, opts.Allocator); err != nil {
		return nil, err
	}

	result, err := finalizePlan(validated, placements, activeTargets)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func computePlacements(v *validatedInput) ([]placement, []Scope, error) {
	activeBySource := make(map[DomainID]Change)
	targetSet := make(map[Scope]struct{})
	for _, c := range v.in.Changes {
		if !c.Valid.Contains(v.at) {
			continue
		}
		for _, m := range c.Mappings {
			activeBySource[m.From] = c
			if m.Generation != 0 {
				targetSet[Scope{Domain: m.To, Generation: m.Generation}] = struct{}{}
			}
		}
	}

	placements := make([]placement, 0, len(v.in.Records))
	for _, r := range v.in.Records {
		if !recordLiveAt(v, r, v.at) {
			continue
		}
		a := Assignment{
			RecordID:   r.ID,
			Origin:     r.Scope(),
			Target:     r.Scope(),
			OldToken:   r.Token,
			NewToken:   r.Token,
			Commitment: r.Commitment,
			Class:      v.classes.find(r.Commitment),
		}
		var moveChange *Change

		if active, ok := activeBySource[r.Domain]; ok {
			moved := false
			for i := range active.Mappings {
				m := &active.Mappings[i]
				if m.From != r.Domain {
					continue
				}
				selected := !m.Explicit || containsRecordID(m.Records, r.ID)
				if !selected {
					continue
				}
				if moved {
					return nil, nil, invalid("record %s is selected by multiple active mappings", r.ID)
				}
				targetGeneration := m.Generation
				if targetGeneration == 0 {
					targetGeneration = r.Generation
				}
				a.Target = Scope{Domain: m.To, Generation: targetGeneration}
				a.Moved = a.Target != a.Origin
				moved = true
				changeCopy := active
				moveChange = &changeCopy
			}
		}
		if a.Moved {
			a.Canonical = a.Class != a.Commitment
			targetSet[a.Target] = struct{}{}
		}
		placements = append(placements, placement{assignment: a, change: moveChange})
	}

	sort.Slice(placements, func(i, j int) bool {
		a, b := placements[i].assignment, placements[j].assignment
		if a.Target != b.Target {
			if a.Target.Domain != b.Target.Domain {
				return a.Target.Domain < b.Target.Domain
			}
			return a.Target.Generation < b.Target.Generation
		}
		if a.OldToken != b.OldToken {
			return a.OldToken < b.OldToken
		}
		return a.RecordID < b.RecordID
	})

	targets := make([]Scope, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Domain != targets[j].Domain {
			return targets[i].Domain < targets[j].Domain
		}
		return targets[i].Generation < targets[j].Generation
	})
	return placements, targets, nil
}

func applyReissues(placements []placement, allocator TokenAllocator) error {
	groups := make(map[string][]int)
	for i := range placements {
		a := placements[i].assignment
		key := scopeTokenKey(a.Target, a.NewToken)
		groups[key] = append(groups[key], i)
	}

	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)

	unavailable := make(map[Scope]map[Token]struct{})
	for i := range placements {
		a := placements[i].assignment
		if unavailable[a.Target] == nil {
			unavailable[a.Target] = make(map[Token]struct{})
		}
		unavailable[a.Target][a.OldToken] = struct{}{}
	}

	for _, key := range groupKeys {
		indices := groups[key]
		rawCommitments := make(map[Commitment]struct{})
		for _, index := range indices {
			rawCommitments[placements[index].assignment.Commitment] = struct{}{}
		}
		if len(rawCommitments) > 1 {
			for _, index := range indices {
				if placements[index].assignment.Class != placements[index].assignment.Commitment {
					placements[index].assignment.Canonical = true
				}
			}
		}
		byClass := make(map[Commitment][]int)
		for _, index := range indices {
			class := placements[index].assignment.Class
			byClass[class] = append(byClass[class], index)
		}
		if len(byClass) <= 1 {
			continue
		}

		buckets := make([]classBucket, 0, len(byClass))
		for class, members := range byClass {
			inBucket := make(map[int]struct{}, len(members))
			for _, index := range members {
				inBucket[index] = struct{}{}
			}
			reissue := make([]RecordID, 0, len(indices)-len(members))
			for _, index := range indices {
				if _, keep := inBucket[index]; keep {
					continue
				}
				reissue = append(reissue, placements[index].assignment.RecordID)
			}
			sort.Slice(reissue, func(i, j int) bool { return reissue[i] < reissue[j] })
			buckets = append(buckets, classBucket{
				class:   class,
				records: append([]int(nil), members...),
				reissue: reissue,
				weight:  len(members),
			})
		}
		sort.Slice(buckets, func(i, j int) bool {
			if buckets[i].weight != buckets[j].weight {
				return buckets[i].weight > buckets[j].weight
			}
			if lessRecordIDs(buckets[i].reissue, buckets[j].reissue) {
				return true
			}
			if lessRecordIDs(buckets[j].reissue, buckets[i].reissue) {
				return false
			}
			return buckets[i].class < buckets[j].class
		})

		keep := buckets[0]
		for _, index := range keep.records {
			placements[index].assignment.NewToken = placements[index].assignment.OldToken
		}
		if len(buckets) == 1 {
			continue
		}
		if allocator == nil {
			return ErrAllocatorRequired
		}

		target := placements[indices[0]].assignment.Target
		for _, bucket := range buckets[1:] {
			fresh, err := allocateFreshToken(target, bucket, placements, unavailable[target], allocator)
			if err != nil {
				return err
			}
			for _, index := range bucket.records {
				placements[index].assignment.NewToken = fresh
				placements[index].assignment.Reissued = true
				if placements[index].assignment.Class != placements[index].assignment.Commitment {
					placements[index].assignment.Canonical = true
				}
			}
		}
	}
	return nil
}

func allocateFreshToken(scope Scope, bucket classBucket, placements []placement, unavailable map[Token]struct{}, allocator TokenAllocator) (Token, error) {
	first := placements[bucket.records[0]].assignment
	unavailableList := make([]Token, 0, len(unavailable))
	for token := range unavailable {
		unavailableList = append(unavailableList, token)
	}
	sort.Slice(unavailableList, func(i, j int) bool { return unavailableList[i] < unavailableList[j] })

	for attempt := 0; attempt < 64; attempt++ {
		req := AllocationRequest{
			Scope:       scope,
			RecordID:    first.RecordID,
			Commitment:  bucket.class,
			OldToken:    first.OldToken,
			Attempt:     attempt,
			Unavailable: unavailableList,
		}
		token, err := allocator.Allocate(req)
		if err != nil {
			return "", err
		}
		if token == "" {
			continue
		}
		if _, exists := unavailable[token]; exists {
			continue
		}
		unavailable[token] = struct{}{}
		return token, nil
	}
	return "", fmt.Errorf("%w: allocator did not produce a fresh token for %s after 64 attempts", ErrAllocatorRequired, scope)
}

func finalizePlan(v *validatedInput, placements []placement, activeTargets []Scope) (*Plan, error) {
	assignments := make([]Assignment, 0, len(placements))
	reissues := make([]Reissue, 0)
	for i := range placements {
		a := placements[i].assignment
		assignments = append(assignments, a)
		if a.Reissued {
			reissues = append(reissues, Reissue{
				RecordID: a.RecordID,
				Origin:   a.Origin,
				OldToken: a.OldToken,
				Target:   a.Target,
				NewToken: a.NewToken,
				Class:    a.Class,
			})
		}
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].RecordID < assignments[j].RecordID })
	sort.Slice(reissues, func(i, j int) bool { return reissues[i].RecordID < reissues[j].RecordID })

	if err := verifyTargetUniqueness(assignments, v.classes); err != nil {
		return nil, err
	}
	table, err := buildCompatibilityTable(v, placements)
	if err != nil {
		return nil, err
	}

	targets := targetScopeUnion(assignments, activeTargets)
	planDigest := canonicalPlanDigest(v.inputDigest, v.at, assignments, reissues, targets)
	certificate := buildCertificate(v, assignments, reissues, targets, table)
	certificate.PlanDigest = planDigest
	plan := &Plan{
		At:           v.at,
		InputDigest:  v.inputDigest,
		PlanDigest:   planDigest,
		Assignments:  assignments,
		Reissues:     reissues,
		TargetScopes: targets,
		Certificate:  certificate,
		table:        table,
	}
	if err := plan.Certificate.Validate(); err != nil {
		return nil, err
	}
	return plan, nil
}

func verifyTargetUniqueness(assignments []Assignment, classes *unionFind) error {
	seen := make(map[string]Commitment)
	for _, a := range assignments {
		key := scopeTokenKey(a.Target, a.NewToken)
		class := classes.find(a.Commitment)
		if previous, ok := seen[key]; ok && previous != class {
			return invalid("target token %s in %s would be ambiguous", a.NewToken, a.Target)
		}
		seen[key] = class
	}
	return nil
}

func targetScopeUnion(assignments []Assignment, activeTargets []Scope) []Scope {
	set := make(map[Scope]struct{}, len(activeTargets))
	for _, target := range activeTargets {
		set[target] = struct{}{}
	}
	for _, a := range assignments {
		set[a.Target] = struct{}{}
	}
	out := make([]Scope, 0, len(set))
	for scope := range set {
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Generation < out[j].Generation
	})
	return out
}

func recordLiveAt(v *validatedInput, r Issuance, at time.Time) bool {
	if !r.Valid.Contains(at) {
		return false
	}
	if d, ok := v.domains[r.Domain]; ok && !d.Valid.Contains(at) {
		return false
	}
	return true
}

func containsRecordID(ids []RecordID, wanted RecordID) bool {
	for _, id := range ids {
		if id == wanted {
			return true
		}
	}
	return false
}

func lessRecordIDs(a, b []RecordID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
