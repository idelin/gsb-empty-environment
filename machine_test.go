package netsec_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.org/netsec/q0098"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestMachine(t *testing.T) (*netsec.StateMachine, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)}
	return netsec.NewStateMachineWithClock(clock.Now), clock
}

func TestCreatorFirstUseSucceeds(t *testing.T) {
	machine, clock := newTestMachine(t)
	const (
		requestURI = "urn:ietf:params:oauth:request_uri:one"
		clientID   = "client-a"
	)

	if err := machine.Issue(requestURI, clientID, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := machine.Use(requestURI, clientID); err != nil {
		t.Fatalf("first Use() error = %v", err)
	}
	if status, ok := machine.Status(requestURI); !ok || status != netsec.StatusUsed {
		t.Fatalf("Status() = (%q, %v), want (%q, true)", status, ok, netsec.StatusUsed)
	}
}

func TestSameReferenceTwiceAndAnotherClient(t *testing.T) {
	machine, clock := newTestMachine(t)
	const (
		requestURI = "urn:ietf:params:oauth:request_uri:replay"
		clientID   = "client-a"
		otherID    = "client-b"
	)

	if err := machine.Issue(requestURI, clientID, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := machine.Use(requestURI, clientID); err != nil {
		t.Fatalf("first Use() error = %v", err)
	}
	if err := machine.Use(requestURI, clientID); !errors.Is(err, netsec.ErrReferenceUsed) {
		t.Fatalf("second Use() error = %v, want %v", err, netsec.ErrReferenceUsed)
	}
	if err := machine.Use(requestURI, otherID); !errors.Is(err, netsec.ErrWrongClient) {
		t.Fatalf("other client Use() error = %v, want %v", err, netsec.ErrWrongClient)
	}
}

func TestAnotherClientCannotConsumeBeforeCreator(t *testing.T) {
	machine, clock := newTestMachine(t)
	const (
		requestURI = "urn:ietf:params:oauth:request_uri:bound"
		clientID   = "client-a"
		otherID    = "client-b"
	)

	if err := machine.Issue(requestURI, clientID, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := machine.Use(requestURI, otherID); !errors.Is(err, netsec.ErrWrongClient) {
		t.Fatalf("other client Use() error = %v, want %v", err, netsec.ErrWrongClient)
	}
	if err := machine.Use(requestURI, clientID); err != nil {
		t.Fatalf("creator Use() after other client attempt = %v, want nil", err)
	}
}

func TestReferenceExpires(t *testing.T) {
	machine, clock := newTestMachine(t)
	const (
		requestURI = "urn:ietf:params:oauth:request_uri:expiring"
		clientID   = "client-a"
	)

	if err := machine.Issue(requestURI, clientID, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	clock.Advance(time.Minute)

	if err := machine.Use(requestURI, clientID); !errors.Is(err, netsec.ErrReferenceExpired) {
		t.Fatalf("Use() error = %v, want %v", err, netsec.ErrReferenceExpired)
	}
	if status, ok := machine.Status(requestURI); !ok || status != netsec.StatusExpired {
		t.Fatalf("Status() = (%q, %v), want (%q, true)", status, ok, netsec.StatusExpired)
	}
}

func TestConcurrentUseAllowsExactlyOneSuccess(t *testing.T) {
	machine, clock := newTestMachine(t)
	const (
		requestURI = "urn:ietf:params:oauth:request_uri:race"
		clientID   = "client-a"
		workers    = 64
	)

	if err := machine.Issue(requestURI, clientID, clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	start := make(chan struct{})
	var successes atomic.Int32
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			<-start
			if err := machine.Use(requestURI, clientID); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, netsec.ErrReferenceUsed) {
				t.Errorf("Use() error = %v, want %v", err, netsec.ErrReferenceUsed)
			}
		}()
	}
	close(start)
	wait.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful uses = %d, want 1", got)
	}
}

func TestDuplicateIssueIsRejected(t *testing.T) {
	machine, clock := newTestMachine(t)
	const requestURI = "urn:ietf:params:oauth:request_uri:duplicate"

	if err := machine.Issue(requestURI, "client-a", clock.Now().Add(time.Minute)); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if err := machine.Issue(requestURI, "client-b", clock.Now().Add(time.Minute)); !errors.Is(err, netsec.ErrReferenceExists) {
		t.Fatalf("duplicate Issue() error = %v, want %v", err, netsec.ErrReferenceExists)
	} else if containsRequestURI(err, requestURI) {
		t.Fatalf("duplicate Issue() error %q leaks request_uri", err)
	}
	if err := machine.Use(requestURI, "client-a"); err != nil {
		t.Fatalf("original creator Use() error = %v", err)
	}
}

func containsRequestURI(err error, requestURI string) bool {
	return err != nil && len(requestURI) > 0 && strings.Contains(err.Error(), requestURI)
}

func TestInvalidIdentifiersAreRejected(t *testing.T) {
	machine := netsec.NewStateMachine()
	expiresAt := time.Now().Add(time.Minute)

	for _, test := range []struct {
		name       string
		requestURI string
		clientID   string
	}{
		{name: "missing request URI", clientID: "client-a"},
		{name: "missing client ID", requestURI: "urn:ietf:params:oauth:request_uri:missing-client"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := machine.Issue(test.requestURI, test.clientID, expiresAt); !errors.Is(err, netsec.ErrInvalidReference) {
				t.Fatalf("Issue() error = %v, want %v", err, netsec.ErrInvalidReference)
			}
			if err := machine.Use(test.requestURI, test.clientID); !errors.Is(err, netsec.ErrInvalidReference) {
				t.Fatalf("Use() error = %v, want %v", err, netsec.ErrInvalidReference)
			}
		})
	}
}
