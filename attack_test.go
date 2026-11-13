package netsec

import (
	"errors"
	"testing"
)

func TestShortestWeakIdentityConfusionWitness(t *testing.T) {
	witness, err := FindShortestIdentityConfusion()
	if err != nil {
		t.Fatal(err)
	}
	if witness.Result.Depth != 6 {
		t.Fatalf("shortest witness depth = %d, want 6\n%s", witness.Result.Depth, witness.Trace)
	}
	if len(witness.Trace) != 6 {
		t.Fatalf("trace length = %d", len(witness.Trace))
	}
	if witness.Trace[1].Op != OpRelayHello || witness.Trace[1].Label != AttackMalloryName {
		t.Fatalf("trace did not relabel the initiator:\n%s", witness.Trace)
	}
	if witness.Trace[3].Op != OpDeliverAuth || witness.Trace[3].Signer != AttackMalloryName {
		t.Fatalf("trace did not substitute Mallory's signature:\n%s", witness.Trace)
	}
	if !witness.Result.Confused || !witness.Result.MatchingKeys {
		t.Fatalf("witness result = %+v", witness.Result)
	}
	if witness.Result.Initiator != AttackInitiatorName || witness.Result.Responder != AttackResponderName {
		t.Fatalf("endpoint identities = %q/%q", witness.Result.Initiator, witness.Result.Responder)
	}
	if witness.Result.InitiatorPeer != AttackResponderName {
		t.Fatalf("A recorded peer %q", witness.Result.InitiatorPeer)
	}
	if witness.Result.ResponderPeer != AttackMalloryName {
		t.Fatalf("B recorded peer %q", witness.Result.ResponderPeer)
	}
	if witness.Result.InitiatorDigest != witness.Result.ResponderDigest {
		t.Fatal("weak endpoints used different transcript digests")
	}
	if witness.Result.Keys.SendA == ([32]byte{}) || witness.Result.Keys.SendB == ([32]byte{}) {
		t.Fatal("witness keys are zero")
	}
	if witness.Result.Keys.SendA == witness.Result.Keys.SendB {
		t.Fatal("directional keys collided")
	}

	shorter, shorterResult, err := search(ModeWeak, witness.Result.Depth-1)
	if err != nil {
		t.Fatal(err)
	}
	if shorter != nil || shorterResult != nil {
		t.Fatalf("found a shorter weak trace:\n%s", shorter)
	}

	replayed, err := RunTrace(ModeWeak, witness.Trace)
	if err != nil {
		t.Fatalf("replay weak witness: %v", err)
	}
	if !replayed.Confused || !replayed.MatchingKeys {
		t.Fatalf("weak replay result = %+v", replayed)
	}
	if replayed.InitiatorPeer != witness.Result.InitiatorPeer || replayed.ResponderPeer != witness.Result.ResponderPeer {
		t.Fatal("weak replay changed peer bindings")
	}

	if err := ProveHardenedRejects(witness); err != nil {
		t.Fatalf("hardened proof: %v", err)
	}
	if _, err := RunTrace(ModeHardened, witness.Trace); !errors.Is(err, ErrSignature) {
		t.Fatalf("hardened replay error = %v, want ErrSignature", err)
	}
	trace, result, err := search(ModeHardened, len(witness.Trace))
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil || result != nil {
		t.Fatalf("hardened search found a trace:\n%s", trace)
	}
}

func TestAttackModelSessionLimit(t *testing.T) {
	three := Trace{
		{Op: OpOpenInitiator, Session: 1},
		{Op: OpOpenInitiator, Session: 2},
		{Op: OpOpenInitiator, Session: 3},
	}
	result, err := RunTrace(ModeWeak, three)
	if err != nil {
		t.Fatalf("three sessions: %v", err)
	}
	if result.Sessions != 3 {
		t.Fatalf("session count = %d", result.Sessions)
	}
	four := append(append(Trace(nil), three...), AttackAction{Op: OpOpenInitiator, Session: 4})
	if _, err := RunTrace(ModeWeak, four); !errors.Is(err, ErrAttackConcurrency) {
		t.Fatalf("four sessions error = %v", err)
	}
}

func TestAttackModelThreeConcurrentSessions(t *testing.T) {
	var trace Trace
	for session := 1; session <= MaxAttackSessions; session++ {
		trace = append(trace, AttackAction{Op: OpOpenInitiator, Session: session})
	}
	for session := 1; session <= MaxAttackSessions; session++ {
		trace = append(trace,
			AttackAction{Op: OpRelayHello, Session: session, Label: AttackInitiatorName},
			AttackAction{Op: OpRelayResponse, Session: session},
			AttackAction{Op: OpDeliverAuth, Session: session, Signer: AttackInitiatorName},
			AttackAction{Op: OpDeliverConfirmA, Session: session},
			AttackAction{Op: OpDeliverConfirmB, Session: session},
		)
	}
	result, err := RunTrace(ModeWeak, trace)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sessions != MaxAttackSessions {
		t.Fatalf("session count = %d", result.Sessions)
	}
	if result.Confused {
		t.Fatal("honest three-session trace reported confusion")
	}
	if !result.MatchingKeys {
		t.Fatal("honest session did not derive matching keys")
	}
}

func TestAttackModelRejectsForgedSigner(t *testing.T) {
	trace := Trace{
		{Op: OpOpenInitiator, Session: 1},
		{Op: OpRelayHello, Session: 1, Label: AttackMalloryName},
		{Op: OpRelayResponse, Session: 1},
		{Op: OpDeliverAuth, Session: 1, Signer: AttackResponderName},
	}
	if _, err := RunTrace(ModeWeak, trace); !errors.Is(err, ErrAttackForge) {
		t.Fatalf("forged signer error = %v", err)
	}
}
