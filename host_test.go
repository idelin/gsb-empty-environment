package credentialhost_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	credentialhost "prescreen.local/ps-88814b7a1d"
)

const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
	roleA   = "arn:aws:iam::111111111111:role/tenant-a"
	roleB   = "arn:aws:iam::222222222222:role/tenant-b"
	resA    = "arn:aws:s3:::tenant-a-bucket"
	resB    = "arn:aws:s3:::tenant-b-bucket"
)

var testNow = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

type fakeSTS struct {
	mu    sync.Mutex
	calls int
	hook  func(context.Context, credentialhost.STSRequest) (*credentialhost.Credentials, error)
}

func (s *fakeSTS) AssumeRole(ctx context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
	s.mu.Lock()
	s.calls++
	hook := s.hook
	s.mu.Unlock()
	return hook(ctx, request)
}

func (s *fakeSTS) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func validCredentials(request credentialhost.STSRequest, destroyed *atomic.Int32) *credentialhost.Credentials {
	return &credentialhost.Credentials{
		Audience:       request.Decision.Audience,
		RoleID:         request.Decision.RoleID,
		SourceIdentity: request.Decision.SourceIdentity,
		ExpiresAt:      request.Decision.NotAfter.Add(-time.Second),
		SessionID:      request.Decision.SessionID,
		DestroyFunc: func() {
			destroyed.Add(1)
		},
	}
}

func setupTenants(t *testing.T) (*credentialhost.Registry, credentialhost.TenantRecord, credentialhost.TenantRecord) {
	t.Helper()
	registry := credentialhost.NewRegistry()
	recordA, err := registry.RegisterTenant(credentialhost.TenantConfig{
		TenantID:          tenantA,
		RoleID:            roleA,
		SourceResources:   []string{resA},
		MaxSessionSeconds: 3600,
		PolicyDigest:      "sha256:policy-a",
	})
	if err != nil {
		t.Fatalf("register tenant A: %v", err)
	}
	recordB, err := registry.RegisterTenant(credentialhost.TenantConfig{
		TenantID:          tenantB,
		RoleID:            roleB,
		SourceResources:   []string{resB},
		MaxSessionSeconds: 1800,
		PolicyDigest:      "sha256:policy-b",
	})
	if err != nil {
		t.Fatalf("register tenant B: %v", err)
	}
	return registry, recordA, recordB
}

func platformContext(extraTenants ...string) context.Context {
	tenants := []string{tenantA, tenantB}
	tenants = append(tenants, extraTenants...)
	return credentialhost.WithPrincipal(context.Background(), credentialhost.Principal{
		Subject:   "platform-service-principal",
		TenantIDs: tenants,
	})
}

func requestFor(requestID string, record credentialhost.TenantRecord, resource, sessionName string) credentialhost.AssumeRequest {
	return credentialhost.AssumeRequest{
		RequestID:        requestID,
		TenantID:         record.TenantID,
		RoleID:           record.RoleID,
		ExternalID:       record.ExternalID,
		SourceResource:   resource,
		SessionName:      sessionName,
		DurationSeconds:  60,
		ParametersDigest: "sha256:invocation-parameters",
	}
}

func newHost(t *testing.T, registry *credentialhost.Registry, sts credentialhost.STS) *credentialhost.Host {
	t.Helper()
	host, err := credentialhost.NewHost(registry, sts, credentialhost.WithClock(func() time.Time {
		return testNow
	}))
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	t.Cleanup(host.Close)
	return host
}

func TestTenantIsolationAndDecisionBinding(t *testing.T) {
	registry, recordA, recordB := setupTenants(t)
	if len(recordA.ExternalID) != 32 || len(recordB.ExternalID) != 32 {
		t.Fatalf("external IDs must be 128-bit hex, got %q and %q", recordA.ExternalID, recordB.ExternalID)
	}
	if _, err := hex.DecodeString(recordA.ExternalID); err != nil {
		t.Fatalf("external ID is not hex: %v", err)
	}
	if recordA.ExternalID == recordB.ExternalID {
		t.Fatal("tenant external IDs must be independently random")
	}

	var destroyed atomic.Int32
	var decisionsMu sync.Mutex
	decisions := make(map[string]credentialhost.Decision)
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		decisionsMu.Lock()
		decisions[request.RequestID] = request.Decision
		decisionsMu.Unlock()
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	ctx := platformContext()

	credentialsA, err := host.Assume(ctx, requestFor("a-ok", recordA, resA, "audit-a"))
	if err != nil {
		t.Fatalf("assume tenant A: %v", err)
	}
	credentialsB, err := host.Assume(ctx, requestFor("b-ok", recordB, resB, "audit-b"))
	if err != nil {
		t.Fatalf("assume tenant B: %v", err)
	}
	if credentialsA.Audience != tenantA || credentialsA.RoleID != roleA || credentialsA.SourceIdentity != "platform-service-principal" {
		t.Fatalf("tenant A credential not bound to A decision: %+v", credentialsA)
	}
	if credentialsB.Audience != tenantB || credentialsB.RoleID != roleB || credentialsB.SourceIdentity != "platform-service-principal" {
		t.Fatalf("tenant B credential not bound to B decision: %+v", credentialsB)
	}
	if credentialsA.SessionID == credentialsB.SessionID {
		t.Fatal("different tenant decisions produced the same session ID")
	}

	decisionsMu.Lock()
	decisionA := decisions["a-ok"]
	decisionB := decisions["b-ok"]
	decisionsMu.Unlock()
	if credentialsA.DecisionDigest != decisionA.Digest ||
		credentialsA.TenantID != tenantA ||
		credentialsA.TenantVersion != decisionA.TenantVersion ||
		credentialsA.ResourceVersion != decisionA.ResourceVersion ||
		credentialsA.RequestID != "a-ok" {
		t.Fatalf("tenant A credential is not traceable to its decision: %+v", credentialsA)
	}
	if credentialsB.DecisionDigest != decisionB.Digest || credentialsB.TenantID != tenantB {
		t.Fatalf("tenant B credential is not traceable to its decision: %+v", credentialsB)
	}
	if decisionA.TenantVersion != recordA.Version || decisionB.TenantVersion != recordB.Version {
		t.Fatalf("decision versions not bound: A=%d B=%d", decisionA.TenantVersion, decisionB.TenantVersion)
	}
	if credentialhost.ComputeDecisionDigest(decisionA) != decisionA.Digest {
		t.Fatal("tenant A decision digest is not canonical/reproducible")
	}
	if decisionA.Digest == decisionB.Digest {
		t.Fatal("different tenant decisions must have different authorization digests")
	}

	attempts := []struct {
		name string
		req  credentialhost.AssumeRequest
		want error
	}{
		{
			name: "other tenant role",
			req: func() credentialhost.AssumeRequest {
				request := requestFor("bad-role", recordA, resA, "audit-role")
				request.RoleID = roleB
				return request
			}(),
			want: credentialhost.ErrRoleMismatch,
		},
		{
			name: "other tenant external ID",
			req: func() credentialhost.AssumeRequest {
				request := requestFor("bad-external", recordA, resA, "audit-external")
				request.ExternalID = recordB.ExternalID
				return request
			}(),
			want: credentialhost.ErrExternalIDMismatch,
		},
		{
			name: "resource outside set",
			req:  requestFor("bad-resource", recordA, resB, "audit-resource"),
			want: credentialhost.ErrSourceResourceNotAllowed,
		},
		{
			name: "tenant not claimed by principal",
			req:  requestFor("bad-principal", recordA, resA, "audit-principal"),
			want: credentialhost.ErrTenantClaimMismatch,
		},
		{
			name: "duration beyond tenant maximum",
			req: func() credentialhost.AssumeRequest {
				request := requestFor("bad-duration", recordA, resA, "audit-duration")
				request.DurationSeconds = recordA.MaxSessionSeconds + 1
				return request
			}(),
			want: credentialhost.ErrInvalidDuration,
		},
	}
	for _, attempt := range attempts {
		t.Run(attempt.name, func(t *testing.T) {
			attemptCtx := ctx
			if attempt.want == credentialhost.ErrTenantClaimMismatch {
				attemptCtx = credentialhost.WithPrincipal(context.Background(), credentialhost.Principal{
					Subject:   "platform-service-principal",
					TenantIDs: []string{tenantB},
				})
			}
			if _, err := host.Assume(attemptCtx, attempt.req); !errors.Is(err, attempt.want) {
				t.Fatalf("got %v, want %v", err, attempt.want)
			}
		})
	}
	if got := sts.count(); got != 2 {
		t.Fatalf("STS must not be called for authorization failures, got %d calls", got)
	}

	sessionNameRequest := requestFor("digest-one", recordA, resA, "audit-one")
	sessionNameRequest.ParametersDigest = "sha256:same-parameters"
	if _, err := host.Assume(ctx, sessionNameRequest); err != nil {
		t.Fatalf("assume with first session name: %v", err)
	}
	otherSessionRequest := requestFor("digest-two", recordA, resA, "audit-two")
	otherSessionRequest.ParametersDigest = "sha256:same-parameters"
	if _, err := host.Assume(ctx, otherSessionRequest); err != nil {
		t.Fatalf("assume with second session name: %v", err)
	}
	decisionsMu.Lock()
	firstDecision := decisions["digest-one"]
	secondDecision := decisions["digest-two"]
	decisionsMu.Unlock()
	if firstDecision.Digest != secondDecision.Digest {
		t.Fatal("audit-only sessionName changed the authorization decision digest")
	}
	if bytes.Contains(firstDecision.CanonicalDecision, []byte("audit-one")) {
		t.Fatal("sessionName leaked into canonical authorization decision")
	}

	if err := registry.SetResourceOwner(resA, tenantB); err != nil {
		t.Fatalf("transfer resource: %v", err)
	}
	if _, err := host.Assume(ctx, requestFor("resource-transferred", recordA, resA, "audit-moved")); !errors.Is(err, credentialhost.ErrResourceOwnerMismatch) {
		t.Fatalf("got %v, want resource-owner mismatch", err)
	}
	if got := sts.count(); got != 4 {
		t.Fatalf("STS called after resource transfer failure, got %d calls", got)
	}
}

func TestSessionNameValidation(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		var destroyed atomic.Int32
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	ctx := platformContext()

	for _, sessionName := range []string{"", strings.Repeat("a", 65), "contains space", "line\nbreak", "非ASCII", "slash/name", `quote"name`} {
		request := requestFor("invalid-"+fmt.Sprint(len(sessionName)), recordA, resA, sessionName)
		if _, err := host.Assume(ctx, request); !errors.Is(err, credentialhost.ErrInvalidSessionName) {
			t.Fatalf("session name %q: got %v, want invalid session name", sessionName, err)
		}
	}
	request := requestFor("valid-64", recordA, resA, strings.Repeat("a", 64))
	if _, err := host.Assume(ctx, request); err != nil {
		t.Fatalf("64-byte safe session name rejected: %v", err)
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("invalid session names reached STS, got %d calls", got)
	}
}

func TestDecisionDigestCoversSixFieldsAndPrincipal(t *testing.T) {
	base := credentialhost.Decision{
		Principal:        "platform-service-principal",
		TenantID:         tenantA,
		RoleID:           roleA,
		ExternalID:       strings.Repeat("a", 32),
		SourceResource:   resA,
		DurationSeconds:  60,
		ParametersDigest: "sha256:parameters",
		RequestID:        "ignored-request-id",
		SessionName:      "ignored-session-name",
	}
	baseDigest := credentialhost.ComputeDecisionDigest(base)
	mutations := map[string]func(*credentialhost.Decision){
		"principal": func(decision *credentialhost.Decision) {
			decision.Principal = "another-principal"
		},
		"tenant": func(decision *credentialhost.Decision) {
			decision.TenantID = tenantB
		},
		"role": func(decision *credentialhost.Decision) {
			decision.RoleID = roleB
		},
		"external ID": func(decision *credentialhost.Decision) {
			decision.ExternalID = strings.Repeat("b", 32)
		},
		"source resource": func(decision *credentialhost.Decision) {
			decision.SourceResource = resB
		},
		"duration": func(decision *credentialhost.Decision) {
			decision.DurationSeconds++
		},
		"parameter digest": func(decision *credentialhost.Decision) {
			decision.ParametersDigest = "sha256:other-parameters"
		},
	}
	for name, mutate := range mutations {
		decision := base
		mutate(&decision)
		if got := credentialhost.ComputeDecisionDigest(decision); got == baseDigest {
			t.Errorf("changing %s did not change the authorization decision digest", name)
		}
	}

	auditOnly := base
	auditOnly.RequestID = "another-request-id"
	auditOnly.SessionName = "another-session-name"
	if got := credentialhost.ComputeDecisionDigest(auditOnly); got != baseDigest {
		t.Fatal("request ID or session name changed the authorization decision digest")
	}
}

func TestCredentialValidationAndDestruction(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*credentialhost.Credentials, credentialhost.STSRequest)
		wantErr error
	}{
		{
			name: "audience mismatch",
			mutate: func(credentials *credentialhost.Credentials, _ credentialhost.STSRequest) {
				credentials.Audience = tenantB
			},
			wantErr: credentialhost.ErrCredentialMismatch,
		},
		{
			name: "role mismatch",
			mutate: func(credentials *credentialhost.Credentials, _ credentialhost.STSRequest) {
				credentials.RoleID = roleB
			},
			wantErr: credentialhost.ErrCredentialMismatch,
		},
		{
			name: "source identity mismatch",
			mutate: func(credentials *credentialhost.Credentials, _ credentialhost.STSRequest) {
				credentials.SourceIdentity = "another-platform-principal"
			},
			wantErr: credentialhost.ErrCredentialMismatch,
		},
		{
			name: "session ID mismatch",
			mutate: func(credentials *credentialhost.Credentials, _ credentialhost.STSRequest) {
				credentials.SessionID = "wrong-session"
			},
			wantErr: credentialhost.ErrCredentialMismatch,
		},
		{
			name: "expiry beyond request limit",
			mutate: func(credentials *credentialhost.Credentials, request credentialhost.STSRequest) {
				credentials.ExpiresAt = request.Decision.NotAfter.Add(time.Second)
			},
			wantErr: credentialhost.ErrCredentialMismatch,
		},
		{
			name: "already expired",
			mutate: func(credentials *credentialhost.Credentials, _ credentialhost.STSRequest) {
				credentials.ExpiresAt = testNow.Add(-time.Second)
			},
			wantErr: credentialhost.ErrCredentialExpired,
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, recordA, _ := setupTenants(t)
			var destroyed atomic.Int32
			sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
				credentials := validCredentials(request, &destroyed)
				test.mutate(credentials, request)
				return credentials, nil
			}}
			host := newHost(t, registry, sts)
			if _, err := host.Assume(platformContext(), requestFor(fmt.Sprintf("mismatch-%d", index), recordA, resA, "audit-mismatch")); !errors.Is(err, test.wantErr) {
				t.Fatalf("got %v, want %v", err, test.wantErr)
			}
			if got := destroyed.Load(); got != 1 {
				t.Fatalf("mismatched credential destroy count = %d, want 1", got)
			}
		})
	}
}

func TestCallbackFailureAndPanicDestroyCredentials(t *testing.T) {
	t.Run("callback error", func(t *testing.T) {
		registry, recordA, _ := setupTenants(t)
		var destroyed atomic.Int32
		sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
			return validCredentials(request, &destroyed), errors.New("upstream unavailable")
		}}
		host := newHost(t, registry, sts)
		_, err := host.Assume(platformContext(), requestFor("callback-error", recordA, resA, "audit-error"))
		if !errors.Is(err, credentialhost.ErrSTSFailure) {
			t.Fatalf("got %v, want STS failure", err)
		}
		if destroyed.Load() != 1 {
			t.Fatalf("credential on callback error was not destroyed")
		}
	})

	t.Run("callback panic", func(t *testing.T) {
		registry, recordA, _ := setupTenants(t)
		sts := &fakeSTS{hook: func(context.Context, credentialhost.STSRequest) (*credentialhost.Credentials, error) {
			panic("boom")
		}}
		host := newHost(t, registry, sts)
		_, err := host.Assume(platformContext(), requestFor("callback-panic", recordA, resA, "audit-panic"))
		if !errors.Is(err, credentialhost.ErrSTSFailure) {
			t.Fatalf("got %v, want STS failure", err)
		}
	})
}

func TestConcurrentIdempotencyAndRequestIDConflict(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	var destroyed atomic.Int32
	var release = make(chan struct{})
	var started = make(chan struct{})
	var once sync.Once
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		once.Do(func() { close(started) })
		<-release
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	ctx := platformContext()
	request := requestFor("concurrent-retry", recordA, resA, "audit-concurrent")

	const workers = 32
	start := make(chan struct{})
	results := make([]*credentialhost.Credentials, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			results[index], errs[index] = host.Assume(ctx, request)
		}(i)
	}
	close(start)
	<-started
	if got := sts.count(); got != 1 {
		t.Fatalf("concurrent retries invoked STS %d times, want 1", got)
	}
	close(release)
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if results[i] == nil || results[i].SessionID != results[0].SessionID {
			t.Fatalf("worker %d did not receive the shared result", i)
		}
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("STS call count after retries = %d, want 1", got)
	}

	changed := request
	changed.SessionName = "different-audit-name"
	if _, err := host.Assume(ctx, changed); !errors.Is(err, credentialhost.ErrRequestIDConflict) {
		t.Fatalf("got %v, want request-ID conflict", err)
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("conflicting request ID invoked STS, got %d calls", got)
	}

	replayed, err := host.Assume(ctx, request)
	if err != nil {
		t.Fatalf("exact replay: %v", err)
	}
	if replayed.SessionID != results[0].SessionID {
		t.Fatal("exact replay did not return the cached result")
	}
}

func TestLateCallbackAfterRotationIsNotPublished(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	var destroyed atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		once.Do(func() { close(started) })
		<-release
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	ctx := platformContext()
	request := requestFor("late-callback", recordA, resA, "audit-late")

	result := make(chan struct {
		credentials *credentialhost.Credentials
		err         error
	}, 1)
	go func() {
		credentials, err := host.Assume(ctx, request)
		result <- struct {
			credentials *credentialhost.Credentials
			err         error
		}{credentials: credentials, err: err}
	}()
	<-started

	rotated, err := registry.ReplaceTenant(credentialhost.TenantConfig{
		TenantID:          tenantA,
		RoleID:            roleA,
		SourceResources:   []string{resA},
		MaxSessionSeconds: 3600,
		PolicyDigest:      "sha256:policy-a-v2",
	})
	if err != nil {
		t.Fatalf("rotate tenant A: %v", err)
	}
	if rotated.ExternalID == recordA.ExternalID {
		t.Fatal("rotation did not generate a new external ID")
	}
	close(release)

	got := <-result
	if !errors.Is(got.err, credentialhost.ErrStaleRequest) {
		t.Fatalf("got %v, want stale request", got.err)
	}
	if got.credentials != nil {
		t.Fatal("stale in-flight credential was published")
	}
	if destroyed.Load() != 1 {
		t.Fatalf("late credential destroy count = %d, want 1", destroyed.Load())
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("STS call count = %d, want 1", got)
	}

	if _, err := host.Assume(ctx, request); !errors.Is(err, credentialhost.ErrStaleRequest) {
		t.Fatalf("old request ID replay got %v, want stale request", err)
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("stale request ID invoked STS again, got %d calls", got)
	}

	oldExternalID := requestFor("old-external-after-rotation", rotated, resA, "audit-old-external")
	oldExternalID.ExternalID = recordA.ExternalID
	if _, err := host.Assume(ctx, oldExternalID); !errors.Is(err, credentialhost.ErrExternalIDMismatch) {
		t.Fatalf("old external ID after rotation got %v, want mismatch", err)
	}

	newGenerationRequest := requestFor("new-generation", rotated, resA, "audit-new-generation")
	newCredentials, err := host.Assume(ctx, newGenerationRequest)
	if err != nil {
		t.Fatalf("new generation request: %v", err)
	}
	if newCredentials.SessionID == "" {
		t.Fatal("new generation did not produce a session ID")
	}
	if got := sts.count(); got != 2 {
		t.Fatalf("new generation STS call count = %d, want 2", got)
	}
}

func TestRevocationInvalidatesCachedCredential(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	var destroyed atomic.Int32
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	ctx := platformContext()
	request := requestFor("revoked-cache", recordA, resA, "audit-revoke")

	credentials, err := host.Assume(ctx, request)
	if err != nil {
		t.Fatalf("assume before revocation: %v", err)
	}
	if credentials.SessionID == "" {
		t.Fatal("successful assume returned no session ID")
	}
	if err := registry.RevokeTenant(tenantA); err != nil {
		t.Fatalf("revoke tenant: %v", err)
	}
	if destroyed.Load() != 0 {
		t.Fatalf("revocation destroyed an already-delivered credential: %d", destroyed.Load())
	}
	if _, err := host.Assume(ctx, request); !errors.Is(err, credentialhost.ErrStaleRequest) {
		t.Fatalf("cached replay after revocation got %v, want stale request", err)
	}
	if got := sts.count(); got != 1 {
		t.Fatalf("cached replay after revocation invoked STS, got %d calls", got)
	}
}

func TestResourceTransferInvalidatesInFlightDecision(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	var destroyed atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		once.Do(func() { close(started) })
		<-release
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	request := requestFor("owner-change-in-flight", recordA, resA, "audit-owner")

	errCh := make(chan error, 1)
	go func() {
		_, err := host.Assume(platformContext(), request)
		errCh <- err
	}()
	<-started
	if err := registry.SetResourceOwner(resA, tenantB); err != nil {
		t.Fatalf("transfer resource: %v", err)
	}
	close(release)
	if err := <-errCh; !errors.Is(err, credentialhost.ErrStaleRequest) {
		t.Fatalf("got %v, want stale request after ownership transfer", err)
	}
	if destroyed.Load() != 1 {
		t.Fatalf("in-flight credential after ownership transfer was not destroyed")
	}
}

func TestPrincipalCannotComeFromRequestFields(t *testing.T) {
	registry, recordA, _ := setupTenants(t)
	sts := &fakeSTS{hook: func(_ context.Context, request credentialhost.STSRequest) (*credentialhost.Credentials, error) {
		var destroyed atomic.Int32
		return validCredentials(request, &destroyed), nil
	}}
	host := newHost(t, registry, sts)
	request := requestFor("missing-context", recordA, resA, "audit-no-context")

	if _, err := host.Assume(context.Background(), request); !errors.Is(err, credentialhost.ErrUnauthenticated) {
		t.Fatalf("got %v, want unauthenticated", err)
	}
	if got := sts.count(); got != 0 {
		t.Fatalf("missing authentication reached STS %d times", got)
	}
}
