package netsec

import (
	"errors"
	"sync"
)

var (
	errFakePurge  = errors.New("fake runtime: purge failed")
	errFakeStage  = errors.New("fake runtime: stage failed")
	errFakeCommit = errors.New("fake runtime: commit failed")
)

// fakeStaged models a staged install in an in-memory runtime. It records
// partial entries on Commit so tests can prove Abort removes them.
type fakeStaged struct {
	runtime *fakeRuntime
	cred    Credential

	committed bool
	aborted   bool
}

func (f *fakeStaged) Commit() error {
	if f.aborted {
		return errors.New("fake runtime: commit after abort")
	}
	if f.runtime.failCommit > 0 {
		f.runtime.failCommit--
		// Simulate a runtime that wrote part of the material before failing:
		// Abort must remove this partial entry.
		f.runtime.partial[f.cred.bindingKey()] = f.cred
		return errFakeCommit
	}

	f.runtime.installed[f.cred.bindingKey()] = f.cred
	f.committed = true
	return nil
}

func (f *fakeStaged) Abort() error {
	if f.aborted {
		return nil
	}
	f.aborted = true
	delete(f.runtime.installed, f.cred.bindingKey())
	delete(f.runtime.partial, f.cred.bindingKey())
	return nil
}

// fakeRuntime is an in-memory CredentialSink with deterministic fault
// injection for stage, commit and purge failures.
type fakeRuntime struct {
	mu sync.Mutex

	// installed holds credentials visible to the running environment.
	installed map[string]Credential

	// partial holds entries left behind by a failed Commit until Abort.
	partial map[string]Credential

	// staged counts every Stage call, proving stale credentials never reach
	// the runtime boundary at all.
	staged int

	// purgeCalls records every purged address in call order.
	purgeCalls []Address

	failStage  int
	failCommit int
	failPurge  int
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		installed: make(map[string]Credential),
		partial:   make(map[string]Credential),
	}
}

func (f *fakeRuntime) Purge(addr Address) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failPurge > 0 {
		f.failPurge--
		return errFakePurge
	}
	f.purgeCalls = append(f.purgeCalls, addr)

	for key := range f.installed {
		if matchesAddress(key, string(addr)) {
			delete(f.installed, key)
		}
	}
	for key := range f.partial {
		if matchesAddress(key, string(addr)) {
			delete(f.partial, key)
		}
	}
	return nil
}

func (f *fakeRuntime) Stage(cred Credential) (StagedCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failStage > 0 {
		f.failStage--
		return nil, errFakeStage
	}
	f.staged++
	return &fakeStaged{runtime: f, cred: cred}, nil
}

func (f *fakeRuntime) snapshot() map[string]Credential {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]Credential, len(f.installed))
	for key, cred := range f.installed {
		cred.Material = append([]byte(nil), cred.Material...)
		out[key] = cred
	}
	return out
}

func (f *fakeRuntime) partialSnapshot() map[string]Credential {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]Credential, len(f.partial))
	for key, cred := range f.partial {
		out[key] = cred
	}
	return out
}

func matchesAddress(key, addr string) bool {
	// bindingKey is joined with NUL: kind, instance, address, nonce, generation.
	parts := splitNUL(key)
	return len(parts) >= 3 && parts[2] == addr
}

func splitNUL(value string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == 0 {
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	return append(parts, value[start:])
}

func newTestCredential(kind CredentialKind, id InstanceID, addr Address, nonce BootNonce, gen Generation) Credential {
	return Credential{
		Kind:       kind,
		InstanceID: id,
		Address:    addr,
		Nonce:      nonce,
		Generation: gen,
		Material:   []byte("material-" + string(kind) + "-gen-" + genString(gen)),
	}
}

func genString(gen Generation) string {
	switch gen {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "x"
	}
}

func toEnvelope(cred Credential) CredentialEnvelope {
	return CredentialEnvelope{
		Kind:       string(cred.Kind),
		InstanceID: string(cred.InstanceID),
		Address:    string(cred.Address),
		Nonce:      string(cred.Nonce),
		Generation: int64(cred.Generation),
		Material:   append([]byte(nil), cred.Material...),
	}
}
