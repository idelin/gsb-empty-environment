package symexec

import (
	"testing"
)

func TestSafeRole(t *testing.T) {
	role := NewRole("safe",
		Fresh("N"),
		Send(Var("N")),
		Begin(Atom("B"), Var("N")),
		End(Atom("B"), Var("N")),
	)
	scenario := NewScenario(Public("A", "B", "K"), NewSession("s1", role, nil))
	result, err := Analyze(scenario, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Safe {
		t.Fatalf("status = %s (%s), want safe", result.Status, result.Reason)
	}
}

func TestMissingBeginIsShortest(t *testing.T) {
	role := NewRole("bad",
		Fresh("N"),
		Send(Var("N")),
		End(Atom("A"), Var("N")),
	)
	scenario := NewScenario(Public("A", "K"), NewSession("s1", role, nil))
	result, err := Analyze(scenario, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Attack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	if got := len(result.Trace); got != 3 {
		t.Fatalf("shortest trace has %d steps, want 3", got)
	}
	if result.Attack.End.Session != "s1" {
		t.Fatalf("end session = %q", result.Attack.End.Session)
	}
	if err := result.Verify(scenario, Options{}); err != nil {
		t.Fatalf("trace did not replay: %v", err)
	}
}

func TestNonceMismatch(t *testing.T) {
	role := NewRole("bad",
		Fresh("N1", "N2"),
		Begin(Atom("A"), Var("N1")),
		End(Atom("A"), Var("N2")),
	)
	scenario := NewScenario(Public("A"), NewSession("s1", role, nil))
	result, err := Analyze(scenario, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Attack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	if got := len(result.Trace); got != 3 {
		t.Fatalf("shortest trace has %d steps, want 3", got)
	}
	if err := result.Verify(scenario, Options{}); err != nil {
		t.Fatalf("trace did not replay: %v", err)
	}
}

func TestShortestInterleavingAcrossSessions(t *testing.T) {
	shared := map[string]Term{"K": Atom("K")}
	initiator := NewRole("initiator",
		Fresh("N"),
		Send(Enc(Var("K"), Var("N"))),
		Begin(Atom("A"), Var("N")),
	)
	responder := NewRole("responder",
		Receive(Enc(Var("K"), Var("N"))),
		End(Atom("A"), Var("N")),
	)
	scenario := NewScenario(
		Public("A"),
		NewSession("a1", initiator, shared),
		NewSession("b1", responder, shared),
	)
	result, err := Analyze(scenario, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Attack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	if got := len(result.Trace); got != 4 {
		t.Fatalf("shortest interleaving has %d steps, want 4", got)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != StepEnd {
		t.Fatalf("attack should reach end before begin, events = %#v", result.Events)
	}
	if err := result.Verify(scenario, Options{}); err != nil {
		t.Fatalf("trace did not replay: %v", err)
	}
}

func TestAttackerReceiveCertificate(t *testing.T) {
	role := NewRole("receiver",
		Receive(Pair(Atom("A"), Var("Y"))),
		Begin(Atom("B"), Atom("A")),
		End(Atom("B"), Var("Y")),
	)
	scenario := NewScenario(Public("A", "B"), NewSession("r1", role, nil))
	result, err := Analyze(scenario, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Attack {
		t.Fatalf("status = %s (%s), want attack", result.Status, result.Reason)
	}
	if len(result.Attack.Certificates) == 0 {
		t.Fatal("attack trace has no attacker derivation certificate")
	}
	if err := result.Verify(scenario, Options{}); err != nil {
		t.Fatalf("trace did not replay: %v", err)
	}
	if err := VerifyCertificates(scenario, result.Trace, Options{}); err != nil {
		t.Fatalf("certificate replay failed: %v", err)
	}

	// Tamper with the first attacker message's certificate.
	tampered := cloneTrace(result.Trace)
	for i := range tampered {
		if tampered[i].Certificate != nil {
			cert := cloneDerivation(tampered[i].Certificate)
			cert.Rule = RuleInitial
			cert.Children = nil
			cert.Term = Atom("NOT-A-KNOWN-ATOM")
			tampered[i].Certificate = cert
			tampered[i].Message = termPtr(cert.Term)
			break
		}
	}
	if err := VerifyCertificates(scenario, tampered, Options{}); err == nil {
		t.Fatal("tampered certificate was accepted")
	}
}

func TestAttackerCannotGuessAtom(t *testing.T) {
	initial := Known(Atom("K"), Atom("A"), Atom("B"))
	secretKey := Atom("SECRET")
	observed := []Term{Enc(secretKey, Atom("N"))}
	knowledge, err := NewKnowledge(initial, observed, Limits{MaxComposeDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if knowledge.Knows(Atom("N")) {
		t.Fatal("attacker learned the plaintext under a secret key")
	}

	// The same value is available when the key is public.
	public, err := NewKnowledge(append(initial, secretKey), observed, Limits{MaxComposeDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !public.Knows(Atom("N")) {
		t.Fatal("atom N should be derivable after decrypting with known key")
	}
}

func TestDecryptAndProjectionCertificate(t *testing.T) {
	initial := Known(Atom("K"), Atom("A"))
	observed := []Term{Enc(Atom("K"), Pair(Atom("A"), Atom("B")))}
	knowledge, err := NewKnowledge(initial, observed, Limits{MaxComposeDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	candidates := mustMatch(t, knowledge, Atom("B"))
	if len(candidates) == 0 {
		t.Fatal("B should be recoverable by decryption and right projection")
	}
	cert := candidates[0].Derivation
	if cert.Rule != RuleSnd || len(cert.Children) != 1 || cert.Children[0].Rule != RuleDecrypt {
		t.Fatalf("unexpected proof shape: rule=%s", cert.Rule)
	}
	if err := knowledge.Verify(cert); err != nil {
		t.Fatalf("valid decryption proof failed: %v", err)
	}

	bad := cloneDerivation(cert)
	bad.Children[0].Children[1].Term = Atom("A")
	if err := knowledge.Verify(bad); err == nil {
		t.Fatal("proof with the wrong decryption key was accepted")
	}
}

func TestCompositionDepthBound(t *testing.T) {
	knowledge, err := NewKnowledge(Public("K", "A"), nil, Limits{MaxComposeDepth: 3})
	if err != nil {
		t.Fatal(err)
	}
	maxDepth := 0
	for _, candidate := range mustMatch(t, knowledge, Var("X")) {
		if candidate.Term.Depth() > 3 {
			t.Fatalf("candidate %s has depth %d", candidate.Term, candidate.Term.Depth())
		}
		if candidate.Term.Depth() > maxDepth {
			maxDepth = candidate.Term.Depth()
		}
	}
	if maxDepth != 3 {
		t.Fatalf("deepest composed candidate has depth %d, want 3", maxDepth)
	}

	// With a bound of one, only pairs/encryptions of atoms can be composed.
	knowledge, err = NewKnowledge(Public("K", "A", "B", "C"), nil, Limits{MaxComposeDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range mustMatch(t, knowledge, Var("X")) {
		if candidate.Term.Depth() > 1 {
			t.Fatalf("bounded candidate %s has depth %d", candidate.Term, candidate.Term.Depth())
		}
	}
}

func TestBudgetExhaustionReturnsUnknown(t *testing.T) {
	role := NewRole("loop",
		Receive(Var("X")),
		Send(Var("X")),
	)
	scenario := NewScenario(Public("A", "B", "K"), NewSession("s1", role, nil))
	result, err := Analyze(scenario, Options{Budget: Budget{MaxStates: 1, MaxSteps: 64}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Unknown {
		t.Fatalf("status = %s (%s), want unknown", result.Status, result.Reason)
	}
	if result.IsAttack() || result.IsSafe() {
		t.Fatal("budget exhaustion must not yield a definitive verdict")
	}
}

func TestRoleStepLimit(t *testing.T) {
	steps := make([]Step, 0, MaxRoleSteps+1)
	for i := 0; i < MaxRoleSteps+1; i++ {
		steps = append(steps, Send(Atom("A")))
	}
	role := NewRole("too-long", steps...)
	scenario := NewScenario(Public("A"), NewSession("s1", role, nil))
	if err := scenario.Validate(); err == nil {
		t.Fatal("role with more than twelve steps passed validation")
	}
}

func TestDepthLimitedAttackerMessageRejected(t *testing.T) {
	role := NewRole("receiver",
		Receive(Var("X")),
		Begin(Atom("A"), Var("X")),
		End(Atom("A"), Var("X")),
	)
	scenario := NewScenario(Public("A", "B", "C", "K"), NewSession("s1", role, nil))
	// A depth-one bound cannot build the nested pair used below, but the
	// receiver's transition would still find replayable initial atoms.
	result, err := Analyze(scenario, Options{MaxComposeDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == Attack {
		for _, step := range result.Trace {
			if step.Message != nil && step.Message.Depth() > 1 {
				t.Fatalf("depth-one search used %s", *step.Message)
			}
		}
	}
}

func mustMatch(t *testing.T, knowledge *Knowledge, pattern Term) []Candidate {
	t.Helper()
	candidates, err := knowledge.Match(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func containsAtomInTerm(term Term, name string) bool {
	switch term.Kind {
	case KindAtom:
		return term.Name == name
	case KindPair, KindEnc:
		return containsAtomInTerm(*term.Left, name) || containsAtomInTerm(*term.Right, name)
	default:
		return false
	}
}

func cloneTrace(in []TraceStep) []TraceStep {
	out := make([]TraceStep, len(in))
	copy(out, in)
	return out
}

func cloneDerivation(in *Derivation) *Derivation {
	if in == nil {
		return nil
	}
	out := &Derivation{Term: in.Term, Rule: in.Rule, Source: in.Source}
	for _, child := range in.Children {
		out.Children = append(out.Children, cloneDerivation(child))
	}
	return out
}
