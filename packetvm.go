// Package packetvm compiles tenant-uploaded packet filter rules into a
// small, verifiable data-plane instruction set.
//
// The compiler only emits immediate loads, packet-length probes, fixed
// length/offset packet loads and forward jumps. Before a program is returned
// it is run through a verifier that proves every reachable packet read is
// inside a length established on every reaching path. Programs that fail the
// proof are rejected with an error naming the source expression responsible.
package packetvm

// Compile lowers a rule to a verified Program. A non-nil error is always a
// *RejectError describing why the rule cannot be proven safe.
func Compile(rule Rule) (*Program, error) {
	if rule.Body == nil {
		return nil, &RejectError{Kind: RejectInvalidAST, Index: -1,
			Detail: "rule has no body"}
	}
	if err := validateAST(rule.Body); err != nil {
		return nil, err
	}
	insns, _, err := compile(rule)
	if err != nil {
		return nil, err
	}
	if err := verify(insns); err != nil {
		return nil, err
	}
	return &Program{RuleName: rule.Name, Insns: insns}, nil
}

// validateAST walks the tree once to reject structurally impossible rules,
// ensuring every node carries a usable source and every load width is valid.
func validateAST(b Bool) error {
	switch v := b.(type) {
	case *Cmp:
		if v == nil || v.LHS == nil || v.RHS == nil {
			return &RejectError{Kind: RejectInvalidAST, Index: -1,
				Detail: "comparison has a missing operand"}
		}
		if err := validateNum(v.LHS); err != nil {
			return err
		}
		return validateNum(v.RHS)
	case *Not:
		if v == nil || v.Inner == nil {
			return &RejectError{Kind: RejectInvalidAST, Index: -1,
				Detail: "NOT has no inner expression"}
		}
		return validateAST(v.Inner)
	case *And:
		if v == nil || len(v.Terms) == 0 {
			return &RejectError{Kind: RejectInvalidAST, Index: -1,
				Detail: "AND has no terms"}
		}
		for _, t := range v.Terms {
			if err := validateAST(t); err != nil {
				return err
			}
		}
		return nil
	case *Or:
		if v == nil || len(v.Terms) == 0 {
			return &RejectError{Kind: RejectInvalidAST, Index: -1,
				Detail: "OR has no terms"}
		}
		for _, t := range v.Terms {
			if err := validateAST(t); err != nil {
				return err
			}
		}
		return nil
	default:
		return &RejectError{Kind: RejectInvalidAST, Index: -1,
			Detail: "unknown boolean expression"}
	}
}

func validateNum(n Numeric) error {
	switch v := n.(type) {
	case *Imm, *Len:
		return nil
	case *Load:
		if v == nil || !v.Size.Valid() {
			return &RejectError{Kind: RejectBadOffset, Source: n.Src(), Index: -1,
				Detail: "unsupported load width"}
		}
		_, overflow := addOverflow(v.Offset, uint32(v.Size))
		if overflow {
			return &RejectError{Kind: RejectBadOffset, Source: v.Pos, Index: -1,
				Detail: "offset plus size overflows 32-bit address space"}
		}
		return nil
	default:
		return &RejectError{Kind: RejectInvalidAST, Index: -1,
			Detail: "unknown numeric expression"}
	}
}
