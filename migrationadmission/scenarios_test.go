package migrationadmission

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// TestSamePrefixDifferentTail exercises prefix-equal/tail-different collision
// through an empty transcription (character deletion) plus the end tail.
func TestSamePrefixDifferentTail(t *testing.T) {
	old, next := mildPolicies(2)
	next.Transcription = tableWith("new-delete", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: nil},
	}, symT)

	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if !errors.Is(result.Violation, ErrUnauthorizedFold) {
		t.Fatalf("want unauthorized fold, got %v (%s)", result.Violation, result)
	}
	if result.Pair[0] == result.Pair[1] {
		t.Fatalf("folding pair must be distinct names, got %q", result.Pair)
	}
	folded, err := newCompiled.transduce([]byte(result.Pair[0]))
	if err != nil {
		t.Fatal(err)
	}
	other, err := newCompiled.transduce([]byte(result.Pair[1]))
	if err != nil {
		t.Fatal(err)
	}
	if !symbolsEqual(folded, other) {
		t.Fatalf("reported pair %q does not share a new identifier", result.Pair)
	}

	_, priv := testRoot(t)
	claims, _ := requiredAliases(t, "tenant-tail", next.Version, 1, old, next, priv)
	if len(claims) == 0 {
		t.Fatal("enumeration found no required alias claims")
	}
	signed := buildBundle(t, "tenant-tail", 1, old, next, claims, priv, nil)
	verified, err := Verify(marshalWire(t, signed), nil)
	if err != nil {
		t.Fatalf("authorized fold rejected: %v", err)
	}
	id, err := verified.Admit([]byte(result.Pair[0]))
	if err != nil {
		t.Fatal(err)
	}
	wantFolded, err := newCompiled.transduce([]byte(result.Pair[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !symbolsEqual(id, wantFolded) {
		t.Fatalf("deleted-character identifier = %v", id)
	}
}

// TestTailOnlyCollision isolates the end marker: when both characters delete,
// every accepted name collapses onto the shared tail.
func TestTailOnlyCollision(t *testing.T) {
	old, next := mildPolicies(2)
	next.Transcription = tableWith("new-tail-only", []TranscriptionRule{
		{On: 'a', Emit: nil},
		{On: 'b', Emit: nil},
	}, symT)
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if !errors.Is(result.Violation, ErrUnauthorizedFold) {
		t.Fatalf("want tail collision, got %v", result.Violation)
	}

	_, priv := testRoot(t)
	claim, err := SignAliasClaim(priv, AliasClaim{
		TenantID:      "tenant-tailonly",
		PolicyVersion: next.Version,
		OldSubjectA:   []Symbol{symA},
		OldSubjectB:   []Symbol{symB},
		Epoch:         1,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed := buildBundle(t, "tenant-tailonly", 1, old, next, []AliasClaim{claim}, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrUnauthorizedFold) {
		t.Fatalf("incomplete alias set accepted: %v", err)
	}
}

// TestCrossCharacterExpansion covers two-symbol expansion, asynchronous
// buffering and divergent streams for inputs of different lengths.
func TestCrossCharacterExpansion(t *testing.T) {
	old, next := mildPolicies(1)
	next.Transcription = tableWith("new-expand", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symX, symY}},
		{On: 'b', Emit: []Symbol{symY}},
	}, symT)
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}

	// The expansion is injective, so the product finds no collision.
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if result.Violation != nil {
		t.Fatalf("injective expansion rejected: %v", result.Violation)
	}
	if result.MaxBacklog != 4 {
		t.Fatalf("expansion backlog = %d, want 4", result.MaxBacklog)
	}

	// Direct transduction confirms deletion/expansion semantics end to end.
	id, err := newCompiled.transduce([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if !symbolsEqual(id, []Symbol{symX, symY, symT}) {
		t.Fatalf("expanded id = %v", id)
	}

	_, priv := testRoot(t)
	signed := buildBundle(t, "tenant-expand", 1, old, next, nil, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); err != nil {
		t.Fatalf("expansion bundle rejected: %v", err)
	}

	// An over-tight declared bound must be rejected.
	wrong := 3
	badSigned := buildBundle(t, "tenant-expand", 1, old, next, nil, priv, &wrong)
	if _, err := Verify(marshalWire(t, badSigned), nil); !errors.Is(err, ErrWrongLatenessBound) {
		t.Fatalf("wrong bound accepted: %v", err)
	}
}

// TestLatenessHardCap rejects a table whose expansion buffers more than four
// unmatched symbols, regardless of the declared certificate.
func TestLatenessHardCap(t *testing.T) {
	old, next := mildPolicies(64)
	next.Transcription = tableWith("new-wide", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symX, symY}},
		{On: 'b', Emit: []Symbol{symY}},
	})
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	newCompiled, err := next.compile()
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: newCompiled,
		aliases:   map[string]bool{},
	})
	if !errors.Is(result.Violation, ErrLatenessExceeded) {
		t.Fatalf("want lateness exceeded, got %v", result.Violation)
	}

	_, priv := testRoot(t)
	declared := 4
	signed := buildBundle(t, tenantWide, 1, old, next, nil, priv, &declared)
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrLatenessExceeded) {
		t.Fatalf("over-cap bundle accepted: %v", err)
	}
}

const tenantWide = "tenant-wide"

// TestTenantScopedAlias verifies that a merge authorized for one tenant cannot
// be replayed for another.
func TestTenantScopedAlias(t *testing.T) {
	old, next := mildPolicies(2)
	next.Transcription = tableWith("new-delete", []TranscriptionRule{
		{On: 'a', Emit: []Symbol{symA}},
		{On: 'b', Emit: nil},
	}, symT)
	oldCompiled, err := old.compile()
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeProduct(productConfig{
		oldPolicy: oldCompiled,
		newPolicy: mustCompile(t, next),
		aliases:   map[string]bool{},
	})
	if !errors.Is(result.Violation, ErrUnauthorizedFold) {
		t.Fatalf("want unauthorized fold, got %v", result.Violation)
	}

	_, priv := testRoot(t)
	claims, _ := requiredAliases(t, "tenant-allowed", next.Version, 1, old, next, priv)
	signed := buildBundle(t, "tenant-allowed", 1, old, next, claims, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); err != nil {
		t.Fatalf("allowed tenant rejected: %v", err)
	}

	// Reusing the claim under a different tenant fails structural binding.
	foreign := buildBundle(t, "tenant-foreign", 1, old, next, claims, priv, nil)
	if _, err := Verify(marshalWire(t, foreign), nil); !errors.Is(err, ErrAliasProofIncomplete) {
		t.Fatalf("foreign tenant accepted alias: %v", err)
	}
}

// TestNoLoginIdentifier rejects rule-only policies that cannot emit an
// identifier: either an empty new language or total symbol deletion.
func TestNoLoginIdentifier(t *testing.T) {
	old, _ := mildPolicies(2)

	// Empty new language: start state is not accepting and has no transitions.
	emptyNext := Policy{
		Version: "new-empty",
		Grammar: Grammar{
			Name:        "g-empty",
			Start:       0,
			Accepts:     []int{2},
			Transitions: []Rule{{From: 0, On: 'a', To: 1}},
		},
		Transcription: newMildTable(),
	}
	_, priv := testRoot(t)
	signed := buildBundle(t, "tenant-empty", 1, old, emptyNext, nil, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrNoLoginIdentifier) {
		t.Fatalf("empty language accepted: %v", err)
	}

	// Every rule deletes and there is no tail: legal names exist, but none can
	// produce a login identifier.
	silentNext := Policy{
		Version: "new-silent",
		Grammar: allAcceptGrammar("g-silent", 2),
		Transcription: tableWith("t-silent", []TranscriptionRule{
			{On: 'a', Emit: nil},
			{On: 'b', Emit: nil},
		}),
	}
	signed = buildBundle(t, "tenant-silent", 1, old, silentNext, nil, priv, nil)
	if _, err := Verify(marshalWire(t, signed), nil); !errors.Is(err, ErrNoLoginIdentifier) {
		t.Fatalf("silent policy accepted: %v", err)
	}
}

// TestCredentialRotation ensures an alias update invalidates old credentials.
func TestCredentialRotation(t *testing.T) {
	old, next := mildPolicies(2)
	_, priv := testRoot(t)
	epochOne := buildBundle(t, "tenant-rot", 1, old, next, nil, priv, nil)
	verifiedOne, err := Verify(marshalWire(t, epochOne), nil)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := verifiedOne.IssueCredential(priv, []byte("ab"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifiedOne.VerifyCredential(credential); err != nil {
		t.Fatalf("fresh credential rejected: %v", err)
	}

	// Alias rotation changes the epoch; the old credential must fail.
	epochTwo := buildBundle(t, "tenant-rot", 2, old, next, nil, priv, nil)
	verifiedTwo, err := Verify(marshalWire(t, epochTwo), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifiedTwo.VerifyCredential(credential); !errors.Is(err, ErrCredentialExpired) {
		t.Fatalf("stale credential accepted: %v", err)
	}

	// A new credential issued under epoch two verifies.
	refreshed, err := verifiedTwo.IssueCredential(priv, []byte("ab"))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifiedTwo.VerifyCredential(refreshed); err != nil {
		t.Fatalf("refreshed credential rejected: %v", err)
	}
}

// TestTamperAndIllegalName covers signature binding and grammar enforcement.
func TestTamperAndIllegalName(t *testing.T) {
	old, next := mildPolicies(2)
	_, priv := testRoot(t)
	signed := buildBundle(t, "tenant-tamper", 1, old, next, nil, priv, nil)
	wire := marshalWire(t, signed)

	tampered := append([]byte(nil), wire...)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := UnmarshalSignedBundle(tampered); err != nil {
		// A parse failure is acceptable, but a parsed blob must then fail Verify.
	} else if _, err := Verify(tampered, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered blob accepted: %v", err)
	}

	verified, err := Verify(wire, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verified.Admit([]byte("aaa")); !errors.Is(err, ErrIllegalName) {
		t.Fatalf("over-length name accepted: %v", err)
	}

	// Pinning a different trusted root rejects the bundle.
	otherPub, _, err := GenerateRootKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(wire, ed25519.PublicKey(otherPub)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("foreign trusted root accepted: %v", err)
	}
}
