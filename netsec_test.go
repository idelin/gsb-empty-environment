package netsec

import (
	"strings"
	"testing"
)

// reflectionProtocol: an initiator that accepts its own reflected
// challenge. The attacker reflects {n}k back, so the end claim fires
// without any begin.
func reflectionProtocol() *Protocol {
	k := AtomOf("k")
	n := VarOf("n")
	return &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			FreshAtom("n"),
			SendMsg(EncOf(k, n)),
			RecvMsg(EncOf(k, n)),
			EndEv(AtomOf("A"), n),
		}},
	}}
}

func TestReflectionAttackFound(t *testing.T) {
	p := reflectionProtocol()
	spec := &SessionSpec{Role: "init"}
	res, err := FindMismatch(p, spec, spec, nil, -1)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Attack {
		t.Fatalf("status = %s, want attack", res.Status)
	}
	// fresh, send, recv, end: the shortest possible mismatch.
	if len(res.Trace) != 4 {
		t.Fatalf("trace length = %d, want 4\n%s", len(res.Trace), res)
	}
	last := res.Trace[len(res.Trace)-1]
	if last.Op != EndStep {
		t.Fatalf("last op = %s, want end", last.Op)
	}
	if got := last.Args[1].String(); got != "n!0" {
		t.Fatalf("end nonce = %s, want n!0", got)
	}
	if err := res.Verify(p, spec, spec, nil); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// safeProtocol: the responder returns the claimed subject and the
// challenge inside an encryption under a key the attacker does not know,
// and its begin claim echoes both, so every reachable end has a matching
// begin.
func safeProtocol() *Protocol {
	k := AtomOf("k")
	return &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			FreshAtom("n"),
			SendMsg(PairOf(VarOf("me"), VarOf("n"))),
			RecvMsg(EncOf(k, PairOf(VarOf("me"), VarOf("n")))),
			EndEv(VarOf("me"), VarOf("n")),
		}},
		{Name: "resp", Steps: []Step{
			RecvMsg(PairOf(VarOf("s"), VarOf("x"))),
			BeginEv(VarOf("s"), VarOf("x")),
			SendMsg(EncOf(k, PairOf(VarOf("s"), VarOf("x")))),
		}},
	}}
}

func TestSafeProtocol(t *testing.T) {
	p := safeProtocol()
	init := &SessionSpec{Role: "init", Params: map[string]string{"me": "A"}}
	resp := &SessionSpec{Role: "resp"}
	res, err := FindMismatch(p, init, resp, nil, 1_000_000)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Safe {
		t.Fatalf("status = %s, want safe\n%s", res.Status, res)
	}
}

// flawedProtocol is safeProtocol with the subject moved outside the
// encryption. The attacker feeds the responder a junk pair so its begin
// claim names the wrong subject while the initiator still completes.
func flawedProtocol() *Protocol {
	k := AtomOf("k")
	return &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			FreshAtom("n"),
			SendMsg(PairOf(VarOf("me"), VarOf("n"))),
			RecvMsg(EncOf(k, VarOf("n"))),
			EndEv(VarOf("me"), VarOf("n")),
		}},
		{Name: "resp", Steps: []Step{
			RecvMsg(PairOf(VarOf("s"), VarOf("x"))),
			BeginEv(VarOf("s"), VarOf("x")),
			SendMsg(EncOf(k, VarOf("x"))),
		}},
	}}
}

func TestFlawedProtocolAttack(t *testing.T) {
	p := flawedProtocol()
	init := &SessionSpec{Role: "init", Params: map[string]string{"me": "A"}}
	resp := &SessionSpec{Role: "resp"}
	res, err := FindMismatch(p, init, resp, nil, 1_000_000)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Attack {
		t.Fatalf("status = %s, want attack", res.Status)
	}
	if err := res.Verify(p, init, resp, nil); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestBudgetExhaustedIsUnknown(t *testing.T) {
	p := reflectionProtocol()
	spec := &SessionSpec{Role: "init"}
	res, err := FindMismatch(p, spec, spec, nil, 2)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Unknown {
		t.Fatalf("status = %s, want unknown", res.Status)
	}
	if res.Explored != 2 {
		t.Fatalf("explored = %d, want 2", res.Explored)
	}
}

func TestAttackerCannotGuessFreshAtom(t *testing.T) {
	// The initiator demands its nonce in the clear before ever sending
	// it; the attacker cannot invent n!0, so the run is safe.
	p := &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			FreshAtom("n"),
			RecvMsg(VarOf("n")),
			EndEv(AtomOf("A"), VarOf("n")),
		}},
	}}
	spec := &SessionSpec{Role: "init"}
	res, err := FindMismatch(p, spec, spec, []*Term{AtomOf("hello")}, -1)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Safe {
		t.Fatalf("status = %s, want safe\n%s", res.Status, res)
	}
}

func TestVerifyRejectsTamperedCertificate(t *testing.T) {
	p := reflectionProtocol()
	spec := &SessionSpec{Role: "init"}
	res, err := FindMismatch(p, spec, spec, nil, -1)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}

	// Tamper: replace the received message with a different term.
	bad := *res
	bad.Trace = append([]TraceEntry(nil), res.Trace...)
	bad.Trace[2].Message = AtomOf("forged")
	if err := bad.Verify(p, spec, spec, nil); err == nil {
		t.Fatal("Verify accepted a tampered message")
	}

	// Tamper: claim the attacker knew the message from the start by
	// checking against empty knowledge with a leaf-only fake proof.
	bad2 := *res
	bad2.Trace = append([]TraceEntry(nil), res.Trace...)
	bad2.Trace[2].Proof = &Derivation{Op: Leaf, Term: AtomOf("forged")}
	if err := bad2.Verify(p, spec, spec, nil); err == nil {
		t.Fatal("Verify accepted a bogus derivation")
	}
}

func TestComposeDepthBound(t *testing.T) {
	// The responder only accepts a depth-4 composition; the attacker may
	// only build depth 3, so the end is unreachable.
	deep := PairOf(PairOf(PairOf(AtomOf("a"), AtomOf("b")), AtomOf("c")), AtomOf("d"))
	p := &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			RecvMsg(deep),
			EndEv(AtomOf("A"), AtomOf("n")),
		}},
	}}
	spec := &SessionSpec{Role: "init"}
	res, err := FindMismatch(p, spec, spec, []*Term{AtomOf("a"), AtomOf("b"), AtomOf("c"), AtomOf("d")}, -1)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res.Status != Safe {
		t.Fatalf("status = %s, want safe (depth-4 term must be out of reach)", res.Status)
	}

	// Sanity: a depth-3 target is reachable and the attack fires.
	shallow := PairOf(PairOf(AtomOf("a"), AtomOf("b")), AtomOf("c"))
	p2 := &Protocol{Roles: []Role{
		{Name: "init", Steps: []Step{
			RecvMsg(shallow),
			EndEv(AtomOf("A"), AtomOf("n")),
		}},
	}}
	res2, err := FindMismatch(p2, spec, spec, []*Term{AtomOf("a"), AtomOf("b"), AtomOf("c")}, -1)
	if err != nil {
		t.Fatalf("FindMismatch: %v", err)
	}
	if res2.Status != Attack {
		t.Fatalf("status = %s, want attack for depth-3 term", res2.Status)
	}
	if err := res2.Verify(p2, spec, spec, []*Term{AtomOf("a"), AtomOf("b"), AtomOf("c")}); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestValidation(t *testing.T) {
	steps := make([]Step, MaxSteps+1)
	for i := range steps {
		steps[i] = SendMsg(AtomOf("a"))
	}
	p := &Protocol{Roles: []Role{{Name: "chatty", Steps: steps}}}
	spec := &SessionSpec{Role: "chatty"}
	if _, err := FindMismatch(p, spec, spec, nil, -1); err == nil ||
		!strings.Contains(err.Error(), "max") {
		t.Fatalf("expected step-limit error, got %v", err)
	}

	if _, err := FindMismatch(p, &SessionSpec{Role: "ghost"}, spec, nil, -1); err == nil {
		t.Fatal("expected unknown-role error")
	}
}
