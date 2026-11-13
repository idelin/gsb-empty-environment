package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	ns "example.org/netsec/q0113"
)

type fixture struct {
	host  *ns.Host
	auth  *ns.Authenticator
	clock *ns.ManualClock
}

func newFixture() *fixture {
	clock := ns.NewManualClock(ns.Instant(100))
	host := ns.NewHost(ns.Identity{Name: "service"}, ns.WithClock(clock))
	return &fixture{host: host, auth: host.Authenticator(), clock: clock}
}

func (f *fixture) request(t *testing.T, connID, reqID string, level ns.Level, deadline ns.Instant) (*ns.Connection, *ns.Request) {
	t.Helper()
	conn, err := f.auth.OpenConnection(connID)
	if err != nil {
		t.Fatalf("OpenConnection(%q): %v", connID, err)
	}
	req, err := f.auth.BeginRequest(conn, reqID, level, deadline)
	if err != nil {
		t.Fatalf("BeginRequest(%q): %v", reqID, err)
	}
	return conn, req
}

func (f *fixture) requestFor(t *testing.T, conn *ns.Connection, reqID string, level ns.Level, deadline ns.Instant) *ns.Request {
	t.Helper()
	req, err := f.auth.BeginRequest(conn, reqID, level, deadline)
	if err != nil {
		t.Fatalf("BeginRequest(%q): %v", reqID, err)
	}
	return req
}

func deadlineNow(f *fixture) ns.Instant {
	return f.clock.Now().Add(time.Minute)
}

func requireError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestAccessMatrix(t *testing.T) {
	f := newFixture()
	cases := []struct {
		level     ns.Level
		localErr  error
		remoteErr error
	}{
		{ns.Identification, ns.ErrIdentityCannotAccessResources, ns.ErrIdentityCannotAccessResources},
		{ns.Impersonation, nil, ns.ErrRemoteRequiresDelegation},
		{ns.Delegation, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.level.String(), func(t *testing.T) {
			_, req := f.request(t, "c-"+tc.level.String(), "r-"+tc.level.String(), tc.level, deadlineNow(f))
			tok := req.Token()

			if _, err := f.host.ReadIdentity(tok); err != nil {
				t.Fatalf("ReadIdentity: %v", err)
			}
			requireError(t, f.host.CheckAccess(req, tok, ns.LocalResource), tc.localErr)
			requireError(t, f.host.CheckAccess(req, tok, ns.RemoteResource), tc.remoteErr)

			called := false
			err := f.host.Access(req, tok, ns.LocalResource, func() error {
				called = true
				return nil
			})
			if tc.localErr == nil {
				if err != nil || !called {
					t.Fatalf("local access: err=%v called=%v", err, called)
				}
			} else if called {
				t.Fatal("denied local access executed callback")
			}
		})
	}
}

func TestTokenIdentityBindingAndDeadline(t *testing.T) {
	f := newFixture()
	conn, req := f.request(t, "conn-1", "req-1", ns.Delegation, f.clock.Now().Add(100*time.Millisecond))
	tok := req.Token()
	snap, err := f.host.ReadIdentity(tok)
	if err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}
	if snap.ConnectionID() != "conn-1" || snap.RequestID() != "req-1" ||
		snap.Generation() != req.Generation() || snap.Level() != ns.Delegation {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.Deadline() != req.Deadline() {
		t.Fatalf("snapshot deadline %v != token deadline %v", snap.Deadline(), tok.Deadline())
	}

	if _, err := f.auth.IssueNested(req, ns.Delegation, req.Deadline().Add(time.Nanosecond)); !errors.Is(err, ns.ErrDeadlineExceedsRequest) {
		t.Fatalf("extended nested deadline error = %v", err)
	}
	if _, err := f.auth.IssueNested(req, ns.Level(99), req.Deadline()); !errors.Is(err, ns.ErrInvalidLevel) {
		t.Fatalf("invalid nested level error = %v", err)
	}
	nested, err := f.auth.IssueNested(req, ns.Identification, req.Deadline())
	if err != nil {
		t.Fatalf("IssueNested: %v", err)
	}
	if nested.Level() != ns.Identification || nested.RequestID() != req.ID() ||
		nested.Generation() != req.Generation() {
		t.Fatalf("nested token binding = %+v", nested.Snapshot())
	}

	f.clock.Advance(100 * time.Millisecond)
	requireError(t, tok.Valid(), ns.ErrTokenExpired)
	requireError(t, f.host.CheckAccess(req, tok, ns.LocalResource), ns.ErrTokenExpired)
	if _, err := f.host.ReadIdentity(tok); !errors.Is(err, ns.ErrTokenExpired) {
		t.Fatalf("ReadIdentity expired error = %v", err)
	}

	if err := req.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, tok.Valid(), ns.ErrRequestEnded)
	if err := conn.Reuse(); err != nil {
		t.Fatalf("Reuse: %v", err)
	}
	req2 := f.requestFor(t, conn, "req-2", ns.Impersonation, deadlineNow(f))
	if req2.Generation() <= req.Generation() {
		t.Fatalf("generation did not advance: %d -> %d", req.Generation(), req2.Generation())
	}
	requireError(t, tok.Valid(), ns.ErrRequestEnded)
	requireError(t, f.host.CheckAccess(req, tok, ns.LocalResource), ns.ErrRequestEnded)
}

func TestForgedAndPrimaryTokensNeverEnter(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "conn-1", "req-1", ns.Delegation, deadlineNow(f))
	thread := f.host.NewThread()

	var zero ns.Token
	requireError(t, thread.Enter(&zero), ns.ErrInvalidToken)
	requireError(t, thread.Enter(f.host.Primary()), ns.ErrPrimaryToken)
	if err := thread.Enter(req.Token()); err != nil {
		t.Fatalf("Enter: %v", err)
	}
	if thread.Depth() != 1 || thread.Current().ID() != req.Token().ID() {
		t.Fatalf("unexpected stack: depth=%d current=%v", thread.Depth(), thread.Current())
	}
	if err := thread.Exit(req.Token()); err != nil {
		t.Fatalf("Exit: %v", err)
	}
	requireError(t, thread.Exit(req.Token()), ns.ErrNoImpersonation)
}

func TestRequestDebugFormatting(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c", "r", ns.Impersonation, deadlineNow(f))
	if req.ID() != "r" || req.ConnectionID() != "c" || req.Level() != ns.Impersonation {
		t.Fatalf("request accessors: %s %s %s", req.ID(), req.ConnectionID(), req.Level())
	}
	if got := fmt.Sprint(req.Token().Snapshot()); got == "" {
		t.Fatal("empty snapshot string")
	}
}

func TestScopeConstrainsEnterExitAndSpawn(t *testing.T) {
	f := newFixture()
	_, req := f.request(t, "c1", "r1", ns.Delegation, deadlineNow(f))
	_, other := f.request(t, "c2", "r2", ns.Delegation, deadlineNow(f))
	scope, err := f.host.ScopeFor(req)
	if err != nil {
		t.Fatalf("ScopeFor: %v", err)
	}
	requireError(t, scope.Enter(other.Token()), ns.ErrTokenRequestMismatch)
	if err := scope.Enter(req.Token()); err != nil {
		t.Fatalf("Scope.Enter: %v", err)
	}
	if err := scope.CheckAccess(ns.LocalResource); err != nil {
		t.Fatalf("Scope.CheckAccess: %v", err)
	}

	ran := false
	task, err := scope.SpawnAsync(ns.NoIdentity, func(context.Context, *ns.TaskContext) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("Scope.SpawnAsync: %v", err)
	}
	if err := task.Run(context.Background()); err != nil {
		t.Fatalf("task.Run: %v", err)
	}
	if !ran {
		t.Fatal("scope async callback did not run")
	}

	if err := scope.Exit(req.Token()); err != nil {
		t.Fatalf("Scope.Exit: %v", err)
	}
	if err := scope.Release(); err != nil {
		t.Fatalf("Scope.Release: %v", err)
	}
	if err := req.End(); err != nil {
		t.Fatalf("End: %v", err)
	}
	requireError(t, scope.Enter(req.Token()), ns.ErrRequestEnded)
}
