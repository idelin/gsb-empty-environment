package eaptls

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeIdentityProvider struct {
	identity   string
	err        error
	callCount  int
	identities []string
}

func (f *fakeIdentityProvider) Identity(context.Context) (string, error) {
	f.callCount++
	if f.err != nil {
		return "", f.err
	}
	if len(f.identities) > 0 {
		next := f.identities[0]
		f.identities = f.identities[1:]
		return next, nil
	}
	return f.identity, nil
}

type fakeAuthorizer struct {
	revoked    map[string]bool
	err        error
	callCount  int
	authorized []string
}

func (f *fakeAuthorizer) Authorize(_ context.Context, identity string) error {
	f.callCount++
	f.authorized = append(f.authorized, identity)
	if f.err != nil {
		return f.err
	}
	if f.revoked[identity] {
		return fmt.Errorf("identity %q revoked", identity)
	}
	return nil
}

func newTestState(t *testing.T) (*State, *fakeIdentityProvider, *fakeAuthorizer) {
	t.Helper()
	ids := &fakeIdentityProvider{identity: "alice@example.com"}
	authz := &fakeAuthorizer{revoked: map[string]bool{}}
	s, err := New(Config{Identities: ids, Authz: authz})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, ids, authz
}

func eapTLSRequest(identifier byte, flags byte, data []byte) []byte {
	if len(data) > 0 {
		flags |= flagLengthIncluded
	}
	payload := []byte{CodeRequest, identifier, 0x00, 0x00, TypeEAPTLS, flags}
	if len(data) > 0 {
		payload = append(payload,
			byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data)))
	}
	payload = append(payload, data...)
	payload[2] = byte(len(payload) >> 8)
	payload[3] = byte(len(payload))
	return payload
}

func eapCodePacket(code, identifier byte) []byte {
	return []byte{code, identifier, 0x00, 0x04}
}

func finishedRecord() []byte {
	verifyData := make([]byte, 32) // TLS 1.3 Finished verify_data
	handshake := []byte{
		handshakeFinished,
		byte(len(verifyData) >> 16), byte(len(verifyData) >> 8), byte(len(verifyData)),
	}
	handshake = append(handshake, verifyData...)
	record := []byte{0x16, 0x03, 0x03, 0x00, byte(len(handshake))}
	return append(record, handshake...)
}

func beginAndCommit(t *testing.T, s *State) []byte {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := s.HandleRequest(eapTLSRequest(1, 0x00, finishedRecord())); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	frame, err := s.Commitment()
	if err != nil {
		t.Fatalf("Commitment: %v", err)
	}
	return frame
}

// The full handshake path accepts Success only after the commitment.
func TestSuccessAcceptedAfterCommitment(t *testing.T) {
	s, _, _ := newTestState(t)
	frame := beginAndCommit(t, s)

	want := []byte{CodeResponse, 1, 0x00, 0x06, TypeEAPTLS, 0x00}
	if len(frame) != len(want) {
		t.Fatalf("commitment length = %d, want %d", len(frame), len(want))
	}
	for i := range want {
		if frame[i] != want[i] {
			t.Fatalf("commitment frame = % x, want % x", frame, want)
		}
	}
	if s.Phase() != PhaseCommitted {
		t.Fatalf("phase = %s, want committed", s.Phase())
	}

	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); err != nil {
		t.Fatalf("HandleSuccess: %v", err)
	}
	if s.Phase() != PhaseSucceeded {
		t.Fatalf("phase = %s, want succeeded", s.Phase())
	}
}

// An attacker-injected Success before the TLS handshake completes must fail.
func TestEarlySuccessRejected(t *testing.T) {
	s, ids, authz := newTestState(t)
	ctx := context.Background()

	// 1. Before Begin: nothing has happened at all.
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 7)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("pre-begin success err = %v, want ErrEarlySuccess", err)
	}

	// 2. Handshake in progress, final message not yet received.
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	appData := []byte{0x17, 0x03, 0x03, 0x00, 0x00}
	if _, err := s.HandleRequest(eapTLSRequest(1, 0x00, appData)); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("mid-handshake success err = %v, want ErrEarlySuccess", err)
	}
	if s.Phase() != PhaseRunning {
		t.Fatalf("phase = %s, want running after rejected success", s.Phase())
	}

	// 3. Final handshake message received, but commitment not yet sent.
	if err := s.MarkFinalHandshake(); err != nil {
		t.Fatalf("MarkFinalHandshake: %v", err)
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("pre-commitment success err = %v, want ErrEarlySuccess", err)
	}
	if s.Phase() != PhaseRunning {
		t.Fatalf("phase = %s, want running", s.Phase())
	}

	// Rejected Success must not end the authentication; the handshake still
	// completes normally afterwards.
	if _, err := s.Commitment(); err != nil {
		t.Fatalf("Commitment: %v", err)
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); err != nil {
		t.Fatalf("HandleSuccess after commitment: %v", err)
	}
	if s.Phase() != PhaseSucceeded {
		t.Fatalf("phase = %s, want succeeded", s.Phase())
	}
	if ids.callCount != 1 || authz.callCount != 1 {
		t.Fatalf("identity calls = %d, authz calls = %d, want 1 each", ids.callCount, authz.callCount)
	}
}

// The commitment message must not be emitted without the final handshake, and
// Success must be rejected while the commitment is missing.
func TestMissingCommitmentRejected(t *testing.T) {
	s, _, _ := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	appData := []byte{0x17, 0x03, 0x03, 0x00, 0x00}
	if _, err := s.HandleRequest(eapTLSRequest(2, 0x00, appData)); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}

	if _, err := s.Commitment(); !errors.Is(err, ErrFinalHandshakeNotReceived) {
		t.Fatalf("Commitment err = %v, want ErrFinalHandshakeNotReceived", err)
	}
	if s.Phase() != PhaseRunning {
		t.Fatalf("phase = %s, want running", s.Phase())
	}

	// No commitment frame exists, so Success stays invalid.
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 2)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("HandleSuccess err = %v, want ErrEarlySuccess", err)
	}

	// Only one commitment is permitted.
	if err := s.MarkFinalHandshake(); err != nil {
		t.Fatalf("MarkFinalHandshake: %v", err)
	}
	if _, err := s.Commitment(); err != nil {
		t.Fatalf("first Commitment: %v", err)
	}
	if _, err := s.Commitment(); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("second Commitment err = %v, want ErrStateTerminal", err)
	}
}

// A resumed handshake must re-fetch the original identity and re-authorize;
// a revoked identity blocks resumption and leaves the machine uncommitted.
func TestResumeAfterRevocationRejected(t *testing.T) {
	s, ids, authz := newTestState(t)
	ctx := context.Background()

	identity, err := s.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if identity != "alice@example.com" || s.Identity() != identity {
		t.Fatalf("identity = %q", identity)
	}
	if _, err := s.HandleRequest(eapTLSRequest(3, 0x00, finishedRecord())); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}

	// Revocation takes effect after the initial authentication started.
	authz.revoked[identity] = true

	resumed, err := s.Resume(ctx)
	if err == nil {
		t.Fatal("Resume with revoked identity succeeded, want error")
	}
	if ids.callCount != 2 {
		t.Fatalf("identity calls = %d, want 2 (re-fetched on resume)", ids.callCount)
	}
	if authz.callCount != 2 {
		t.Fatalf("authorization calls = %d, want 2 (re-queried on resume)", authz.callCount)
	}
	if resumed != "" {
		t.Fatalf("resumed identity = %q, want empty", resumed)
	}

	// A failed resume must reset handshake progress, so a Success sent during
	// the aborted resume is still rejected.
	if s.FinalHandshakeReceived() {
		t.Fatal("final handshake flag should be cleared after failed resume")
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 3)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("HandleSuccess after failed resume err = %v, want ErrEarlySuccess", err)
	}
}

// Once authorization passes again, resume starts a fresh handshake that can
// complete end to end with its own identifier and commitment.
func TestResumeSucceedsAfterReauthorization(t *testing.T) {
	s, ids, authz := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := s.HandleRequest(eapTLSRequest(4, 0x00, finishedRecord())); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	if _, err := s.Commitment(); err != nil {
		t.Fatalf("Commitment: %v", err)
	}

	identity, err := s.Resume(ctx)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if identity != "alice@example.com" {
		t.Fatalf("resumed identity = %q", identity)
	}
	if ids.callCount != 2 || authz.callCount != 2 {
		t.Fatalf("identity calls = %d, authz calls = %d, want 2 each", ids.callCount, authz.callCount)
	}
	if s.FinalHandshakeReceived() {
		t.Fatal("resumed handshake must start without a final handshake marker")
	}

	// Fresh handshake flight, fresh identifier, fresh commitment.
	if _, err := s.HandleRequest(eapTLSRequest(5, 0x00, finishedRecord())); err != nil {
		t.Fatalf("HandleRequest: %v", err)
	}
	frame, err := s.Commitment()
	if err != nil {
		t.Fatalf("Commitment: %v", err)
	}
	if frame[1] != 5 {
		t.Fatalf("commitment identifier = %d, want 5", frame[1])
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 5)); err != nil {
		t.Fatalf("HandleSuccess: %v", err)
	}
	if s.Phase() != PhaseSucceeded {
		t.Fatalf("phase = %s, want succeeded", s.Phase())
	}
}

// A resumed handshake whose identity differs from the original is rejected.
func TestResumeIdentityMismatch(t *testing.T) {
	s, ids, _ := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ids.identities = []string{"mallory@example.com"}
	if _, err := s.Resume(ctx); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Resume err = %v, want ErrIdentityMismatch", err)
	}
	if s.Identity() != "alice@example.com" {
		t.Fatalf("original identity changed to %q", s.Identity())
	}
}

// Fragmented EAP-TLS requests are reassembled before Finished detection and
// commitment; a Success injected mid-fragmentation stays invalid.
func TestFragmentedHandshakeAndEarlySuccess(t *testing.T) {
	s, _, _ := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	record := finishedRecord()
	mid := len(record) / 2
	if _, err := s.HandleRequest(eapTLSRequest(6, flagMoreFragments, record[:mid])); !errors.Is(err, ErrMoreFragments) {
		t.Fatalf("first fragment err = %v, want ErrMoreFragments", err)
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 6)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("success during fragmentation err = %v, want ErrEarlySuccess", err)
	}

	data, err := s.HandleRequest(eapTLSRequest(6, 0x00, record[mid:]))
	if err != nil {
		t.Fatalf("final fragment: %v", err)
	}
	if len(data) != len(record) {
		t.Fatalf("reassembled length = %d, want %d", len(data), len(record))
	}
	if !s.FinalHandshakeReceived() {
		t.Fatal("plaintext Finished in reassembled stream not detected")
	}

	frame, err := s.Commitment()
	if err != nil {
		t.Fatalf("Commitment: %v", err)
	}
	if frame[1] != 6 {
		t.Fatalf("commitment identifier = %d, want 6", frame[1])
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 6)); err != nil {
		t.Fatalf("HandleSuccess: %v", err)
	}
}

// Identifier mismatches and terminal transitions behave defensively.
func TestIdentifierMismatchAndFailure(t *testing.T) {
	s, _, _ := newTestState(t)
	beginAndCommit(t, s)

	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 9)); !errors.Is(err, ErrIdentifierMismatch) {
		t.Fatalf("identifier mismatch err = %v, want ErrIdentifierMismatch", err)
	}

	if err := s.HandleFailure(eapCodePacket(CodeFailure, 1)); err != nil {
		t.Fatalf("HandleFailure: %v", err)
	}
	if s.Phase() != PhaseFailed {
		t.Fatalf("phase = %s, want failed", s.Phase())
	}
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); !errors.Is(err, ErrEarlySuccess) {
		t.Fatalf("success after failure err = %v, want ErrEarlySuccess", err)
	}
}

func TestMalformedAndWrongCodePackets(t *testing.T) {
	s, _, _ := newTestState(t)
	if _, err := s.HandleRequest([]byte{CodeRequest, 1}); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("short packet err = %v, want ErrMalformedPacket", err)
	}
	if err := s.HandleSuccess([]byte{CodeFailure, 1, 0x00, 0x04}); !errors.Is(err, ErrNotSuccess) {
		t.Fatalf("HandleSuccess wrong code err = %v, want ErrNotSuccess", err)
	}
	if err := s.HandleFailure([]byte{CodeSuccess, 1, 0x00, 0x04}); !errors.Is(err, ErrNotFailure) {
		t.Fatalf("HandleFailure wrong code err = %v, want ErrNotFailure", err)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("New with nil dependencies succeeded, want error")
	}
}

func TestGuardedTransitions(t *testing.T) {
	s, _, _ := newTestState(t)
	ctx := context.Background()

	// Callbacks before Begin.
	if _, err := s.Commitment(); !errors.Is(err, ErrNotBegun) {
		t.Fatalf("Commitment before Begin err = %v, want ErrNotBegun", err)
	}
	if _, err := s.Resume(ctx); !errors.Is(err, ErrNotBegun) {
		t.Fatalf("Resume before Begin err = %v, want ErrNotBegun", err)
	}
	if err := s.MarkFinalHandshake(); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("MarkFinalHandshake before Begin err = %v, want ErrStateTerminal", err)
	}

	beginAndCommit(t, s)
	if err := s.HandleSuccess(eapCodePacket(CodeSuccess, 1)); err != nil {
		t.Fatalf("HandleSuccess: %v", err)
	}

	// Callbacks after Success.
	if _, err := s.HandleRequest(eapTLSRequest(2, 0x00, []byte{0x17})); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("HandleRequest after Success err = %v, want ErrStateTerminal", err)
	}
	if err := s.MarkFinalHandshake(); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("MarkFinalHandshake after Success err = %v, want ErrStateTerminal", err)
	}
	if _, err := s.Commitment(); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("Commitment after Success err = %v, want ErrStateTerminal", err)
	}
	if _, err := s.Resume(ctx); !errors.Is(err, ErrStateTerminal) {
		t.Fatalf("Resume after Success err = %v, want ErrStateTerminal", err)
	}
}

func TestRequestValidation(t *testing.T) {
	s, _, _ := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Non-request packet handed to HandleRequest.
	response := []byte{CodeResponse, 1, 0x00, 0x06, TypeEAPTLS, 0x00}
	if _, err := s.HandleRequest(response); !errors.Is(err, ErrNotRequest) {
		t.Fatalf("response to HandleRequest err = %v, want ErrNotRequest", err)
	}
	// Start flag on a server request is illegal.
	if _, err := s.HandleRequest(eapTLSRequest(1, flagStart, nil)); !errors.Is(err, ErrServerStart) {
		t.Fatalf("Start flag err = %v, want ErrServerStart", err)
	}
	// Unsupported EAP type.
	other := []byte{CodeRequest, 1, 0x00, 0x06, TypeEAPTLS + 1, 0x00}
	if _, err := s.HandleRequest(other); !errors.Is(err, ErrUnsupportedEAPType) {
		t.Fatalf("unsupported type err = %v, want ErrUnsupportedEAPType", err)
	}
	// Explicit final-handshake callback then commitment without any request
	// identifier must fail loudly rather than emitting a bad frame.
	if err := s.MarkFinalHandshake(); err != nil {
		t.Fatalf("MarkFinalHandshake: %v", err)
	}
	if _, err := s.Commitment(); !errors.Is(err, ErrNoRequestIdentifier) {
		t.Fatalf("Commitment without identifier err = %v, want ErrNoRequestIdentifier", err)
	}
}

func TestObserveTLSData(t *testing.T) {
	s, _, _ := newTestState(t)
	ctx := context.Background()
	if _, err := s.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := s.ObserveTLSData(finishedRecord()); err != nil {
		t.Fatalf("ObserveTLSData: %v", err)
	}
	if !s.FinalHandshakeReceived() {
		t.Fatal("ObserveTLSData did not mark the final handshake")
	}
	if err := s.ObserveTLSData([]byte{0x17, 0x03, 0x03}); err != nil {
		t.Fatalf("ObserveTLSData short record: %v", err)
	}
}
