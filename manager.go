package netsec

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// ConfirmStatus classifies the outcome of presenting a confirmation token.
type ConfirmStatus string

const (
	// StatusPending means the proof was recorded but the other side is missing.
	StatusPending ConfirmStatus = "pending"
	// StatusAlreadyConfirmed means this side's proof was already recorded; the
	// response to an earlier delivery was lost and retried.
	StatusAlreadyConfirmed ConfirmStatus = "already_confirmed"
	// StatusSwitched means both sides are now confirmed and the channel switched.
	StatusSwitched ConfirmStatus = "switched"
)

// ConfirmResult is returned by Manager.Confirm.
type ConfirmResult struct {
	Status ConfirmStatus
	Digest string
	Side   Side
	Active Channel
}

// PendingChange describes an opened transaction and the two tokens the host
// delivers out-of-band: OldToken goes to the currently active channel,
// NewToken to the proposed replacement.
type PendingChange struct {
	Digest      string
	OldToken    string
	NewToken    string
	Old         Channel
	New         Channel
	ExpiresAt   time.Time
	ExpiresNano int64
}

// Manager is the host-facing entry point for recovery-channel rebinding.
type Manager struct {
	store Store
	sign  *signer
	clock Clock
	ttl   time.Duration

	// Observe, when set, receives every domain event after it durably commits.
	// It must be fast and must not call back into the manager.
	Observe func(Event)
}

// maxCASRetries bounds optimistic retries under contention.
const maxCASRetries = 8

// Option configures a Manager.
type Option func(*Manager) error

// WithClock overrides the clock (mainly for tests).
func WithClock(c Clock) Option {
	return func(m *Manager) error { m.clock = c; return nil }
}

// WithTTL overrides the confirmation deadline.
func WithTTL(ttl time.Duration) Option {
	return func(m *Manager) error {
		if ttl <= 0 {
			return ErrProofExpired
		}
		m.ttl = ttl
		return nil
	}
}

// NewManager builds a Manager. key is the HMAC secret for confirmation tokens
// and must contain at least 32 random bytes.
func NewManager(store Store, key []byte, opts ...Option) (*Manager, error) {
	sign, err := newSigner(key)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		store: store,
		sign:  sign,
		clock: systemClock{},
		ttl:   24 * time.Hour,
	}
	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// BeginChange opens a rebinding transaction. The host must deliver OldToken to
// the current channel and NewToken to the replacement channel. Any previously
// pending change is superseded and its proofs are revoked.
func (m *Manager) BeginChange(userID string, old, newChannel Channel) (PendingChange, error) {
	now := m.clock.Now()
	expires := now.Add(m.ttl)
	nonce, err := randomNonce()
	if err != nil {
		return PendingChange{}, err
	}
	digest := computeDigest(userID, old, newChannel, expires.UnixNano(), nonce)

	var pending PendingChange
	_, err = m.cas(userID, func(state *State) (*State, []Event, error) {
		tx, ev, err := state.Begin(old, newChannel, digest, now.UnixNano(), expires.UnixNano())
		if err != nil {
			return nil, nil, err
		}
		oldToken, err := m.issueToken(userID, tx, SideOld, nonce)
		if err != nil {
			return nil, nil, err
		}
		newToken, err := m.issueToken(userID, tx, SideNew, nonce)
		if err != nil {
			return nil, nil, err
		}
		pending = PendingChange{
			Digest:      digest,
			OldToken:    oldToken,
			NewToken:    newToken,
			Old:         old,
			New:         newChannel,
			ExpiresAt:   expires,
			ExpiresNano: expires.UnixNano(),
		}
		return state, []Event{*ev}, nil
	})
	if err != nil {
		return PendingChange{}, err
	}
	return pending, nil
}

// Confirm validates a token delivered out-of-band and records its proof. The
// active channel switches only when both sides of the same digest confirm
// before the deadline. Replaying an already recorded proof is idempotent.
func (m *Manager) Confirm(userID, token string) (ConfirmResult, error) {
	claims, err := m.sign.parse(token)
	if err != nil {
		return ConfirmResult{}, err
	}
	if claims.UserID != userID {
		return ConfirmResult{}, ErrSideMismatch
	}
	now := m.clock.Now()

	var result ConfirmResult
	_, err = m.cas(userID, func(state *State) (*State, []Event, error) {
		res, ev, err := state.Confirm(claims.Side, claims.Digest, now.UnixNano(), claims.ExpireNano)
		if err != nil {
			if errors.Is(err, ErrAlreadyComplete) {
				// Idempotent retry of a lost response after the switch.
				return state, nil, nil
			}
			return nil, nil, err
		}
		var events []Event
		if ev != nil {
			events = append(events, *ev)
			if ev.Switch != nil {
				events = append(events, *ev.Switch)
			}
		}
		result = res
		return state, events, nil
	})
	if err != nil {
		return ConfirmResult{}, err
	}
	if result.Status == "" {
		active, loadErr := m.store.Load(userID)
		if loadErr != nil {
			return ConfirmResult{}, loadErr
		}
		result = ConfirmResult{Status: StatusSwitched, Digest: claims.Digest, Side: claims.Side, Active: active.Active}
	}
	return result, nil
}

// CancelChange revokes the in-flight transaction and all of its proofs. The
// active recovery channel is untouched.
func (m *Manager) CancelChange(userID string) error {
	now := m.clock.Now()
	_, err := m.cas(userID, func(state *State) (*State, []Event, error) {
		_, ev, err := state.Cancel(now.UnixNano())
		if err != nil {
			return nil, nil, err
		}
		return state, []Event{*ev}, nil
	})
	return err
}

// State returns a snapshot of the user's recovery state.
func (m *Manager) State(userID string) (*State, error) {
	return m.store.Load(userID)
}

func (m *Manager) issueToken(userID string, tx *Transaction, side Side, nonce string) (string, error) {
	return m.sign.issue(tokenClaims{
		Version:    tokenVersion,
		UserID:     userID,
		Digest:     tx.Digest,
		Side:       side,
		ExpireNano: tx.ExpiresNano,
		Nonce:      nonce,
	})
}

// cas runs mutate against fresh state with optimistic retries and emits
// committed events exactly once per successful commit.
func (m *Manager) cas(userID string, mutate func(*State) (*State, []Event, error)) (*State, error) {
	base, err := m.store.Load(userID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		working := base.Clone()
		next, events, err := mutate(working)
		if err != nil {
			return nil, err
		}
		if next.Version == base.Version {
			// Idempotent retry: nothing changed, so no commit is needed and
			// no event is emitted.
			return next, nil
		}
		committed, commitErr := m.store.CommitCAS(userID, base, func(*State) (*State, error) {
			return next, nil
		})
		if commitErr == nil {
			if m.Observe != nil {
				for _, ev := range events {
					m.Observe(ev)
				}
			}
			return committed, nil
		}
		if commitErr != ErrConflict || attempt >= maxCASRetries {
			return nil, commitErr
		}
		base, err = m.store.Load(userID)
		if err != nil {
			return nil, err
		}
	}
}

func randomNonce() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
