package netsec

import "sync"

// Generation identifies one epoch of process credentials. It increases
// monotonically every time the process demotes itself, so any artifact
// stamped with an older generation is recognizably stale.
type Generation uint64

// Credential is a trusted snapshot of the process credentials taken at a
// specific generation.
type Credential struct {
	UID uint32
	GID uint32
	Gen Generation
}

// Registry is the trusted source of current process credentials. Callers
// must snapshot it at message-receive time; they must never substitute an
// identity captured earlier (for example at connection-accept time).
type Registry struct {
	mu   sync.Mutex
	cred Credential
	ok   bool
}

// NewRegistry returns a Registry holding the given initial credentials at
// generation 1.
func NewRegistry(uid, gid uint32) *Registry {
	return &Registry{cred: Credential{UID: uid, GID: gid, Gen: 1}, ok: true}
}

// Snapshot returns the current trusted credential snapshot. The second
// return value is false when no trustworthy credentials are available.
func (r *Registry) Snapshot() (Credential, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cred, r.ok
}

// Demote drops privileges to the given uid/gid and advances the
// generation, invalidating every snapshot taken before this point.
func (r *Registry) Demote(uid, gid uint32) Generation {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cred = Credential{UID: uid, GID: gid, Gen: r.cred.Gen + 1}
	r.ok = true
	return r.cred.Gen
}

// Clear removes the trusted credentials, simulating a state where the
// credential source cannot vouch for any identity.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ok = false
}
