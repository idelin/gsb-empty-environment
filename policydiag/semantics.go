package policydiag

// ApproverPair is one concrete two-approver witness. Both key identities must
// belong to different persons.
type ApproverPair struct {
	FirstKey     string
	FirstPerson  string
	SecondKey    string
	SecondPerson string
}

// Decision explains how the full policy treats one concrete request.
type Decision struct {
	Operation         string
	SubjectKey        string
	PersonID          string
	FileID            string
	Granted           bool
	Denied            bool
	DualRequired      bool
	DualFeasible      bool
	MatchedAllow      []string
	MatchedDeny       []string
	MatchedDual       []string
	EligibleApprovers []ApproverPair
}

// request is one row of the finite Cartesian product operation x subject x file.
type request struct {
	op        string
	subject   string
	person    string
	file      string
	fileLevel string
	entitled  bool
	pairs     []ApproverPair
}

// requestTable holds everything reused across the exhaustive enumeration.
type requestTable struct {
	requests []request
	// match[i] is the bit set of requests matched by rule i.
	match [][]uint64
	// dualFeasible marks requests whose dual approval can be discharged.
	dualFeasible []uint64
	words        int
}

func bitSet(bits []uint64, idx int) {
	bits[idx/64] |= 1 << uint(idx%64)
}

func bitIsSet(bits []uint64, idx int) bool {
	return bits[idx/64]&(1<<uint(idx%64)) != 0
}

// matches reports whether rule r applies to one concrete request.
func (r *Rule) matches(u *universe, req *request) bool {
	if len(r.Operations) > 0 && !containsString(r.Operations, req.op) {
		return false
	}
	if len(r.SubjectKeys) > 0 && !containsString(r.SubjectKeys, req.subject) {
		return false
	}
	if len(r.SubjectRoles) > 0 {
		roleHit := false
		for _, role := range u.roles[req.subject] {
			if containsString(r.SubjectRoles, role) {
				roleHit = true
				break
			}
		}
		if !roleHit {
			return false
		}
	}
	if r.MinClearance != "" && u.levelRank[u.clearance[req.subject]] < u.levelRank[r.MinClearance] {
		return false
	}
	if r.FileMinLevel != "" && u.levelRank[req.fileLevel] < u.levelRank[r.FileMinLevel] {
		return false
	}
	if len(r.FileIDs) > 0 && !containsString(r.FileIDs, req.file) {
		return false
	}
	if len(r.FileLevels) > 0 && !containsString(r.FileLevels, req.fileLevel) {
		return false
	}
	return true
}

// eligibleApproverPairs enumerates every unordered pair of key identities that
// may approve the request: neither approver is the actor, both hold clearance
// at least the file's classification, and the two keys belong to distinct
// persons. Same-person key aliases are deliberately excluded.
func eligibleApproverPairs(p *Policy, u *universe, actorKey, fileLevel string) []ApproverPair {
	var candidates []Subject
	for _, s := range p.Subjects {
		// The actor cannot approve their own request, and neither can another
		// key alias controlled by the same human.
		if s.PersonID == u.personOf[actorKey] {
			continue
		}
		if u.levelRank[s.Clearance] >= u.levelRank[fileLevel] {
			candidates = append(candidates, s)
		}
	}
	var pairs []ApproverPair
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[i].PersonID == candidates[j].PersonID {
				// Two aliases of one human are not two distinct approvers.
				continue
			}
			pairs = append(pairs, ApproverPair{
				FirstKey:     candidates[i].KeyID,
				FirstPerson:  candidates[i].PersonID,
				SecondKey:    candidates[j].KeyID,
				SecondPerson: candidates[j].PersonID,
			})
		}
	}
	return pairs
}

func buildRequestTable(p *Policy, u *universe) *requestTable {
	t := &requestTable{words: (len(p.Operations)*len(p.Subjects)*len(p.Files) + 63) / 64}
	for _, op := range p.Operations {
		for _, s := range p.Subjects {
			for _, f := range p.Files {
				req := request{
					op:        op,
					subject:   s.KeyID,
					person:    s.PersonID,
					file:      f.FileID,
					fileLevel: f.Classification,
					entitled:  u.levelRank[s.Clearance] >= u.levelRank[f.Classification],
					pairs:     eligibleApproverPairs(p, u, s.KeyID, f.Classification),
				}
				t.requests = append(t.requests, req)
			}
		}
	}
	t.match = make([][]uint64, len(p.Rules))
	for i := range p.Rules {
		t.match[i] = make([]uint64, t.words)
		for ri, req := range t.requests {
			if p.Rules[i].matches(u, &req) {
				bitSet(t.match[i], ri)
			}
		}
	}
	t.dualFeasible = make([]uint64, t.words)
	for ri, req := range t.requests {
		if len(req.pairs) > 0 {
			bitSet(t.dualFeasible, ri)
		}
	}
	return t
}

// Evaluate applies every rule of the policy to one request and explains the
// outcome, including deny precedence and dual-approval feasibility.
func Evaluate(p *Policy, op, subjectKey, fileID string) (Decision, error) {
	u, err := buildUniverse(p)
	if err != nil {
		return Decision{}, err
	}
	if err := p.validate(u); err != nil {
		return Decision{}, err
	}
	if _, ok := u.opIndex[op]; !ok {
		return Decision{}, unknownRef("operation", op)
	}
	if _, ok := u.keyIndex[subjectKey]; !ok {
		return Decision{}, unknownRef("subject", subjectKey)
	}
	if _, ok := u.fileIndex[fileID]; !ok {
		return Decision{}, unknownRef("file", fileID)
	}
	t := buildRequestTable(p, u)
	var reqIdx int
	for ri, req := range t.requests {
		if req.op == op && req.subject == subjectKey && req.file == fileID {
			reqIdx = ri
			break
		}
	}
	req := t.requests[reqIdx]
	d := Decision{
		Operation:         op,
		SubjectKey:        subjectKey,
		PersonID:          req.person,
		FileID:            fileID,
		EligibleApprovers: req.pairs,
	}
	for i, r := range p.Rules {
		if !bitIsSet(t.match[i], reqIdx) {
			continue
		}
		switch r.Kind {
		case KindAllow:
			d.MatchedAllow = append(d.MatchedAllow, r.ID)
		case KindDeny:
			d.MatchedDeny = append(d.MatchedDeny, r.ID)
		case KindDual:
			d.MatchedDual = append(d.MatchedDual, r.ID)
		}
	}
	d.Denied = len(d.MatchedDeny) > 0
	d.DualRequired = len(d.MatchedDual) > 0 && !d.Denied
	d.Granted = !d.Denied && (len(d.MatchedAllow) > 0 || len(d.MatchedDual) > 0)
	d.DualFeasible = len(req.pairs) > 0
	if d.DualRequired {
		d.Granted = d.Granted && d.DualFeasible
	}
	return d, nil
}

func unknownRef(kind, id string) error {
	return &DiagnosticError{Message: "policydiag: unknown " + kind + " " + id}
}

// DiagnosticError carries validation failures back to the caller.
type DiagnosticError struct {
	Message string
}

func (e *DiagnosticError) Error() string { return e.Message }
