package outerjoin

func IsStrongNullRejecting(pred Pred, schema Schema, targetRelations ...string) (bool, Witness, error) {
	return IsNullRejecting(pred, schema, targetRelations...)
}

func DegradeOuterJoins(query Query) (Query, DegradeReport, error) {
	return DegradeQuery(query)
}

func EnumerateEquivalentJoinTrees(query Query) ([]LegalPlan, error) {
	return EnumerateLegalPlans(query)
}

func VerifyRewritesOnDatabase(query Query, db Database) ([]VerifiedPlan, error) {
	return VerifyLegalPlans(query, db)
}

func EnumerateBagEquivalentPlans(query Query, db Database) ([]VerifiedPlan, error) {
	return EnumerateStrictEquivalentPlans(query, db)
}

func (q Query) Degrade() (Query, DegradeReport, error) {
	return DegradeQuery(q)
}

func (q Query) Evaluate(db Database) ([]Row, error) {
	return Evaluate(q, db)
}

func (q Query) LegalPlans() ([]LegalPlan, error) {
	return EnumerateLegalPlans(q)
}

func (q Query) VerifyRewrites(db Database) ([]VerifiedPlan, error) {
	return VerifyLegalPlans(q, db)
}

func (q Query) WhereNullRejecting(targetRelations ...string) (bool, Witness, error) {
	return IsNullRejecting(q.Where, q.From.Schema(), targetRelations...)
}
