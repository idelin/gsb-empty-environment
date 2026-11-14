package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Errors returned by Manager. Callers should use errors.Is so that a failure
// can never be mistaken for a successful confirmation or delivery check.
var (
	ErrInvalidList       = errors.New("netsec: invalid list")
	ErrInvalidAddress    = errors.New("netsec: invalid email address")
	ErrInvalidRequester  = errors.New("netsec: invalid requester")
	ErrAlreadySubscribed = errors.New("netsec: address is already subscribed to this list")
	ErrTokenInvalid      = errors.New("netsec: invalid confirmation token")
	ErrTokenExpired      = errors.New("netsec: confirmation token expired")
	ErrTokenConsumed     = errors.New("netsec: confirmation token already used")
	ErrTokenRevoked      = errors.New("netsec: confirmation token revoked")
	ErrListMismatch      = errors.New("netsec: confirmation token belongs to another list")
	ErrAddressMismatch   = errors.New("netsec: confirmation token belongs to another address")
	ErrRequesterMismatch = errors.New("netsec: confirmation token belongs to another requester")
	ErrNotConfirmed      = errors.New("netsec: address has not confirmed this list")
	ErrUnsubscribed      = errors.New("netsec: address is unsubscribed")
	ErrComplained        = errors.New("netsec: address has complained and is suppressed")
	ErrTokenGeneration   = errors.New("netsec: could not generate confirmation token")
)

const (
	defaultTokenTTL = 24 * time.Hour
	maxTokenTTL     = 30 * 24 * time.Hour
	maxTokenLength  = 512
	pruneInterval   = time.Minute
)

// Options configures a Manager. The zero value is ready to use: tokens live
// for 24 hours and the Manager uses the system clock and crypto/rand.
type Options struct {
	// TokenTTL is the lifetime of a confirmation token. A zero value selects
	// 24 hours. Negative or excessively large values are rejected by
	// NewManager.
	TokenTTL time.Duration

	// Clock supplies the current time. It is useful for tests and for services
	// with a controlled clock. A nil clock selects time.Now.
	Clock func() time.Time

	// Rand supplies cryptographically strong bytes for token generation. A nil
	// value selects crypto/rand.Reader.
	Rand io.Reader
}

// SubscriptionRequest starts a double opt-in subscription. Requester must be
// the service's stable identity for the principal that initiated the request
// (for example an authenticated account ID), not an address supplied by the
// requester's browser. Confirmation succeeds only when the same Requester
// confirms.
type SubscriptionRequest struct {
	List      string
	Address   string
	Requester string
}

// Confirmation is the result of a subscription request. The service must send
// Token only to Address. It must not include the token in logs or send it to
// any other address.
type Confirmation struct {
	Token     string
	List      string
	Address   string
	Requester string
	ExpiresAt time.Time
}

// ConfirmationRequest consumes a token. List, Address and Requester must all
// match the values bound at request time. A forwarded link normally fails the
// Requester check because the recipient is a different principal.
type ConfirmationRequest struct {
	Token     string
	List      string
	Address   string
	Requester string
}

// Subscription is a verified, current list membership.
type Subscription struct {
	List        string
	Address     string
	Requester   string
	ConfirmedAt time.Time
}

type tokenState uint8

const (
	tokenPending tokenState = iota
	tokenConsumed
	tokenRevoked
)

type tokenKey [sha256.Size]byte

type tokenRecord struct {
	list      string
	address   string
	requester string
	createdAt time.Time
	expiresAt time.Time
	state     tokenState
}

type membershipKey struct {
	list    string
	address string
}

// Manager implements double opt-in confirmation, one-time token consumption,
// complaint suppression and unsubscribe state. All exported methods are safe
// for concurrent use.
//
// State is held in memory. A multi-process deployment should put the same
// state behind a shared, transactional store so that token consumption and
// unsubscribe/complaint updates remain atomic across service instances.
type Manager struct {
	mu        sync.Mutex
	ttl       time.Duration
	now       func() time.Time
	rand      io.Reader
	tokens    map[tokenKey]*tokenRecord
	members   map[membershipKey]Subscription
	softOpt   map[membershipKey]time.Time
	complaint map[string]time.Time
	lastPrune time.Time
}

// NewManager creates a Manager. The returned Manager is empty and safe to
// share between goroutines.
func NewManager(opts Options) (*Manager, error) {
	ttl := opts.TokenTTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}
	if ttl < 0 || ttl > maxTokenTTL {
		return nil, errors.New("netsec: token TTL must be positive and at most 30 days")
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	entropy := opts.Rand
	if entropy == nil {
		entropy = rand.Reader
	}
	return &Manager{
		ttl:       ttl,
		now:       clock,
		rand:      entropy,
		tokens:    make(map[tokenKey]*tokenRecord),
		members:   make(map[membershipKey]Subscription),
		softOpt:   make(map[membershipKey]time.Time),
		complaint: make(map[string]time.Time),
	}, nil
}

// RequestSubscription creates a fresh confirmation token. No delivery is
// authorized for the address until ConfirmSubscription succeeds. A new request
// does not resurrect a complaint; complained addresses stay suppressed.
//
// The caller is responsible for sending the confirmation message to
// Confirmation.Address, and only to that address, through its normal
// transactional-mail path.
func (m *Manager) RequestSubscription(ctx context.Context, req SubscriptionRequest) (Confirmation, error) {
	if err := contextError(ctx); err != nil {
		return Confirmation{}, err
	}
	list, err := canonicalList(req.List)
	if err != nil {
		return Confirmation{}, err
	}
	address, err := canonicalAddress(req.Address)
	if err != nil {
		return Confirmation{}, err
	}
	requester, err := canonicalRequester(req.Requester)
	if err != nil {
		return Confirmation{}, err
	}

	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now)

	if _, ok := m.complaint[address]; ok {
		return Confirmation{}, ErrComplained
	}
	if _, ok := m.members[membershipKey{list: list, address: address}]; ok {
		return Confirmation{}, ErrAlreadySubscribed
	}

	token, key, err := m.newTokenLocked()
	if err != nil {
		return Confirmation{}, ErrTokenGeneration
	}
	expiresAt := now.Add(m.ttl)
	m.tokens[key] = &tokenRecord{
		list:      list,
		address:   address,
		requester: requester,
		createdAt: now,
		expiresAt: expiresAt,
		state:     tokenPending,
	}
	return Confirmation{
		Token:     token,
		List:      list,
		Address:   address,
		Requester: requester,
		ExpiresAt: expiresAt,
	}, nil
}

// ConfirmSubscription validates every token binding and atomically consumes
// the token before creating the membership. A failed attempt does not consume
// the token, so a legitimate requester can still confirm after a forged or
// cross-list attempt.
func (m *Manager) ConfirmSubscription(ctx context.Context, req ConfirmationRequest) (Subscription, error) {
	if err := contextError(ctx); err != nil {
		return Subscription{}, err
	}
	list, err := canonicalList(req.List)
	if err != nil {
		return Subscription{}, err
	}
	address, err := canonicalAddress(req.Address)
	if err != nil {
		return Subscription{}, err
	}
	requester, err := canonicalRequester(req.Requester)
	if err != nil {
		return Subscription{}, err
	}
	token := strings.TrimSpace(req.Token)
	if token == "" || len(token) > maxTokenLength {
		return Subscription{}, ErrTokenInvalid
	}

	key := tokenKey(sha256.Sum256([]byte(token)))
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.tokens[key]
	if !ok {
		return Subscription{}, ErrTokenInvalid
	}
	switch record.state {
	case tokenConsumed:
		return Subscription{}, ErrTokenConsumed
	case tokenRevoked:
		return Subscription{}, ErrTokenRevoked
	}
	if !now.Before(record.expiresAt) {
		return Subscription{}, ErrTokenExpired
	}
	if record.list != list {
		return Subscription{}, ErrListMismatch
	}
	if record.address != address {
		return Subscription{}, ErrAddressMismatch
	}
	if record.requester != requester {
		return Subscription{}, ErrRequesterMismatch
	}
	if _, suppressed := m.complaint[address]; suppressed {
		record.state = tokenRevoked
		return Subscription{}, ErrComplained
	}
	mk := membershipKey{list: list, address: address}
	if _, alreadyConfirmed := m.members[mk]; alreadyConfirmed {
		return Subscription{}, ErrAlreadySubscribed
	}

	// Consume before changing any other state. A token can create at most one
	// membership, even when confirmation requests race.
	record.state = tokenConsumed
	delete(m.softOpt, mk)
	sub := Subscription{
		List:        list,
		Address:     address,
		Requester:   requester,
		ConfirmedAt: now,
	}
	m.members[mk] = sub
	return sub, nil
}

// AuthorizeDelivery is the mandatory gate before sending list mail. A nil
// error means the address is confirmed for this list and is not suppressed.
// Every other result means the message must not be sent.
func (m *Manager) AuthorizeDelivery(ctx context.Context, list, address string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	canonicalListName, err := canonicalList(list)
	if err != nil {
		return err
	}
	canonicalAddressValue, err := canonicalAddress(address)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.complaint[canonicalAddressValue]; ok {
		return ErrComplained
	}
	mk := membershipKey{list: canonicalListName, address: canonicalAddressValue}
	if _, ok := m.softOpt[mk]; ok {
		return ErrUnsubscribed
	}
	if _, ok := m.members[mk]; !ok {
		return ErrNotConfirmed
	}
	return nil
}

// CanDeliver is a boolean convenience wrapper around AuthorizeDelivery.
func (m *Manager) CanDeliver(ctx context.Context, list, address string) bool {
	return m.AuthorizeDelivery(ctx, list, address) == nil
}

// Unsubscribe immediately removes a membership and revokes all still-pending
// confirmation tokens for that address and list. The opt-out is list-scoped
// and idempotent. A later, newly confirmed double opt-in may restore this
// list membership.
func (m *Manager) Unsubscribe(ctx context.Context, list, address string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	listName, err := canonicalList(list)
	if err != nil {
		return err
	}
	addressValue, err := canonicalAddress(address)
	if err != nil {
		return err
	}
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()
	mk := membershipKey{list: listName, address: addressValue}
	m.revokeTokensLocked(func(record *tokenRecord) bool {
		return record.state == tokenPending &&
			record.list == listName && record.address == addressValue
	})
	delete(m.members, mk)
	if _, hardSuppressed := m.complaint[addressValue]; !hardSuppressed {
		m.softOpt[mk] = now
	}
	return nil
}

// Complaint records a hard suppression for an address and also performs the
// unsubscribe linkage: all memberships and all pending confirmation tokens
// for the address are revoked, across every list. A complaint cannot be
// cleared by requesting a new confirmation. Deployments that permit appeals
// should remove the hard suppression through an administrative data path
// according to their abuse policy.
func (m *Manager) Complaint(ctx context.Context, list, address string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if _, err := canonicalList(list); err != nil {
		return err
	}
	addressValue, err := canonicalAddress(address)
	if err != nil {
		return err
	}
	now := m.now()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokeTokensLocked(func(record *tokenRecord) bool {
		return record.state == tokenPending && record.address == addressValue
	})
	for mk := range m.members {
		if mk.address == addressValue {
			delete(m.members, mk)
		}
	}
	for mk := range m.softOpt {
		if mk.address == addressValue {
			delete(m.softOpt, mk)
		}
	}
	m.complaint[addressValue] = now
	return nil
}

func (m *Manager) newTokenLocked() (string, tokenKey, error) {
	for attempt := 0; attempt < 4; attempt++ {
		var raw [32]byte
		if _, err := io.ReadFull(m.rand, raw[:]); err != nil {
			return "", tokenKey{}, err
		}
		token := base64.RawURLEncoding.EncodeToString(raw[:])
		key := tokenKey(sha256.Sum256([]byte(token)))
		if _, exists := m.tokens[key]; !exists {
			return token, key, nil
		}
	}
	return "", tokenKey{}, errors.New("netsec: token collision")
}

func (m *Manager) revokeTokensLocked(match func(*tokenRecord) bool) {
	for _, record := range m.tokens {
		if match(record) {
			record.state = tokenRevoked
		}
	}
}

// Tokens are kept briefly after expiry so that reuse reports ErrTokenExpired
// or ErrTokenConsumed rather than being confused with an unknown token.
func (m *Manager) pruneLocked(now time.Time) {
	if !m.lastPrune.IsZero() && now.Sub(m.lastPrune) < pruneInterval {
		return
	}
	m.lastPrune = now
	retention := m.ttl
	if retention < time.Hour {
		retention = time.Hour
	}
	for key, record := range m.tokens {
		if now.After(record.expiresAt.Add(retention)) {
			delete(m.tokens, key)
		}
	}
}

func canonicalList(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 255 {
		return "", ErrInvalidList
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", ErrInvalidList
		}
	}
	return strings.ToLower(value), nil
}

func canonicalAddress(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrInvalidAddress
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Name != "" {
		return "", ErrInvalidAddress
	}
	address := parsed.Address
	if address == "" || len(address) > 254 || strings.ContainsAny(address, " \t\r\n\x00") {
		return "", ErrInvalidAddress
	}
	at := strings.LastIndexByte(address, '@')
	if at <= 0 || at == len(address)-1 {
		return "", ErrInvalidAddress
	}
	// Treat addresses case-insensitively. Most mail systems do so for the
	// domain, and this avoids duplicate memberships that differ only by case.
	return strings.ToLower(address), nil
}

func canonicalRequester(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 512 {
		return "", ErrInvalidRequester
	}
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 || unicode.IsControl(r) {
			return "", ErrInvalidRequester
		}
	}
	return value, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
