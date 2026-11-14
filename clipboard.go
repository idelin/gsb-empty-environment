package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// EntryType classifies clipboard payloads. Passwords and tokens are
// sensitive and are subject to the router's SensitivePolicy.
type EntryType int

const (
	TypeText EntryType = iota
	TypeURL
	TypeImage
	TypePassword
	TypeToken
)

// Sensitive reports whether the entry type carries credentials.
func (t EntryType) Sensitive() bool {
	return t == TypePassword || t == TypeToken
}

func (t EntryType) String() string {
	switch t {
	case TypeText:
		return "text"
	case TypeURL:
		return "url"
	case TypeImage:
		return "image"
	case TypePassword:
		return "password"
	case TypeToken:
		return "token"
	default:
		return "unknown"
	}
}

// SensitivePolicy controls how sensitive entry types are routed.
type SensitivePolicy int

const (
	// SensitiveBlock rejects sensitive entries outright. This is the default.
	SensitiveBlock SensitivePolicy = iota
	// SensitiveConfirm routes sensitive entries only after Confirm is called
	// for the entry ID.
	SensitiveConfirm
)

var (
	ErrEntryExpired          = errors.New("clipboard: entry expired")
	ErrPayloadTooLarge       = errors.New("clipboard: payload too large")
	ErrSourceDeviceUnknown   = errors.New("clipboard: source device unknown")
	ErrSourceDeviceRevoked   = errors.New("clipboard: source device not authorized")
	ErrSourceAccountMismatch = errors.New("clipboard: source device not bound to entry account")
	ErrTargetDeviceUnknown   = errors.New("clipboard: target device unknown")
	ErrTargetDeviceRevoked   = errors.New("clipboard: target device not authorized")
	ErrTargetAccountMismatch = errors.New("clipboard: target device belongs to a different account")
	ErrSensitiveBlocked      = errors.New("clipboard: sensitive entry type is not synced")
	ErrConfirmationRequired  = errors.New("clipboard: sensitive entry requires confirmation")
	ErrRateLimited           = errors.New("clipboard: rate limit exceeded")
	ErrInvalidEntry          = errors.New("clipboard: invalid entry")
)

// Device is a registered sync participant. A device may only send or
// receive clipboard entries while Authorized is true.
type Device struct {
	ID         string
	AccountID  string
	Authorized bool
}

// Entry is a single clipboard item offered for synchronization.
type Entry struct {
	ID             string
	AccountID      string
	SourceDeviceID string
	Type           EntryType
	Payload        []byte
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Expired reports whether the entry is past its expiry at the given time.
func (e Entry) Expired(now time.Time) bool {
	return !now.Before(e.ExpiresAt)
}

// Option customizes a Router.
type Option func(*Router)

// WithMaxPayloadSize sets the maximum payload size in bytes.
func WithMaxPayloadSize(n int) Option {
	return func(r *Router) { r.maxPayloadSize = n }
}

// WithRateLimit allows at most n routed entries per account per window.
func WithRateLimit(n int, window time.Duration) Option {
	return func(r *Router) {
		r.rateLimit = n
		r.rateWindow = window
	}
}

// WithHistoryLimit caps how many entries are retained per account.
func WithHistoryLimit(n int) Option {
	return func(r *Router) { r.historyLimit = n }
}

// WithSensitivePolicy sets the policy for sensitive entry types.
func WithSensitivePolicy(p SensitivePolicy) Option {
	return func(r *Router) { r.sensitivePolicy = p }
}

// WithClock overrides the time source, mainly for testing.
func WithClock(now func() time.Time) Option {
	return func(r *Router) { r.now = now }
}

// Router validates clipboard entries and delivers them only to devices
// that belong to the entry's account and are currently authorized.
type Router struct {
	mu              sync.Mutex
	devices         map[string]Device
	maxPayloadSize  int
	rateLimit       int
	rateWindow      time.Duration
	historyLimit    int
	sensitivePolicy SensitivePolicy
	now             func() time.Time

	confirmations map[string]bool
	rateEvents    map[string][]time.Time
	history       map[string][]Entry
	deliveries    map[string][]Entry
}

// NewRouter builds a Router with secure defaults: sensitive types blocked,
// 64 KiB payload cap, 30 entries per minute per account, 100-entry history.
func NewRouter(opts ...Option) *Router {
	r := &Router{
		devices:         make(map[string]Device),
		maxPayloadSize:  64 * 1024,
		rateLimit:       30,
		rateWindow:      time.Minute,
		historyLimit:    100,
		sensitivePolicy: SensitiveBlock,
		now:             time.Now,
		confirmations:   make(map[string]bool),
		rateEvents:      make(map[string][]time.Time),
		history:         make(map[string][]Entry),
		deliveries:      make(map[string][]Entry),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RegisterDevice adds or replaces a device registration.
func (r *Router) RegisterDevice(d Device) error {
	if d.ID == "" || d.AccountID == "" {
		return fmt.Errorf("clipboard: device requires ID and AccountID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.devices[d.ID] = d
	return nil
}

// RevokeDevice marks a device as unauthorized so it can no longer
// send or receive clipboard entries.
func (r *Router) RevokeDevice(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d, ok := r.devices[deviceID]; ok {
		d.Authorized = false
		r.devices[deviceID] = d
	}
}

// Confirm records user confirmation for a sensitive entry so it may be
// routed when the SensitiveConfirm policy is active.
func (r *Router) Confirm(entryID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.confirmations[entryID] = true
}

// Route validates the entry and, on success, records delivery to the
// target device and appends the entry to the account history.
func (r *Router) Route(e Entry, targetDeviceID string) error {
	if e.ID == "" || e.AccountID == "" || e.SourceDeviceID == "" {
		return ErrInvalidEntry
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("%w: empty payload", ErrInvalidEntry)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if e.Expired(now) {
		return ErrEntryExpired
	}
	if len(e.Payload) > r.maxPayloadSize {
		return fmt.Errorf("%w: %d bytes exceeds limit of %d", ErrPayloadTooLarge, len(e.Payload), r.maxPayloadSize)
	}

	src, ok := r.devices[e.SourceDeviceID]
	if !ok {
		return ErrSourceDeviceUnknown
	}
	if !src.Authorized {
		return ErrSourceDeviceRevoked
	}
	if src.AccountID != e.AccountID {
		return ErrSourceAccountMismatch
	}

	dst, ok := r.devices[targetDeviceID]
	if !ok {
		return ErrTargetDeviceUnknown
	}
	if !dst.Authorized {
		return ErrTargetDeviceRevoked
	}
	if dst.AccountID != e.AccountID {
		return ErrTargetAccountMismatch
	}

	if e.Type.Sensitive() {
		switch r.sensitivePolicy {
		case SensitiveBlock:
			return fmt.Errorf("%w: %s", ErrSensitiveBlocked, e.Type)
		case SensitiveConfirm:
			if !r.confirmations[e.ID] {
				return fmt.Errorf("%w: %s", ErrConfirmationRequired, e.Type)
			}
		}
	}

	if err := r.checkRate(e.AccountID, now); err != nil {
		return err
	}

	r.deliveries[targetDeviceID] = append(r.deliveries[targetDeviceID], e)
	r.appendHistory(e)
	return nil
}

func (r *Router) checkRate(accountID string, now time.Time) error {
	if r.rateLimit <= 0 {
		return nil
	}
	cutoff := now.Add(-r.rateWindow)
	events := r.rateEvents[accountID]
	kept := events[:0]
	for _, t := range events {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.rateLimit {
		r.rateEvents[accountID] = kept
		return ErrRateLimited
	}
	r.rateEvents[accountID] = append(kept, now)
	return nil
}

func (r *Router) appendHistory(e Entry) {
	if r.historyLimit <= 0 {
		return
	}
	h := append(r.history[e.AccountID], e)
	if len(h) > r.historyLimit {
		h = append([]Entry(nil), h[len(h)-r.historyLimit:]...)
	}
	r.history[e.AccountID] = h
}

// History returns the retained entries for an account, oldest first.
// Expired entries are pruned before the result is returned.
func (r *Router) History(accountID string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	var live []Entry
	for _, e := range r.history[accountID] {
		if !e.Expired(now) {
			live = append(live, e)
		}
	}
	r.history[accountID] = live
	return append([]Entry(nil), live...)
}

// DeliveredTo returns the entries routed to a device, oldest first.
func (r *Router) DeliveredTo(deviceID string) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Entry(nil), r.deliveries[deviceID]...)
}
