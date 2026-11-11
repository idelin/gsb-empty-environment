package netsec

import (
	"encoding/json"
	"math/rand"
	"testing"
)

func mustTerm(t *testing.T, s string) Term {
	t.Helper()
	term, err := ParseTerm(s)
	if err != nil {
		t.Fatalf("ParseTerm(%q): %v", s, err)
	}
	return term
}

func TestParseAndCanonical(t *testing.T) {
	cases := map[string]string{
		"a":                  "a",
		"n1":                 "n1",
		"k_sess":             "k_sess",
		"pair(a,alice)":      "pair(a,alice)",
		"enc( pair(n,k) ,k)": "enc(pair(n,k),k)",
	}
	for input, want := range cases {
		term, err := ParseTerm(input)
		if err != nil {
			t.Fatalf("ParseTerm(%q): %v", input, err)
		}
		if got := term.String(); got != want {
			t.Fatalf("canonical(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := ParseTerm("enc(n,a)"); err == nil {
		t.Fatal("enc with non-key second child must be rejected")
	}
	if _, err := ParseTerm("pair(n)"); err == nil {
		t.Fatal("pair with one child must be rejected")
	}
}

func TestPairProjectionTakesLatestPremise(t *testing.T) {
	e := NewEngine()
	pairID := e.MustIntern(mustTerm(t, "pair(n1,n2)"))
	n1 := e.MustIntern(mustTerm(t, "n1"))
	n2 := e.MustIntern(mustTerm(t, "n2"))
	e.SetTarget(e.Term(n1))
	e.SetTarget(e.Term(n2))

	if err := e.AddFactString("pair(n1,n2)", 3); err != nil {
		t.Fatal(err)
	}
	for _, id := range []TermID{n1, n2, pairID} {
		at, known := e.Earliest(id)
		if !known || at != 3 {
			t.Fatalf("%s known=%v at=%d, want event 3", e.TermString(id), known, at)
		}
	}

	if edge, ok := e.Derivation(n1); !ok || edge.Rule != RuleFst {
		t.Fatalf("n1 derivation = %+v ok=%v, want fst", edge, ok)
	}
	if edge, ok := e.Derivation(n2); !ok || edge.Rule != RuleSnd {
		t.Fatalf("n2 derivation = %+v ok=%v, want snd", edge, ok)
	}

	// A tighter observation of the tuple cannot make components available
	// earlier than the tuple itself.
	if err := e.AddFactString("pair(n1,n2)", 1); err != nil {
		t.Fatal(err)
	}
	for _, id := range []TermID{pairID, n1, n2} {
		if at, _ := e.Earliest(id); at != 1 {
			t.Fatalf("%s at=%d, want 1 after tighter fact", e.TermString(id), at)
		}
	}
	if !e.LastCheck().OK {
		t.Fatalf("consistency check: %s", e.LastCheck().Reason)
	}
}

func TestLateKeyLeakage(t *testing.T) {
	e := NewEngine()

	secret := mustTerm(t, "n_secret")
	key := mustTerm(t, "k_session")
	message := mustTerm(t, "enc(n_secret,k_session)")
	target, err := e.SetTarget(secret)
	if err != nil {
		t.Fatal(err)
	}
	messageID := e.MustIntern(message)
	keyID := e.MustIntern(key)

	// Event 1: the ciphertext is observed while the protocol session runs.
	if err := e.AddFact(message, 1); err != nil {
		t.Fatal(err)
	}
	if _, known := e.Earliest(target); known {
		t.Fatal("secret must remain unknown while the key is unavailable")
	}
	if _, known := e.Earliest(messageID); !known {
		t.Fatal("ciphertext itself must be known at event 1")
	}

	dag := e.DerivationDAG(target)
	for _, edge := range dag.Edges {
		if edge.Head == target {
			t.Fatalf("unknown target must have no deriving edge, got %+v", edge)
		}
	}

	// Events 2 and 3 finish the session; the key leaks only at event 4.
	if err := e.AddFactString("a", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("pair(a,alice)", 3); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFact(key, 4); err != nil {
		t.Fatal(err)
	}

	at, known := e.Earliest(target)
	if !known || at != 4 {
		t.Fatalf("secret known=%v at=%d, want first knowability at event 4", known, at)
	}
	if at, _ := e.Earliest(keyID); at != 4 {
		t.Fatalf("key at=%d, want 4", at)
	}
	edge, ok := e.Derivation(target)
	if !ok || edge.Rule != RuleDec {
		t.Fatalf("secret derivation = %+v ok=%v, want dec", edge, ok)
	}

	check := e.Check()
	if !check.OK {
		t.Fatalf("full recomputation mismatch: %s", check.Reason)
	}

	dag = e.DerivationDAG(target)
	wantNodes := map[string]bool{"n_secret": true, "k_session": true, "enc(n_secret,k_session)": true}
	gotNodes := map[string]bool{}
	for _, node := range dag.Nodes {
		gotNodes[node.Term] = true
		if node.Term == "n_secret" && node.AtEvent != 4 {
			t.Fatalf("DAG node secret at event %d, want 4", node.AtEvent)
		}
	}
	if len(gotNodes) != len(wantNodes) {
		t.Fatalf("DAG nodes = %v, want %v", gotNodes, wantNodes)
	}
	for term := range wantNodes {
		if !gotNodes[term] {
			t.Fatalf("DAG missing node %s", term)
		}
	}

	report := e.Report()
	if !report.Consistent || len(report.Targets) != 1 || !report.Targets[0].Known ||
		report.Targets[0].AtEvent != 4 || report.Targets[0].Rule != "dec" {
		t.Fatalf("unexpected report: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Targets[0].AtEvent != 4 {
		t.Fatal("JSON round trip lost target timing")
	}
}

func TestNestedDecryptionWaitsForInnerKey(t *testing.T) {
	e := NewEngine()
	inner := e.MustIntern(mustTerm(t, "n_data"))
	outerMessage := mustTerm(t, "enc(enc(n_data,k_in),k_out)")

	if err := e.AddFact(outerMessage, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("k_out", 2); err != nil {
		t.Fatal(err)
	}
	if _, known := e.Earliest(inner); known {
		t.Fatal("inner secret must wait for the inner key")
	}
	if err := e.AddFactString("k_in", 5); err != nil {
		t.Fatal(err)
	}
	at, known := e.Earliest(inner)
	if !known || at != 5 {
		t.Fatalf("inner secret known=%v at=%d, want event 5", known, at)
	}

	dag := e.DerivationDAG(inner)
	decEdges := 0
	for _, edgeView := range dag.Edges {
		if edgeView.Rule == "dec" {
			decEdges++
		}
	}
	if decEdges != 2 {
		t.Fatalf("DAG dec edges = %d, want 2", decEdges)
	}
}

func TestConstructorFiresAtLatestPremise(t *testing.T) {
	e := NewEngine()
	pair := e.MustIntern(mustTerm(t, "pair(n1,n2)"))
	enc := e.MustIntern(mustTerm(t, "enc(pair(n1,n2),k)"))

	if err := e.AddFactString("n1", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("n2", 3); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("k", 2); err != nil {
		t.Fatal(err)
	}
	if at, known := e.Earliest(pair); !known || at != 3 {
		t.Fatalf("pair known=%v at=%v, want event 3", known, at)
	}
	if at, known := e.Earliest(enc); !known || at != 3 {
		t.Fatalf("enc known=%v at=%v, want event 3 (max of pair@3 and k@2)", known, at)
	}
	if edge, ok := e.Derivation(enc); !ok || edge.Rule != RuleEnc {
		t.Fatalf("enc derivation = %+v, want enc edge", edge)
	}
}

func TestTieBreakIsStableBetweenEngines(t *testing.T) {
	e := NewEngine()
	n1 := e.MustIntern(mustTerm(t, "n1"))
	if err := e.AddFactString("pair(n1,n2)", 2); err != nil {
		t.Fatal(err)
	}
	if err := e.AddFactString("n1", 2); err != nil {
		t.Fatal(err)
	}
	edge, ok := e.Derivation(n1)
	if !ok {
		t.Fatal("n1 must be known")
	}
	if edge.Rule != RuleFact {
		t.Fatalf("same-time tie must prefer fact, got rule %s", edge.Rule)
	}

	batch := e.Recompute()
	batchEdge := batch.Edges[batch.BestEdge[n1]]
	incrementalEdge, _ := e.Derivation(n1)
	if batchEdge.Rule != incrementalEdge.Rule {
		t.Fatalf("batch picked %s, incremental picked %s", batchEdge.Rule, incrementalEdge.Rule)
	}
}

func TestRandomIncrementalMatchesRecompute(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	terms := []string{
		"n1", "n2", "n3", "k1", "k2",
		"pair(n1,n2)",
		"enc(n3,k1)",
		"enc(pair(n1,n3),k2)",
		"pair(enc(n2,k1),k2)",
	}

	for iteration := 0; iteration < 200; iteration++ {
		e := NewEngine(WithAutoVerify(false))
		target := e.MustIntern(mustTerm(t, "n3"))
		e.SetTarget(e.Term(target))

		shuffled := rng.Perm(len(terms))
		for order, idx := range shuffled {
			if rng.Intn(2) == 0 {
				if err := e.AddFactString(terms[idx], 1+rng.Intn(8)); err != nil {
					t.Fatal(err)
				}
			}
			_ = order
			check := e.Check()
			if !check.OK {
				t.Fatalf("iteration %d after term %s: %s", iteration, terms[idx], check.Reason)
			}
		}

		// Late duplicate facts must only tighten times and preserve agreement.
		for _, term := range terms {
			if rng.Intn(2) == 0 {
				if err := e.AddFactString(term, 1+rng.Intn(8)); err != nil {
					t.Fatal(err)
				}
			}
		}
		if check := e.Check(); !check.OK {
			t.Fatalf("iteration %d late facts: %s", iteration, check.Reason)
		}
		if _, err := e.JSON(); err != nil {
			t.Fatalf("json report: %v", err)
		}
	}
}

func TestInvalidFactEvents(t *testing.T) {
	e := NewEngine()
	if err := e.AddFactString("n1", 0); err == nil {
		t.Fatal("event 0 must be rejected")
	}
	if err := e.AddFactString("n1", -1); err == nil {
		t.Fatal("negative events must be rejected")
	}
}
