package outerjoin

import (
	"fmt"
	"sort"
)

type RuleCertificate struct {
	Legal             bool
	Rule              string
	RequiredRelations []string
	NullRejecting     bool
	Reason            string
}

type LegalPlan struct {
	Query     Query
	Key       string
	RuleChain []string
}

type VerifiedPlan struct {
	LegalPlan
	BagEqual  bool
	Diff      BagDiff
	EvalError error
}

func CheckGLLeftAssociativity(pBC Pred, schema Schema, middleRelations ...string) (RuleCertificate, error) {
	nr, witness, err := IsNullRejecting(pBC, schema, middleRelations...)
	if err != nil {
		return RuleCertificate{}, err
	}
	cert := RuleCertificate{
		Legal:             nr,
		Rule:              "Galindo-Legaria/Rosenthal LEFT-LEFT associativity",
		RequiredRelations: append([]string{}, middleRelations...),
		NullRejecting:     nr,
	}
	if nr {
		cert.Reason = "middle relation is null-rejected by the cross join predicate"
	} else {
		cert.Reason = "cross predicate can be TRUE after middle attributes are null-extended"
		if witness != nil {
			cert.Reason += "; witness=" + RowKey(Row(witness))
		}
	}
	return cert, nil
}

func CheckGLFullAssociativity(pAB, pBC Pred, schema Schema, middleRelations ...string) (RuleCertificate, error) {
	nrAB, witnessAB, err := IsNullRejecting(pAB, schema, middleRelations...)
	if err != nil {
		return RuleCertificate{}, err
	}
	nrBC, witnessBC, err := IsNullRejecting(pBC, schema, middleRelations...)
	if err != nil {
		return RuleCertificate{}, err
	}
	cert := RuleCertificate{
		Legal:             nrAB && nrBC,
		Rule:              "Galindo-Legaria/Rosenthal FULL-FULL associativity",
		RequiredRelations: append([]string{}, middleRelations...),
		NullRejecting:     nrAB && nrBC,
	}
	switch {
	case nrAB && nrBC:
		cert.Reason = "both ON predicates reject null extension of the shared/middle input"
	case !nrAB && !nrBC:
		cert.Reason = "neither ON predicate rejects middle nulls"
	case !nrAB:
		cert.Reason = "p_ab can match a synthetic middle-null row produced by FULL JOIN"
		if witnessAB != nil {
			cert.Reason += "; witness=" + RowKey(Row(witnessAB))
		}
	default:
		cert.Reason = "p_bc can match a synthetic middle-null row produced by FULL JOIN"
		if witnessBC != nil {
			cert.Reason += "; witness=" + RowKey(Row(witnessBC))
		}
	}
	return cert, nil
}

func EnumerateLegalPlans(query Query) ([]LegalPlan, error) {
	initial := query.Clone()
	queue := []LegalPlan{{
		Query:     initial,
		Key:       QueryKey(initial),
		RuleChain: []string{"original"},
	}}
	seen := map[string]bool{queue[0].Key: true}

	for head := 0; head < len(queue); head++ {
		current := queue[head]

		degraded, report, err := DegradeQuery(current.Query)
		if err != nil {
			return nil, err
		}
		if report.Changed {
			key := QueryKey(degraded)
			if !seen[key] {
				seen[key] = true
				chain := append(append([]string{}, current.RuleChain...), "top-down null-rejecting degradation")
				queue = append(queue, LegalPlan{Query: degraded, Key: key, RuleChain: chain})
			}
		}

		for _, variant := range commutationVariants(current.Query.From) {
			candidate := Query{From: variant.node, Where: clonePred(current.Query.Where)}
			key := QueryKey(candidate)
			if seen[key] {
				continue
			}
			seen[key] = true
			chain := append(append([]string{}, current.RuleChain...), variant.rule)
			queue = append(queue, LegalPlan{Query: candidate, Key: key, RuleChain: chain})
		}

		rotations, err := rotationVariants(current.Query)
		if err != nil {
			return nil, err
		}
		for _, variant := range rotations {
			key := QueryKey(variant.query)
			if seen[key] {
				continue
			}
			seen[key] = true
			chain := append(append([]string{}, current.RuleChain...), variant.rule)
			queue = append(queue, LegalPlan{Query: variant.query, Key: key, RuleChain: chain})
		}

		if len(queue) > 5000 {
			return nil, errorf("rewrite enumeration limit exceeded")
		}
	}

	sort.Slice(queue, func(i, j int) bool { return queue[i].Key < queue[j].Key })
	return queue, nil
}

func VerifyLegalPlans(query Query, db Database) ([]VerifiedPlan, error) {
	plans, err := EnumerateLegalPlans(query)
	if err != nil {
		return nil, err
	}
	originalRows, err := Evaluate(query, db)
	if err != nil {
		return nil, err
	}
	out := make([]VerifiedPlan, 0, len(plans))
	for _, plan := range plans {
		rows, evalErr := Evaluate(plan.Query, db)
		verified := VerifiedPlan{LegalPlan: plan, EvalError: evalErr}
		if evalErr == nil {
			verified.Diff = VerifyBagEqual(originalRows, rows)
			verified.BagEqual = verified.Diff.Equal
		}
		out = append(out, verified)
	}
	return out, nil
}

func EnumerateStrictEquivalentPlans(query Query, db Database) ([]VerifiedPlan, error) {
	verified, err := VerifyLegalPlans(query, db)
	if err != nil {
		return nil, err
	}
	out := make([]VerifiedPlan, 0, len(verified))
	for _, plan := range verified {
		if plan.EvalError == nil && plan.BagEqual {
			out = append(out, plan)
		}
	}
	return out, nil
}

type namedNode struct {
	node Node
	rule string
	err  error
}

type namedQuery struct {
	query Query
	rule  string
}

func commutationVariants(node Node) []namedNode {
	join, ok := node.(*JoinNode)
	if !ok {
		return nil
	}
	var out []namedNode

	commuted := &JoinNode{
		Type:  commuteJoinType(join.Type),
		Left:  join.Right.Clone(),
		Right: join.Left.Clone(),
		On:    clonePred(join.On),
	}
	out = append(out, namedNode{
		node: commuted,
		rule: fmt.Sprintf("bag-safe commutativity %s -> %s", join.Type, commuted.Type),
	})

	for _, childVariant := range commutationVariants(join.Left) {
		cp := &JoinNode{
			Type:  join.Type,
			Left:  childVariant.node,
			Right: join.Right.Clone(),
			On:    clonePred(join.On),
		}
		out = append(out, namedNode{node: cp, rule: childVariant.rule})
	}
	for _, childVariant := range commutationVariants(join.Right) {
		cp := &JoinNode{
			Type:  join.Type,
			Left:  join.Left.Clone(),
			Right: childVariant.node,
			On:    clonePred(join.On),
		}
		out = append(out, namedNode{node: cp, rule: childVariant.rule})
	}
	return out
}

func commuteJoinType(t JoinType) JoinType {
	switch t {
	case Left:
		return Right
	case Right:
		return Left
	default:
		return t
	}
}

func rotationVariants(query Query) ([]namedQuery, error) {
	schema := query.From.Schema()
	var out []namedQuery
	var walk func(Node) []namedNode
	walk = func(node Node) []namedNode {
		join, ok := node.(*JoinNode)
		if !ok {
			return nil
		}
		var local []namedNode

		if rotated, cert, err := leftAssocRotation(join, schema); err != nil {
			return []namedNode{{err: err}}
		} else if cert.Legal {
			local = append(local, namedNode{node: rotated, rule: cert.Rule})
		}
		if rotated, cert, err := rightAssocRotation(join, schema); err != nil {
			return []namedNode{{err: err}}
		} else if cert.Legal {
			local = append(local, namedNode{node: rotated, rule: cert.Rule})
		}

		for _, child := range walk(join.Left) {
			if child.err != nil {
				return []namedNode{child}
			}
			cp := &JoinNode{Type: join.Type, Left: child.node, Right: join.Right.Clone(), On: clonePred(join.On)}
			local = append(local, namedNode{node: cp, rule: child.rule})
		}
		for _, child := range walk(join.Right) {
			if child.err != nil {
				return []namedNode{child}
			}
			cp := &JoinNode{Type: join.Type, Left: join.Left.Clone(), Right: child.node, On: clonePred(join.On)}
			local = append(local, namedNode{node: cp, rule: child.rule})
		}
		return local
	}

	for _, variant := range walk(query.From) {
		if variant.err != nil {
			return nil, variant.err
		}
		out = append(out, namedQuery{
			query: Query{From: variant.node, Where: clonePred(query.Where)},
			rule:  variant.rule,
		})
	}
	return out, nil
}

func leftAssocRotation(j *JoinNode, schema Schema) (*JoinNode, RuleCertificate, error) {
	inner, ok := j.Left.(*JoinNode)
	if !ok {
		return nil, RuleCertificate{Legal: false}, nil
	}
	a, b, c := inner.Left, inner.Right, j.Right
	pAB, pBC := inner.On, j.On
	if !refsWithin(pAB, mergeSchema(a.Schema(), b.Schema())) ||
		!refsWithin(pBC, mergeSchema(b.Schema(), c.Schema())) {
		return nil, RuleCertificate{Legal: false, Reason: "predicate references cross-level relations"}, nil
	}

	middle := b.Relations()
	nrBC, witnessBC, err := IsNullRejecting(pBC, schema, middle...)
	if err != nil {
		return nil, RuleCertificate{}, err
	}

	switch {
	case inner.Type == Inner && j.Type == Inner:
		return &JoinNode{
			Type:  Inner,
			Left:  a.Clone(),
			Right: &JoinNode{Type: Inner, Left: b.Clone(), Right: c.Clone(), On: clonePred(pBC)},
			On:    clonePred(pAB),
		}, RuleCertificate{Legal: true, Rule: "INNER associativity"}, nil

	case inner.Type == Inner && j.Type == Left:
		return &JoinNode{
			Type:  Inner,
			Left:  a.Clone(),
			Right: &JoinNode{Type: Left, Left: b.Clone(), Right: c.Clone(), On: clonePred(pBC)},
			On:    clonePred(pAB),
		}, RuleCertificate{Legal: true, Rule: "INNER/LEFT associativity lifting"}, nil

	case inner.Type == Left && j.Type == Left:
		cert, err := CheckGLLeftAssociativity(pBC, schema, middle...)
		if err != nil {
			return nil, RuleCertificate{}, err
		}
		if !cert.Legal {
			return nil, cert, nil
		}
		return &JoinNode{
			Type:  Left,
			Left:  a.Clone(),
			Right: &JoinNode{Type: Left, Left: b.Clone(), Right: c.Clone(), On: clonePred(pBC)},
			On:    clonePred(pAB),
		}, cert, nil

	case inner.Type == Full && j.Type == Full:
		cert, err := CheckGLFullAssociativity(pAB, pBC, schema, middle...)
		if err != nil {
			return nil, RuleCertificate{}, err
		}
		if !cert.Legal {
			return nil, cert, nil
		}
		return &JoinNode{
			Type:  Full,
			Left:  a.Clone(),
			Right: &JoinNode{Type: Full, Left: b.Clone(), Right: c.Clone(), On: clonePred(pBC)},
			On:    clonePred(pAB),
		}, cert, nil

	case inner.Type == Left && j.Type == Inner && nrBC:
		return &JoinNode{
			Type:  Inner,
			Left:  a.Clone(),
			Right: &JoinNode{Type: Inner, Left: b.Clone(), Right: c.Clone(), On: clonePred(pBC)},
			On:    clonePred(pAB),
		}, RuleCertificate{
			Legal:             true,
			Rule:              "INNER upper ON null-rejects middle: promote LEFT to INNER and associate",
			RequiredRelations: middle,
			NullRejecting:     true,
			Reason:            RowKey(Row(witnessBC)),
		}, nil

	default:
		reason := "mixed outer-join associativity is not legal without prior null-rejecting degradation"
		if witnessBC != nil {
			reason += "; witness=" + RowKey(Row(witnessBC))
		}
		return nil, RuleCertificate{Legal: false, Rule: "GL/Rosenthal strict gate", NullRejecting: nrBC, Reason: reason}, nil
	}
}

func rightAssocRotation(j *JoinNode, schema Schema) (*JoinNode, RuleCertificate, error) {
	inner, ok := j.Right.(*JoinNode)
	if !ok {
		return nil, RuleCertificate{Legal: false}, nil
	}
	a, b, c := j.Left, inner.Left, inner.Right
	pAB, pBC := j.On, inner.On
	if !refsWithin(pAB, mergeSchema(a.Schema(), b.Schema())) ||
		!refsWithin(pBC, mergeSchema(b.Schema(), c.Schema())) {
		return nil, RuleCertificate{Legal: false, Reason: "predicate references cross-level relations"}, nil
	}

	op1 := j.Type
	op2 := inner.Type
	middle := b.Relations()
	nrBC, _, err := IsNullRejecting(pBC, schema, middle...)
	if err != nil {
		return nil, RuleCertificate{}, err
	}
	nrAB, _, err := IsNullRejecting(pAB, schema, middle...)
	if err != nil {
		return nil, RuleCertificate{}, err
	}

	legal := false
	rule := ""
	var cert RuleCertificate
	switch {
	case op1 == Inner && op2 == Inner:
		legal = true
		rule = "INNER associativity"
	case op1 == Inner && op2 == Left:
		legal = true
		rule = "INNER/LEFT associativity lifting"
	case op1 == Left && op2 == Left && nrBC:
		cert, err = CheckGLLeftAssociativity(pBC, schema, middle...)
		if err != nil {
			return nil, RuleCertificate{}, err
		}
		legal = true
		rule = cert.Rule
	case op1 == Full && op2 == Full && nrAB && nrBC:
		cert, err = CheckGLFullAssociativity(pAB, pBC, schema, middle...)
		if err != nil {
			return nil, RuleCertificate{}, err
		}
		legal = true
		rule = cert.Rule
	default:
		return nil, RuleCertificate{
			Legal:         false,
			Rule:          "GL/Rosenthal strict gate",
			Reason:        "right-associated form has no exact inverse identity for these join types/predicates",
			NullRejecting: nrBC && nrAB,
		}, nil
	}
	if !legal {
		return nil, cert, nil
	}

	return &JoinNode{
		Type:  op2,
		Left:  &JoinNode{Type: op1, Left: a.Clone(), Right: b.Clone(), On: clonePred(pAB)},
		Right: c.Clone(),
		On:    clonePred(pBC),
	}, RuleCertificate{Legal: true, Rule: rule, NullRejecting: true}, nil
}

func refsWithin(pred Pred, schema Schema) bool {
	for _, ref := range CollectRefsPred(pred) {
		if _, ok := schema[ref]; !ok {
			return false
		}
	}
	return true
}

func clonePred(pred Pred) Pred {
	if pred == nil {
		return nil
	}
	return pred.Clone()
}
