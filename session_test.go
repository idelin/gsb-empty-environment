package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

const (
	testSessionHandle Handle = 0x03000001
	testNVIndexA      Handle = 0x01000001
	testNVIndexB      Handle = 0x01000002
	testNVIndexC      Handle = 0x01000003
	testAuthHandle    Handle = 0x40000001
)

func newTestAuthority(t *testing.T) (*fakeTPM, *Authority) {
	t.Helper()
	fake := newFakeTPM()
	a, err := NewAuthority(fake, fake.startup)
	if err != nil {
		t.Fatal(err)
	}
	return fake, a
}

func openTestSession(t *testing.T, a *Authority, digest []byte) *Session {
	t.Helper()
	opts := SessionOptions{
		Handle:       testSessionHandle,
		HashAlg:      AlgSHA256,
		NonceTPM:     bytes.Repeat([]byte{0x11}, sha256.Size),
		PolicyDigest: digest,
		Principal:    "nv-agent",
		StartupCount: a.StartupCount(),
		VerifyRecovery: func(_ context.Context, proof RecoveryProof) error {
			if len(proof.Quote) == 0 || len(proof.Audit) == 0 {
				return errors.New("empty proof")
			}
			return nil
		},
	}
	s, err := a.Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testAuth() *AuthValues {
	return &AuthValues{
		NonceCaller: bytes.Repeat([]byte{0x22}, sha256.Size),
		HMAC:        []byte{0x01, 0x02, 0x03},
	}
}

func nvWriteTemplate(index Handle, data []byte, offset uint16) CommandTemplate {
	parameters, err := NVWriteParameters(data, offset)
	if err != nil {
		panic(err)
	}
	return CommandTemplate{
		Code:       CCNVWrite,
		Handles:    []Handle{testAuthHandle, index},
		Names:      []Name{testNameFor(testAuthHandle), testNameFor(index)},
		Parameters: parameters,
	}
}

func TestCPHashUsesExactCommandAndParameterBytes(t *testing.T) {
	template := nvWriteTemplate(testNVIndexA, []byte{0x10, 0x20, 0x30}, 0)
	got, err := template.CPHash(AlgSHA256)
	if err != nil {
		t.Fatal(err)
	}

	h := sha256.New()
	var code [4]byte
	code[0] = byte(CCNVWrite >> 24)
	code[1] = byte(CCNVWrite >> 16)
	code[2] = byte(CCNVWrite >> 8)
	code[3] = byte(uint32(CCNVWrite) & 0xff)
	h.Write(code[:])
	for _, name := range template.Names {
		h.Write(name)
	}
	h.Write(template.Parameters)
	want := h.Sum(nil)
	if !bytes.Equal(got, want) {
		t.Fatalf("cpHash mismatch:\n got %x\nwant %x", got, want)
	}

	template.Parameters[len(template.Parameters)-1] ^= 0x01
	changed, err := template.CPHash(AlgSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(changed, got) {
		t.Fatal("a parameter tail change did not change cpHash")
	}
}

func TestConcurrentNVIndexExchangeCannotRedirectApproval(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))

	type result struct {
		index Handle
		prep  *Prepared
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, index := range []Handle{testNVIndexA, testNVIndexB} {
		index := index
		go func() {
			<-start
			prep, err := session.Authorize(context.Background(), nvWriteTemplate(index, []byte{byte(uint32(index) & 0xff), 0xaa}, 0), AuthorizeOptions{
				NameHash: true,
				Auth:     testAuth(),
			})
			results <- result{index: index, prep: prep, err: err}
		}()
	}
	close(start)

	var winner result
	busy := 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			if winner.prep != nil {
				t.Fatal("two authorizations won the same session")
			}
			winner = r
			continue
		}
		if !errors.Is(r.err, ErrSessionBusy) {
			t.Fatalf("loser got %v, want ErrSessionBusy", r.err)
		}
		busy++
	}
	if winner.prep == nil || busy != 1 {
		t.Fatalf("winner=%v busy=%d", winner.prep != nil, busy)
	}
	if _, err := winner.prep.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(fake.writtenAt(winner.index), []byte{byte(uint32(winner.index) & 0xff), 0xaa}) {
		t.Fatalf("approved NV index was not the written index")
	}
	other := testNVIndexA
	if winner.index == testNVIndexA {
		other = testNVIndexB
	}
	if data := fake.writtenAt(other); len(data) != 0 {
		t.Fatalf("losing NV index was written: %x", data)
	}

	approved := winner.prep.Template()
	if approved.Handles[1] != winner.index {
		t.Fatalf("frozen handle %#x differs from approved index %#x", uint32(approved.Handles[1]), uint32(winner.index))
	}
}

func TestContextSaveLoadCreatesGenerationWithoutCommitments(t *testing.T) {
	fake, authority := newTestAuthority(t)
	s1 := openTestSession(t, authority, make([]byte, sha256.Size))
	if fake.policyDigest(testSessionHandle) == nil {
		_ = fake.policyDigest(testSessionHandle)
	}
	blob, err := s1.ContextSave(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s1.State() != StateSaved {
		t.Fatalf("saved session state=%s", s1.State())
	}
	generation1 := s1.Binding().Generation
	if _, err := s1.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{1}, 0), AuthorizeOptions{Auth: testAuth()}); !errors.Is(err, ErrGeneration) {
		t.Fatalf("old session after save: %v, want ErrGeneration", err)
	}

	s2, err := authority.ContextLoad(context.Background(), blob, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	generation2 := s2.Binding().Generation
	if generation2 <= generation1 {
		t.Fatalf("generation did not advance: %d -> %d", generation1, generation2)
	}
	if c := s2.Commitments(); c.CPHash != nil || c.NameHash != nil || c.OR != nil {
		t.Fatal("loaded generation inherited host commitments")
	}

	p2, err := s2.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x77}, 0), AuthorizeOptions{Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Submit(context.Background()); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("aborted approval submit: %v, want ErrSessionExpired", err)
	}

	// The second load restores the pre-commit TPM context at the same numeric
	// handle, but the authority must allocate another generation and start with
	// an empty commitment set.
	s3, err := authority.ContextLoad(context.Background(), blob, SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s3.Binding().Generation <= generation2 {
		t.Fatalf("rollback reused generation %d", s3.Binding().Generation)
	}
	if c := s3.Commitments(); c.CPHash != nil {
		t.Fatal("rollback inherited a cpHash commitment")
	}

	p3, err := s3.Authorize(context.Background(), nvWriteTemplate(testNVIndexB, []byte{0x88}, 0), AuthorizeOptions{NameHash: true, Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p3.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexB), []byte{0x88}) {
		t.Fatal("new generation did not execute its own command")
	}
	if data := fake.writtenAt(testNVIndexA); len(data) != 0 {
		t.Fatalf("aborted command was replayed: %x", data)
	}
}

func TestContextLoadRequiresRegisteredContextUnlessOptedIn(t *testing.T) {
	fake, authority1 := newTestAuthority(t)
	session := openTestSession(t, authority1, make([]byte, sha256.Size))
	blob, err := session.ContextSave(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	authority2, err := NewAuthority(fake, fake.startup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority2.ContextLoad(context.Background(), blob, SessionOptions{}); !errors.Is(err, ErrUnregisteredContext) {
		t.Fatalf("unregistered context: %v, want ErrUnregisteredContext", err)
	}

	restored, err := authority2.ContextLoad(context.Background(), blob, SessionOptions{
		Handle:                   testSessionHandle,
		HashAlg:                  AlgSHA256,
		NonceTPM:                 bytes.Repeat([]byte{0x11}, sha256.Size),
		PolicyDigest:             make([]byte, sha256.Size),
		Principal:                "nv-agent",
		StartupCount:             fake.startup,
		AllowUnregisteredContext: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Binding().SessionHandle != testSessionHandle {
		t.Fatalf("restored handle=%#x", uint32(restored.Binding().SessionHandle))
	}
}

func TestPrincipalBindingMismatchRejectedBeforePolicyCommand(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	ctx := WithPrincipal(context.Background(), "different-principal")
	_, err := session.Authorize(ctx, nvWriteTemplate(testNVIndexA, []byte{1}, 0), AuthorizeOptions{Auth: testAuth()})
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("principal mismatch: %v, want ErrBindingMismatch", err)
	}
	if fake.countCommands(CCPolicyCpHash) != 0 {
		t.Fatal("principal mismatch reached PolicyCpHash")
	}
}

func TestTPMResetInvalidatesStartupBoundSession(t *testing.T) {
	_, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	prep, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x99}, 0), AuthorizeOptions{Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}

	authority.TPMReset(2)
	if _, err := prep.Submit(context.Background()); !errors.Is(err, ErrStartupChanged) {
		t.Fatalf("submit after reset: %v, want ErrStartupChanged", err)
	}
	if _, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x99}, 0), AuthorizeOptions{Auth: testAuth()}); !errors.Is(err, ErrStartupChanged) {
		t.Fatalf("authorize after reset: %v, want ErrStartupChanged", err)
	}

	reopened := openTestSession(t, authority, make([]byte, sha256.Size))
	if reopened.Binding().StartupCount != 2 {
		t.Fatalf("new startup count=%d", reopened.Binding().StartupCount)
	}
}

func TestPolicyORRejectsWrongBranchBeforeTPMWrite(t *testing.T) {
	fake, authority := newTestAuthority(t)
	branchA := bytes.Repeat([]byte{0xa1}, sha256.Size)
	branchB := bytes.Repeat([]byte{0xb2}, sha256.Size)
	fake.setPolicyDigest(testSessionHandle, branchA)
	session := openTestSession(t, authority, branchA)

	before := fake.countCommands(CCPolicyOR)
	_, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x42}, 0), AuthorizeOptions{
		OR:   &ORSelection{Branches: [][]byte{branchA, branchB}, Selected: 1},
		Auth: testAuth(),
	})
	if !errors.Is(err, ErrPolicyORBranch) {
		t.Fatalf("wrong OR branch: %v, want ErrPolicyORBranch", err)
	}
	if fake.countCommands(CCPolicyOR) != before {
		t.Fatal("wrong OR branch reached the TPM")
	}

	prep, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x42}, 0), AuthorizeOptions{
		OR:       &ORSelection{Branches: [][]byte{branchA, branchB}, Selected: 0},
		NameHash: true,
		Auth:     testAuth(),
	})
	if err != nil {
		t.Fatal(err)
	}
	or := prep.Commitments().OR
	if or == nil || or.Selected != 0 || len(or.Branches) != 2 || !bytes.Equal(or.SelectedDigest, branchA) {
		t.Fatalf("bad OR commitment: %+v", or)
	}
	if _, err := prep.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexA), []byte{0x42}) {
		t.Fatal("correct OR branch did not execute the approved command")
	}
}

func TestFrozenTemplateIgnoresParameterAndHandleMutation(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	template := nvWriteTemplate(testNVIndexA, []byte{0x11, 0x22, 0x33}, 0)
	original := template.clone()

	prep, err := session.Authorize(context.Background(), template, AuthorizeOptions{NameHash: true, Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	approvedCPHash := prep.CPHash()

	template.Parameters[len(template.Parameters)-1] ^= 0x80
	template.Handles[1] = testNVIndexB
	template.Names[1] = testNameFor(testNVIndexB)
	if TemplatesEqual(prep.Template(), template) {
		t.Fatal("mutated template compares equal to frozen approval")
	}
	if !bytes.Equal(prep.Template().Parameters, original.Parameters) {
		t.Fatal("frozen parameter bytes changed")
	}
	if !TemplatesEqual(prep.Template(), original) {
		t.Fatal("frozen template differs from original approval")
	}
	wire, err := prep.Wire()
	if err != nil {
		t.Fatal(err)
	}
	wireCommand, err := parseFakeCommand(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wireCommand.parameters, original.Parameters) {
		t.Fatal("wire packet contains mutated parameters")
	}
	if wireCommand.authSessionHandle() != testSessionHandle {
		t.Fatal("wire packet uses the wrong bound session handle")
	}

	if _, err := prep.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexA), []byte{0x11, 0x22, 0x33}) {
		t.Fatalf("written data %x does not match approved tail", fake.writtenAt(testNVIndexA))
	}
	if data := fake.writtenAt(testNVIndexB); len(data) != 0 {
		t.Fatalf("handle mutation redirected write: %x", data)
	}
	requests := fake.requests()
	for _, cmd := range requests {
		if cmd.code != CCNVWrite {
			continue
		}
		names := []Name{testNameFor(cmd.handles[0]), testNameFor(cmd.handles[1])}
		actual, err := (CommandTemplate{Code: cmd.code, Handles: cmd.handles, Names: names, Parameters: cmd.parameters}).CPHash(AlgSHA256)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, approvedCPHash) {
			t.Fatalf("actual cpHash %x != approved %x", actual, approvedCPHash)
		}
	}
}

func TestPolicyDigestRaceBeforeSubmitIsDetected(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	prep, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0x55}, 0), AuthorizeOptions{Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}

	fake.setPolicyDigest(testSessionHandle, bytes.Repeat([]byte{0xee}, sha256.Size))
	if _, err := prep.Submit(context.Background()); !errors.Is(err, ErrPolicyDigest) {
		t.Fatalf("policy digest race: %v, want ErrPolicyDigest", err)
	}
	if fake.countCommands(CCNVWrite) != 0 {
		t.Fatal("command was sent after detecting a changed policy digest")
	}
}

func TestLegitimateOneTimeNVWriteAndSessionConsumption(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	prep, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexC, []byte{0xca, 0xfe}, 0), AuthorizeOptions{NameHash: true, Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prep.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexC), []byte{0xca, 0xfe}) {
		t.Fatal("legitimate one-time write did not happen")
	}
	if session.State() != StateConsumed {
		t.Fatalf("state after write=%s", session.State())
	}
	if _, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexC, []byte{0xca, 0xfe}, 0), AuthorizeOptions{Auth: testAuth()}); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("reuse after write: %v, want ErrSessionExpired", err)
	}
	if err := session.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	flushedGeneration := session.Binding().Generation

	session2 := openTestSession(t, authority, make([]byte, sha256.Size))
	if session2.Binding().Generation <= flushedGeneration {
		t.Fatal("Flush reused the same handle generation")
	}
	if c := session2.Commitments(); c.CPHash != nil || c.NameHash != nil || c.OR != nil {
		t.Fatal("Flush inheritance leaked old commitments")
	}
	prep2, err := session2.Authorize(context.Background(), nvWriteTemplate(testNVIndexC, []byte{0x00}, 0), AuthorizeOptions{Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = prep2.Submit(context.Background())
	var tpmErr *TPMError
	if !errors.As(err, &tpmErr) || tpmErr.Code != 0x0000014c {
		t.Fatalf("second write: %v, want one-time-write TPM error", err)
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexC), []byte{0xca, 0xfe}) {
		t.Fatal("failed second write changed the one-time value")
	}
}

func TestTimeoutIsUnknownAndRequiresReconciliationWithoutReplay(t *testing.T) {
	fake, authority := newTestAuthority(t)
	session := openTestSession(t, authority, make([]byte, sha256.Size))
	prep, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0xb0}, 0), AuthorizeOptions{Auth: testAuth()})
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.dropFinalResponse = true
	fake.mu.Unlock()

	_, err = prep.Submit(context.Background())
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("timeout: %v, want ErrOutcomeUnknown", err)
	}
	if session.State() != StateUnknown {
		t.Fatalf("timeout state=%s", session.State())
	}
	if !bytes.Equal(fake.writtenAt(testNVIndexA), []byte{0xb0}) {
		t.Fatal("fake TPM did not record the ambiguous write")
	}
	if _, err := session.Authorize(context.Background(), nvWriteTemplate(testNVIndexA, []byte{0xb1}, 0), AuthorizeOptions{Auth: testAuth()}); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("authorize while unknown: %v, want ErrSessionUnknown", err)
	}
	if err := session.Flush(context.Background()); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("flush while unknown: %v, want ErrSessionUnknown", err)
	}

	proof := RecoveryProof{
		StartupCount:  session.Binding().StartupCount,
		SessionHandle: testSessionHandle,
		Generation:    session.Binding().Generation,
		CPHash:        prep.CPHash(),
		Quote:         []byte("fresh-quote"),
		Audit:         []byte("fresh-audit"),
		Outcome:       RecoveryApplied,
	}
	if err := session.Reconcile(context.Background(), proof); err != nil {
		t.Fatal(err)
	}
	if session.State() != StateConsumed {
		t.Fatalf("state after reconcile=%s", session.State())
	}
	if fake.countCommands(CCNVWrite) != 1 {
		t.Fatalf("unknown outcome was replayed: NV_Write count=%d", fake.countCommands(CCNVWrite))
	}
}
