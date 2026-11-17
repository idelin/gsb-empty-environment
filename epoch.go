package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Capability is an indivisible privilege token, e.g. "msg:write".
type Capability string

// CapSet is an immutable, deduplicated set of capabilities.
type CapSet map[Capability]struct{}

// NewCapSet builds a capability set from the given tokens.
func NewCapSet(caps ...Capability) CapSet {
	set := make(CapSet, len(caps))
	for _, capName := range caps {
		set[capName] = struct{}{}
	}
	return set
}

// Contains reports whether the set holds capName.
func (s CapSet) Contains(capName Capability) bool {
	_, ok := s[capName]
	return ok
}

// SubsetOf reports whether every capability in s is also present in other.
func (s CapSet) SubsetOf(other CapSet) bool {
	for capName := range s {
		if !other.Contains(capName) {
			return false
		}
	}
	return true
}

// Equal reports whether two capability sets carry the same tokens.
func (s CapSet) Equal(other CapSet) bool {
	return len(s) == len(other) && s.SubsetOf(other)
}

func (s CapSet) sorted() []string {
	out := make([]string, 0, len(s))
	for capName := range s {
		out = append(out, string(capName))
	}
	sort.Strings(out)
	return out
}

// Credential is the trusted, immutable credential snapshot bound to one
// message at receive time. It must never be mutated after publication.
type Credential struct {
	Principal string
	Caps      CapSet
}

// PeerCred is the kernel-provided identity of a local transport peer,
// observed per message (e.g. SO_PEERCRED/SO_PEERSEC on an AF_UNIX socket).
type PeerCred struct {
	PID     int
	UID     int
	GID     int
	Context string
}

// Snapshot is the trusted credential view for one credential epoch.
type Snapshot struct {
	Epoch uint64
	Creds map[PeerCred]Credential
}

// EpochRecord keeps a historical snapshot so later audit can prove which
// identity and privileges were in force when a message was authorized.
type EpochRecord struct {
	Epoch uint64
	Creds map[PeerCred]Credential
}

// ErrInvalidDowngrade is returned when a proposed new credential table does
// not strictly reduce the privileges of the current epoch.
var ErrInvalidDowngrade = errors.New("netsec: credential downgrade must only remove capabilities and identities")

// CredentialEpoch is the credential-generation component. Every
// Downgrade call opens a new generation; lookups always return the snapshot
// of the generation currently in force at the moment of the call.
//
// A CredentialEpoch is safe for concurrent use.
type CredentialEpoch struct {
	mu      sync.RWMutex
	epoch   uint64
	current map[PeerCred]Credential
	history []EpochRecord
}

// NewCredentialEpoch creates the component with the generation-1 baseline
// credential table.
func NewCredentialEpoch(initial map[PeerCred]Credential) *CredentialEpoch {
	baseline := cloneCreds(initial)
	return &CredentialEpoch{
		epoch:   1,
		current: baseline,
		history: []EpochRecord{{Epoch: 1, Creds: baseline}},
	}
}

// CurrentEpoch returns the generation currently in force.
func (ce *CredentialEpoch) CurrentEpoch() uint64 {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return ce.epoch
}

// Lookup resolves a per-message peer identity against the snapshot of the
// generation in force at call time. The boolean is false when the identity
// has no credential in the current generation (missing credential).
func (ce *CredentialEpoch) Lookup(peer PeerCred) (Credential, uint64, bool) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	cred, ok := ce.current[peer]
	return cred, ce.epoch, ok
}

// SnapshotNow returns an immutable copy of the generation in force.
func (ce *CredentialEpoch) SnapshotNow() Snapshot {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	return Snapshot{Epoch: ce.epoch, Creds: cloneCreds(ce.current)}
}

// Downgrade opens a new generation. Privilege reduction is monotonic: the new
// table may only drop identities and capabilities; any new identity or
// capability is rejected without advancing the generation.
func (ce *CredentialEpoch) Downgrade(next map[PeerCred]Credential) (uint64, error) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	proposed := cloneCreds(next)
	for peer, cred := range proposed {
		old, ok := ce.current[peer]
		if !ok {
			return ce.epoch, fmt.Errorf("%w: new identity %+v", ErrInvalidDowngrade, peer)
		}
		if cred.Principal != old.Principal {
			return ce.epoch, fmt.Errorf("%w: principal rename for %+v", ErrInvalidDowngrade, peer)
		}
		if !cred.Caps.SubsetOf(old.Caps) {
			return ce.epoch, fmt.Errorf("%w: added capabilities %v for %s",
				ErrInvalidDowngrade, cred.Caps.diff(old.Caps), cred.Principal)
		}
	}

	ce.epoch++
	ce.current = proposed
	ce.history = append(ce.history, EpochRecord{Epoch: ce.epoch, Creds: cloneCreds(proposed)})
	return ce.epoch, nil
}

// AtEpoch returns the credential table recorded for a past generation.
func (ce *CredentialEpoch) AtEpoch(epoch uint64) (map[PeerCred]Credential, bool) {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	for _, record := range ce.history {
		if record.Epoch == epoch {
			return cloneCreds(record.Creds), true
		}
	}
	return nil, false
}

// History returns a copy of every generation record, ordered by epoch.
func (ce *CredentialEpoch) History() []EpochRecord {
	ce.mu.RLock()
	defer ce.mu.RUnlock()
	out := make([]EpochRecord, len(ce.history))
	for i, record := range ce.history {
		out[i] = EpochRecord{Epoch: record.Epoch, Creds: cloneCreds(record.Creds)}
	}
	return out
}

func cloneCreds(in map[PeerCred]Credential) map[PeerCred]Credential {
	out := make(map[PeerCred]Credential, len(in))
	for peer, cred := range in {
		caps := make(CapSet, len(cred.Caps))
		for capName := range cred.Caps {
			caps[capName] = struct{}{}
		}
		out[peer] = Credential{Principal: cred.Principal, Caps: caps}
	}
	return out
}

func (s CapSet) diff(other CapSet) []string {
	var added []string
	for capName := range s {
		if !other.Contains(capName) {
			added = append(added, string(capName))
		}
	}
	sort.Strings(added)
	return added
}
