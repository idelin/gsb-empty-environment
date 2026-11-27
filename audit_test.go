package outerjoin

import "testing"

func TestAuditTripleFormsRejectsCounterexample(t *testing.T) {
	a := Relation("a", "id", "y")
	b := Relation("b", "id", "aid", "x")
	c := Relation("c", "bid", "z")
	pAB := EqPred(Col("a", "id"), Col("b", "aid"))
	strictBC := EqPred(Col("b", "id"), Col("c", "bid"))
	weakBC := Or(EqPred(Col("b", "x"), Lit(1)), EqPred(Col("c", "z"), Lit(2)))
	db := acceptanceDB()

	legalOriginal := Query{From: Join(Left, Join(Left, a, b, pAB), c, strictBC)}
	forms, err := AuditTripleForms(legalOriginal, a, b, c, pAB, strictBC, db)
	if err != nil {
		t.Fatal(err)
	}
	safeCount := 0
	foundRightDeep := false
	for _, form := range forms {
		if form.Audit.Safe {
			safeCount++
			if !form.Audit.BagEqual {
				t.Fatalf("safe form unexpectedly not bag equal: %s", form.Audit.Key)
			}
		}
		if form.Shape == "(A AB (B BC C))" && form.ABType == Left && form.BCType == Left {
			foundRightDeep = true
			if !form.Audit.FormallyRecognized || !form.Audit.BagEqual || !form.Audit.Safe {
				t.Fatalf("legal GL right-deep form not safe: %+v", form.Audit)
			}
		}
	}
	if !foundRightDeep || safeCount == 0 {
		t.Fatalf("missing legal GL form; safeCount=%d found=%v", safeCount, foundRightDeep)
	}

	weakOriginal := Query{From: Join(Left, Join(Left, a, b, pAB), c, weakBC)}
	weakForms, err := AuditTripleForms(weakOriginal, a, b, c, pAB, weakBC, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range weakForms {
		if form.Shape == "(A AB (B BC C))" && form.ABType == Left && form.BCType == Left {
			if form.Audit.FormallyRecognized {
				t.Fatalf("weak GL form was formally recognized")
			}
			if form.Audit.BagEqual {
				t.Fatalf("test database unexpectedly made weak rearrangement bag-equal")
			}
			if form.Audit.Safe {
				t.Fatalf("unsafe form marked safe")
			}
			return
		}
	}
	t.Fatalf("right-deep weak form not enumerated")
}
