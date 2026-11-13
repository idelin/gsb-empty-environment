package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Engine owns the recovery-method state machine for one or more accounts.
type Engine struct {
	store    Store
	policy   Policy
	verifier SignatureVerifier
	clock    Clock

	lockMu sync.Mutex
	locks  map[string]*sync.Mutex
}

// Option configures an Engine.
type Option func(*Engine)

// WithPolicy sets the delay, risk, and strong-factor policy.
func WithPolicy(p Policy) Option {
	return func(e *Engine) { e.policy = p.withDefaults() }
}

// WithVerifier sets the confirmation signature verifier.
func WithVerifier(v SignatureVerifier) Option {
	return func(e *Engine) {
		if v != nil {
			e.verifier = v
		}
	}
}

// WithClock sets the monotonic clock.
func WithClock(c Clock) Option {
	return func(e *Engine) {
		if c != nil {
			e.clock = c
		}
	}
}

// NewEngine creates an Engine. A nil store uses a fresh MemoryStore.
func NewEngine(store Store, opts ...Option) *Engine {
	if store == nil {
		store = NewMemoryStore()
	}
	e := &Engine{
		store:    store,
		policy:   DefaultPolicy(),
		verifier: RejectAllVerifier{},
		clock:    systemClock{},
		locks:    make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// NewInMemoryEngine is a convenience constructor for ephemeral hosts.
func NewInMemoryEngine(opts ...Option) *Engine {
	return NewEngine(NewMemoryStore(), opts...)
}

func (e *Engine) accountLock(accountID string) func() {
	e.lockMu.Lock()
	lock := e.locks[accountID]
	if lock == nil {
		lock = &sync.Mutex{}
		e.locks[accountID] = lock
	}
	e.lockMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (e *Engine) load(accountID string) (*AccountState, error) {
	state, err := e.store.Load(accountID)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, accountID)
	}
	state = state.Clone()
	if state.ID == "" {
		state.ID = accountID
	}
	if state.ID != accountID {
		return nil, fmt.Errorf("%w: stored account ID mismatch", ErrStorage)
	}
	if state.Pending == nil {
		state.Pending = make(map[string]PendingChange)
	}
	if state.Sessions == nil {
		state.Sessions = make(map[string]Session)
	}
	return state, nil
}

func (e *Engine) save(state *AccountState) error {
	if err := e.store.Save(state); err != nil {
		if errors.Is(err, ErrStorage) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return nil
}

// CreateAccount bootstraps an account. Initial entries are trusted setup state
// and become Active immediately; subsequent changes must follow the state
// machine.
func (e *Engine) CreateAccount(accountID string, initial ...Entry) (Snapshot, error) {
	if strings.TrimSpace(accountID) == "" {
		return Snapshot{}, fmt.Errorf("%w: accountID is required", ErrInvalidRequest)
	}
	unlock := e.accountLock(accountID)
	defer unlock()

	if existing, err := e.load(accountID); err == nil && existing != nil {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrAccountExists, accountID)
	} else if err != nil && !errors.Is(err, ErrAccountNotFound) {
		return Snapshot{}, err
	}

	now := e.clock.Now()
	state := &AccountState{
		ID:           accountID,
		Generation:   1,
		SessionEpoch: 1,
		Entries:      make([]Entry, 0, len(initial)),
		Pending:      make(map[string]PendingChange),
		Sessions:     make(map[string]Session),
	}
	seen := make(map[string]bool, len(initial))
	seenIDs := make(map[string]bool, len(initial))
	for _, entry := range initial {
		if strings.TrimSpace(entry.MethodID) == "" {
			return Snapshot{}, fmt.Errorf("%w: initial methodID is required", ErrInvalidRequest)
		}
		if !entry.Kind.Valid() {
			return Snapshot{}, fmt.Errorf("%w: unsupported initial method kind %q", ErrInvalidRequest, entry.Kind)
		}
		if strings.TrimSpace(entry.MaterialDigest) == "" {
			return Snapshot{}, fmt.Errorf("%w: initial material digest is required", ErrInvalidRequest)
		}
		if seen[entry.MethodID] {
			return Snapshot{}, fmt.Errorf("%w: duplicate initial methodID %q", ErrInvalidRequest, entry.MethodID)
		}
		seen[entry.MethodID] = true
		if entry.ID == "" {
			id, err := RandomEntryID()
			if err != nil {
				return Snapshot{}, err
			}
			entry.ID = id
		}
		if seenIDs[entry.ID] {
			return Snapshot{}, fmt.Errorf("%w: duplicate initial entry ID %q", ErrInvalidRequest, entry.ID)
		}
		seenIDs[entry.ID] = true
		entry.Status = StatusActive
		entry.CreatedGeneration = 1
		entry.EffectiveAt = now
		entry.NotBefore = 0
		entry.Risk = normalizeRisk(e.policy.Risk(ChangeRequest{Target: Target{Kind: entry.Kind}}))
		entry.Strong = e.policy.Strong(entry.Kind)
		state.Entries = append(state.Entries, entry)
	}
	if err := e.save(state); err != nil {
		return Snapshot{}, err
	}
	return snapshotOf(state), nil
}

// Snapshot returns a copy of the account state.
func (e *Engine) Snapshot(accountID string) (Snapshot, error) {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotOf(state), nil
}

// ConfigDigest returns the digest a new request must bind.
func (e *Engine) ConfigDigest(accountID string) (string, error) {
	snap, err := e.Snapshot(accountID)
	if err != nil {
		return "", err
	}
	return snap.ConfigDigest, nil
}

// NewChangeRequest fills the current config digest and a fresh random request
// ID. The returned value is what confirmations must sign.
func (e *Engine) NewChangeRequest(accountID, sessionID string, action Action, target Target, replaceMethodID string) (ChangeRequest, error) {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return ChangeRequest{}, err
	}
	if err := validateSession(state, sessionID); err != nil {
		return ChangeRequest{}, err
	}
	requestID, err := RandomRequestID()
	if err != nil {
		return ChangeRequest{}, err
	}
	return ChangeRequest{
		AccountID:       accountID,
		ConfigDigest:    state.configDigest(),
		SessionID:       sessionID,
		Action:          action,
		Target:          target,
		ReplaceMethodID: replaceMethodID,
		RequestID:       requestID,
	}, nil
}

// Propose validates a fully signed change and, once enough independent
// confirmations are present, records it as Pending until its policy delay
// elapses.
func (e *Engine) Propose(req ChangeRequest, confirmations ...Confirmation) (PendingChange, error) {
	if err := req.Validate(); err != nil {
		return PendingChange{}, err
	}
	unlock := e.accountLock(req.AccountID)
	defer unlock()

	state, err := e.load(req.AccountID)
	if err != nil {
		return PendingChange{}, err
	}
	now := e.clock.Now()
	pending, err := e.buildPending(state, req, confirmations, now)
	if err != nil {
		return PendingChange{}, err
	}
	state.Pending[req.RequestID] = pending
	state.Entries = append(state.Entries, pending.Entry)
	if err := e.save(state); err != nil {
		return PendingChange{}, err
	}
	return pending.clone(), nil
}

// SubmitChange is an alias for Propose.
func (e *Engine) SubmitChange(req ChangeRequest, confirmations ...Confirmation) (PendingChange, error) {
	return e.Propose(req, confirmations...)
}

func (e *Engine) buildPending(state *AccountState, req ChangeRequest, confirmations []Confirmation, now MonoTime) (PendingChange, error) {
	if _, ok := state.entryByID(req.RequestID); ok {
		return PendingChange{}, fmt.Errorf("%w: %s", ErrRequestExists, req.RequestID)
	}
	if _, ok := state.Pending[req.RequestID]; ok {
		return PendingChange{}, fmt.Errorf("%w: %s", ErrRequestExists, req.RequestID)
	}
	if state.configDigest() != req.ConfigDigest {
		return PendingChange{}, ErrConfigChanged
	}
	session, err := currentSession(state, req.SessionID)
	if err != nil {
		return PendingChange{}, err
	}

	oldEntry, err := validateTarget(state, req)
	if err != nil {
		return PendingChange{}, err
	}
	risk := normalizeRisk(e.policy.Risk(req))
	strong := e.policy.Strong(req.Target.Kind)

	if err := e.checkLastStrong(state, req, oldEntry, strong); err != nil {
		return PendingChange{}, err
	}

	records, err := e.validateConfirmations(state, req, risk, session, confirmations)
	if err != nil {
		return PendingChange{}, err
	}

	delay := e.policy.Delay(req)
	if delay < 0 {
		delay = 0
	}
	entry := Entry{
		ID:                req.RequestID,
		MethodID:          req.Target.MethodID,
		Kind:              req.Target.Kind,
		MaterialDigest:    req.Target.MaterialDigest,
		CreatedGeneration: state.Generation,
		Status:            StatusPending,
		NotBefore:         now + MonoTime(delay),
		Risk:              risk,
		Strong:            strong,
	}
	return PendingChange{
		Request:         req,
		Entry:           entry,
		Risk:            risk,
		Confirmations:   records,
		SessionEpoch:    state.SessionEpoch,
		SessionMethodID: session.MethodID,
		CreatedAt:       now,
		NotBefore:       entry.NotBefore,
	}, nil
}

func validateTarget(state *AccountState, req ChangeRequest) (Entry, error) {
	switch req.Action {
	case ActionAdd:
		if req.ReplaceMethodID != "" {
			return Entry{}, fmt.Errorf("%w: Add must not name replaceMethodID", ErrInvalidRequest)
		}
		if _, ok := state.activeByMethodID(req.Target.MethodID); ok {
			return Entry{}, fmt.Errorf("%w: %s", ErrMethodExists, req.Target.MethodID)
		}
		if _, ok := state.pendingForMethodID(req.Target.MethodID); ok {
			return Entry{}, fmt.Errorf("%w: pending change already targets %s", ErrMethodExists, req.Target.MethodID)
		}
		return Entry{}, nil

	case ActionReplace:
		old, ok := state.activeByMethodID(req.ReplaceMethodID)
		if !ok {
			return Entry{}, fmt.Errorf("%w: %s", ErrMethodNotActive, req.ReplaceMethodID)
		}
		if _, ok := state.pendingForMethodID(req.Target.MethodID); ok {
			return Entry{}, fmt.Errorf("%w: pending change already targets %s", ErrMethodExists, req.Target.MethodID)
		}
		for _, p := range state.Pending {
			if p.Request.ReplaceMethodID == req.ReplaceMethodID {
				return Entry{}, fmt.Errorf("%w: method %s already has a pending replacement", ErrMethodExists, req.ReplaceMethodID)
			}
		}
		if req.Target.MethodID != req.ReplaceMethodID {
			if _, ok := state.activeByMethodID(req.Target.MethodID); ok {
				return Entry{}, fmt.Errorf("%w: %s", ErrMethodExists, req.Target.MethodID)
			}
		}
		return old, nil

	case ActionDelete:
		if req.ReplaceMethodID != "" {
			return Entry{}, fmt.Errorf("%w: Delete must not name replaceMethodID", ErrInvalidRequest)
		}
		old, ok := state.activeByMethodID(req.Target.MethodID)
		if !ok {
			return Entry{}, fmt.Errorf("%w: %s", ErrMethodNotActive, req.Target.MethodID)
		}
		if old.Kind != req.Target.Kind {
			return Entry{}, fmt.Errorf("%w: target kind does not match active method", ErrInvalidRequest)
		}
		if _, ok := state.pendingForMethodID(req.Target.MethodID); ok {
			return Entry{}, fmt.Errorf("%w: method %s has a pending change", ErrMethodExists, req.Target.MethodID)
		}
		return old, nil
	}
	return Entry{}, fmt.Errorf("%w: unknown action", ErrInvalidRequest)
}

func (e *Engine) checkLastStrong(state *AccountState, req ChangeRequest, old Entry, newStrong bool) error {
	before := state.activeStrongCount(nil)
	if before == 0 {
		return nil
	}
	result := before
	if old.ID != "" && old.Strong {
		result--
	}
	if req.Action == ActionReplace && newStrong {
		result++
	}
	if result == 0 {
		return ErrLastStrongFactor
	}
	return nil
}

func (e *Engine) validateConfirmations(state *AccountState, req ChangeRequest, risk RiskLevel, session Session, confirmations []Confirmation) ([]ConfirmationRecord, error) {
	digest := RequestDigest(req)
	records := make([]ConfirmationRecord, 0, len(confirmations))
	seenIDs := make(map[string]bool)
	seenEntryIDs := make(map[string]bool)

	for _, confirmation := range confirmations {
		methodID := strings.TrimSpace(confirmation.MethodID)
		if methodID == "" || len(confirmation.Signature) == 0 {
			return nil, fmt.Errorf("%w: empty confirmation", ErrInsufficientConfirmation)
		}
		if seenIDs[methodID] {
			return nil, fmt.Errorf("%w: duplicate methodID %s", ErrInsufficientConfirmation, methodID)
		}
		entry, ok := state.activeByMethodID(methodID)
		if !ok {
			if _, pending := state.pendingForMethodID(methodID); pending {
				return nil, fmt.Errorf("%w: %s", ErrPendingNotUsable, methodID)
			}
			return nil, fmt.Errorf("%w: method %s is not active", ErrInsufficientConfirmation, methodID)
		}
		if !entry.Strong {
			return nil, fmt.Errorf("%w: method %s is not a strong factor", ErrInsufficientConfirmation, methodID)
		}
		if methodID == req.Target.MethodID || methodID == req.ReplaceMethodID {
			return nil, fmt.Errorf("%w: target method cannot confirm its own change", ErrInsufficientConfirmation)
		}
		if session.MethodID != "" && methodID == session.MethodID {
			return nil, fmt.Errorf("%w: current login session factor does not count", ErrInsufficientConfirmation)
		}
		if seenEntryIDs[entry.ID] {
			return nil, fmt.Errorf("%w: duplicate factor version", ErrInsufficientConfirmation)
		}
		if !e.verifier.Verify(req.AccountID, methodID, digest[:], confirmation.Signature) {
			return nil, fmt.Errorf("%w: bad signature from %s", ErrInsufficientConfirmation, methodID)
		}
		seenIDs[methodID] = true
		seenEntryIDs[entry.ID] = true
		records = append(records, ConfirmationRecord{
			MethodID:  methodID,
			EntryID:   entry.ID,
			Kind:      entry.Kind,
			Signature: append([]byte(nil), confirmation.Signature...),
		})
	}

	if risk == RiskHigh {
		if len(records) < 2 {
			return nil, fmt.Errorf("%w: high-risk change needs two strong factors", ErrInsufficientConfirmation)
		}
		categories := make(map[MethodKind]bool, len(records))
		for _, record := range records {
			categories[record.Kind] = true
		}
		if len(categories) < 2 {
			return nil, fmt.Errorf("%w: high-risk confirmations must use different categories", ErrInsufficientConfirmation)
		}
	} else if len(records) < 1 {
		return nil, fmt.Errorf("%w: change needs at least one strong factor", ErrInsufficientConfirmation)
	}
	return records, nil
}

func (e *Engine) revalidateConfirmations(state *AccountState, pending PendingChange) error {
	digest := RequestDigest(pending.Request)
	records := pending.Confirmations
	if pending.Risk == RiskHigh {
		if len(records) < 2 {
			return fmt.Errorf("%w: high-risk confirmations no longer satisfy policy", ErrInsufficientConfirmation)
		}
	} else if len(records) < 1 {
		return fmt.Errorf("%w: confirmation missing", ErrInsufficientConfirmation)
	}
	seenIDs := make(map[string]bool)
	seenEntryIDs := make(map[string]bool)
	categories := make(map[MethodKind]bool)
	for _, record := range records {
		if seenIDs[record.MethodID] || seenEntryIDs[record.EntryID] {
			return fmt.Errorf("%w: duplicate confirmation", ErrInsufficientConfirmation)
		}
		entry, ok := state.entryByID(record.EntryID)
		if !ok || entry.Status != StatusActive || entry.MethodID != record.MethodID || entry.Kind != record.Kind {
			return fmt.Errorf("%w: confirmation factor %s is no longer active", ErrInsufficientConfirmation, record.MethodID)
		}
		if !entry.Strong {
			return fmt.Errorf("%w: confirmation factor %s is no longer strong", ErrInsufficientConfirmation, record.MethodID)
		}
		if record.MethodID == pending.Request.Target.MethodID || record.MethodID == pending.Request.ReplaceMethodID {
			return fmt.Errorf("%w: invalid self-confirmation", ErrInsufficientConfirmation)
		}
		if pending.SessionMethodID != "" && record.MethodID == pending.SessionMethodID {
			return fmt.Errorf("%w: session factor cannot confirm", ErrInsufficientConfirmation)
		}
		if !e.verifier.Verify(state.ID, record.MethodID, digest[:], record.Signature) {
			return fmt.Errorf("%w: bad signature from %s", ErrInsufficientConfirmation, record.MethodID)
		}
		seenIDs[record.MethodID] = true
		seenEntryIDs[record.EntryID] = true
		categories[record.Kind] = true
	}
	if pending.Risk == RiskHigh && len(categories) < 2 {
		return fmt.Errorf("%w: high-risk confirmations need two categories", ErrInsufficientConfirmation)
	}
	return nil
}

// Activate applies a pending change. It succeeds only at or after the delay
// boundary and after every precondition has been rechecked against the current
// account state.
func (e *Engine) Activate(accountID, requestID string) (Entry, error) {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return Entry{}, err
	}
	pending, ok := state.Pending[requestID]
	if !ok {
		return Entry{}, fmt.Errorf("%w: %s", ErrRequestNotFound, requestID)
	}
	if pending.Request.RequestID != requestID || pending.Request.AccountID != accountID || pending.Entry.ID != requestID {
		return Entry{}, fmt.Errorf("%w: pending record does not match account", ErrStorage)
	}
	now := e.clock.Now()
	if now < pending.NotBefore {
		return Entry{}, ErrDelayNotElapsed
	}
	if pending.Entry.Status != StatusPending {
		return Entry{}, fmt.Errorf("%w: change is not pending", ErrRequestNotFound)
	}
	if pending.Entry.CreatedGeneration != state.Generation {
		return Entry{}, ErrConfigChanged
	}
	session, err := currentSession(state, pending.Request.SessionID)
	if err != nil {
		return Entry{}, err
	}
	if session.Epoch != pending.SessionEpoch || session.MethodID != pending.SessionMethodID {
		return Entry{}, ErrSessionRevoked
	}
	if err := e.revalidateConfirmations(state, pending); err != nil {
		return Entry{}, err
	}
	if state.configDigest() != pending.Request.ConfigDigest {
		return Entry{}, ErrConfigChanged
	}
	if err := e.revalidateTarget(state, pending); err != nil {
		return Entry{}, err
	}
	if err := e.recheckLastStrong(state, pending); err != nil {
		return Entry{}, err
	}

	newGeneration := state.Generation + 1
	updated := pending.Entry
	if pending.Request.Action == ActionDelete {
		updated.Status = StatusRevoked
	} else {
		updated.Status = StatusActive
	}
	updated.CreatedGeneration = newGeneration
	updated.EffectiveAt = now

	if err := e.ensureResultingTargetFree(state, pending); err != nil {
		return Entry{}, err
	}

	state.replaceEntry(updated)
	if pending.Request.Action == ActionReplace || pending.Request.Action == ActionDelete {
		e.revokeOldTarget(state, pending.Request, updated.ID, now)
	}
	state.Generation = newGeneration
	state.invalidateOtherPending(requestID, now)
	delete(state.Pending, requestID)
	if err := e.save(state); err != nil {
		return Entry{}, err
	}
	return updated.Clone(), nil
}

func replacementTargetID(state *AccountState, req ChangeRequest) string {
	if req.Action == ActionReplace || req.Action == ActionDelete {
		if old, ok := state.activeByMethodID(req.ReplaceMethodID); ok && req.Action == ActionReplace {
			return old.ID
		}
		if old, ok := state.activeByMethodID(req.Target.MethodID); ok {
			return old.ID
		}
	}
	return ""
}

func (e *Engine) revalidateTarget(state *AccountState, pending PendingChange) error {
	req := pending.Request
	switch req.Action {
	case ActionAdd:
		if _, ok := state.activeByMethodID(req.Target.MethodID); ok {
			return fmt.Errorf("%w: %s", ErrMethodExists, req.Target.MethodID)
		}
		for id, other := range state.Pending {
			if id != req.RequestID && other.Entry.MethodID == req.Target.MethodID {
				return fmt.Errorf("%w: concurrent pending change targets %s", ErrMethodExists, req.Target.MethodID)
			}
		}
	case ActionReplace:
		if _, ok := state.activeByMethodID(req.ReplaceMethodID); !ok {
			return fmt.Errorf("%w: %s", ErrMethodNotActive, req.ReplaceMethodID)
		}
		if req.Target.MethodID != req.ReplaceMethodID {
			if _, ok := state.activeByMethodID(req.Target.MethodID); ok {
				return fmt.Errorf("%w: %s", ErrMethodExists, req.Target.MethodID)
			}
		}
	case ActionDelete:
		if _, ok := state.activeByMethodID(req.Target.MethodID); !ok {
			return fmt.Errorf("%w: %s", ErrMethodNotActive, req.Target.MethodID)
		}
	}
	return nil
}

func (e *Engine) recheckLastStrong(state *AccountState, pending PendingChange) error {
	before := state.activeStrongCount(nil)
	if before == 0 {
		return nil
	}
	result := before
	if id := replacementTargetID(state, pending.Request); id != "" {
		if entry, ok := state.entryByID(id); ok && entry.Strong {
			result--
		}
	}
	if pending.Request.Action == ActionReplace && pending.Entry.Strong {
		result++
	}
	if result == 0 {
		return ErrLastStrongFactor
	}
	return nil
}

func (e *Engine) ensureResultingTargetFree(state *AccountState, pending PendingChange) error {
	if pending.Request.Action != ActionReplace {
		return nil
	}
	for _, entry := range state.Entries {
		if entry.Status != StatusActive || entry.MethodID != pending.Request.Target.MethodID {
			continue
		}
		if entry.MethodID == pending.Request.ReplaceMethodID {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrMethodExists, pending.Request.Target.MethodID)
	}
	return nil
}

func (e *Engine) revokeOldTarget(state *AccountState, req ChangeRequest, newEntryID string, now MonoTime) {
	targetMethodID := req.Target.MethodID
	if req.Action == ActionReplace {
		targetMethodID = req.ReplaceMethodID
	}
	for i := range state.Entries {
		entry := &state.Entries[i]
		if entry.Status != StatusActive || entry.MethodID != targetMethodID || entry.ID == newEntryID {
			continue
		}
		entry.Status = StatusRevoked
		entry.EffectiveAt = now
	}
}

// Cancel revokes a pending change. Any currently active strong factor may
// cancel, including a factor that also confirmed the change.
func (e *Engine) Cancel(accountID, requestID, factorMethodID string) error {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return err
	}
	pending, ok := state.Pending[requestID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrRequestNotFound, requestID)
	}
	if pending.Request.RequestID != requestID || pending.Request.AccountID != accountID || pending.Entry.ID != requestID {
		return fmt.Errorf("%w: pending record does not match account", ErrStorage)
	}
	factor, ok := state.activeByMethodID(factorMethodID)
	if !ok || !factor.Strong {
		return fmt.Errorf("%w: %s", ErrMethodNotActive, factorMethodID)
	}
	now := e.clock.Now()
	pending.Entry.Status = StatusRevoked
	pending.Entry.EffectiveAt = now
	state.replaceEntry(pending.Entry)
	delete(state.Pending, requestID)
	return e.save(state)
}

// ResetPassword revokes every existing session. Pending changes remain
// recorded but can no longer activate with the revoked session.
func (e *Engine) ResetPassword(accountID string) error {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return err
	}
	state.SessionEpoch++
	state.PasswordResetAt = e.clock.Now()
	state.Sessions = make(map[string]Session)
	return e.save(state)
}

// OpenSession records an authenticated session. A non-empty methodID must be
// an Active factor; pending methods cannot be used to log in.
func (e *Engine) OpenSession(accountID, sessionID, methodID string) (Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Session{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return Session{}, err
	}
	if methodID != "" {
		entry, ok := state.activeByMethodID(methodID)
		if !ok {
			if _, pending := state.pendingForMethodID(methodID); pending {
				return Session{}, ErrPendingNotUsable
			}
			return Session{}, fmt.Errorf("%w: %s", ErrMethodNotActive, methodID)
		}
		methodID = entry.MethodID
	}
	session := Session{
		ID:        sessionID,
		MethodID:  methodID,
		Epoch:     state.SessionEpoch,
		CreatedAt: e.clock.Now(),
	}
	state.Sessions[sessionID] = session
	if err := e.save(state); err != nil {
		return Session{}, err
	}
	return session, nil
}

// CloseSession removes a session.
func (e *Engine) CloseSession(accountID, sessionID string) error {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return err
	}
	delete(state.Sessions, sessionID)
	return e.save(state)
}

// Authenticate returns the Active entry for methodID. Pending entries are
// never usable for login.
func (e *Engine) Authenticate(accountID, methodID string) (Entry, error) {
	if entry, err := e.lookupUsableMethod(accountID, methodID); err != nil {
		return Entry{}, err
	} else {
		return entry, nil
	}
}

// Login is an alias for Authenticate.
func (e *Engine) Login(accountID, methodID string) (Entry, error) {
	return e.Authenticate(accountID, methodID)
}

// Recover returns the Active entry for methodID. Pending entries can never
// complete account recovery.
func (e *Engine) Recover(accountID, methodID string) (Entry, error) {
	return e.lookupUsableMethod(accountID, methodID)
}

func (e *Engine) lookupUsableMethod(accountID, methodID string) (Entry, error) {
	unlock := e.accountLock(accountID)
	defer unlock()
	state, err := e.load(accountID)
	if err != nil {
		return Entry{}, err
	}
	if entry, ok := state.activeByMethodID(methodID); ok {
		return entry.Clone(), nil
	}
	if _, pending := state.pendingForMethodID(methodID); pending {
		return Entry{}, fmt.Errorf("%w: %s", ErrPendingNotUsable, methodID)
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrMethodNotActive, methodID)
}

// SortedEntries returns entries in the same deterministic order used by
// Snapshot.
func SortedEntries(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sortEntries(out)
	return out
}

func validateSession(state *AccountState, sessionID string) error {
	_, err := currentSession(state, sessionID)
	return err
}

func currentSession(state *AccountState, sessionID string) (Session, error) {
	session, ok := state.Sessions[sessionID]
	if !ok || session.Epoch != state.SessionEpoch {
		return Session{}, fmt.Errorf("%w: %s", ErrSessionRevoked, sessionID)
	}
	return session, nil
}

func normalizeRisk(risk RiskLevel) RiskLevel {
	if risk == RiskLow {
		return RiskLow
	}
	return RiskHigh
}

// DelayFor exposes the policy delay for a request. It is useful when callers
// want to display the exact activation boundary.
func (e *Engine) DelayFor(req ChangeRequest) time.Duration {
	delay := e.policy.Delay(req)
	if delay < 0 {
		return 0
	}
	return delay
}

// ActiveStrongFactors returns the currently active strong factors.
func (e *Engine) ActiveStrongFactors(accountID string) ([]Entry, error) {
	snap, err := e.Snapshot(accountID)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, entry := range snap.ActiveEntries() {
		if entry.Strong {
			out = append(out, entry)
		}
	}
	return out, nil
}
