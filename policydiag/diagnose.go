package policydiag

import "sort"

// Options controls the exhaustive enumeration.
type Options struct {
	// MaxChecks bounds the number of rule subsets evaluated. Zero means full
	// enumeration, which always fits at the 18-rule limit (262144 subsets).
	MaxChecks int
}

// DecisionOutcome is the granted/denied state of one concrete request, derived
// from a finite-domain exhaustive enumeration rather than a boolean SAT call.
type DecisionOutcome struct {
	Operation    string
	SubjectKey   string
	PersonID     string
	FileID       string
	FileLevel    string
	Granted      bool
	Denied       bool
	DualRequired bool
	ApproverPair *ApproverPair
	MatchedAllow []string
	MatchedDeny  []string
	MatchedDual  []string
}

// RequirementFailure explains a requirement that still cannot be met.
type RequirementFailure struct {
	RequirementID string
	Kind          RequirementKind
	Operation     string
	SubjectKey    string
	FileID        string
	Reason        string
	Outcome       *DecisionOutcome
}

// Witness is the concrete evidence that removing one conflict group repairs
// the policy. It names real subjects, files and distinct-person approvers.
type Witness struct {
	// RemovedRuleIDs are the rules removed to obtain this repaired policy view.
	RemovedRuleIDs []string
	// Availability proves every availability requirement now works.
	Availability []DecisionOutcome
	// NoUnauthorized samples the full-domain check. It is empty when every
	// under-cleared request is denied, which is the desired state.
	NoUnauthorized []DecisionOutcome
	// RemainingFailures lists requirements that removing this group alone does
	// not repair. Empty means the single removal is a complete repair.
	RemainingFailures []RequirementFailure
}

// ConflictGroup is one minimal conflict rule group.
type ConflictGroup struct {
	// RuleIDs is the minimal correction set: removing all of them repairs the
	// policy and no proper subset does.
	RuleIDs []string
	// Minimal is false only when the enumeration budget ran out before the
	// minimality proof could be completed.
	Minimal bool
	// Witness demonstrates the repaired policy with concrete domain values.
	Witness *Witness
}

// Report is the full diagnostic result returned to the release service.
type Report struct {
	// ConflictGroups are minimal correction sets, smallest groups first.
	ConflictGroups []ConflictGroup
	// Complete is true when every minimal conflict group was enumerated.
	Complete bool
	// BudgetExhausted is true when MaxChecks stopped the enumeration early.
	BudgetExhausted bool
	// ChecksUsed is the number of rule subsets actually evaluated.
	ChecksUsed int
	// Unfixable lists requirements no rule removal can satisfy. It is set
	// only when the policy has no repair at all.
	Unfixable []RequirementFailure
	// AliasAudit records the finite-domain check that same-person key aliases
	// can never be accepted as the two required approvers.
	AliasAudit AliasAudit
}

// AliasAudit proves the dual-approval anti-alias property over the domain.
type AliasAudit struct {
	// PersonAliases maps every person holding several key identities to them.
	PersonAliases map[string][]string
	// RejectedAliasPairs lists same-person key pairs that are never eligible.
	RejectedAliasPairs []AliasPair
	// CheckedRequests is the number of request rows enumerated.
	CheckedRequests int
	// PropertyHolds is true when no request accepts an alias pair as approval.
	PropertyHolds bool
}

// AliasPair is a same-person key pair that must not count as dual approval.
type AliasPair struct {
	PersonID  string
	FirstKey  string
	SecondKey string
}

type evalResult struct {
	granted    []uint64
	dualNeeded []uint64
	denied     []uint64
}

// applySubset evaluates the selected rules against the full finite request
// universe. Deny takes precedence; a dual grant without an eligible
// distinct-person approver pair is not an effective grant.
func (t *requestTable) applySubset(active uint32, kinds []RuleKind) evalResult {
	allow := make([]uint64, t.words)
	deny := make([]uint64, t.words)
	dual := make([]uint64, t.words)
	for i := range kinds {
		if active&(1<<uint(i)) == 0 {
			continue
		}
		for w := 0; w < t.words; w++ {
			bits := t.match[i][w]
			switch kinds[i] {
			case KindAllow:
				allow[w] |= bits
			case KindDeny:
				deny[w] |= bits
			case KindDual:
				dual[w] |= bits
			}
		}
	}
	res := evalResult{
		granted:    make([]uint64, t.words),
		dualNeeded: make([]uint64, t.words),
		denied:     make([]uint64, t.words),
	}
	for w := 0; w < t.words; w++ {
		permitted := (allow[w] | dual[w]) &^ deny[w]
		res.denied[w] = (allow[w] | dual[w]) & deny[w]
		res.dualNeeded[w] = permitted & dual[w]
		res.granted[w] = permitted &^ dual[w]
		res.granted[w] |= permitted & dual[w] & t.dualFeasible[w]
	}
	return res
}

func requirementIndex(t *requestTable, op, key, file string) int {
	for ri, req := range t.requests {
		if req.op == op && req.subject == key && req.file == file {
			return ri
		}
	}
	return -1
}

// checkRequirements returns the unsatisfied requirements under one subset.
func checkRequirements(p *Policy, t *requestTable, res evalResult) []RequirementFailure {
	var failures []RequirementFailure
	for _, req := range p.Requirements {
		switch req.Kind {
		case ReqAvailability:
			ri := requirementIndex(t, req.Operations[0], req.SubjectKey, req.FileID)
			if !bitIsSet(res.granted, ri) {
				failures = append(failures, RequirementFailure{
					RequirementID: req.ID,
					Kind:          req.Kind,
					Operation:     req.Operations[0],
					SubjectKey:    req.SubjectKey,
					FileID:        req.FileID,
					Reason:        availabilityReason(res, ri),
					Outcome:       describeOutcome(t, res, ri),
				})
			}
		case ReqNoUnauthorized:
			opFilter := map[string]bool{}
			for _, op := range req.Operations {
				opFilter[op] = true
			}
			for ri, row := range t.requests {
				if row.entitled || !bitIsSet(res.granted, ri) {
					continue
				}
				if len(opFilter) > 0 && !opFilter[row.op] {
					continue
				}
				failures = append(failures, RequirementFailure{
					RequirementID: req.ID,
					Kind:          req.Kind,
					Operation:     row.op,
					SubjectKey:    row.subject,
					FileID:        row.file,
					Reason:        "subject clearance is below the file classification but the request is granted",
					Outcome:       describeOutcome(t, res, ri),
				})
			}
		}
	}
	return failures
}

func availabilityReason(res evalResult, ri int) string {
	switch {
	case bitIsSet(res.denied, ri):
		return "a matching deny rule blocks the request (deny precedence)"
	case bitIsSet(res.dualNeeded, ri) && !bitIsSet(res.granted, ri):
		return "dual approval is required but no two distinct-person eligible approvers exist"
	default:
		return "no matching allow or require_dual rule grants the request"
	}
}

func describeOutcome(t *requestTable, res evalResult, ri int) *DecisionOutcome {
	row := t.requests[ri]
	out := &DecisionOutcome{
		Operation:    row.op,
		SubjectKey:   row.subject,
		PersonID:     row.person,
		FileID:       row.file,
		FileLevel:    row.fileLevel,
		Granted:      bitIsSet(res.granted, ri),
		Denied:       bitIsSet(res.denied, ri),
		DualRequired: bitIsSet(res.dualNeeded, ri),
	}
	if out.DualRequired {
		out.ApproverPair = firstPair(row.pairs)
	}
	return out
}

func firstPair(pairs []ApproverPair) *ApproverPair {
	if len(pairs) == 0 {
		return nil
	}
	pair := pairs[0]
	return &pair
}

// Diagnose runs the finite-domain exhaustive enumeration and returns every
// minimal conflict rule group together with concrete repair witnesses.
func Diagnose(p *Policy, opts Options) (*Report, error) {
	u, err := buildUniverse(p)
	if err != nil {
		return nil, err
	}
	if err := p.validate(u); err != nil {
		return nil, err
	}
	n := len(p.Rules)
	t := buildRequestTable(p, u)
	kinds := make([]RuleKind, n)
	for i, r := range p.Rules {
		kinds[i] = r.Kind
	}
	full := uint32(1)<<uint(n) - 1
	budget := opts.MaxChecks
	if budget <= 0 {
		budget = 1 << uint(n)
	}

	report := &Report{Complete: true, AliasAudit: auditAliases(p, u, t)}
	checks := 0
	satisfiable := func(active uint32) (bool, []RequirementFailure) {
		checks++
		res := t.applySubset(active, kinds)
		failures := checkRequirements(p, t, res)
		return len(failures) == 0, failures
	}

	ok, baselineFailures := satisfiable(full)
	if ok {
		report.ChecksUsed = checks
		return report, nil
	}

	// Enumerate removed-rule masks by increasing popcount. A mask is a minimal
	// correction set when the remaining rules satisfy every requirement and no
	// smaller recorded correction set is contained in it.
	found := map[uint32]bool{}
	budgetExhausted := false
enumerate:
	for size := 1; size <= n; size++ {
		stopped := false
		enumerateCombinations(n, size, func(mask uint32) bool {
			if checks >= budget {
				budgetExhausted = true
				stopped = true
				return false
			}
			if isDominated(mask, found) {
				return true
			}
			ok, _ := satisfiable(full &^ mask)
			if !ok {
				return true
			}
			found[mask] = true
			report.ConflictGroups = append(report.ConflictGroups,
				buildConflictGroup(p, t, kinds, full, mask))
			report.Unfixable = nil
			return true
		})
		if stopped || budgetExhausted {
			break enumerate
		}
	}

	report.BudgetExhausted = budgetExhausted
	report.Complete = !budgetExhausted
	for i := range report.ConflictGroups {
		// Ascending-size enumeration proves minimality against every smaller
		// removal. When the budget ran out mid-enumeration the proof cannot be
		// completed, so the groups are explicitly reported as not-proven-minimal.
		report.ConflictGroups[i].Minimal = !budgetExhausted
	}
	sortConflictGroups(report.ConflictGroups)
	if len(report.ConflictGroups) == 0 {
		// No rule subset repairs the requirements: surface the concrete
		// failures so the caller knows the policy cannot be fixed by deletion.
		report.Unfixable = baselineFailures
	}
	report.ChecksUsed = checks
	return report, nil
}

func isDominated(mask uint32, found map[uint32]bool) bool {
	for smaller := range found {
		if smaller&^mask == 0 {
			return true
		}
	}
	return false
}

func enumerateCombinations(n, size int, visit func(uint32) bool) {
	var rec func(start, remaining int, mask uint32) bool
	rec = func(start, remaining int, mask uint32) bool {
		if remaining == 0 {
			return visit(mask)
		}
		for i := start; i <= n-remaining; i++ {
			if !rec(i+1, remaining-1, mask|1<<uint(i)) {
				return false
			}
		}
		return true
	}
	rec(0, size, 0)
}

func buildConflictGroup(p *Policy, t *requestTable, kinds []RuleKind, full, removed uint32) ConflictGroup {
	active := full &^ removed
	res := t.applySubset(active, kinds)
	return ConflictGroup{
		RuleIDs: ruleIDs(p, removed),
		Minimal: true,
		Witness: buildWitness(p, t, kinds, active, removed, res),
	}
}

func ruleIDs(p *Policy, mask uint32) []string {
	var ids []string
	for i, r := range p.Rules {
		if mask&(1<<uint(i)) != 0 {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func sortConflictGroups(groups []ConflictGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].RuleIDs) != len(groups[j].RuleIDs) {
			return len(groups[i].RuleIDs) < len(groups[j].RuleIDs)
		}
		return joined(groups[i].RuleIDs) < joined(groups[j].RuleIDs)
	})
}

func joined(ids []string) string {
	out := ""
	for _, id := range ids {
		out += id + "\x00"
	}
	return out
}

func buildWitness(p *Policy, t *requestTable, kinds []RuleKind, active, removed uint32, res evalResult) *Witness {
	w := &Witness{RemovedRuleIDs: ruleIDs(p, removed)}
	noUnauthOps := map[string]bool{}
	for _, req := range p.Requirements {
		if req.Kind == ReqNoUnauthorized {
			for _, op := range req.Operations {
				noUnauthOps[op] = true
			}
		}
	}
	for _, req := range p.Requirements {
		if req.Kind != ReqAvailability {
			continue
		}
		ri := requirementIndex(t, req.Operations[0], req.SubjectKey, req.FileID)
		out := describeOutcomeWithMatches(p, t, kinds, active, res, ri)
		w.Availability = append(w.Availability, *out)
	}
	for ri, row := range t.requests {
		if row.entitled || !bitIsSet(res.granted, ri) {
			continue
		}
		if len(noUnauthOps) > 0 && !noUnauthOps[row.op] {
			continue
		}
		out := describeOutcomeWithMatches(p, t, kinds, active, res, ri)
		w.NoUnauthorized = append(w.NoUnauthorized, *out)
		if len(w.NoUnauthorized) >= 10 {
			break
		}
	}
	w.RemainingFailures = checkRequirements(p, t, res)
	return w
}

func describeOutcomeWithMatches(p *Policy, t *requestTable, kinds []RuleKind, active uint32, res evalResult, ri int) *DecisionOutcome {
	out := describeOutcome(t, res, ri)
	for i, r := range p.Rules {
		if active&(1<<uint(i)) == 0 || !bitIsSet(t.match[i], ri) {
			continue
		}
		switch kinds[i] {
		case KindAllow:
			out.MatchedAllow = append(out.MatchedAllow, r.ID)
		case KindDeny:
			out.MatchedDeny = append(out.MatchedDeny, r.ID)
		case KindDual:
			out.MatchedDual = append(out.MatchedDual, r.ID)
		}
	}
	return out
}

// auditAliases enumerates every same-person key pair and every request row to
// confirm that no dual-approval witness can be formed from two aliases of one
// human. This is an explicit finite-domain cross-check, not an assumption.
func auditAliases(p *Policy, u *universe, t *requestTable) AliasAudit {
	audit := AliasAudit{
		PersonAliases:   map[string][]string{},
		CheckedRequests: len(t.requests),
		PropertyHolds:   true,
	}
	byPerson := map[string][]string{}
	for _, s := range p.Subjects {
		byPerson[s.PersonID] = append(byPerson[s.PersonID], s.KeyID)
	}
	persons := make([]string, 0, len(byPerson))
	for person, keys := range byPerson {
		persons = append(persons, person)
		sorted := append([]string(nil), keys...)
		sort.Strings(sorted)
		if len(sorted) > 1 {
			audit.PersonAliases[person] = sorted
		}
	}
	sort.Strings(persons)
	for _, person := range persons {
		keys := byPerson[person]
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				audit.RejectedAliasPairs = append(audit.RejectedAliasPairs, AliasPair{
					PersonID:  person,
					FirstKey:  keys[i],
					SecondKey: keys[j],
				})
			}
		}
	}
	for _, req := range t.requests {
		for _, pair := range req.pairs {
			if pair.FirstPerson == pair.SecondPerson {
				audit.PropertyHolds = false
			}
		}
	}
	// Every same-person alias pair must be absent from every eligible pair list.
	for _, rejected := range audit.RejectedAliasPairs {
		for _, req := range t.requests {
			for _, pair := range req.pairs {
				if pair.FirstKey == rejected.FirstKey && pair.SecondKey == rejected.SecondKey {
					audit.PropertyHolds = false
				}
				if pair.FirstKey == rejected.SecondKey && pair.SecondKey == rejected.FirstKey {
					audit.PropertyHolds = false
				}
			}
		}
	}
	return audit
}

// EligibleApproverPairs exposes the finite-domain enumeration of distinct
// person approver pairs for one concrete request.
func EligibleApproverPairs(p *Policy, actorKey, fileID string) ([]ApproverPair, error) {
	u, err := buildUniverse(p)
	if err != nil {
		return nil, err
	}
	if err := p.validate(u); err != nil {
		return nil, err
	}
	if _, ok := u.keyIndex[actorKey]; !ok {
		return nil, unknownRef("subject", actorKey)
	}
	f, ok := p.fileByID(fileID)
	if !ok {
		return nil, unknownRef("file", fileID)
	}
	return eligibleApproverPairs(p, u, actorKey, f.Classification), nil
}

func (p *Policy) fileByID(fileID string) (File, bool) {
	for _, f := range p.Files {
		if f.FileID == fileID {
			return f, true
		}
	}
	return File{}, false
}
