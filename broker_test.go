package netsec

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPrincipal = "platform-principal"

type testEnv struct {
	registry *Registry
	tenantA  TenantRecord
	tenantB  TenantRecord
	auth     AuthContext
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	externalA := mustExternalID(t)
	externalB := mustExternalID(t)
	reg := NewRegistry()
	a := TenantRecord{
		TenantID:          "tenant-a",
		RoleID:            "arn:aws:iam::111111111111:role/backup-a",
		ExternalID:        externalA,
		SourceResources:   []string{"res-a", "shared-res"},
		MaxSessionSeconds: 3600,
		PolicyDigest:      "sha256:policy-a-v1",
	}
	b := TenantRecord{
		TenantID:          "tenant-b",
		RoleID:            "arn:aws:iam::222222222222:role/backup-b",
		ExternalID:        externalB,
		SourceResources:   []string{"res-b"},
		MaxSessionSeconds: 1800,
		PolicyDigest:      "sha256:policy-b-v1",
	}
	if err := reg.RegisterTenant(a); err != nil {
		t.Fatalf("register tenant A: %v", err)
	}
	if err := reg.RegisterTenant(b); err != nil {
		t.Fatalf("register tenant B: %v", err)
	}
	return &testEnv{
		registry: reg,
		tenantA:  a,
		tenantB:  b,
		auth: AuthContext{
			PrincipalID: testPrincipal,
			TenantIDs:   []string{"tenant-a", "tenant-b"},
		},
	}
}

func mustExternalID(t *testing.T) string {
	t.Helper()
	id, err := GenerateExternalID()
	if err != nil {
		t.Fatalf("generate external ID: %v", err)
	}
	return id
}

func (e *testEnv) record(tenantID string) TenantRecord {
	switch tenantID {
	case e.tenantA.TenantID:
		return e.tenantA
	case e.tenantB.TenantID:
		return e.tenantB
	default:
		panic("unknown test tenant")
	}
}

func requestFor(e *testEnv, tenantID, requestID string) AssumeRequest {
	rec := e.record(tenantID)
	return AssumeRequest{
		RequestID:        requestID,
		TenantID:         rec.TenantID,
		RoleID:           rec.RoleID,
		ExternalID:       rec.ExternalID,
		SourceResource:   rec.SourceResources[0],
		SessionName:      "sess-" + requestID,
		DurationSeconds:  300,
		ParametersDigest: "sha256:call-parameters",
	}
}

type fakeSTS struct {
	mu       sync.Mutex
	calls    int
	requests []STSRequest
	respond  func(STSRequest) (*STSCredential, error)
	started  chan STSRequest
	release  <-chan struct{}
}

func newFakeSTS(respond func(STSRequest) (*STSCredential, error)) *fakeSTS {
	return &fakeSTS{respond: respond}
}

func (f *fakeSTS) AssumeRole(ctx context.Context, req STSRequest) (*STSCredential, error) {
	f.mu.Lock()
	f.calls++
	f.requests = append(f.requests, req)
	respond := f.respond
	started := f.started
	release := f.release
	f.mu.Unlock()

	if started != nil {
		started <- req
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return respond(req)
}

func (f *fakeSTS) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSTS) request(index int) STSRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[index]
}

func goodCredential(req STSRequest) *STSCredential {
	return &STSCredential{
		AccessKeyID:     "AKIA-" + req.TenantID,
		SecretAccessKey: "secret-" + req.TenantID,
		SessionToken:    "token-" + req.TenantID,
		Audience:        req.Audience,
		RoleID:          req.RoleID,
		SourceIdentity:  req.SourceIdentity,
		Expiration:      req.NotAfter,
		SessionID:       req.SessionID,
	}
}

func newBroker(e *testEnv, sts STS, opts ...Option) *Broker {
	return NewBroker(e.registry, sts, opts...)
}

func TestAssumeSuccessIsTenantBoundAndTraceable(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "req-1")

	credential, err := broker.Assume(context.Background(), e.auth, req)
	if err != nil {
		t.Fatalf("Assume: %v", err)
	}
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
	stsReq := fake.request(0)
	if stsReq.TenantID != req.TenantID || stsReq.RoleID != req.RoleID {
		t.Fatalf("STS request not bound to tenant: %+v", stsReq)
	}
	if stsReq.ExternalID != e.tenantA.ExternalID {
		t.Fatalf("STS received wrong external ID")
	}
	if stsReq.SourceIdentity != testPrincipal {
		t.Fatalf("STS source identity = %q, want %q", stsReq.SourceIdentity, testPrincipal)
	}
	if stsReq.DurationSeconds != 300 {
		t.Fatalf("STS duration = %d, want 300", stsReq.DurationSeconds)
	}

	if credential.TenantID != e.tenantA.TenantID ||
		credential.PrincipalID != testPrincipal ||
		credential.RequestID != req.RequestID ||
		credential.SessionName != req.SessionName {
		t.Fatalf("credential metadata is not traceable: %+v", credential)
	}
	if credential.DecisionID == "" {
		t.Fatal("credential has no decision ID")
	}
	if credential.SessionID != stsReq.SessionID {
		t.Fatal("credential session ID does not match decision")
	}
	if credential.PolicyDigest != e.tenantA.PolicyDigest {
		t.Fatal("credential policy digest mismatch")
	}

	decision, ok := broker.DecisionByID(credential.DecisionID)
	if !ok {
		t.Fatal("credential decision is missing from audit trail")
	}
	if !decision.Allowed || decision.TenantID != e.tenantA.TenantID ||
		decision.PrincipalID != testPrincipal || decision.TenantGeneration != 1 {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if decision.RecomputeDigest() != decision.Digest {
		t.Fatal("decision digest is not canonical")
	}
	if decision.ExternalID != req.ExternalID || decision.SessionName != req.SessionName {
		t.Fatal("decision does not bind request fields")
	}
	if len(broker.Decisions()) != 1 {
		t.Fatalf("audit decisions = %d, want 1", len(broker.Decisions()))
	}
}

func TestCrossTenantSubstitutionRejected(t *testing.T) {
	tests := []struct {
		name    string
		auth    func(*testEnv) AuthContext
		mutate  func(*testEnv, *AssumeRequest)
		wantErr error
	}{
		{
			name: "role from another tenant",
			auth: func(e *testEnv) AuthContext { return e.auth },
			mutate: func(e *testEnv, req *AssumeRequest) {
				req.RoleID = e.tenantB.RoleID
			},
			wantErr: ErrRoleMismatch,
		},
		{
			name: "external ID from another tenant",
			auth: func(e *testEnv) AuthContext { return e.auth },
			mutate: func(e *testEnv, req *AssumeRequest) {
				req.ExternalID = e.tenantB.ExternalID
			},
			wantErr: ErrExternalIDMismatch,
		},
		{
			name: "same length wrong external ID",
			auth: func(e *testEnv) AuthContext { return e.auth },
			mutate: func(e *testEnv, req *AssumeRequest) {
				last := req.ExternalID[len(req.ExternalID)-1]
				if last == 'a' {
					req.ExternalID = req.ExternalID[:len(req.ExternalID)-1] + "b"
				} else {
					req.ExternalID = req.ExternalID[:len(req.ExternalID)-1] + "a"
				}
			},
			wantErr: ErrExternalIDMismatch,
		},
		{
			name: "tenant not delegated to principal",
			auth: func(e *testEnv) AuthContext {
				return AuthContext{PrincipalID: testPrincipal, TenantIDs: []string{"tenant-a"}}
			},
			mutate: func(e *testEnv, req *AssumeRequest) {
				*req = requestFor(e, e.tenantB.TenantID, req.RequestID)
			},
			wantErr: ErrTenantNotAllowed,
		},
		{
			name: "resource from another tenant",
			auth: func(e *testEnv) AuthContext { return e.auth },
			mutate: func(e *testEnv, req *AssumeRequest) {
				req.SourceResource = "res-b"
			},
			wantErr: ErrResourceNotAllowed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
				return goodCredential(req), nil
			})
			broker := newBroker(e, fake)
			req := requestFor(e, e.tenantA.TenantID, "req-cross")
			tc.mutate(e, &req)
			_, err := broker.Assume(context.Background(), tc.auth(e), req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if fake.callCount() != 0 {
				t.Fatalf("STS was called %d times for a denied request", fake.callCount())
			}
		})
	}
}

func TestSessionNameValidationAndAuditOnly(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)

	invalid := []string{
		"",
		strings.Repeat("a", MaxSessionNameBytes+1),
		"bad name",
		"bad\nname",
		"bad\"name",
		"角色",
	}
	for i, name := range invalid {
		req := requestFor(e, e.tenantA.TenantID, fmt.Sprintf("bad-%d", i))
		req.SessionName = name
		if _, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrInvalidSessionName) {
			t.Fatalf("session name %q: error = %v, want ErrInvalidSessionName", name, err)
		}
	}
	if fake.callCount() != 0 {
		t.Fatalf("STS called for invalid session names")
	}

	for i, name := range []string{"Z", strings.Repeat("a", MaxSessionNameBytes)} {
		req := requestFor(e, e.tenantA.TenantID, fmt.Sprintf("good-%d", i))
		req.SessionName = name
		if _, err := broker.Assume(context.Background(), e.auth, req); err != nil {
			t.Fatalf("valid session name of %d bytes: %v", len(name), err)
		}
	}

	base := requestFor(e, e.tenantA.TenantID, "audit-1")
	other := base
	other.RequestID = "audit-2"
	other.SessionName = "different-safe-session"
	first, err := broker.Assume(context.Background(), e.auth, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.Assume(context.Background(), e.auth, other)
	if err != nil {
		t.Fatal(err)
	}
	if first.DecisionID == second.DecisionID {
		t.Fatal("different requests unexpectedly share a decision")
	}
	if first.TenantID != second.TenantID {
		t.Fatal("session name changed tenant authorization")
	}

	otherTenantName := requestFor(e, e.tenantA.TenantID, "audit-3")
	otherTenantName.SessionName = e.tenantB.TenantID
	third, err := broker.Assume(context.Background(), e.auth, otherTenantName)
	if err != nil {
		t.Fatalf("valid session name matching another tenant was rejected: %v", err)
	}
	if third.TenantID != e.tenantA.TenantID {
		t.Fatalf("session name selected tenant %q instead of request tenant %q", third.TenantID, e.tenantA.TenantID)
	}
}

func TestDurationCapAndExpiration(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)

	req := requestFor(e, e.tenantA.TenantID, "duration-1")
	req.DurationSeconds = e.tenantA.MaxSessionSeconds + 1000
	credential, err := broker.Assume(context.Background(), e.auth, req)
	if err != nil {
		t.Fatalf("over-max duration should be clamped, got %v", err)
	}
	stsReq := fake.request(0)
	if stsReq.DurationSeconds != e.tenantA.MaxSessionSeconds {
		t.Fatalf("effective duration = %d, want %d", stsReq.DurationSeconds, e.tenantA.MaxSessionSeconds)
	}
	decision, ok := broker.DecisionByID(credential.DecisionID)
	if !ok || decision.EffectiveDurationSeconds != e.tenantA.MaxSessionSeconds {
		t.Fatalf("decision did not record clamped duration: %+v", decision)
	}

	for _, duration := range []int64{0, -1} {
		req := requestFor(e, e.tenantA.TenantID, fmt.Sprintf("duration-bad-%d", duration))
		req.DurationSeconds = duration
		if _, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrInvalidDuration) {
			t.Fatalf("duration %d: error = %v, want ErrInvalidDuration", duration, err)
		}
	}
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
}

func TestCredentialMismatchDestroysCredential(t *testing.T) {
	mutations := []struct {
		name    string
		mutate  func(*STSCredential, STSRequest)
		wantErr error
	}{
		{
			name: "audience",
			mutate: func(c *STSCredential, req STSRequest) {
				c.Audience = "other-tenant"
			},
			wantErr: ErrCredentialMismatch,
		},
		{
			name: "role",
			mutate: func(c *STSCredential, req STSRequest) {
				c.RoleID = "arn:aws:iam::999999999999:role/other"
			},
			wantErr: ErrCredentialMismatch,
		},
		{
			name: "source identity",
			mutate: func(c *STSCredential, req STSRequest) {
				c.SourceIdentity = "other-principal"
			},
			wantErr: ErrCredentialMismatch,
		},
		{
			name: "session ID",
			mutate: func(c *STSCredential, req STSRequest) {
				c.SessionID = strings.Repeat("0", len(c.SessionID))
			},
			wantErr: ErrCredentialMismatch,
		},
		{
			name: "expiration beyond cap",
			mutate: func(c *STSCredential, req STSRequest) {
				c.Expiration = req.NotAfter.Add(time.Nanosecond)
			},
			wantErr: ErrCredentialMismatch,
		},
	}

	for i, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			var issued *STSCredential
			fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
				issued = goodCredential(req)
				tc.mutate(issued, req)
				return issued, nil
			})
			broker := newBroker(e, fake)
			req := requestFor(e, e.tenantA.TenantID, fmt.Sprintf("mismatch-%d", i))
			_, err := broker.Assume(context.Background(), e.auth, req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if issued == nil {
				t.Fatal("test did not observe issued credential")
			}
			assertDestroyed(t, issued)
			last := broker.Decisions()[len(broker.Decisions())-1]
			if last.Allowed {
				t.Fatal("mismatched credential produced an allowed decision")
			}
		})
	}

	t.Run("expired", func(t *testing.T) {
		e := newTestEnv(t)
		clock := &fixedClock{now: time.Now().UTC()}
		var issued *STSCredential
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			issued = goodCredential(req)
			issued.Expiration = clock.Now().Add(-time.Second)
			return issued, nil
		})
		broker := newBroker(e, fake, WithClock(clock))
		req := requestFor(e, e.tenantA.TenantID, "mismatch-expired")
		if _, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrCredentialExpired) {
			t.Fatalf("error = %v, want ErrCredentialExpired", err)
		}
		assertDestroyed(t, issued)
	})

	t.Run("nil credential", func(t *testing.T) {
		e := newTestEnv(t)
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			return nil, nil
		})
		broker := newBroker(e, fake)
		req := requestFor(e, e.tenantA.TenantID, "mismatch-nil")
		if _, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrSTSFailure) {
			t.Fatalf("error = %v, want ErrSTSFailure", err)
		}
	})

	t.Run("callback error destroys partial credential", func(t *testing.T) {
		e := newTestEnv(t)
		var issued *STSCredential
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			issued = goodCredential(req)
			return issued, errors.New("upstream failure")
		})
		broker := newBroker(e, fake)
		req := requestFor(e, e.tenantA.TenantID, "mismatch-sts-error")
		if _, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrSTSFailure) {
			t.Fatalf("error = %v, want ErrSTSFailure", err)
		}
		assertDestroyed(t, issued)
	})
}

func assertDestroyed(t *testing.T, credential *STSCredential) {
	t.Helper()
	if credential == nil {
		t.Fatal("credential is nil")
	}
	if credential.AccessKeyID != "" || credential.SecretAccessKey != "" || credential.SessionToken != "" ||
		credential.Audience != "" || credential.RoleID != "" || credential.SourceIdentity != "" ||
		credential.SessionID != "" || !credential.Expiration.IsZero() {
		t.Fatalf("credential was not destroyed: %+v", credential)
	}
}

func TestResourceOwnershipAndInFlightTransfer(t *testing.T) {
	t.Run("ownership mismatch", func(t *testing.T) {
		e := newTestEnv(t)
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			return goodCredential(req), nil
		})
		broker := newBroker(e, fake)

		updatedB := e.tenantB
		updatedB.SourceResources = append(append([]string(nil), updatedB.SourceResources...), "shared-res")
		if err := e.registry.UpdateTenant(updatedB); err != nil {
			t.Fatalf("update tenant B: %v", err)
		}
		bReq := requestFor(e, e.tenantB.TenantID, "owner-b-1")
		bReq.SourceResource = "shared-res"
		if _, err := broker.Assume(context.Background(), e.auth, bReq); !errors.Is(err, ErrResourceOwnerMismatch) {
			t.Fatalf("error = %v, want ErrResourceOwnerMismatch", err)
		}
		if fake.callCount() != 0 {
			t.Fatal("STS called while resource belonged to another tenant")
		}

		if err := e.registry.TransferResource("shared-res", e.tenantA.TenantID, e.tenantB.TenantID); err != nil {
			t.Fatalf("transfer resource: %v", err)
		}
		aReq := requestFor(e, e.tenantA.TenantID, "owner-a-1")
		aReq.SourceResource = "shared-res"
		if _, err := broker.Assume(context.Background(), e.auth, aReq); !errors.Is(err, ErrResourceOwnerMismatch) {
			t.Fatalf("old owner error = %v, want ErrResourceOwnerMismatch", err)
		}
		bReq.RequestID = "owner-b-2"
		if _, err := broker.Assume(context.Background(), e.auth, bReq); err != nil {
			t.Fatalf("new owner should be authorized: %v", err)
		}
		if fake.callCount() != 1 {
			t.Fatalf("STS calls = %d, want 1", fake.callCount())
		}
	})

	t.Run("transfer during callback", func(t *testing.T) {
		e := newTestEnv(t)
		updatedB := e.tenantB
		updatedB.SourceResources = append(append([]string(nil), updatedB.SourceResources...), "shared-res")
		if err := e.registry.UpdateTenant(updatedB); err != nil {
			t.Fatalf("update tenant B: %v", err)
		}

		block := make(chan struct{})
		started := make(chan STSRequest, 1)
		var issued *STSCredential
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			issued = goodCredential(req)
			return issued, nil
		})
		fake.started = started
		fake.release = block
		broker := newBroker(e, fake)

		req := requestFor(e, e.tenantA.TenantID, "transfer-inflight")
		req.SourceResource = "shared-res"
		errCh := make(chan error, 1)
		go func() {
			_, err := broker.Assume(context.Background(), e.auth, req)
			errCh <- err
		}()
		<-started
		if err := e.registry.TransferResource("shared-res", e.tenantA.TenantID, e.tenantB.TenantID); err != nil {
			t.Fatalf("transfer during callback: %v", err)
		}
		close(block)
		if err := <-errCh; !errors.Is(err, ErrStaleConfiguration) {
			t.Fatalf("error = %v, want ErrStaleConfiguration", err)
		}
		assertDestroyed(t, issued)
	})
}

func TestTenantUpdateFencesLateCallback(t *testing.T) {
	e := newTestEnv(t)
	block := make(chan struct{})
	started := make(chan STSRequest, 1)
	var issued *STSCredential
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		issued = goodCredential(req)
		return issued, nil
	})
	fake.started = started
	fake.release = block
	broker := newBroker(e, fake)

	req := requestFor(e, e.tenantA.TenantID, "late-update")
	errCh := make(chan error, 1)
	credCh := make(chan *IssuedCredential, 1)
	go func() {
		credential, err := broker.Assume(context.Background(), e.auth, req)
		credCh <- credential
		errCh <- err
	}()
	<-started

	updated := e.tenantA
	updated.PolicyDigest = "sha256:policy-a-v2"
	if err := e.registry.UpdateTenant(updated); err != nil {
		t.Fatalf("update tenant: %v", err)
	}
	close(block)
	if err := <-errCh; !errors.Is(err, ErrStaleConfiguration) {
		t.Fatalf("error = %v, want ErrStaleConfiguration", err)
	}
	if credential := <-credCh; credential != nil {
		t.Fatalf("stale credential was published: %+v", credential)
	}
	assertDestroyed(t, issued)
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
	last := broker.Decisions()[len(broker.Decisions())-1]
	if last.Allowed || last.DenyCode != "stale_configuration" {
		t.Fatalf("stale result audit = %+v", last)
	}
}

func TestCachedResultNotPublishedAfterUpdate(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "cached-stale")
	first, err := broker.Assume(context.Background(), e.auth, req)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("missing first credential")
	}

	updated := e.tenantA
	updated.PolicyDigest = "sha256:policy-a-v2"
	if err := e.registry.UpdateTenant(updated); err != nil {
		t.Fatal(err)
	}
	if credential, err := broker.Assume(context.Background(), e.auth, req); !errors.Is(err, ErrStaleConfiguration) {
		t.Fatalf("error = %v, want ErrStaleConfiguration", err)
	} else if credential != nil {
		t.Fatal("stale cached credential was republished")
	}
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
}

func TestReplayRechecksTenantDelegation(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "revoked-delegation")
	if _, err := broker.Assume(context.Background(), e.auth, req); err != nil {
		t.Fatal(err)
	}

	revoked := AuthContext{PrincipalID: testPrincipal}
	if credential, err := broker.Assume(context.Background(), revoked, req); !errors.Is(err, ErrTenantNotAllowed) {
		t.Fatalf("error = %v, want ErrTenantNotAllowed", err)
	} else if credential != nil {
		t.Fatal("cached credential returned after tenant delegation was revoked")
	}
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
}

func TestRemovedTenantGenerationIsNotReused(t *testing.T) {
	e := newTestEnv(t)
	block := make(chan struct{})
	started := make(chan STSRequest, 1)
	var issued *STSCredential
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		issued = goodCredential(req)
		return issued, nil
	})
	fake.started = started
	fake.release = block
	broker := newBroker(e, fake)

	req := requestFor(e, e.tenantA.TenantID, "remove-reregister")
	errCh := make(chan error, 1)
	go func() {
		_, err := broker.Assume(context.Background(), e.auth, req)
		errCh <- err
	}()
	<-started
	if err := e.registry.RemoveTenant(e.tenantA.TenantID); err != nil {
		t.Fatalf("remove tenant: %v", err)
	}
	if err := e.registry.RegisterTenant(e.tenantA); err != nil {
		t.Fatalf("re-register tenant: %v", err)
	}
	if generation, ok := e.registry.Generation(e.tenantA.TenantID); !ok || generation <= 1 {
		t.Fatalf("re-registered generation = %d, ok=%v; old generation was reused", generation, ok)
	}
	close(block)
	if err := <-errCh; !errors.Is(err, ErrStaleConfiguration) {
		t.Fatalf("error = %v, want ErrStaleConfiguration", err)
	}
	assertDestroyed(t, issued)
}

func TestConcurrentRetrySingleFlight(t *testing.T) {
	e := newTestEnv(t)
	block := make(chan struct{})
	started := make(chan STSRequest, 1)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	fake.started = started
	fake.release = block
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "concurrent-1")

	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	results := make(chan *IssuedCredential, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			credential, err := broker.Assume(context.Background(), e.auth, req)
			results <- credential
			errs <- err
		}()
	}
	close(start)
	<-started
	time.Sleep(20 * time.Millisecond)
	close(block)
	wg.Wait()
	close(results)
	close(errs)

	var decisionID string
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Assume: %v", err)
		}
	}
	for credential := range results {
		if credential == nil {
			t.Fatal("nil credential")
		}
		if decisionID == "" {
			decisionID = credential.DecisionID
		} else if credential.DecisionID != decisionID {
			t.Fatalf("different decision IDs: %q vs %q", decisionID, credential.DecisionID)
		}
	}
	if fake.callCount() != 1 {
		t.Fatalf("STS calls = %d, want 1", fake.callCount())
	}
	if len(broker.Decisions()) != 1 {
		t.Fatalf("audit decisions = %d, want 1", len(broker.Decisions()))
	}
}

func TestRequestIDConflict(t *testing.T) {
	t.Run("while in flight", func(t *testing.T) {
		e := newTestEnv(t)
		block := make(chan struct{})
		started := make(chan STSRequest, 1)
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			return goodCredential(req), nil
		})
		fake.started = started
		fake.release = block
		broker := newBroker(e, fake)
		req := requestFor(e, e.tenantA.TenantID, "conflict-inflight")

		firstErr := make(chan error, 1)
		go func() {
			_, err := broker.Assume(context.Background(), e.auth, req)
			firstErr <- err
		}()
		<-started
		changed := req
		changed.DurationSeconds++
		if _, err := broker.Assume(context.Background(), e.auth, changed); !errors.Is(err, ErrRequestConflict) {
			t.Fatalf("error = %v, want ErrRequestConflict", err)
		}
		close(block)
		if err := <-firstErr; err != nil {
			t.Fatal(err)
		}
		if fake.callCount() != 1 {
			t.Fatalf("STS calls = %d, want 1", fake.callCount())
		}
	})

	t.Run("after completion", func(t *testing.T) {
		e := newTestEnv(t)
		fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
			return goodCredential(req), nil
		})
		broker := newBroker(e, fake)
		req := requestFor(e, e.tenantA.TenantID, "conflict-complete")
		credential, err := broker.Assume(context.Background(), e.auth, req)
		if err != nil {
			t.Fatal(err)
		}

		changed := req
		changed.ParametersDigest = "sha256:different-parameters"
		if _, err := broker.Assume(context.Background(), e.auth, changed); !errors.Is(err, ErrRequestConflict) {
			t.Fatalf("error = %v, want ErrRequestConflict", err)
		}

		otherTenant := requestFor(e, e.tenantB.TenantID, req.RequestID)
		if _, err := broker.Assume(context.Background(), e.auth, otherTenant); !errors.Is(err, ErrRequestConflict) {
			t.Fatalf("cross-tenant request ID error = %v, want ErrRequestConflict", err)
		}

		replay, err := broker.Assume(context.Background(), e.auth, req)
		if err != nil {
			t.Fatal(err)
		}
		if replay.DecisionID != credential.DecisionID {
			t.Fatalf("replay decision = %q, want %q", replay.DecisionID, credential.DecisionID)
		}
		if fake.callCount() != 1 {
			t.Fatalf("STS calls = %d, want 1", fake.callCount())
		}
	})
}

func TestTraceabilityAcrossTwoTenants(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)

	credA, err := broker.Assume(context.Background(), e.auth, requestFor(e, e.tenantA.TenantID, "trace-a"))
	if err != nil {
		t.Fatal(err)
	}
	credB, err := broker.Assume(context.Background(), e.auth, requestFor(e, e.tenantB.TenantID, "trace-b"))
	if err != nil {
		t.Fatal(err)
	}
	if credA.DecisionID == credB.DecisionID {
		t.Fatal("two tenant decisions share one digest")
	}
	if credA.SessionID == credB.SessionID {
		t.Fatal("two decisions share one session ID")
	}

	for _, credential := range []*IssuedCredential{credA, credB} {
		decision, ok := broker.DecisionByID(credential.DecisionID)
		if !ok {
			t.Fatalf("missing decision for credential %q", credential.DecisionID)
		}
		if decision.TenantID != credential.TenantID {
			t.Fatalf("decision tenant %q != credential tenant %q", decision.TenantID, credential.TenantID)
		}
		if decision.PrincipalID != testPrincipal || credential.PrincipalID != testPrincipal {
			t.Fatal("principal is not bound to the decision")
		}
		if decision.PolicyDigest != credential.PolicyDigest {
			t.Fatal("policy digest is not traceable")
		}
		if !decision.Allowed || decision.RecomputeDigest() != credential.DecisionID {
			t.Fatalf("invalid canonical decision for %q", credential.DecisionID)
		}
	}
	if len(broker.Decisions()) != 2 {
		t.Fatalf("audit decisions = %d, want 2", len(broker.Decisions()))
	}

	replay, err := broker.Assume(context.Background(), e.auth, requestFor(e, e.tenantA.TenantID, "trace-a"))
	if err != nil {
		t.Fatal(err)
	}
	if replay.DecisionID != credA.DecisionID {
		t.Fatal("idempotent replay did not return the original decision")
	}
	if len(broker.Decisions()) != 2 {
		t.Fatal("replay incorrectly appended a new decision")
	}
}

func TestAuthorizeDoesNotCallSTS(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "authorize-1")
	decision, err := broker.Authorize(e.auth, req)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed || decision.RecomputeDigest() != decision.Digest {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if fake.callCount() != 0 {
		t.Fatal("Authorize invoked STS")
	}

	req.RequestID = "authorize-2"
	req.RoleID = e.tenantB.RoleID
	denied, err := broker.Authorize(e.auth, req)
	if !errors.Is(err, ErrRoleMismatch) {
		t.Fatalf("error = %v, want ErrRoleMismatch", err)
	}
	if denied.Allowed || denied.DenyCode != "role_mismatch" || denied.RecomputeDigest() != denied.Digest {
		t.Fatalf("unexpected denial: %+v", denied)
	}
}

func TestDecisionBindsAuthenticatedPrincipal(t *testing.T) {
	e := newTestEnv(t)
	fake := newFakeSTS(func(req STSRequest) (*STSCredential, error) {
		return goodCredential(req), nil
	})
	broker := newBroker(e, fake)
	req := requestFor(e, e.tenantA.TenantID, "principal-binding")

	first, err := broker.Authorize(AuthContext{
		PrincipalID: "platform-one",
		TenantIDs:   []string{e.tenantA.TenantID},
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := broker.Authorize(AuthContext{
		PrincipalID: "platform-two",
		TenantIDs:   []string{e.tenantA.TenantID},
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.PrincipalID == second.PrincipalID || first.Digest == second.Digest {
		t.Fatal("authenticated principal is not bound into the decision digest")
	}
	if first.SourceIdentity != "platform-one" || second.SourceIdentity != "platform-two" {
		t.Fatal("decision source identity does not match the authenticated principal")
	}
}

func TestNoSTSConfigured(t *testing.T) {
	e := newTestEnv(t)
	broker := NewBroker(e.registry, nil)
	_, err := broker.Assume(context.Background(), e.auth, requestFor(e, e.tenantA.TenantID, "no-sts"))
	if !errors.Is(err, ErrNoSTS) {
		t.Fatalf("error = %v, want ErrNoSTS", err)
	}
}

func TestRegistryValidationAndGenerations(t *testing.T) {
	t.Run("generated external IDs are 128-bit hex", func(t *testing.T) {
		first := mustExternalID(t)
		second := mustExternalID(t)
		if first == second {
			t.Fatal("external IDs repeated")
		}
		raw, err := hex.DecodeString(first)
		if err != nil || len(raw) != ExternalIDSize {
			t.Fatalf("external ID is not %d bytes of hex: %v (%d bytes)", ExternalIDSize, err, len(raw))
		}
	})

	t.Run("invalid records", func(t *testing.T) {
		reg := NewRegistry()
		base := TenantRecord{
			TenantID:          "tenant",
			RoleID:            "role",
			ExternalID:        "external",
			SourceResources:   []string{"resource"},
			MaxSessionSeconds: 60,
			PolicyDigest:      "policy",
		}
		cases := map[string]TenantRecord{}
		bad := base
		bad.PolicyDigest = ""
		cases["missing policy"] = bad
		bad = base
		bad.ExternalID = ""
		cases["missing external ID"] = bad
		bad = base
		bad.MaxSessionSeconds = 0
		cases["zero max duration"] = bad
		bad = base
		bad.SourceResources = []string{"resource", "resource"}
		cases["duplicate resource"] = bad
		bad = base
		bad.SourceResources = []string{"bad\nresource"}
		cases["control character"] = bad
		for name, record := range cases {
			if err := reg.RegisterTenant(record); !errors.Is(err, ErrInvalidTenantRecord) {
				t.Fatalf("%s: error = %v, want ErrInvalidTenantRecord", name, err)
			}
		}
	})

	t.Run("duplicate and update", func(t *testing.T) {
		reg := NewRegistry()
		record := TenantRecord{
			TenantID:          "tenant",
			RoleID:            "role",
			ExternalID:        "external",
			SourceResources:   []string{"resource"},
			MaxSessionSeconds: 60,
			PolicyDigest:      "policy-v1",
		}
		if err := reg.RegisterTenant(record); err != nil {
			t.Fatal(err)
		}
		if err := reg.RegisterTenant(record); !errors.Is(err, ErrTenantExists) {
			t.Fatalf("error = %v, want ErrTenantExists", err)
		}
		if generation, ok := reg.Generation(record.TenantID); !ok || generation != 1 {
			t.Fatalf("generation = %d, ok=%v", generation, ok)
		}
		record.PolicyDigest = "policy-v2"
		if err := reg.UpdateTenant(record); err != nil {
			t.Fatal(err)
		}
		if generation, ok := reg.Generation(record.TenantID); !ok || generation != 2 {
			t.Fatalf("generation = %d, ok=%v, want 2", generation, ok)
		}
		unknown := record
		unknown.TenantID = "missing"
		if err := reg.UpdateTenant(unknown); !errors.Is(err, ErrTenantNotFound) {
			t.Fatalf("error = %v, want ErrTenantNotFound", err)
		}
	})

	t.Run("resource transfer bumps generations", func(t *testing.T) {
		reg := NewRegistry()
		for _, id := range []string{"a", "b"} {
			record := TenantRecord{
				TenantID:          id,
				RoleID:            "role-" + id,
				ExternalID:        "external-" + id,
				SourceResources:   []string{"resource-" + id},
				MaxSessionSeconds: 60,
				PolicyDigest:      "policy-" + id,
			}
			if err := reg.RegisterTenant(record); err != nil {
				t.Fatal(err)
			}
		}
		if err := reg.SetResourceOwner("resource-a", "b"); err != nil {
			t.Fatal(err)
		}
		if generation, _ := reg.Generation("a"); generation != 2 {
			t.Fatalf("old owner generation = %d, want 2", generation)
		}
		if generation, _ := reg.Generation("b"); generation != 2 {
			t.Fatalf("new owner generation = %d, want 2", generation)
		}
		if owner, ok := reg.ResourceOwner("resource-a"); !ok || owner != "b" {
			t.Fatalf("owner = %q, ok=%v", owner, ok)
		}
		if err := reg.TransferResource("resource-a", "a", "b"); !errors.Is(err, ErrResourceOwnerMismatch) {
			t.Fatalf("error = %v, want ErrResourceOwnerMismatch", err)
		}
		if err := reg.RemoveTenant("b"); err != nil {
			t.Fatal(err)
		}
		if _, ok := reg.ResourceOwner("resource-a"); ok {
			t.Fatal("removed tenant still owns resource")
		}
	})
}

func TestAudienceDefaultsToTenant(t *testing.T) {
	e := newTestEnv(t)
	snapshot, ok := e.registry.Snapshot(e.tenantA.TenantID)
	if !ok {
		t.Fatal("missing tenant snapshot")
	}
	if snapshot.Record.Audience != e.tenantA.TenantID {
		t.Fatalf("audience = %q, want %q", snapshot.Record.Audience, e.tenantA.TenantID)
	}
}

type fixedClock struct {
	now time.Time
}

func (c *fixedClock) Now() time.Time { return c.now }
