package policyauth_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.org/netsec/q0193/policyauth"
)

type fixture struct {
	authorityKey *ecdsa.PrivateKey
	otherKey     *ecdsa.PrivateKey
	ref          policyauth.PolicyRef
	newPCRPolicy policyauth.Digest
	oldPCRPolicy policyauth.Digest
	otherPolicy  policyauth.Digest
	counter      *policyauth.MemoryCounter
	verifier     *policyauth.Verifier
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	authorityKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate authority key: %v", err)
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}

	keyName, err := policyauth.PublicKeyName(&authorityKey.PublicKey)
	if err != nil {
		t.Fatalf("compute authorized key name: %v", err)
	}

	ref := policyauth.PolicyRef{DeviceID: "device-001", ObjectID: "disk-key"}
	newPCRPolicy := policyauth.Digest{0x10}
	oldPCRPolicy := policyauth.Digest{0x09} // vulnerable pre-upgrade image
	otherPolicy := policyauth.Digest{0xAA}

	policy := policyauth.ObjectPolicy{
		AuthorizedKeyName: keyName,
		ApprovedPolicy:    newPCRPolicy,
		PolicyRef:         ref,
	}
	counter := policyauth.NewMemoryCounter(0)
	verifier, err := policyauth.NewVerifier(policy, counter)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	return fixture{
		authorityKey: authorityKey,
		otherKey:     otherKey,
		ref:          ref,
		newPCRPolicy: newPCRPolicy,
		oldPCRPolicy: oldPCRPolicy,
		otherPolicy:  otherPolicy,
		counter:      counter,
		verifier:     verifier,
	}
}

func unsealOK(context.Context) error { return nil }

func mustCounter(t *testing.T, counter policyauth.CounterStore, want uint64) {
	t.Helper()
	got, err := counter.Current(context.Background())
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if got != want {
		t.Fatalf("counter = %d, want %d", got, want)
	}
}

func TestAuthorizeSuccessAdvancesCounter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	token, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	called := false
	unseal := func(context.Context) error {
		called = true
		return nil
	}
	if err := f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unseal); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !called {
		t.Fatal("unseal callback was not invoked")
	}
	mustCounter(t, f.counter, 1)

	// Replaying the same token is now a stale-generation rejection.
	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrStaleGeneration) {
		t.Fatalf("replay error = %v, want ErrStaleGeneration", err)
	}
	mustCounter(t, f.counter, 1)
}

func TestPolicyRefSwapRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Token validly signed by the authority, but for a different object.
	otherRef := policyauth.PolicyRef{DeviceID: "device-001", ObjectID: "network-key"}
	token, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, otherRef, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrPolicyRefMismatch) {
		t.Fatalf("error = %v, want ErrPolicyRefMismatch", err)
	}
	mustCounter(t, f.counter, 0)

	// A token for the right object on a different device is rejected too.
	otherDeviceRef := policyauth.PolicyRef{DeviceID: "device-002", ObjectID: "disk-key"}
	token, err = policyauth.SignToken(f.authorityKey, f.newPCRPolicy, otherDeviceRef, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrPolicyRefMismatch) {
		t.Fatalf("error = %v, want ErrPolicyRefMismatch", err)
	}
	mustCounter(t, f.counter, 0)
}

func TestStaleGenerationRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Simulate that generations 0..4 have already been consumed by the
	// current and previous authorized images.
	if _, err := f.counter.Advance(ctx, 4); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	mustCounter(t, f.counter, 5)

	// A captured token at a consumed generation (here signed over the
	// current PCR policy so only the monotonic check can reject it).
	// Replays of the vulnerable image's digest are covered separately.
	oldToken, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 3)
	if err != nil {
		t.Fatalf("sign old token: %v", err)
	}
	err = f.verifier.Authorize(ctx, oldToken, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrStaleGeneration) {
		t.Fatalf("old-generation error = %v, want ErrStaleGeneration", err)
	}
	mustCounter(t, f.counter, 5)

	// Even a current-policy token is rejected when its algebra is behind.
	token, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 4)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrStaleGeneration) {
		t.Fatalf("error = %v, want ErrStaleGeneration", err)
	}
	mustCounter(t, f.counter, 5)

	// A fresh generation for the current policy passes and moves the floor.
	fresh, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 5)
	if err != nil {
		t.Fatalf("sign fresh token: %v", err)
	}
	if err := f.verifier.Authorize(ctx, fresh, &f.authorityKey.PublicKey, unsealOK); err != nil {
		t.Fatalf("fresh authorization: %v", err)
	}
	mustCounter(t, f.counter, 6)
}

func TestWrongKeyRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// An attacker key signs an otherwise perfectly well-formed token.
	token, err := policyauth.SignToken(f.otherKey, f.newPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token with other key: %v", err)
	}

	err = f.verifier.Authorize(ctx, token, &f.otherKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrUnauthorizedKey) {
		t.Fatalf("error = %v, want ErrUnauthorizedKey", err)
	}
	mustCounter(t, f.counter, 0)

	// A token signed by the authority presented alongside another key is
	// stopped by name pinning before signature verification.
	goodToken, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	err = f.verifier.Authorize(ctx, goodToken, &f.otherKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrUnauthorizedKey) {
		t.Fatalf("error = %v, want ErrUnauthorizedKey (name pinning runs first)", err)
	}
	mustCounter(t, f.counter, 0)
}

func TestOldPCRPolicyRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// A still-fresh, correctly signed token over the pre-upgrade PCR digest.
	token, err := policyauth.SignToken(f.authorityKey, f.oldPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrPolicyMismatch) {
		t.Fatalf("error = %v, want ErrPolicyMismatch", err)
	}
	mustCounter(t, f.counter, 0)

	// A token over another object's approved policy is rejected too.
	token, err = policyauth.SignToken(f.authorityKey, f.otherPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK)
	if !errors.Is(err, policyauth.ErrPolicyMismatch) {
		t.Fatalf("error = %v, want ErrPolicyMismatch", err)
	}
	mustCounter(t, f.counter, 0)
}

func TestFailedUnsealDoesNotAdvanceCounter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	token, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	unsealErr := errors.New("TPM unseal: policy digest mismatch")
	calls := 0
	failingUnseal := func(context.Context) error {
		calls++
		return unsealErr
	}

	err = f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, failingUnseal)
	if !errors.Is(err, unsealErr) {
		t.Fatalf("error = %v, want wrapped unseal error", err)
	}
	if calls != 1 {
		t.Fatalf("unseal called %d times, want 1", calls)
	}
	mustCounter(t, f.counter, 0)

	// Verification alone must also leave the counter untouched.
	if err := f.verifier.Verify(ctx, token, &f.authorityKey.PublicKey); err != nil {
		t.Fatalf("verify: %v", err)
	}
	mustCounter(t, f.counter, 0)

	// The same generation can be retried and succeeds once unseal works.
	if err := f.verifier.Authorize(ctx, token, &f.authorityKey.PublicKey, unsealOK); err != nil {
		t.Fatalf("retry authorize: %v", err)
	}
	mustCounter(t, f.counter, 1)
}

func TestTamperedTokenRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	token, err := policyauth.SignToken(f.authorityKey, f.newPCRPolicy, f.ref, 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	// Relabeling a valid signature with a newer generation must not work:
	// the generation is part of the signed message.
	tampered := *token
	tampered.Generation = 100
	err = f.verifier.Verify(ctx, &tampered, &f.authorityKey.PublicKey)
	if !errors.Is(err, policyauth.ErrBadSignature) {
		t.Fatalf("generation tamper error = %v, want ErrBadSignature", err)
	}

	// Flipping a byte of the signature breaks verification.
	tampered = *token
	tampered.Signature[0] ^= 0xFF
	err = f.verifier.Verify(ctx, &tampered, &f.authorityKey.PublicKey)
	if !errors.Is(err, policyauth.ErrBadSignature) {
		t.Fatalf("signature tamper error = %v, want ErrBadSignature", err)
	}
	mustCounter(t, f.counter, 0)
}

func TestFileCounterPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nv-counter.json")

	counter, err := policyauth.NewFileCounter(path)
	if err != nil {
		t.Fatalf("new file counter: %v", err)
	}
	mustCounter(t, counter, 0)

	next, err := counter.Advance(ctx, 7)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if next != 8 {
		t.Fatalf("advance returned %d, want 8", next)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("counter file not persisted: %v", err)
	}

	// A fresh instance over the same NV-backed file sees the advanced floor.
	reopened, err := policyauth.NewFileCounter(path)
	if err != nil {
		t.Fatalf("reopen file counter: %v", err)
	}
	mustCounter(t, reopened, 8)

	// The persisted store enforces monotonicity on its own.
	_, err = reopened.Advance(ctx, 6)
	if !errors.Is(err, policyauth.ErrStaleGeneration) {
		t.Fatalf("error = %v, want ErrStaleGeneration", err)
	}
	mustCounter(t, reopened, 8)
}

func TestPolicyRefEncodingIsBoundAndDeterministic(t *testing.T) {
	ref := policyauth.PolicyRef{DeviceID: "device-001", ObjectID: "disk-key"}
	encoded, err := ref.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(encoded) != 2+len(ref.DeviceID)+2+len(ref.ObjectID) {
		t.Fatalf("encoded length = %d, want length-prefixed layout", len(encoded))
	}
	encodedAgain, _ := ref.MarshalBinary()
	if string(encoded) != string(encodedAgain) {
		t.Fatal("policyRef encoding is not deterministic")
	}
	if ref.Equal(policyauth.PolicyRef{DeviceID: "device-001", ObjectID: "network-key"}) {
		t.Fatal("refs for different objects compare equal")
	}
	if _, err := (policyauth.PolicyRef{DeviceID: "", ObjectID: "disk-key"}).MarshalBinary(); !errors.Is(err, policyauth.ErrEmptyDeviceID) {
		t.Fatalf("error = %v, want ErrEmptyDeviceID", err)
	}
}
