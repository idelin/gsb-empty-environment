package netsec

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func reasonsFor(evidence *Evidence) map[SourceID]RejectionReason {
	out := map[SourceID]RejectionReason{}
	for _, rejection := range evidence.Rejected {
		out[rejection.Source] = rejection.Reason
	}
	return out
}

func TestRefreshFiltersByValidityTenantAndAudience(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

	instance := NewMutableSource(srcInstance)
	task := NewMutableSource(srcTask)
	env := NewMutableSource(srcEnv)
	peer := NewMutableSource(srcPeer)

	expired := cred(srcInstance, "expired")
	expired.ExpiresAt = now.Add(-time.Hour)
	instance.Set(expired)

	notYet := cred(srcTask, "not-yet")
	notYet.NotBefore = now.Add(time.Hour)
	task.Set(notYet)

	wrongTenant := cred(srcPeer, "wrong-tenant")
	wrongTenant.Tenant = "tenant-b"
	peer.Set(wrongTenant)

	env.Set(cred(srcEnv, "env-id"))

	selector, err := NewSelector(testGraph(t), []Source{instance, task, env, peer})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	selection, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if selection.Credential.Source != srcEnv {
		t.Fatalf("selected %q, want %q", selection.Credential.Source, srcEnv)
	}

	wantReasons := map[SourceID]RejectionReason{
		srcInstance: RejectExpired,
		srcTask:     RejectNotYetValid,
		srcPeer:     RejectTenantMismatch,
	}
	if got := reasonsFor(selection.Evidence); !reflect.DeepEqual(got, wantReasons) {
		t.Fatalf("rejections = %v, want %v", got, wantReasons)
	}

	wrongAudience := cred(srcPeer, "wrong-audience")
	wrongAudience.Audience = "other"
	peer.Set(wrongAudience)
	selection, err = selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh audience: %v", err)
	}
	if got := reasonsFor(selection.Evidence)[srcPeer]; got != RejectAudienceMismatch {
		t.Fatalf("peer rejection = %q, want %q", got, RejectAudienceMismatch)
	}

	boundary := cred(srcEnv, "boundary")
	boundary.ExpiresAt = now
	env.Set(boundary)
	if _, err := selector.Refresh(context.Background(), testRequest(now)); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("boundary refresh err = %v, want ErrNoCandidate", err)
	}
}

func TestRefreshNoCandidateRetainsCurrent(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	env := NewMutableSource(srcEnv)
	env.Set(cred(srcEnv, "env-id"))
	selector, err := NewSelector(testGraph(t), []Source{env})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	first, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("first Refresh: %v", err)
	}

	env.Revoke()
	retained, err := selector.Refresh(context.Background(), testRequest(now))
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate", err)
	}
	if retained != first || selector.Current() != first {
		t.Fatal("failed refresh must retain the current selection")
	}
}

func TestRevocationIsRecordedAndAllowsFallback(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	env := NewRevokingSource(srcEnv)
	task := NewMutableSource(srcTask)
	env.Set(cred(srcEnv, "env-id"))
	task.Set(cred(srcTask, "task-id"))
	selector, err := NewSelector(testGraph(t), []Source{env, task})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	if _, err := selector.Refresh(context.Background(), testRequest(now)); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	env.Revoke()
	selection, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh after revoke: %v", err)
	}
	if selection.Credential.Source != srcTask {
		t.Fatalf("selected %q, want task fallback", selection.Credential.Source)
	}
	if got := reasonsFor(selection.Evidence)[srcEnv]; got != RejectRevoked {
		t.Fatalf("env rejection = %q, want %q", got, RejectRevoked)
	}

	env.Set(cred(srcEnv, "env-id-2"))
	selection, err = selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh after reissue: %v", err)
	}
	if selection.Credential.Source != srcEnv {
		t.Fatalf("selected %q, want env restored", selection.Credential.Source)
	}
}

func TestSourceErrorIsRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	task := NewMutableSource(srcTask)
	task.Set(cred(srcTask, "task-id"))
	boom := &errorSource{id: srcEnv, err: errors.New("metadata unavailable")}
	selector, err := NewSelector(testGraph(t), []Source{boom, task})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	selection, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if selection.Credential.Source != srcTask {
		t.Fatalf("selected %q, want task fallback", selection.Credential.Source)
	}
	rejections := reasonsFor(selection.Evidence)
	if rejections[srcEnv] != RejectSourceError {
		t.Fatalf("env rejection = %q, want %q", rejections[srcEnv], RejectSourceError)
	}
}

type errorSource struct {
	id  SourceID
	err error
}

func (s *errorSource) ID() SourceID { return s.id }

func (s *errorSource) Credential(context.Context) (*Credential, error) {
	return nil, s.err
}

func TestNewSelectorRejectsInvalidSources(t *testing.T) {
	graph := testGraph(t)
	unknown := NewMutableSource("ghost")
	if _, err := NewSelector(graph, []Source{unknown}); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("unknown source err = %v, want ErrUnknownSource", err)
	}

	first := NewMutableSource(srcEnv)
	second := NewMutableSource(srcEnv)
	if _, err := NewSelector(graph, []Source{first, second}); !errors.Is(err, ErrDuplicateSource) {
		t.Fatalf("duplicate err = %v, want ErrDuplicateSource", err)
	}

	if _, err := NewSelector(graph, []Source{nil}); err == nil {
		t.Fatal("nil source must be rejected")
	}
	if _, err := NewSelector(nil, nil); err == nil {
		t.Fatal("nil graph must be rejected")
	}
}

func TestRefreshRejectsInvalidRequest(t *testing.T) {
	selector, err := NewSelector(testGraph(t), []Source{NewMutableSource(srcEnv)})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	if _, err := selector.Refresh(context.Background(), Request{Audience: "a"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing tenant err = %v, want ErrInvalidRequest", err)
	}
	if _, err := selector.Refresh(context.Background(), Request{Tenant: "t"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing audience err = %v, want ErrInvalidRequest", err)
	}
}

func TestEvaluatePureSelection(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	selector, err := NewSelector(testGraph(t), nil)
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	candidates := []Credential{
		cred(srcInstance, "instance-id"),
		cred(srcTask, "task-id"),
	}
	ranking, eligible, err := selector.Evaluate(testRequest(now), candidates)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if ranking.Source != srcTask {
		t.Fatalf("ranking source = %q, want %q", ranking.Source, srcTask)
	}
	if len(eligible) != 2 {
		t.Fatalf("eligible = %d, want 2", len(eligible))
	}

	candidates = append(candidates, cred(srcPeer, "peer-id"))
	if _, _, err := selector.Evaluate(testRequest(now), candidates); err == nil {
		t.Fatal("incomparable candidates must return an error")
	}
}

func TestCredentialCloneIsolatesSecret(t *testing.T) {
	original := cred(srcEnv, "env-id")
	original.Secret = []byte("secret")
	source := NewMutableSource(srcEnv)
	source.Set(original)

	got, err := source.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	got.Secret[0] = 'X'

	again, err := source.Credential(context.Background())
	if err != nil {
		t.Fatalf("Credential again: %v", err)
	}
	if string(again.Secret) != "secret" {
		t.Fatalf("stored secret mutated through returned copy: %q", again.Secret)
	}
}
