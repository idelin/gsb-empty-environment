package outerjoin

import "fmt"

func Example_endToEnd() {
	a := Relation("a", "id", "y")
	b := Relation("b", "aid", "x")
	q := Query{
		From:  Join(Left, a, b, EqPred(Col("a", "id"), Col("b", "aid"))),
		Where: Not(EqPred(Col("b", "x"), Lit(1))),
	}

	nr, _, _ := q.WhereNullRejecting("b")
	degraded, report, _ := q.Degrade()

	db := Database{
		"a": {
			NewRow().Set("a", "id", 1),
			NewRow().Set("a", "id", 2),
		},
		"b": {
			NewRow().Set("b", "aid", 1).Set("b", "x", 1),
			NewRow().Set("b", "aid", 1).Set("b", "x", 2),
			NewRow().Set("b", "aid", 2).Set("b", "x", nil),
		},
	}
	verified, _ := q.VerifyRewrites(db)
	allBagEqual := len(verified) > 0
	for _, plan := range verified {
		if !plan.BagEqual || plan.EvalError != nil {
			allBagEqual = false
		}
	}

	fmt.Println(nr)
	fmt.Println(report.Changed, degraded.From.(*JoinNode).Type)
	fmt.Println(allBagEqual)
	// Output:
	// true
	// true INNER
	// true
}
