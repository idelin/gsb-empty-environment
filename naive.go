package handoff

// BuildNaiveImmediatePlan publishes the new prefix and makes the authorization
// cut without an egress-ACK barrier. Its final state is correct, but allowed
// interleavings contain early new-tenant raw winners and post-cut stale
// old-tenant winners. Certificates are expected to reject it.
func BuildNaiveImmediatePlan(spec Spec) (*Plan, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	transfer := MergePrefixes(spec.Transfer)
	claims := make([]Claim, 0, len(transfer))
	for _, p := range transfer {
		claims = append(claims, Claim{Prefix: p, Tenant: spec.NewTenant})
	}
	stmt := Statement{
		PlanID:     spec.PlanID,
		ID:         "naive-new",
		Generation: 1,
		Announces:  claims,
		Nonce:      1,
	}.Canonical()

	protocol, err := NewProtocol(spec)
	if err != nil {
		return nil, err
	}
	protocol.AddStatementBroadcast(stmt)
	cut := protocol.AddCut("main", cutOps(spec, transfer)...)

	return &Plan{
		Spec:                 spec,
		TransferPrefixes:     transfer,
		NewClaims:            claims,
		PrepositionStatement: stmt,
		CutEvent:             cut,
		Protocol:             protocol,
		Requirements: [][]AckRequirement{{
			{StatementID: stmt.ID, Digest: stmt.Digest(), Reason: "naive plan does not enforce this barrier"},
		}},
	}, nil
}
