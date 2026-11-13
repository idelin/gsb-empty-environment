package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

var errNoChange = errors.New("no state change")

type commitError struct {
	err error
}

func (e *commitError) Error() string { return e.err.Error() }
func (e *commitError) Unwrap() error { return e.err }

func commitWithError(err error) error { return &commitError{err: err} }

type Host struct {
	store Store
	rand  io.Reader
	mu    sync.Mutex
}

type Option func(*Host)

func WithRandomSource(source io.Reader) Option {
	return func(h *Host) {
		if source != nil {
			h.rand = source
		}
	}
}

func NewHost(store Store, options ...Option) *Host {
	if store == nil {
		store = NewMemoryStore()
	}
	host := &Host{store: store}
	for _, option := range options {
		if option != nil {
			option(host)
		}
	}
	return host
}

func NewMemoryHost(options ...Option) *Host {
	return NewHost(NewMemoryStore(), options...)
}

func (h *Host) CreateAccount(account Account) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if account.ID == "" {
		return ErrInvalidAccountID
	}
	config, err := normalizeConfig(account.Config, account.ID)
	if err != nil {
		return err
	}
	if err := validateStrongFactors(account.StrongFactors); err != nil {
		return err
	}

	credential := account.Credential
	if credential.ID == "" {
		credential.ID, err = randomHex(h.rand, credentialIDBytes)
		if err != nil {
			return err
		}
	}
	if credential.Generation == 0 {
		credential.Generation = 1
	}

	sessions := make(map[SessionID]struct{}, len(account.Sessions))
	for _, id := range account.Sessions {
		if id == "" {
			return fmt.Errorf("%w: empty session id", ErrInvalidConfig)
		}
		sessions[id] = struct{}{}
	}

	state := AccountState{
		Config:        config,
		Credential:    credential,
		Sessions:      sessions,
		StrongFactors: cloneStrongFactors(account.StrongFactors),
		Recoveries:    make(map[RecoveryID]Recovery),
	}
	if err := h.store.Commit(account.ID, 0, state); err != nil {
		return err
	}
	return nil
}

func cloneStrongFactors(factors []StrongFactor) []StrongFactor {
	out := make([]StrongFactor, len(factors))
	for i := range factors {
		out[i] = factors[i].clone()
	}
	return out
}

func (h *Host) update(accountID AccountID, mutate func(*AccountState) error) error {
	for attempt := 0; attempt < 16; attempt++ {
		state, err := h.store.Load(accountID)
		if err != nil {
			return err
		}
		next := state.Clone()
		err = mutate(&next)
		if errors.Is(err, errNoChange) {
			return nil
		}
		var deferred *commitError
		if errors.As(err, &deferred) {
			commitErr := h.store.Commit(accountID, state.Version, next)
			if errors.Is(commitErr, ErrVersionConflict) {
				continue
			}
			if commitErr != nil {
				return commitErr
			}
			return deferred.err
		}
		if err != nil {
			return err
		}
		err = h.store.Commit(accountID, state.Version, next)
		if errors.Is(err, ErrVersionConflict) {
			continue
		}
		return err
	}
	return ErrVersionConflict
}

func (h *Host) GetAccount(accountID AccountID) (*AccountState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := h.store.Load(accountID)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (h *Host) GetRecovery(accountID AccountID, recoveryID RecoveryID) (*Recovery, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := h.store.Load(accountID)
	if err != nil {
		return nil, err
	}
	recovery, ok := state.Recoveries[recoveryID]
	if !ok {
		return nil, ErrRecoveryNotFound
	}
	return &recovery, nil
}

// GetRecoveryAt returns a recovery view with deadline expiry applied at now
// without writing the derived status back to the store.
func (h *Host) GetRecoveryAt(accountID AccountID, recoveryID RecoveryID, now time.Time) (*Recovery, error) {
	recovery, err := h.GetRecovery(accountID, recoveryID)
	if err != nil {
		return nil, err
	}
	recovery.Status = recovery.EffectiveStatus(now)
	return recovery, nil
}

// UpdateConfig applies mutate to a copy of the current immutable config,
// advances RecoveryEpoch, and invalidates any in-flight recovery.
func (h *Host) UpdateConfig(accountID AccountID, mutate func(*Config) error) (Config, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var updated Config
	err := h.update(accountID, func(state *AccountState) error {
		oldEpoch := state.Config.RecoveryEpoch
		config := state.Config.clone()
		if mutate != nil {
			if err := mutate(&config); err != nil {
				return err
			}
		}
		config.AccountID = accountID
		config.RecoveryEpoch = oldEpoch + 1
		normalized, err := normalizeConfig(config, accountID)
		if err != nil {
			return err
		}
		invalidateActiveRecovery(state, "config changed", time.Time{})
		state.Config = normalized
		updated = normalized.clone()
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

func (h *Host) ReplaceConfig(accountID AccountID, next Config) (Config, error) {
	return h.UpdateConfig(accountID, func(config *Config) error {
		*config = next.clone()
		return nil
	})
}

// RevokeContact marks a contact revoked, advances RecoveryEpoch, and invalidates
// every pending approval from the old epoch.
func (h *Host) RevokeContact(accountID AccountID, contactID ContactID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(accountID, func(state *AccountState) error {
		found := false
		for i := range state.Config.Contacts {
			if state.Config.Contacts[i].ContactID == contactID {
				found = true
				if state.Config.Contacts[i].Revoked {
					return errNoChange
				}
				state.Config.Contacts[i].Revoked = true
				break
			}
		}
		if !found {
			return ErrContactNotFound
		}
		state.Config.RecoveryEpoch++
		invalidateActiveRecovery(state, "contact revoked", time.Time{})
		return nil
	})
}

// RemoveContact removes a contact entry from the next immutable config
// snapshot, advancing RecoveryEpoch and invalidating old approvals.
func (h *Host) RemoveContact(accountID AccountID, contactID ContactID) (Config, error) {
	return h.UpdateConfig(accountID, func(config *Config) error {
		for i := range config.Contacts {
			if config.Contacts[i].ContactID == contactID {
				config.Contacts = append(config.Contacts[:i], config.Contacts[i+1:]...)
				return nil
			}
		}
		return ErrContactNotFound
	})
}

// RevokeStrongFactor removes a factor's ability to veto a pending recovery.
func (h *Host) RevokeStrongFactor(accountID AccountID, factorID StrongFactorID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(accountID, func(state *AccountState) error {
		for i := range state.StrongFactors {
			if state.StrongFactors[i].ID == factorID {
				if state.StrongFactors[i].Revoked {
					return errNoChange
				}
				state.StrongFactors[i].Revoked = true
				return nil
			}
		}
		return ErrFactorNotFound
	})
}

func (h *Host) uniqueRecoveryID(state *AccountState) (RecoveryID, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := newRecoveryID(h.rand)
		if err != nil {
			return "", err
		}
		if _, exists := state.Recoveries[id]; !exists {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: could not allocate unique recovery id", ErrInvalidRecoveryID)
}

func (h *Host) uniqueCredentialID(previous string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		id, err := randomHex(h.rand, credentialIDBytes)
		if err != nil {
			return "", err
		}
		if id != previous {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: could not allocate unique credential id", ErrCommitFailure)
}

func (h *Host) StartRecovery(accountID AccountID, target Digest, device Digest, now time.Time) (*Recovery, error) {
	now = now.Round(0)
	var start recoveryCapture
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.update(accountID, func(state *AccountState) error {
		deadline := now.Add(state.Config.ApprovalWindow)
		return h.startRecovery(state, target, device, deadline, now, &start)
	})
	if err != nil {
		return nil, err
	}
	recovery := start.recovery
	return &recovery, nil
}

func (h *Host) StartRecoveryUntil(accountID AccountID, target Digest, device Digest, deadline time.Time, now time.Time) (*Recovery, error) {
	now = now.Round(0)
	var start recoveryCapture
	h.mu.Lock()
	defer h.mu.Unlock()
	err := h.update(accountID, func(state *AccountState) error {
		return h.startRecovery(state, target, device, deadline.Round(0), now, &start)
	})
	if err != nil {
		return nil, err
	}
	recovery := start.recovery
	return &recovery, nil
}

type recoveryCapture struct {
	recovery Recovery
}

func (h *Host) startRecovery(state *AccountState, target Digest, device Digest, deadline time.Time, now time.Time, capture *recoveryCapture) error {
	if !deadline.After(now) {
		return fmt.Errorf("%w: deadline must be after start time", ErrRecoveryExpired)
	}
	id, err := h.uniqueRecoveryID(state)
	if err != nil {
		return err
	}
	epoch := state.Config.RecoveryEpoch + 1
	invalidateActiveRecovery(state, "new recovery started", now)
	recovery := Recovery{
		ID:           id,
		AccountID:    state.Config.AccountID,
		Epoch:        epoch,
		TargetDigest: target,
		DeviceDigest: device,
		CreatedAt:    now,
		Deadline:     deadline,
		Approvals:    make(map[ContactID]Approval),
		Status:       StatusPending,
	}
	state.Config.RecoveryEpoch = epoch
	state.Recoveries[id] = recovery
	state.ActiveRecovery = id
	capture.recovery = recovery.clone()
	return nil
}

func approvalsOf(recovery Recovery) []Approval {
	approvals := make([]Approval, 0, len(recovery.Approvals))
	for _, approval := range recovery.Approvals {
		approvals = append(approvals, approval.clone())
	}
	sort.Slice(approvals, func(i, j int) bool {
		return approvals[i].ReceivedAt.Before(approvals[j].ReceivedAt)
	})
	return approvals
}

func (h *Host) Approve(accountID AccountID, recoveryID RecoveryID, contactID ContactID, signature []byte, now time.Time) error {
	now = now.Round(0)
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(accountID, func(state *AccountState) error {
		recovery, ok := state.Recoveries[recoveryID]
		if !ok {
			return ErrRecoveryNotFound
		}
		switch recovery.Status {
		case StatusCompleted:
			return ErrRecoveryCompleted
		case StatusVetoed:
			return ErrRecoveryVetoed
		case StatusInvalidated:
			return ErrRecoveryInvalidated
		case StatusExpired:
			return ErrRecoveryExpired
		}
		if recovery.Epoch != state.Config.RecoveryEpoch {
			return fmt.Errorf("%w: request epoch %d, account epoch %d", ErrStaleEpoch, recovery.Epoch, state.Config.RecoveryEpoch)
		}
		if !now.Before(recovery.Deadline) {
			if recovery.Status == StatusCooling {
				return ErrRecoveryExpired
			}
			recovery.Status = StatusExpired
			state.Recoveries[recoveryID] = recovery
			if state.ActiveRecovery == recoveryID {
				state.ActiveRecovery = ""
			}
			return commitWithError(ErrRecoveryExpired)
		}
		contact, ok := state.Config.Contact(contactID)
		if !ok {
			return ErrContactNotFound
		}
		if contact.Revoked {
			return ErrContactRevoked
		}
		if !contact.ValidAt(recovery.Epoch) {
			return ErrContactOutOfEpoch
		}

		approval := Approval{
			ContactID:          contact.ContactID,
			ContactFingerprint: contact.Fingerprint,
			AccountID:          recovery.AccountID,
			RecoveryID:         recovery.ID,
			Epoch:              recovery.Epoch,
			TargetDigest:       recovery.TargetDigest,
			DeviceDigest:       recovery.DeviceDigest,
			Deadline:           recovery.Deadline,
			Signature:          append([]byte(nil), signature...),
			ReceivedAt:         now,
		}
		if !verifyApprovalSignature(contact, approval) {
			return ErrInvalidSignature
		}
		if _, alreadyApproved := recovery.Approvals[contactID]; alreadyApproved {
			return errNoChange
		}
		recovery.Approvals[contactID] = approval

		evaluation := EvaluateApprovals(state.Config, recovery, approvalsOf(recovery), now, correctPolicy)
		if evaluation.Eligible && recovery.Status == StatusPending {
			recovery.Status = StatusCooling
			recovery.CoolingStartedAt = now
			recovery.CoolingEndsAt = now.Add(state.Config.CoolingPeriod)
		}
		state.Recoveries[recoveryID] = recovery
		return nil
	})
}

func (h *Host) Veto(accountID AccountID, recoveryID RecoveryID, factorID StrongFactorID, signature []byte, now time.Time) error {
	now = now.Round(0)
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.update(accountID, func(state *AccountState) error {
		recovery, ok := state.Recoveries[recoveryID]
		if !ok {
			return ErrRecoveryNotFound
		}
		switch recovery.Status {
		case StatusCompleted:
			return ErrRecoveryCompleted
		case StatusVetoed:
			return ErrRecoveryVetoed
		case StatusInvalidated:
			return ErrRecoveryInvalidated
		case StatusExpired:
			return ErrRecoveryExpired
		case StatusPending:
			if !now.Before(recovery.Deadline) {
				recovery.Status = StatusExpired
				state.Recoveries[recoveryID] = recovery
				if state.ActiveRecovery == recoveryID {
					state.ActiveRecovery = ""
				}
				return commitWithError(ErrRecoveryExpired)
			}
		case StatusCooling:
			if !now.Before(recovery.CoolingEndsAt) {
				return ErrVetoNotAllowed
			}
		}
		var factor StrongFactor
		found := false
		for _, candidate := range state.StrongFactors {
			if candidate.ID == factorID {
				factor = candidate.clone()
				found = true
				break
			}
		}
		if !found {
			return ErrFactorNotFound
		}
		if factor.Revoked {
			return ErrFactorRevoked
		}
		if !verifyVetoSignature(factor, signature, &recovery) {
			return ErrInvalidSignature
		}
		recovery.Status = StatusVetoed
		recovery.VetoedBy = factorID
		recovery.VetoedAt = now
		state.Recoveries[recoveryID] = recovery
		if state.ActiveRecovery == recoveryID {
			state.ActiveRecovery = ""
		}
		return nil
	})
}

func (h *Host) ChangeTarget(accountID AccountID, recoveryID RecoveryID, target Digest, now time.Time) (Config, error) {
	now = now.Round(0)
	h.mu.Lock()
	defer h.mu.Unlock()

	var updated Config
	err := h.update(accountID, func(state *AccountState) error {
		updated = state.Config.clone()
		recovery, ok := state.Recoveries[recoveryID]
		if !ok {
			return ErrRecoveryNotFound
		}
		switch recovery.Status {
		case StatusCompleted:
			return ErrRecoveryCompleted
		case StatusVetoed:
			return ErrRecoveryVetoed
		case StatusInvalidated:
			return ErrRecoveryInvalidated
		case StatusExpired:
			return ErrRecoveryExpired
		}
		if recovery.TargetDigest == target {
			return errNoChange
		}
		recovery.TargetDigest = target
		recovery.Status = StatusInvalidated
		recovery.InvalidationReason = "target operation changed"
		state.Recoveries[recoveryID] = recovery
		if state.ActiveRecovery == recoveryID {
			state.ActiveRecovery = ""
		}
		state.Config.RecoveryEpoch++
		updated = state.Config.clone()
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

func (h *Host) EvaluateRecovery(accountID AccountID, recoveryID RecoveryID, now time.Time) (Evaluation, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state, err := h.store.Load(accountID)
	if err != nil {
		return Evaluation{}, err
	}
	recovery, ok := state.Recoveries[recoveryID]
	if !ok {
		return Evaluation{}, ErrRecoveryNotFound
	}
	return EvaluateApprovals(state.Config, recovery, approvalsOf(recovery), now, correctPolicy), nil
}

// CompleteRecovery atomically rotates the primary credential, revokes all
// sessions and the recovery ID, and advances RecoveryEpoch. Repeating it after
// a successful durable commit returns the recorded completion without rotating
// anything again.
func (h *Host) CompleteRecovery(accountID AccountID, recoveryID RecoveryID, now time.Time) (*Completion, error) {
	now = now.Round(0)
	h.mu.Lock()
	defer h.mu.Unlock()

	var completed Completion
	haveCompletion := false
	err := h.update(accountID, func(state *AccountState) error {
		recovery, ok := state.Recoveries[recoveryID]
		if !ok {
			return ErrRecoveryNotFound
		}
		if recovery.Status == StatusCompleted {
			if recovery.Completion == nil {
				return ErrRecoveryCompleted
			}
			completed = recovery.Completion.clone()
			haveCompletion = true
			return errNoChange
		}
		switch recovery.Status {
		case StatusVetoed:
			return ErrRecoveryVetoed
		case StatusInvalidated:
			return ErrRecoveryInvalidated
		case StatusExpired:
			return ErrRecoveryExpired
		case StatusPending:
			if !now.Before(recovery.Deadline) {
				recovery.Status = StatusExpired
				state.Recoveries[recoveryID] = recovery
				if state.ActiveRecovery == recoveryID {
					state.ActiveRecovery = ""
				}
				return commitWithError(ErrRecoveryExpired)
			}
			return ErrCoolingNotStarted
		case StatusCooling:
			if now.Before(recovery.CoolingEndsAt) {
				return ErrCoolingNotFinished
			}
		default:
			return ErrRecoveryNotActive
		}
		if recovery.Epoch != state.Config.RecoveryEpoch {
			return fmt.Errorf("%w: request epoch %d, account epoch %d", ErrStaleEpoch, recovery.Epoch, state.Config.RecoveryEpoch)
		}
		evaluation := EvaluateApprovals(state.Config, recovery, approvalsOf(recovery), now, correctPolicy)
		if !evaluation.ThresholdMet {
			return ErrThresholdNotMet
		}
		if !evaluation.MinPersonsMet {
			return ErrMinPersonsNotMet
		}
		if !evaluation.Eligible {
			return ErrThresholdNotMet
		}

		credentialID, err := h.uniqueCredentialID(state.Credential.ID)
		if err != nil {
			return err
		}
		newCredential := Credential{ID: credentialID, Generation: state.Credential.Generation + 1}
		revokedSessions := make([]SessionID, 0, len(state.Sessions))
		for id := range state.Sessions {
			revokedSessions = append(revokedSessions, id)
		}
		sort.Slice(revokedSessions, func(i, j int) bool {
			return revokedSessions[i] < revokedSessions[j]
		})
		state.Sessions = make(map[SessionID]struct{})
		state.Credential = newCredential
		state.Config.RecoveryEpoch++
		completion := Completion{
			RecoveryID:      recoveryID,
			CompletedAt:     now,
			NewCredential:   newCredential.clone(),
			RevokedSessions: append([]SessionID(nil), revokedSessions...),
			NewEpoch:        state.Config.RecoveryEpoch,
		}
		recovery.Status = StatusCompleted
		recovery.CompletedAt = now
		recovery.Completion = &completion
		state.Recoveries[recoveryID] = recovery
		if state.ActiveRecovery == recoveryID {
			state.ActiveRecovery = ""
		}
		for id, other := range state.Recoveries {
			if id == recoveryID {
				continue
			}
			if other.Status == StatusPending || other.Status == StatusCooling {
				other.Status = StatusInvalidated
				other.InvalidationReason = "another recovery completed"
				state.Recoveries[id] = other
			}
		}
		completed = completion.clone()
		haveCompletion = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !haveCompletion {
		return nil, ErrRecoveryCompleted
	}
	return &completed, nil
}

// VerifyApprovalKey reports whether a public key matches a contact fingerprint.
func VerifyApprovalKey(fingerprint Fingerprint, publicKey ed25519.PublicKey) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return PublicKeyFingerprint(publicKey) == fingerprint
}
