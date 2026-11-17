package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Channel is one verified recovery endpoint, typically an email address.
type Channel struct {
	// ID is the stable identifier of the channel, e.g. the normalized email.
	ID string
	// Kind distinguishes transport families, e.g. "email" or "phone".
	Kind string
}

func (c Channel) valid() bool {
	return c.ID != "" && c.Kind != ""
}

func (c Channel) equal(other Channel) bool {
	return c.ID == other.ID && c.Kind == other.Kind
}

// Digest is the canonical summary of one replacement transaction. Both
// channel proofs are bound to the same digest.
type Digest []byte

func (d Digest) String() string {
	return base64.RawURLEncoding.EncodeToString(d)
}

// Equal reports whether two digests are byte-identical.
func (d Digest) Equal(other Digest) bool {
	return equalBytes(d, other)
}

func equalBytes(a, b []byte) bool {
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

// ChangeView is an immutable snapshot of a replacement transaction.
type ChangeView struct {
	ID           string
	AccountID    string
	Digest       Digest
	Old          Channel
	New          Channel
	Deadline     time.Time
	OldConfirmed bool
	NewConfirmed bool
	Status       Status
}

// change is the mutable transaction owned by a single account.
type change struct {
	id           string
	accountID    string
	digest       Digest
	old          Channel
	new          Channel
	deadline     time.Time
	oldConfirmed bool
	newConfirmed bool
	status       Status
}

func (c *change) view() ChangeView {
	return ChangeView{
		ID:           c.id,
		AccountID:    c.accountID,
		Digest:       append(Digest(nil), c.digest...),
		Old:          c.old,
		New:          c.new,
		Deadline:     c.deadline,
		OldConfirmed: c.oldConfirmed,
		NewConfirmed: c.newConfirmed,
		Status:       c.status,
	}
}

// accountState holds the single active channel and at most one in-flight
// transaction. Archived transactions are retained for auditing and proof
// invalidation checks.
type accountState struct {
	active   Channel
	pending  *change
	archived []change
}

// Clock lets tests and hosts inject deterministic time.
type Clock func() time.Time

// Manager serializes replacement transactions per account. It is safe for
// concurrent use.
type Manager struct {
	mu       sync.Mutex
	secret   []byte
	clock    Clock
	accounts map[string]*accountState
}

// NewManager builds a manager using wall-clock time. The secret must be
// persistent and private: it signs every confirmation proof.
func NewManager(secret []byte) (*Manager, error) {
	return NewManagerWithClock(secret, time.Now)
}

// NewManagerWithClock builds a manager with an injected clock.
func NewManagerWithClock(secret []byte, clock Clock) (*Manager, error) {
	if len(secret) == 0 {
		return nil, ErrNoManager
	}
	if clock == nil {
		return nil, fmt.Errorf("%w: nil clock", ErrNoManager)
	}
	return &Manager{
		secret:   append([]byte(nil), secret...),
		clock:    clock,
		accounts: make(map[string]*accountState),
	}, nil
}

// now reads the injected clock. State-changing callers must hold m.mu.
func (m *Manager) now() time.Time {
	return m.clock()
}

func (m *Manager) account(accountID string) (*accountState, error) {
	if accountID == "" {
		return nil, ErrInvalidAccount
	}
	acct, ok := m.accounts[accountID]
	if !ok {
		return nil, ErrNoActiveChannel
	}
	return acct, nil
}

// Provision registers the first verified recovery channel for an account.
// Re-provisioning the same account replaces its channel only when there is no
// pending transaction; hosts normally call this once at account creation.
func (m *Manager) Provision(accountID string, active Channel) error {
	if accountID == "" {
		return ErrInvalidAccount
	}
	if !active.valid() {
		return ErrInvalidChannel
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if acct, ok := m.accounts[accountID]; ok {
		if acct.pending != nil {
			return fmt.Errorf("%w: change in flight", ErrDuplicateChannel)
		}
		acct.active = active
		return nil
	}
	m.accounts[accountID] = &accountState{active: active}
	return nil
}

// computeDigest builds the change summary over the account, both channels and
// the unique change id. Including the change id makes two concurrent
// transactions over the same channel pair produce different digests.
func computeDigest(accountID string, changeID string, old, next Channel) Digest {
	h := sha256.New()
	writeLengthPrefixed := func(s string) {
		var length [8]byte
		n := len(s)
		for i := 0; i < 8; i++ {
			length[i] = byte(n >> (56 - 8*i))
		}
		h.Write(length[:])
		h.Write([]byte(s))
	}
	writeLengthPrefixed(accountID)
	writeLengthPrefixed(changeID)
	writeLengthPrefixed(old.Kind)
	writeLengthPrefixed(old.ID)
	writeLengthPrefixed(next.Kind)
	writeLengthPrefixed(next.ID)
	return Digest(h.Sum(nil))
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// Begin opens a replacement transaction from the active channel to next with
// the given lifetime. If another transaction is pending it is archived as
// superseded and every proof issued for it stops verifying. The active
// recovery channel is untouched until both confirmations arrive.
func (m *Manager) Begin(accountID string, next Channel, ttl time.Duration) (ChangeView, error) {
	if ttl <= 0 {
		return ChangeView{}, fmt.Errorf("%w: non-positive ttl", ErrExpired)
	}
	if !next.valid() {
		return ChangeView{}, ErrInvalidChannel
	}
	acct, err := m.account(accountID)
	if err != nil {
		return ChangeView{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if next.equal(acct.active) {
		return ChangeView{}, ErrDuplicateChannel
	}

	if acct.pending != nil {
		acct.pending.status = StatusSuperseded
		acct.archived = append(acct.archived, *acct.pending)
		acct.pending = nil
	}

	id, err := randomID()
	if err != nil {
		return ChangeView{}, err
	}
	txn := &change{
		id:        id,
		accountID: accountID,
		old:       acct.active,
		new:       next,
		deadline:  m.now().Add(ttl),
		status:    StatusPending,
	}
	txn.digest = computeDigest(accountID, id, txn.old, txn.new)
	acct.pending = txn
	return txn.view(), nil
}

// Pending returns the in-flight transaction, or ErrNoPendingChange when none
// exists or its term has elapsed.
func (m *Manager) Pending(accountID string) (ChangeView, error) {
	acct, err := m.account(accountID)
	if err != nil {
		return ChangeView{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	txn, err := m.livePendingLocked(acct)
	if err != nil {
		return ChangeView{}, err
	}
	return txn.view(), nil
}

// livePendingLocked returns the pending transaction after rejecting expired,
// revoked or superseded ones. Caller holds m.mu.
func (m *Manager) livePendingLocked(acct *accountState) (*change, error) {
	txn := acct.pending
	if txn == nil {
		return nil, ErrNoPendingChange
	}
	switch txn.status {
	case StatusRevoked:
		return nil, ErrRevoked
	case StatusSuperseded:
		return nil, ErrSuperseded
	}
	if !m.now().Before(txn.deadline) {
		return nil, ErrExpired
	}
	return txn, nil
}

// archivedErrorLocked maps a change id that used to exist to the sentinel for
// its terminal state, so a stale proof yields the reason it is dead instead of
// a generic lookup failure. Caller holds m.mu.
func (m *Manager) archivedErrorLocked(acct *accountState, changeID string) error {
	for i := range acct.archived {
		if acct.archived[i].id == changeID {
			switch acct.archived[i].status {
			case StatusSuperseded:
				return ErrSuperseded
			case StatusRevoked:
				return ErrRevoked
			case StatusCommitted:
				return ErrNoPendingChange
			}
		}
	}
	return ErrUnknownChange
}

// SendChallenge re-issues the confirmation proof for one role of the pending
// transaction. Re-calling after a lost response yields an identical token for
// the same digest and term, so delivery retries never create extra state.
func (m *Manager) SendChallenge(accountID, changeID string, role Role) (string, time.Time, error) {
	if !role.valid() {
		return "", time.Time{}, ErrInvalidRole
	}
	acct, err := m.account(accountID)
	if err != nil {
		return "", time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	txn, err := m.livePendingLocked(acct)
	if err != nil {
		return "", time.Time{}, err
	}
	if txn.id != changeID {
		return "", time.Time{}, m.archivedErrorLocked(acct, changeID)
	}
	token, err := EncodeProof(m.secret, txn.digest, role, txn.deadline)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, txn.deadline, nil
}

// Confirm presents a proof for one role of the pending transaction. The proof
// must carry the transaction's digest, the requested role and the
// transaction's exact deadline. Once both roles have confirmed, the active
// recovery channel is swapped to the new channel and the transaction is
// archived as committed.
func (m *Manager) Confirm(accountID, changeID string, role Role, proof string) (ChangeView, error) {
	if !role.valid() {
		return ChangeView{}, ErrInvalidRole
	}
	acct, err := m.account(accountID)
	if err != nil {
		return ChangeView{}, err
	}

	digest, proofRole, deadline, err := DecodeProof(m.secret, proof)
	if err != nil {
		return ChangeView{}, err
	}
	if proofRole != role {
		return ChangeView{}, ErrRoleMismatch
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	txn, err := m.livePendingLocked(acct)
	if err != nil {
		return ChangeView{}, err
	}
	if txn.id != changeID {
		return ChangeView{}, m.archivedErrorLocked(acct, changeID)
	}
	if !digest.Equal(txn.digest) {
		return ChangeView{}, ErrDigestMismatch
	}
	if !deadline.Equal(txn.deadline) {
		return ChangeView{}, ErrDeadlineMismatch
	}

	switch role {
	case RoleOld:
		if txn.oldConfirmed {
			return ChangeView{}, ErrRoleAlreadyDone
		}
		txn.oldConfirmed = true
	case RoleNew:
		if txn.newConfirmed {
			return ChangeView{}, ErrRoleAlreadyDone
		}
		txn.newConfirmed = true
	}

	if txn.oldConfirmed && txn.newConfirmed {
		acct.active = txn.new
		txn.status = StatusCommitted
		committed := txn.view()
		acct.archived = append(acct.archived, *txn)
		acct.pending = nil
		return committed, nil
	}
	return txn.view(), nil
}

// Revoke cancels the pending transaction before it completes. All proofs
// issued for it stop verifying immediately; the active recovery channel is
// unchanged. Revoking an expired transaction still clears it.
func (m *Manager) Revoke(accountID, changeID string) error {
	acct, err := m.account(accountID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	txn := acct.pending
	if txn == nil {
		return m.archivedErrorLocked(acct, changeID)
	}
	if txn.id != changeID {
		return m.archivedErrorLocked(acct, changeID)
	}

	expired := !m.now().Before(txn.deadline)
	txn.status = StatusRevoked
	acct.archived = append(acct.archived, *txn)
	acct.pending = nil
	if expired {
		return ErrExpired
	}
	return nil
}

// ActiveChannel returns the currently authoritative recovery channel.
func (m *Manager) ActiveChannel(accountID string) (Channel, error) {
	acct, err := m.account(accountID)
	if err != nil {
		return Channel{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !acct.active.valid() {
		return Channel{}, ErrNoActiveChannel
	}
	return acct.active, nil
}

// History returns archived transactions for an account, newest last.
func (m *Manager) History(accountID string) ([]ChangeView, error) {
	acct, err := m.account(accountID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ChangeView, 0, len(acct.archived))
	for i := range acct.archived {
		out = append(out, acct.archived[i].view())
	}
	if acct.pending != nil {
		out = append(out, acct.pending.view())
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Deadline.Equal(out[j].Deadline) {
			return out[i].ID < out[j].ID
		}
		return out[i].Deadline.Before(out[j].Deadline)
	})
	return out, nil
}
