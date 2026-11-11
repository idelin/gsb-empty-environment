package netsec

import "testing"

// A pair that contains a term which itself needs the pair as a premise
// must not produce a circular derivation; the engine falls back to a
// direct or acyclic derivation instead.
func TestCircularConstructorIsRejected(t *testing.T) {
	e := NewEngine()

	n3 := e.MustIntern(mustTerm(t, "n3"))
	if err := e.AddFactString("enc(n3,k1)", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("k1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("n1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("enc(pair(n1,n3),k2)", 2); err != nil {
		t.Fatal(err)
	}

	at, known := e.Earliest(n3)
	if !known || at != 2 {
		t.Fatalf("n3 known=%v at=%d, want event 2 via dec", known, at)
	}
	edge, ok := e.Derivation(n3)
	if !ok || edge.Rule != RuleDec {
		t.Fatalf("n3 must use decryption, got %+v ok=%v", edge, ok)
	}

	pair := e.MustIntern(mustTerm(t, "pair(n1,n3)"))
	if at, known := e.Earliest(pair); !known || at != 2 {
		t.Fatalf("pair known=%v at=%d, want event 2 via dec+snd chain", known, at)
	}
	if check := e.Check(); !check.OK {
		t.Fatalf("consistency: %s", check.Reason)
	}

	dag := e.DerivationDAG(pair)
	seen := map[TermID]bool{}
	for _, edgeView := range dag.Edges {
		for _, dep := range edgeView.Deps {
			if dep == edgeView.Head {
				t.Fatalf("self-loop in DAG edge %+v", edgeView)
			}
		}
		if seen[edgeView.Head] {
			t.Fatalf("duplicate DAG edge for head %d", edgeView.Head)
		}
		seen[edgeView.Head] = true
	}
}

func TestDAGOnlyContainsReachableNodes(t *testing.T) {
	e := NewEngine()
	target := e.MustIntern(mustTerm(t, "n_secret"))
	e.SetTarget(e.Term(target))
	if err := e.AddFactString("enc(n_secret,k)", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("n_unrelated", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("k", 4); err != nil {
		t.Fatal(err)
	}

	dag := e.DerivationDAG(target)
	for _, node := range dag.Nodes {
		if node.Term == "n_unrelated" {
			t.Fatal("DAG must not include nodes unreachable from the target")
		}
	}
	if len(dag.Edges) != 3 {
		t.Fatalf("DAG edges = %d, want fact/dec/fact chain of 3", len(dag.Edges))
	}
}
