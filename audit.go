package outerjoin

type CandidateAudit struct {
	Key                string
	FormallyRecognized bool
	RuleChain          []string
	BagEqual           bool
	Diff               BagDiff
	CandidateEvalError error
	OriginalEvalError  error
	Safe               bool
}

func AuditCandidate(original, candidate Query, db Database) (CandidateAudit, error) {
	formal, err := EnumerateLegalPlans(original)
	if err != nil {
		return CandidateAudit{}, err
	}
	formalByKey := map[string]LegalPlan{}
	for _, plan := range formal {
		formalByKey[plan.Key] = plan
	}

	audit := CandidateAudit{Key: QueryKey(candidate)}
	if plan, ok := formalByKey[audit.Key]; ok {
		audit.FormallyRecognized = true
		audit.RuleChain = plan.RuleChain
	}

	originalRows, originalErr := Evaluate(original, db)
	audit.OriginalEvalError = originalErr
	candidateRows, candidateErr := Evaluate(candidate, db)
	audit.CandidateEvalError = candidateErr
	if originalErr == nil && candidateErr == nil {
		audit.Diff = VerifyBagEqual(originalRows, candidateRows)
		audit.BagEqual = audit.Diff.Equal
	}
	audit.Safe = audit.FormallyRecognized && audit.BagEqual &&
		originalErr == nil && candidateErr == nil
	return audit, nil
}

type TripleForm struct {
	Name   string
	Shape  string
	ABType JoinType
	BCType JoinType
	Query  Query
}

func AllJoinTypes() []JoinType {
	return []JoinType{Inner, Left, Right, Full}
}

func EnumerateTripleForms(where Pred, a, b, c Node, pAB, pBC Pred) []TripleForm {
	var forms []TripleForm
	for _, abType := range AllJoinTypes() {
		for _, bcType := range AllJoinTypes() {
			leftDeep := Query{
				Where: clonePred(where),
				From: Join(
					bcType,
					Join(abType, a.Clone(), b.Clone(), clonePred(pAB)),
					c.Clone(),
					clonePred(pBC),
				),
			}
			forms = append(forms, TripleForm{
				Name:   string(abType) + "-" + string(bcType) + "-left-deep",
				Shape:  "((A AB B) BC C)",
				ABType: abType,
				BCType: bcType,
				Query:  leftDeep,
			})

			rightDeep := Query{
				Where: clonePred(where),
				From: Join(
					abType,
					a.Clone(),
					Join(bcType, b.Clone(), c.Clone(), clonePred(pBC)),
					clonePred(pAB),
				),
			}
			forms = append(forms, TripleForm{
				Name:   string(abType) + "-" + string(bcType) + "-right-deep",
				Shape:  "(A AB (B BC C))",
				ABType: abType,
				BCType: bcType,
				Query:  rightDeep,
			})
		}
	}
	return forms
}

type AuditedTripleForm struct {
	TripleForm
	Audit CandidateAudit
}

func AuditTripleForms(original Query, a, b, c Node, pAB, pBC Pred, db Database) ([]AuditedTripleForm, error) {
	forms := EnumerateTripleForms(original.Where, a, b, c, pAB, pBC)
	out := make([]AuditedTripleForm, 0, len(forms))
	for _, form := range forms {
		audit, err := AuditCandidate(original, form.Query, db)
		if err != nil {
			return nil, err
		}
		out = append(out, AuditedTripleForm{TripleForm: form, Audit: audit})
	}
	return out, nil
}

func SafeTripleForms(original Query, a, b, c Node, pAB, pBC Pred, db Database) ([]AuditedTripleForm, error) {
	audited, err := AuditTripleForms(original, a, b, c, pAB, pBC, db)
	if err != nil {
		return nil, err
	}
	out := make([]AuditedTripleForm, 0)
	for _, form := range audited {
		if form.Audit.Safe {
			out = append(out, form)
		}
	}
	return out, nil
}
