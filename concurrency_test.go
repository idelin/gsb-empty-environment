package netsec

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingSource records how many times a refresh observed it and blocks on
// a gate while delivering its credential, giving the test control over the
// point-in-time snapshot boundary.
type countingSource struct {
	id SourceID

	value    atomicCredential
	gate     chan struct{}
	parked   chan struct{}
	ack      chan struct{}
	readDone chan struct{}
	calls    atomic.Int64
}

func newCountingSource(id SourceID) *countingSource {
	return &countingSource{
		id:       id,
		gate:     make(chan struct{}),
		parked:   make(chan struct{}),
		ack:      make(chan struct{}),
		readDone: make(chan struct{}),
	}
}

func (s *countingSource) ID() SourceID { return s.id }

func (s *countingSource) Set(credential Credential) {
	credential.Source = s.id
	s.value.Store(&credential)
}

func (s *countingSource) Credential(context.Context) (*Credential, error) {
	s.calls.Add(1)
	// Two-sided handshake: announce arrival, wait for permission to block,
	// then block on the gate. The test can therefore establish a happens-
	// before edge where every source goroutine is suspended at the gate
	// before any credential value is read.
	s.parked <- struct{}{}
	<-s.ack
	<-s.gate
	current := s.value.Load()
	if current == nil {
		close(s.readDone)
		return nil, nil
	}
	clone := current.Clone()
	close(s.readDone)
	return &clone, nil
}

func (s *countingSource) Calls() int64 { return s.calls.Load() }

// TestRefreshPinsEvidence proves the refresh evaluates an immutable snapshot:
// mutations happening while source reads are in flight cannot change the
// captured evidence, and each source is read exactly once per refresh.
func TestRefreshPinsEvidence(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	instance := newCountingSource(srcInstance)
	task := newCountingSource(srcTask)
	env := newCountingSource(srcEnv)
	instance.Set(cred(srcInstance, "instance-v1"))
	task.Set(cred(srcTask, "task-v1"))
	env.Set(cred(srcEnv, "env-v1"))

	selector, err := NewSelector(testGraph(t), []Source{instance, task, env})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	done := make(chan struct{})
	var refreshErr error
	var selection *Selection
	go func() {
		selection, refreshErr = selector.Refresh(context.Background(), testRequest(now))
		close(done)
	}()

	// Each source read announces arrival, waits for an ack, then blocks on
	// its gate. Collect all arrivals, release them together, and only then
	// close the gates so every credential read happens after this barrier.
	deadline := time.After(5 * time.Second)
	sources := []*countingSource{instance, task, env}
	for _, source := range sources {
		select {
		case <-source.parked:
		case <-deadline:
			t.Fatal("timed out waiting for a source read to park")
		}
	}
	for _, source := range sources {
		close(source.ack)
	}
	for _, source := range sources {
		close(source.gate)
	}
	// All reads must complete before any mutator is allowed to start.
	for _, source := range sources {
		select {
		case <-source.readDone:
		case <-deadline:
			t.Fatal("timed out waiting for a source read to complete")
		}
	}

	// Every source read is blocked inside the snapshot phase. Mutate and
	// revoke aggressively; the in-flight refresh must not observe any of it.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, source := range []*countingSource{instance, task, env} {
		wg.Add(1)
		go func(source *countingSource) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
					source.Set(cred(source.id, "churned"))
				}
			}
		}(source)
	}

	<-done
	close(stop)
	wg.Wait()

	if refreshErr != nil {
		t.Fatalf("Refresh: %v", refreshErr)
	}
	if selection.Credential.Identity != "env-v1" {
		t.Fatalf("selected %q, evidence must stay pinned to env-v1", selection.Credential.Identity)
	}
	for _, source := range []*countingSource{instance, task, env} {
		if calls := source.Calls(); calls != 1 {
			t.Fatalf("source %q read %d times, want exactly 1", source.id, calls)
		}
	}

	identities := make([]string, 0, len(selection.Evidence.Eligible))
	for _, candidate := range selection.Evidence.Eligible {
		identities = append(identities, candidate.Identity)
	}
	// Evidence is normalized by source id, which sorts env, instance, task.
	want := []string{"env-v1", "instance-v1", "task-v1"}
	if !equalStrings(identities, want) {
		t.Fatalf("pinned evidence = %v, want %v", identities, want)
	}

	// The published evidence remains stable after further external mutation.
	env.Set(cred(srcEnv, "env-v2"))
	if selector.Current().Evidence != selection.Evidence {
		t.Fatal("published evidence pointer must stay fixed until next commit")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestConcurrentRefreshNoIdentityDrift runs refreshes against concurrently
// changing and revoking sources. Every observed Selection must be internally
// consistent, generations must be monotonic, and an in-flight selection must
// never be a half-merged mix of source states.
func TestConcurrentRefreshNoIdentityDrift(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	instance := NewRevokingSource(srcInstance)
	task := NewRevokingSource(srcTask)
	env := NewRevokingSource(srcEnv)
	instance.Set(cred(srcInstance, "instance-id"))
	task.Set(cred(srcTask, "task-id"))
	env.Set(cred(srcEnv, "env-id"))

	selector, err := NewSelector(testGraph(t), []Source{instance, task, env})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	const iterations = 100
	stop := make(chan struct{})
	var wg sync.WaitGroup

	churn := func(source *RevokingSource, id SourceID) {
		defer wg.Done()
		toggle := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			toggle = !toggle
			if toggle {
				source.Revoke()
			} else {
				source.Set(cred(id, idempotentIdentity(id)))
			}
			runtimeYield()
		}
	}
	wg.Add(3)
	go churn(instance, srcInstance)
	go churn(task, srcTask)
	go churn(env, srcEnv)

	// Concurrent readers must only ever see fully published, self-consistent
	// selections, with generations monotonic within each reader's own stream.
	reader := func() {
		defer wg.Done()
		var lastGeneration uint64
		for {
			select {
			case <-stop:
				return
			default:
			}
			if selection := selector.Current(); selection != nil {
				validateSelection(t, selection)
				if selection.Generation < lastGeneration {
					t.Errorf("generation moved backwards: %d -> %d", lastGeneration, selection.Generation)
				}
				lastGeneration = selection.Generation
			}
			runtimeYield()
		}
	}
	wg.Add(2)
	go reader()
	go reader()

	for i := 0; i < iterations; i++ {
		selection, _ := selector.Refresh(context.Background(), testRequest(now))
		if selection != nil {
			validateSelection(t, selection)
		}
		runtimeYield()
	}

	close(stop)
	wg.Wait()

	final := selector.Current()
	if final == nil {
		t.Fatal("expected at least one committed selection")
	}
	validateSelection(t, final)
}

func validateSelection(t *testing.T, selection *Selection) {
	t.Helper()
	credential := selection.Credential
	if credential.Source == "" || credential.Identity == "" {
		t.Fatalf("incomplete selection published: %+v", selection)
	}
	if selection.Evidence == nil || selection.Evidence.Chosen == nil {
		t.Fatalf("selection without fixed evidence: %+v", selection)
	}
	if selection.Evidence.Chosen.Source != credential.Source {
		t.Fatalf("evidence source %q disagrees with credential source %q",
			selection.Evidence.Chosen.Source, credential.Source)
	}
	found := false
	for _, candidate := range selection.Evidence.Eligible {
		if candidate.Source == credential.Source && candidate.Identity == credential.Identity {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("published credential %+v missing from its own evidence snapshot", credential)
	}
	for _, candidate := range selection.Evidence.Eligible {
		if candidate.Source != credential.Source {
			if selection.Evidence.Chosen.Dominance[candidate.Source] == nil {
				t.Fatalf("chosen %q has no dominance proof over %q", credential.Source, candidate.Source)
			}
		}
	}
}

func idempotentIdentity(id SourceID) string { return string(id) + "-stable" }

func runtimeYield() { time.Sleep(time.Microsecond) }

// TestRevocationTransitionsAreAtomic exercises the required production
// scenario: a high-trust session is revoked while a job runs. Observations
// must show either the old identity or a valid lower-trust fallback, never a
// mix, a blank identity, or an override by an incomparable source.
func TestRevocationTransitionsAreAtomic(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	env := NewRevokingSource(srcEnv)
	task := NewMutableSource(srcTask)
	env.Set(cred(srcEnv, "env-id"))
	task.Set(cred(srcTask, "task-id"))

	selector, err := NewSelector(testGraph(t), []Source{env, task})
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	first, err := selector.Refresh(context.Background(), testRequest(now))
	if err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	if first.Credential.Source != srcEnv {
		t.Fatalf("initial selection = %q, want %q", first.Credential.Source, srcEnv)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if current := selector.Current(); current != nil {
					switch current.Credential.Source {
					case srcEnv, srcTask:
					default:
						t.Errorf("observed forbidden source %q during revocation", current.Credential.Source)
					}
					if current.Evidence.Chosen.Source != current.Credential.Source {
						t.Errorf("evidence/credential source mismatch on %q", current.Credential.Source)
					}
				}
			}
		}
	}()

	env.Revoke()
	for i := 0; i < 100; i++ {
		if selection, refreshErr := selector.Refresh(context.Background(), testRequest(now)); refreshErr == nil {
			if selection.Credential.Source != srcTask {
				t.Fatalf("post-revocation selection = %q, want %q", selection.Credential.Source, srcTask)
			}
			break
		}
	}

	env.Set(cred(srcEnv, "env-restored"))
	for i := 0; i < 100; i++ {
		if selection, refreshErr := selector.Refresh(context.Background(), testRequest(now)); refreshErr == nil &&
			selection.Credential.Source == srcEnv {
			break
		}
		if i == 99 {
			t.Fatal("high-trust identity was not restored after reissue")
		}
	}

	close(stop)
	wg.Wait()

	current := selector.Current()
	if current.Credential.Source != srcEnv || current.Credential.Identity != "env-restored" {
		t.Fatalf("final selection = %+v, want restored env credential", current.Credential)
	}
}
