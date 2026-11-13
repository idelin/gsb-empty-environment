package netsec

import (
	"sort"
)

// Session is an authenticated account session. MethodID is the factor used to
// create the session; an empty MethodID denotes a password-only management
// session, which is never a change confirmation.
type Session struct {
	ID        string   `json:"id"`
	MethodID  string   `json:"methodID,omitempty"`
	Epoch     uint64   `json:"epoch"`
	CreatedAt MonoTime `json:"createdAt"`
}

// AccountState is the durable account record. Stores may serialize it as-is;
// callers should treat it as owned by the Engine and use Clone when retaining
// a copy.
type AccountState struct {
	ID              string                   `json:"id"`
	Generation      uint64                   `json:"generation"`
	SessionEpoch    uint64                   `json:"sessionEpoch"`
	PasswordResetAt MonoTime                 `json:"passwordResetAt,omitempty"`
	Entries         []Entry                  `json:"entries"`
	Pending         map[string]PendingChange `json:"pending"`
	Sessions        map[string]Session       `json:"sessions"`
}

// Clone returns a deep copy of the account state.
func (a *AccountState) Clone() *AccountState {
	if a == nil {
		return nil
	}
	out := &AccountState{
		ID:              a.ID,
		Generation:      a.Generation,
		SessionEpoch:    a.SessionEpoch,
		PasswordResetAt: a.PasswordResetAt,
		Entries:         make([]Entry, len(a.Entries)),
		Pending:         make(map[string]PendingChange, len(a.Pending)),
		Sessions:        make(map[string]Session, len(a.Sessions)),
	}
	copy(out.Entries, a.Entries)
	for k, v := range a.Pending {
		out.Pending[k] = v.clone()
	}
	for k, v := range a.Sessions {
		out.Sessions[k] = v
	}
	return out
}

// Snapshot is an immutable view of an account.
type Snapshot struct {
	AccountID       string          `json:"accountID"`
	Generation      uint64          `json:"generation"`
	SessionEpoch    uint64          `json:"sessionEpoch"`
	ConfigDigest    string          `json:"configDigest"`
	PasswordResetAt MonoTime        `json:"passwordResetAt,omitempty"`
	Entries         []Entry         `json:"entries"`
	Pending         []PendingChange `json:"pending"`
	Sessions        []Session       `json:"sessions"`
}

// ActiveEntries returns the currently active methods in deterministic order.
func (s Snapshot) ActiveEntries() []Entry {
	out := make([]Entry, 0, len(s.Entries))
	for _, e := range s.Entries {
		if e.Status == StatusActive {
			out = append(out, e)
		}
	}
	sortEntries(out)
	return out
}

// PendingChange returns the pending change with requestID, if any.
func (s Snapshot) PendingChange(requestID string) (PendingChange, bool) {
	for _, p := range s.Pending {
		if p.Request.RequestID == requestID {
			return p, true
		}
	}
	return PendingChange{}, false
}

// ActiveMethod returns the active entry with methodID.
func (s Snapshot) ActiveMethod(methodID string) (Entry, bool) {
	for _, e := range s.Entries {
		if e.Status == StatusActive && e.MethodID == methodID {
			return e, true
		}
	}
	return Entry{}, false
}

func (a *AccountState) configDigest() string {
	return ConfigDigest(a.ID, a.Entries)
}

func (a *AccountState) activeEntries() []Entry {
	out := make([]Entry, 0, len(a.Entries))
	for _, e := range a.Entries {
		if e.Status == StatusActive {
			out = append(out, e)
		}
	}
	return out
}

func (a *AccountState) activeByMethodID(methodID string) (Entry, bool) {
	for _, e := range a.Entries {
		if e.Status == StatusActive && e.MethodID == methodID {
			return e, true
		}
	}
	return Entry{}, false
}

func (a *AccountState) entryByID(id string) (Entry, bool) {
	for _, e := range a.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func (a *AccountState) hasMethodID(methodID string) bool {
	for _, e := range a.Entries {
		if e.MethodID == methodID && (e.Status == StatusActive || e.Status == StatusPending) {
			return true
		}
	}
	return false
}

func (a *AccountState) pendingForMethodID(methodID string) (PendingChange, bool) {
	for _, p := range a.Pending {
		if p.Entry.MethodID == methodID {
			return p, true
		}
	}
	return PendingChange{}, false
}

func (a *AccountState) activeStrongCount(excludeEntryIDs map[string]bool) int {
	count := 0
	for _, e := range a.Entries {
		if e.Status != StatusActive || !e.Strong {
			continue
		}
		if excludeEntryIDs != nil && excludeEntryIDs[e.ID] {
			continue
		}
		count++
	}
	return count
}

func (a *AccountState) replaceEntry(updated Entry) {
	for i := range a.Entries {
		if a.Entries[i].ID == updated.ID {
			a.Entries[i] = updated
			return
		}
	}
	a.Entries = append(a.Entries, updated)
}

func (a *AccountState) invalidateOtherPending(exceptRequestID string, now MonoTime) {
	for id, p := range a.Pending {
		if id == exceptRequestID {
			continue
		}
		p.Entry.Status = StatusRevoked
		p.Entry.EffectiveAt = now
		a.replaceEntry(p.Entry)
		delete(a.Pending, id)
	}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].MethodID != entries[j].MethodID {
			return entries[i].MethodID < entries[j].MethodID
		}
		if entries[i].CreatedGeneration != entries[j].CreatedGeneration {
			return entries[i].CreatedGeneration < entries[j].CreatedGeneration
		}
		return entries[i].ID < entries[j].ID
	})
}

func cloneConfirmations(in []ConfirmationRecord) []ConfirmationRecord {
	out := make([]ConfirmationRecord, len(in))
	for i := range in {
		out[i] = in[i].clone()
	}
	return out
}

func cloneSessionsMap(in map[string]Session) []Session {
	out := make([]Session, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func snapshotOf(a *AccountState) Snapshot {
	entries := make([]Entry, len(a.Entries))
	copy(entries, a.Entries)
	sortEntries(entries)

	pending := make([]PendingChange, 0, len(a.Pending))
	for _, p := range a.Pending {
		pending = append(pending, p.clone())
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].NotBefore != pending[j].NotBefore {
			return pending[i].NotBefore < pending[j].NotBefore
		}
		return pending[i].Request.RequestID < pending[j].Request.RequestID
	})

	return Snapshot{
		AccountID:       a.ID,
		Generation:      a.Generation,
		SessionEpoch:    a.SessionEpoch,
		ConfigDigest:    a.configDigest(),
		PasswordResetAt: a.PasswordResetAt,
		Entries:         entries,
		Pending:         pending,
		Sessions:        cloneSessionsMap(a.Sessions),
	}
}
