package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	netsec "example.org/netsec/q0837"
)

const (
	digestOne  = netsec.Digest("sha256:" + "1111111111111111111111111111111111111111111111111111111111111111")
	digestTwo  = netsec.Digest("sha256:" + "2222222222222222222222222222222222222222222222222222222222222222")
	digestAnon = netsec.Digest("sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	credA = netsec.CredentialHash("credential:tenant-a")
	credB = netsec.CredentialHash("credential:tenant-b")
	credC = netsec.CredentialHash("credential:tenant-c")
)

func allow(context.Context, netsec.SourceRequest) (bool, error) {
	return true, nil
}

func deny(context.Context, netsec.SourceRequest) (bool, error) {
	return false, nil
}

func failIfCalled(t *testing.T) netsec.VerifyFunc {
	t.Helper()
	return func(context.Context, netsec.SourceRequest) (bool, error) {
		t.Fatal("source verifier was called for an already-authorized cache hit")
		return false, nil
	}
}

type countingVerifier struct {
	calls  atomic.Int32
	result bool
	err    error
}

func (v *countingVerifier) verify(context.Context, netsec.SourceRequest) (bool, error) {
	v.calls.Add(1)
	return v.result, v.err
}

func bootstrap(t *testing.T, auth *netsec.Authorizer, digest netsec.Digest, cred netsec.CredentialHash) {
	t.Helper()
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digest,
		Credential: cred,
		Cached:     false,
		Policy:     netsec.IfNotPresent,
	}, allow)
	if err != nil {
		t.Fatalf("bootstrap %s/%s: unexpected error: %v", digest, cred, err)
	}
	if decision != netsec.SourceRequired {
		t.Fatalf("bootstrap %s/%s: decision = %s, want %s", digest, cred, decision, netsec.SourceRequired)
	}
	if !auth.IsAuthorized(digest, cred) {
		t.Fatalf("bootstrap %s/%s: credential was not recorded", digest, cred)
	}
}

func TestTenantBWithoutCredentialCannotUsePrivateCache(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil {
		t.Fatalf("tenant A cache hit: unexpected error: %v", err)
	}
	if decision != netsec.CacheHit {
		t.Fatalf("tenant A cache hit: decision = %s, want %s", decision, netsec.CacheHit)
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digestOne,
		Credential: "",
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if decision != netsec.Denied {
		t.Fatalf("tenant B without credentials: decision = %s, want %s", decision, netsec.Denied)
	}
	if !errors.Is(err, netsec.ErrMissingCredential) {
		t.Fatalf("tenant B without credentials: error = %v, want %v", err, netsec.ErrMissingCredential)
	}

	verify := &countingVerifier{result: false}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digestOne,
		Credential: credB,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil {
		t.Fatalf("tenant B with unknown credential: unexpected error: %v", err)
	}
	if decision != netsec.Denied {
		t.Fatalf("tenant B with unknown credential: decision = %s, want %s", decision, netsec.Denied)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("tenant B with unknown credential: verifier calls = %d, want 1", verify.calls.Load())
	}
	if auth.IsAuthorized(digestOne, credB) {
		t.Fatal("rejected tenant B credential was added to the cache authorization set")
	}
}

func TestCacheHitMissingHashValidatesThenUsesCache(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	verify := &countingVerifier{result: true}
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digestOne,
		Credential: credB,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil {
		t.Fatalf("tenant B cache hit: unexpected error: %v", err)
	}
	if decision != netsec.CacheHit {
		t.Fatalf("tenant B cache hit: decision = %s, want %s", decision, netsec.CacheHit)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("tenant B cache hit: verifier calls = %d, want 1", verify.calls.Load())
	}
	if !auth.IsAuthorized(digestOne, credB) {
		t.Fatal("validated tenant B credential was not recorded")
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Reference:  "registry.example/team/app:latest",
		Digest:     digestOne,
		Credential: credB,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil {
		t.Fatalf("tenant B second cache hit: unexpected error: %v", err)
	}
	if decision != netsec.CacheHit {
		t.Fatalf("tenant B second cache hit: decision = %s, want %s", decision, netsec.CacheHit)
	}

	sourceErr := errors.New("registry unavailable")
	verify = &countingVerifier{result: true, err: sourceErr}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credC,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if decision != netsec.Denied {
		t.Fatalf("source error: decision = %s, want %s", decision, netsec.Denied)
	}
	if !errors.Is(err, sourceErr) {
		t.Fatalf("source error: error = %v, want wrapped %v", err, sourceErr)
	}
	if auth.IsAuthorized(digestOne, credC) {
		t.Fatal("credential was recorded despite a source verification error")
	}
}

func TestRevocationRemovesHashFromAllRecords(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)
	bootstrap(t, auth, digestTwo, credA)

	verify := &countingVerifier{result: true}
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credB,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("record tenant B on digest one: decision = %s, error = %v", decision, err)
	}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestTwo,
		Credential: credC,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("record tenant C on digest two: decision = %s, error = %v", decision, err)
	}

	if removed := auth.Revoke(credA); removed != 2 {
		t.Fatalf("Revoke removed %d records, want 2", removed)
	}
	if auth.IsAuthorized(digestOne, credA) || auth.IsAuthorized(digestTwo, credA) {
		t.Fatal("revoked credential remains authorized for at least one digest")
	}
	if !auth.IsAuthorized(digestOne, credB) || !auth.IsAuthorized(digestTwo, credC) {
		t.Fatal("revocation removed an unrelated credential")
	}
	if removed := auth.Revoke(credA); removed != 0 {
		t.Fatalf("second Revoke removed %d records, want 0", removed)
	}
	if removed := auth.Revoke(""); removed != 0 {
		t.Fatalf("empty Revoke removed %d records, want 0", removed)
	}

	verify = &countingVerifier{result: false}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil {
		t.Fatalf("revoked credential cache hit: unexpected error: %v", err)
	}
	if decision != netsec.Denied {
		t.Fatalf("revoked credential cache hit: decision = %s, want %s", decision, netsec.Denied)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("revoked credential cache hit: verifier calls = %d, want 1", verify.calls.Load())
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credB,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("unrelated credential cache hit: decision = %s, error = %v", decision, err)
	}
}

func TestTagDriftDoesNotCarryOldDigestAuthorization(t *testing.T) {
	auth := netsec.New()
	reference := "registry.example/team/app:latest"

	bootstrap(t, auth, digestOne, credA)

	verify := &countingVerifier{result: false}
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Reference:  reference,
		Digest:     digestTwo,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil {
		t.Fatalf("drifted tag: unexpected error: %v", err)
	}
	if decision != netsec.Denied {
		t.Fatalf("drifted tag: decision = %s, want %s", decision, netsec.Denied)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("drifted tag: verifier calls = %d, want 1", verify.calls.Load())
	}
	if !auth.IsAuthorized(digestOne, credA) {
		t.Fatal("authorization for the old digest was lost")
	}
	if auth.IsAuthorized(digestTwo, credA) {
		t.Fatal("authorization for the old digest was applied to the new digest")
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Reference:  reference,
		Digest:     digestTwo,
		Credential: "",
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if decision != netsec.Denied || !errors.Is(err, netsec.ErrMissingCredential) {
		t.Fatalf("drifted tag without credentials: decision = %s, error = %v", decision, err)
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Reference:  reference,
		Digest:     netsec.Digest(reference),
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if decision != netsec.Denied || !errors.Is(err, netsec.ErrInvalidDigest) {
		t.Fatalf("tag used as digest: decision = %s, error = %v", decision, err)
	}
}

func TestAnonymousImageNeedsNoCredential(t *testing.T) {
	auth := netsec.New()

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:    digestAnon,
		Anonymous: true,
		Cached:    false,
		Policy:    netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil {
		t.Fatalf("anonymous cache miss: unexpected error: %v", err)
	}
	if decision != netsec.SourceRequired {
		t.Fatalf("anonymous cache miss: decision = %s, want %s", decision, netsec.SourceRequired)
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:    digestAnon,
		Anonymous: true,
		Cached:    true,
		Policy:    netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("anonymous cache hit: decision = %s, error = %v", decision, err)
	}

	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:    digestAnon,
		Anonymous: true,
		Cached:    true,
		Policy:    netsec.Always,
	}, failIfCalled(t))
	if err != nil || decision != netsec.SourceRequired {
		t.Fatalf("anonymous Always: decision = %s, error = %v", decision, err)
	}

	if auth.IsAuthorized(digestAnon, credA) {
		t.Fatal("anonymous pull recorded a credential hash")
	}
}

func TestAlwaysBypassesCacheAndRevalidates(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	verify := &countingVerifier{result: true}
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.Always,
	}, verify.verify)
	if err != nil || decision != netsec.SourceRequired {
		t.Fatalf("Always authorized: decision = %s, error = %v", decision, err)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("Always authorized: verifier calls = %d, want 1", verify.calls.Load())
	}

	verify = &countingVerifier{result: false}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.Always,
	}, verify.verify)
	if err != nil || decision != netsec.Denied {
		t.Fatalf("Always denied: decision = %s, error = %v", decision, err)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("Always denied: verifier calls = %d, want 1", verify.calls.Load())
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("source denial left the old cache authorization in place")
	}

	verify = &countingVerifier{result: false}
	decision, err = auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, verify.verify)
	if err != nil || decision != netsec.Denied {
		t.Fatalf("IfNotPresent after denial: decision = %s, error = %v", decision, err)
	}
	if verify.calls.Load() != 1 {
		t.Fatalf("IfNotPresent after denial: verifier calls = %d, want 1", verify.calls.Load())
	}
}

func TestInvalidRequests(t *testing.T) {
	auth := netsec.New()

	tests := []struct {
		name string
		req  netsec.Request
		err  error
	}{
		{
			name: "empty digest",
			req:  netsec.Request{Credential: credA, Cached: true},
			err:  netsec.ErrInvalidDigest,
		},
		{
			name: "tag instead of digest",
			req:  netsec.Request{Digest: "registry.example/team/app:latest", Credential: credA, Cached: true},
			err:  netsec.ErrInvalidDigest,
		},
		{
			name: "digest without encoded value",
			req:  netsec.Request{Digest: "sha256:", Credential: credA, Cached: true},
			err:  netsec.ErrInvalidDigest,
		},
		{
			name: "private pull without credential",
			req:  netsec.Request{Digest: digestOne, Cached: true},
			err:  netsec.ErrMissingCredential,
		},
		{
			name: "unknown policy",
			req:  netsec.Request{Digest: digestOne, Credential: credA, Cached: true, Policy: netsec.PullPolicy(99)},
			err:  netsec.ErrInvalidPolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := auth.Authorize(context.Background(), tt.req, failIfCalled(t))
			if decision != netsec.Denied {
				t.Fatalf("decision = %s, want %s", decision, netsec.Denied)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
		})
	}

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
	}, nil)
	if decision != netsec.Denied || !errors.Is(err, netsec.ErrNoVerifier) {
		t.Fatalf("nil verifier for private cache hit: decision = %s, error = %v", decision, err)
	}

	decision, err = auth.Authorize(nil, netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     false,
	}, allow)
	if err != nil || decision != netsec.SourceRequired {
		t.Fatalf("nil context: decision = %s, error = %v", decision, err)
	}
}

func TestHashCredential(t *testing.T) {
	first := netsec.HashCredential("same secret")
	second := netsec.HashCredential("same secret")
	other := netsec.HashCredential("other secret")

	if first != second {
		t.Fatal("HashCredential is not deterministic")
	}
	if first == other {
		t.Fatal("HashCredential ignored a different secret")
	}
	if !strings.HasPrefix(string(first), "sha256:") {
		t.Fatalf("HashCredential = %q, want sha256: prefix", first)
	}
	if strings.Contains(string(first), "same secret") {
		t.Fatal("HashCredential exposed the original secret")
	}
}

func TestZeroValueAuthorizerIsUsable(t *testing.T) {
	var auth netsec.Authorizer

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     false,
		Policy:     netsec.IfNotPresent,
	}, allow)
	if err != nil || decision != netsec.SourceRequired {
		t.Fatalf("zero value Authorize: decision = %s, error = %v", decision, err)
	}
	if !auth.IsAuthorized(digestOne, credA) {
		t.Fatal("zero value Authorizer did not record authorization")
	}
	if removed := auth.Revoke(credA); removed != 1 {
		t.Fatalf("zero value Revoke removed %d records, want 1", removed)
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("zero value Revoke did not remove authorization")
	}
}

func TestForgetRemovesRecordAndInvalidatesInFlightValidation(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	if !auth.Forget(digestOne) {
		t.Fatal("Forget reported no record for an authorized digest")
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("Forget left the credential authorized")
	}
	if auth.Forget(digestOne) {
		t.Fatal("second Forget reported a record")
	}
	if auth.Forget("") {
		t.Fatal("Forget with an empty digest reported a record")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	verify := func(context.Context, netsec.SourceRequest) (bool, error) {
		close(started)
		<-release
		return true, nil
	}
	result := make(chan pullResult, 1)
	go func() {
		decision, err := auth.Authorize(context.Background(), netsec.Request{
			Digest:     digestOne,
			Credential: credA,
			Cached:     true,
			Policy:     netsec.IfNotPresent,
		}, verify)
		result <- pullResult{decision: decision, err: err}
	}()
	waitSignal(t, started)

	auth.Forget(digestOne)
	close(release)

	got := waitResult(t, result)
	if got.err != nil || got.decision != netsec.Denied {
		t.Fatalf("validation raced with Forget: decision = %s, error = %v", got.decision, got.err)
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("in-flight validation recreated a forgotten authorization record")
	}
}

func TestRevalidationAfterRevocationCanEstablishFreshAuthorization(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)
	auth.Revoke(credA)

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, allow)
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("fresh validation after revocation: decision = %s, error = %v", decision, err)
	}
	if !auth.IsAuthorized(digestOne, credA) {
		t.Fatal("successful fresh validation was not recorded")
	}
}

type pullResult struct {
	decision netsec.Decision
	err      error
}

type blockedVerifier struct {
	started chan<- struct{}
	release <-chan struct{}
	result  bool
}

func (v blockedVerifier) verify(ctx context.Context, _ netsec.SourceRequest) (bool, error) {
	v.started <- struct{}{}
	select {
	case <-v.release:
		return v.result, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent operation to start")
	}
}

func waitResult(t *testing.T, ch <-chan pullResult) pullResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent operation to finish")
		return pullResult{}
	}
}

func TestRevocationInterleavingDuringValidation(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	started := make(chan struct{})
	release := make(chan struct{})
	verify := func(context.Context, netsec.SourceRequest) (bool, error) {
		close(started)
		<-release
		return true, nil
	}
	result := make(chan pullResult, 1)
	go func() {
		decision, err := auth.Authorize(context.Background(), netsec.Request{
			Digest:     digestOne,
			Credential: credA,
			Cached:     true,
			Policy:     netsec.Always,
		}, verify)
		result <- pullResult{decision: decision, err: err}
	}()
	waitSignal(t, started)

	if removed := auth.Revoke(credA); removed != 1 {
		t.Fatalf("Revoke during validation removed %d records, want 1", removed)
	}
	close(release)

	got := waitResult(t, result)
	if got.err != nil || got.decision != netsec.Denied {
		t.Fatalf("revoke during validation: decision = %s, error = %v", got.decision, got.err)
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("in-flight validation restored a revoked credential")
	}
}

func TestRevocationInterleavingFromInsideVerifier(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	verify := func(context.Context, netsec.SourceRequest) (bool, error) {
		auth.Revoke(credA)
		return true, nil
	}
	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.Always,
	}, verify)
	if err != nil || decision != netsec.Denied {
		t.Fatalf("revoke before verification return: decision = %s, error = %v", decision, err)
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("verification result was committed after revocation")
	}
}

func TestRevocationInterleavingAfterCacheDecision(t *testing.T) {
	auth := netsec.New()
	bootstrap(t, auth, digestOne, credA)

	decision, err := auth.Authorize(context.Background(), netsec.Request{
		Digest:     digestOne,
		Credential: credA,
		Cached:     true,
		Policy:     netsec.IfNotPresent,
	}, failIfCalled(t))
	if err != nil || decision != netsec.CacheHit {
		t.Fatalf("cache hit before revocation: decision = %s, error = %v", decision, err)
	}

	auth.Revoke(credA)
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("revocation after a cache decision did not remove the record")
	}
}

func TestConcurrentPullsAndSelectiveRevocation(t *testing.T) {
	tests := []struct {
		name   string
		revoke []netsec.CredentialHash
		wantA  netsec.Decision
		wantB  netsec.Decision
	}{
		{name: "revoke neither", wantA: netsec.SourceRequired, wantB: netsec.SourceRequired},
		{name: "revoke A", revoke: []netsec.CredentialHash{credA}, wantA: netsec.Denied, wantB: netsec.SourceRequired},
		{name: "revoke B", revoke: []netsec.CredentialHash{credB}, wantA: netsec.SourceRequired, wantB: netsec.Denied},
		{
			name:   "revoke both",
			revoke: []netsec.CredentialHash{credA, credB},
			wantA:  netsec.Denied,
			wantB:  netsec.Denied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := netsec.New()
			bootstrap(t, auth, digestOne, credA)
			bootstrap(t, auth, digestOne, credB)

			started := make(chan struct{}, 2)
			release := make(chan struct{})
			verifyA := blockedVerifier{started: started, release: release, result: true}
			verifyB := blockedVerifier{started: started, release: release, result: true}

			resultA := make(chan pullResult, 1)
			resultB := make(chan pullResult, 1)
			go func() {
				decision, err := auth.Authorize(context.Background(), netsec.Request{
					Digest:     digestOne,
					Credential: credA,
					Cached:     true,
					Policy:     netsec.Always,
				}, verifyA.verify)
				resultA <- pullResult{decision: decision, err: err}
			}()
			go func() {
				decision, err := auth.Authorize(context.Background(), netsec.Request{
					Digest:     digestOne,
					Credential: credB,
					Cached:     true,
					Policy:     netsec.Always,
				}, verifyB.verify)
				resultB <- pullResult{decision: decision, err: err}
			}()

			waitSignal(t, started)
			waitSignal(t, started)
			for _, cred := range tt.revoke {
				auth.Revoke(cred)
			}
			close(release)

			gotA := waitResult(t, resultA)
			gotB := waitResult(t, resultB)
			if gotA.err != nil || gotA.decision != tt.wantA {
				t.Fatalf("credential A: decision = %s, error = %v, want %s", gotA.decision, gotA.err, tt.wantA)
			}
			if gotB.err != nil || gotB.decision != tt.wantB {
				t.Fatalf("credential B: decision = %s, error = %v, want %s", gotB.decision, gotB.err, tt.wantB)
			}

			wantAAuthorized := tt.wantA != netsec.Denied
			wantBAuthorized := tt.wantB != netsec.Denied
			if auth.IsAuthorized(digestOne, credA) != wantAAuthorized {
				t.Fatalf("credential A authorization = %v, want %v", auth.IsAuthorized(digestOne, credA), wantAAuthorized)
			}
			if auth.IsAuthorized(digestOne, credB) != wantBAuthorized {
				t.Fatalf("credential B authorization = %v, want %v", auth.IsAuthorized(digestOne, credB), wantBAuthorized)
			}
		})
	}
}

func TestConcurrentPullsOfOneCredentialAreInvalidatedByRevoke(t *testing.T) {
	const pullers = 16

	auth := netsec.New()
	started := make(chan struct{}, pullers)
	release := make(chan struct{})
	verify := blockedVerifier{started: started, release: release, result: true}

	results := make([]chan pullResult, pullers)
	var wg sync.WaitGroup
	wg.Add(pullers)
	for i := 0; i < pullers; i++ {
		results[i] = make(chan pullResult, 1)
		go func(result chan<- pullResult) {
			defer wg.Done()
			decision, err := auth.Authorize(context.Background(), netsec.Request{
				Digest:     digestOne,
				Credential: credA,
				Cached:     true,
				Policy:     netsec.Always,
			}, verify.verify)
			result <- pullResult{decision: decision, err: err}
		}(results[i])
	}

	for i := 0; i < pullers; i++ {
		waitSignal(t, started)
	}
	if removed := auth.Revoke(credA); removed != 0 {
		t.Fatalf("Revoke of never-recorded credential removed %d records, want 0", removed)
	}
	close(release)
	wg.Wait()

	for i, result := range results {
		got := waitResult(t, result)
		if got.err != nil || got.decision != netsec.Denied {
			t.Fatalf("puller %d: decision = %s, error = %v", i, got.decision, got.err)
		}
	}
	if auth.IsAuthorized(digestOne, credA) {
		t.Fatal("concurrent validations restored a credential revoked while they were in flight")
	}
}

func TestConcurrentFastPathAndRevocation(t *testing.T) {
	const iterations = 200

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration-%d", i), func(t *testing.T) {
			auth := netsec.New()
			bootstrap(t, auth, digestOne, credA)

			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)

			denyCalls := &countingVerifier{result: false}
			go func() {
				defer wg.Done()
				<-start
				// If the fast path wins, the verifier is not called. If revocation
				// wins, the denial ensures this pull cannot restore the hash.
				_, _ = auth.Authorize(context.Background(), netsec.Request{
					Digest:     digestOne,
					Credential: credA,
					Cached:     true,
					Policy:     netsec.IfNotPresent,
				}, denyCalls.verify)
			}()
			go func() {
				defer wg.Done()
				<-start
				auth.Revoke(credA)
			}()

			close(start)
			wg.Wait()

			if auth.IsAuthorized(digestOne, credA) {
				t.Fatal("concurrent cache authorization survived revocation")
			}
		})
	}
}
