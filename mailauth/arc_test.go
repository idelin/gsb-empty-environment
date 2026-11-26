package mailauth

import (
	"strconv"
	"strings"
	"testing"
)

func buildValidARC(t *testing.T, hops int) (*Verifier, []byte) {
	t.Helper()
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	builder := newARCBuilder(keys)
	builder.register(store, hops)

	msg, err := builder.buildChain(baseMessage(), hops)
	if err != nil {
		t.Fatal(err)
	}
	return newTestVerifier(t, store), msg
}

func TestValidARCChain(t *testing.T) {
	verifier, msg := buildValidARC(t, 3)
	verdict, err := verifier.VerifyMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || !verdict.ARC.Present {
		t.Fatal("ARC chain should be detected")
	}
	if !verdict.ARC.Valid {
		t.Fatalf("valid ARC chain rejected: %+v", verdict.ARC.Findings)
	}
	want := []int{1, 2, 3}
	if strings.TrimSpace(strings.Join(intsToStrings(verdict.ARC.Instances), ",")) !=
		strings.Join(intsToStrings(want), ",") {
		t.Fatalf("instances = %v, want %v", verdict.ARC.Instances, want)
	}
	if verdict.ARC.Hops[1].CV != cvNone || verdict.ARC.Hops[2].CV != cvPass {
		t.Fatalf("cv state machine wrong: %+v", verdict.ARC.Hops)
	}
}

func TestARCBrokenInstanceSequence(t *testing.T) {
	verifier, msg := buildValidARC(t, 3)

	// Remove the whole instance 2 set: 1 -> 3 is a jump.
	var lines []string
	for _, line := range strings.Split(string(msg), "\r\n") {
		if strings.Contains(line, "ARC-Authentication-Results2") ||
			strings.Contains(line, "ARC-Message-Signature2") ||
			strings.Contains(line, "ARC-Seal2") {
			continue
		}
		lines = append(lines, line)
	}
	broken := strings.Join(lines, "\r\n")

	verdict, err := verifier.VerifyMessage([]byte(broken))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatalf("skipped instance must invalidate chain: %+v", verdict.ARC)
	}
	if !verdict.HasCode(FindingARCInstanceSequence) {
		t.Fatalf("expected %s, got %v", FindingARCInstanceSequence, verdict.ARC.Findings)
	}
}

func TestARCDuplicateInstance(t *testing.T) {
	verifier, msg := buildValidARC(t, 2)

	// Rename instance 2 fields to instance 1 so the number repeats.
	dup := strings.ReplaceAll(string(msg), "ARC-Authentication-Results2", "ARC-Authentication-Results1")
	dup = strings.ReplaceAll(dup, "ARC-Message-Signature2", "ARC-Message-Signature1")
	dup = strings.ReplaceAll(dup, "ARC-Seal2", "ARC-Seal1")

	verdict, err := verifier.VerifyMessage([]byte(dup))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("duplicate ARC instance must invalidate chain")
	}
	if !verdict.HasCode(FindingARCInstanceSequence) {
		t.Fatalf("expected %s", FindingARCInstanceSequence)
	}
}

func TestARCInitialCVNotNone(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	builder := newARCBuilder(keys)
	builder.register(store, 1)

	msg, err := builder.addHop(baseMessage(), 1, cvPass)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := newTestVerifier(t, store).VerifyMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("i=1 with cv=pass must be invalid")
	}
	if !verdict.HasCode(FindingARCInitialCV) {
		t.Fatalf("expected %s", FindingARCInitialCV)
	}
}

func TestARCCVFailVoidsChain(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	builder := newARCBuilder(keys)
	builder.register(store, 3)

	msg, err := builder.buildChain(baseMessage(), 2)
	if err != nil {
		t.Fatal(err)
	}
	msg, err = builder.addHop(msg, 3, cvFail)
	if err != nil {
		t.Fatal(err)
	}

	verdict, err := newTestVerifier(t, store).VerifyMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("cv=fail must void the whole chain")
	}
	if !verdict.HasCode(FindingARCCVFail) {
		t.Fatalf("expected %s", FindingARCCVFail)
	}
}

func TestARCTamperedAMSFails(t *testing.T) {
	verifier, msg := buildValidARC(t, 2)
	tampered := strings.Replace(string(msg), "Amount: 100 USD", "Amount: 9999 USD", 1)
	verdict, err := verifier.VerifyMessage([]byte(tampered))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("body tampering must break AMS body hashes and the chain")
	}
	if !verdict.HasCode(FindingARCMessageSigInvalid) {
		t.Fatalf("expected %s, got %+v", FindingARCMessageSigInvalid, verdict.ARC.Findings)
	}
}

func TestARCCVPassMismatchDetected(t *testing.T) {
	verifier, msg := buildValidARC(t, 2)
	// Tamper the instance 2 AMS body-protected content while leaving its
	// ARC-Seal cv=pass claim in place.
	tampered := strings.Replace(string(msg), "Amount: 100 USD", "Amount: 42 USD", 1)
	verdict, err := verifier.VerifyMessage([]byte(tampered))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("cv=pass with a failing AMS must invalidate the chain")
	}
	if !verdict.HasCode(FindingARCCVMismatch) {
		t.Fatalf("expected %s, got %+v", FindingARCCVMismatch, verdict.ARC.Findings)
	}
}

func TestARCSealSignatureTampered(t *testing.T) {
	verifier, msg := buildValidARC(t, 2)

	// Flip a byte inside the first ARC-Seal b= value.
	text := string(msg)
	marker := "ARC-Seal1:"
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatal("ARC-Seal1 not found")
	}
	bPos := strings.Index(text[idx:], "b=") + idx
	tampered := text[:bPos+2] + "A" + text[bPos+3:]

	verdict, err := verifier.VerifyMessage([]byte(tampered))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("tampered ARC-Seal must invalidate chain")
	}
	if !verdict.HasCode(FindingARCSealInvalid) {
		t.Fatalf("expected %s, got %+v", FindingARCSealInvalid, verdict.ARC.Findings)
	}
}

func TestARCHopLimit(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	builder := newARCBuilder(keys)
	builder.register(store, 3)

	msg, err := builder.buildChain(baseMessage(), 3)
	if err != nil {
		t.Fatal(err)
	}
	verifier := newTestVerifier(t, store)
	verifier.policy.MaxARCHops = 2

	verdict, err := verifier.VerifyMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("chain exceeding hop limit must be invalid")
	}
	if !verdict.HasCode(FindingARCHopLimit) {
		t.Fatalf("expected %s", FindingARCHopLimit)
	}
}

func TestARC50HopBoundary(t *testing.T) {
	keys, err := generateTestKeys()
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryKeyStore()
	builder := newARCBuilder(keys)

	// The builder cycles selectors, so every hop shares a registered key.
	builder.register(store, 3)

	msg, err := builder.buildChain(baseMessage(), 50)
	if err != nil {
		t.Fatal(err)
	}
	verifier := newTestVerifier(t, store)
	verdict, err := verifier.VerifyMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || !verdict.ARC.Valid {
		t.Fatalf("50-hop chain must be accepted (RFC 8617 cap): %+v", verdict.ARC)
	}

	over, err := builder.buildChain(baseMessage(), 51)
	if err != nil {
		t.Fatal(err)
	}
	verdict2, err := verifier.VerifyMessage(over)
	if err != nil {
		t.Fatal(err)
	}
	if verdict2.ARC == nil || verdict2.ARC.Valid {
		t.Fatal("51-hop chain must exceed the 50 hop limit")
	}
	if !verdict2.HasCode(FindingARCHopLimit) {
		t.Fatalf("expected %s", FindingARCHopLimit)
	}
}

func TestARCMissingSetFails(t *testing.T) {
	verifier, msg := buildValidARC(t, 2)

	var lines []string
	for _, line := range strings.Split(string(msg), "\r\n") {
		if strings.Contains(line, "ARC-Seal2") {
			continue
		}
		lines = append(lines, line)
	}
	verdict, err := verifier.VerifyMessage([]byte(strings.Join(lines, "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if verdict.ARC == nil || verdict.ARC.Valid {
		t.Fatal("incomplete ARC set must invalidate chain")
	}
	if !verdict.HasCode(FindingARCMissingSet) {
		t.Fatalf("expected %s", FindingARCMissingSet)
	}
}

func intsToStrings(values []int) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strconv.Itoa(v)
	}
	return out
}
