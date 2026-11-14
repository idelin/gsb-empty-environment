// Package netsec provides a security-scoped contacts synchronization
// component for a Go contacts service.
//
// A synchronization request is authorized only when the account, registered
// and approved device, opaque token, and cursor all agree. Tokens are
// single-use and rotate after every successful sync. The token's scope is
// derived server-side from the registration and token records; client-supplied
// identifiers are never used on their own to select contacts.
package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sort"
	"strings"
	"sync"
)

// Token is an opaque, high-entropy synchronization token. Its contents are
// never parsed by clients or used to identify a scope.
type Token string

// Contact is a contact record. IDs are unique within one account; the same ID
// may be used independently by different accounts.
type Contact struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
}

// Change describes one incremental mutation.
//
// When Deleted is false, Contact is the current contact value. When Deleted is
// true, Contact contains only the ID of the tombstone; prior contact data is
// deliberately not returned.
type Change struct {
	Version uint64  `json:"version"`
	Deleted bool    `json:"deleted"`
	Contact Contact `json:"contact"`
}

// SyncRequest is the complete scope for a pull. AccountID and DeviceID must
// match the registration and token binding. Cursor must be the cursor returned
// with Token; arbitrary or older cursors are rejected.
type SyncRequest struct {
	AccountID string
	DeviceID  string
	Token     Token
	Cursor    uint64
}

// SyncResult is an incremental page of changes and the credentials for the
// next pull. NextToken is single-use and bound to NextCursor.
type SyncResult struct {
	Changes    []Change `json:"changes"`
	NextCursor uint64   `json:"next_cursor"`
	NextToken  Token    `json:"next_token"`
}

type deviceRecord struct {
	accountID string
	approved  bool
	revoked   bool
}

type tokenRecord struct {
	accountID string
	deviceID  string
	cursor    uint64
	used      bool
}

type accountState struct {
	contacts map[string]Contact
	changes  []Change
	version  uint64
}

// SyncScope owns device registrations, token state, cursors, and contact
// changes. All methods are safe for concurrent use.
type SyncScope struct {
	mu       sync.Mutex
	devices  map[string]*deviceRecord
	tokens   map[[sha256.Size]byte]*tokenRecord
	accounts map[string]*accountState
}

// NewSyncScope returns an empty, ready-to-use synchronization scope.
func NewSyncScope() *SyncScope {
	return &SyncScope{
		devices:  make(map[string]*deviceRecord),
		tokens:   make(map[[sha256.Size]byte]*tokenRecord),
		accounts: make(map[string]*accountState),
	}
}

// New is an alias for NewSyncScope.
func New() *SyncScope {
	return NewSyncScope()
}

// RegisterDevice registers a device under an account. A new registration is
// pending and cannot receive a token until ApproveDevice is called. Registering
// an already-known device for the same account is idempotent and never resets
// approval or revocation. A device identifier cannot be registered by a second
// account.
func (s *SyncScope) RegisterDevice(accountID, deviceID string) error {
	accountID = strings.TrimSpace(accountID)
	deviceID = strings.TrimSpace(deviceID)
	if accountID == "" {
		return ErrInvalidAccountID
	}
	if deviceID == "" {
		return ErrInvalidDeviceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	if existing, ok := s.devices[deviceID]; ok {
		if existing.accountID != accountID {
			return ErrDeviceScopeMismatch
		}
		return nil
	}
	s.devices[deviceID] = &deviceRecord{accountID: accountID}
	return nil
}

// ApproveDevice approves a previously registered device. Approval is scoped to
// the account that registered the device.
func (s *SyncScope) ApproveDevice(accountID, deviceID string) error {
	accountID = strings.TrimSpace(accountID)
	deviceID = strings.TrimSpace(deviceID)
	if accountID == "" {
		return ErrInvalidAccountID
	}
	if deviceID == "" {
		return ErrInvalidDeviceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	device, ok := s.devices[deviceID]
	if !ok {
		return ErrDeviceNotRegistered
	}
	if device.accountID != accountID {
		return ErrDeviceScopeMismatch
	}
	if device.revoked {
		return ErrDeviceRevoked
	}
	device.approved = true
	return nil
}

// RevokeDevice permanently disables a device for synchronization. Existing
// tokens for the device stop working immediately.
func (s *SyncScope) RevokeDevice(accountID, deviceID string) error {
	accountID = strings.TrimSpace(accountID)
	deviceID = strings.TrimSpace(deviceID)
	if accountID == "" {
		return ErrInvalidAccountID
	}
	if deviceID == "" {
		return ErrInvalidDeviceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	device, ok := s.devices[deviceID]
	if !ok {
		return ErrDeviceNotRegistered
	}
	if device.accountID != accountID {
		return ErrDeviceScopeMismatch
	}
	device.revoked = true
	device.approved = false
	return nil
}

// IssueToken creates a single-use bootstrap token for an approved device. The
// token is bound to the device's account, the device, and cursor zero, which
// makes a new device consume the account's full change history (including
// tombstones). The raw token is returned exactly once; only its hash is
// retained.
func (s *SyncScope) IssueToken(accountID, deviceID string) (Token, error) {
	return s.IssueTokenAt(accountID, deviceID, 0)
}

// IssueTokenAt creates a single-use token bound to a caller-selected cursor.
// The cursor must be in [0, current account version]. This is useful when a
// trusted service is resuming a known prior sync state. Clients cannot choose a
// cursor independently of the token: Sync requires the exact bound value.
func (s *SyncScope) IssueTokenAt(accountID, deviceID string, cursor uint64) (Token, error) {
	accountID = strings.TrimSpace(accountID)
	deviceID = strings.TrimSpace(deviceID)
	if accountID == "" {
		return "", ErrInvalidAccountID
	}
	if deviceID == "" {
		return "", ErrInvalidDeviceID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	if err := s.checkDeviceLocked(accountID, deviceID); err != nil {
		return "", err
	}
	account := s.accountLocked(accountID)
	if cursor > account.version {
		return "", ErrCursorStale
	}
	return s.newTokenLocked(accountID, deviceID, cursor)
}

// IssueSyncToken is an alias for IssueToken.
func (s *SyncScope) IssueSyncToken(accountID, deviceID string) (Token, error) {
	return s.IssueToken(accountID, deviceID)
}

// UpsertContact creates or replaces a contact in one account and returns the
// new account version. Contact data is stored only in that account's namespace.
func (s *SyncScope) UpsertContact(accountID string, contact Contact) (uint64, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, ErrInvalidAccountID
	}
	contact.ID = strings.TrimSpace(contact.ID)
	if contact.ID == "" {
		return 0, ErrInvalidContactID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	account := s.accountLocked(accountID)
	account.version++
	contact = cloneContact(contact)
	account.contacts[contact.ID] = contact
	account.changes = append(account.changes, Change{
		Version: account.version,
		Contact: contact,
	})
	return account.version, nil
}

// PutContact is an alias for UpsertContact.
func (s *SyncScope) PutContact(accountID string, contact Contact) (uint64, error) {
	return s.UpsertContact(accountID, contact)
}

// DeleteContact records a deletion tombstone in the account and returns the new
// account version. Tombstones are included in subsequent incremental pulls so
// that every device observes the deletion.
func (s *SyncScope) DeleteContact(accountID, contactID string) (uint64, error) {
	accountID = strings.TrimSpace(accountID)
	contactID = strings.TrimSpace(contactID)
	if accountID == "" {
		return 0, ErrInvalidAccountID
	}
	if contactID == "" {
		return 0, ErrInvalidContactID
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	account := s.accountLocked(accountID)
	if _, ok := account.contacts[contactID]; !ok {
		return 0, ErrContactNotFound
	}
	delete(account.contacts, contactID)
	account.version++
	account.changes = append(account.changes, Change{
		Version: account.version,
		Deleted: true,
		Contact: Contact{ID: contactID},
	})
	return account.version, nil
}

// Sync validates the full account/device/token/cursor scope and returns the
// account's incremental changes after the cursor. The presented token is
// consumed only after all validation succeeds. A fresh token and cursor are
// returned for the next call.
func (s *SyncScope) Sync(req SyncRequest) (SyncResult, error) {
	accountID := strings.TrimSpace(req.AccountID)
	deviceID := strings.TrimSpace(req.DeviceID)
	if accountID == "" {
		return SyncResult{}, ErrInvalidAccountID
	}
	if deviceID == "" {
		return SyncResult{}, ErrInvalidDeviceID
	}
	if req.Token == "" {
		return SyncResult{}, ErrTokenInvalid
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureInitLocked()

	// Re-check the approved device on every pull. A device revoked after token
	// issuance cannot use its remaining token.
	if err := s.checkDeviceLocked(accountID, deviceID); err != nil {
		return SyncResult{}, err
	}

	key := hashToken(req.Token)
	token, ok := s.tokens[key]
	if !ok {
		return SyncResult{}, ErrTokenInvalid
	}
	if token.used {
		return SyncResult{}, ErrTokenUsed
	}
	if token.accountID != accountID || token.deviceID != deviceID {
		return SyncResult{}, ErrTokenScopeMismatch
	}
	if token.cursor != req.Cursor {
		return SyncResult{}, ErrCursorStale
	}

	account := s.accountLocked(accountID)
	if token.cursor > account.version {
		return SyncResult{}, ErrCursorStale
	}

	changes := changesAfter(account.changes, token.cursor)

	// Generate the replacement before consuming the old token so a random
	// source failure cannot leave the caller without a usable token.
	nextCursor := account.version
	nextToken, nextKey, err := newOpaqueToken()
	if err != nil {
		return SyncResult{}, err
	}
	for s.tokens[nextKey] != nil {
		nextToken, nextKey, err = newOpaqueToken()
		if err != nil {
			return SyncResult{}, err
		}
	}

	token.used = true
	s.tokens[nextKey] = &tokenRecord{
		accountID: accountID,
		deviceID:  deviceID,
		cursor:    nextCursor,
	}

	return SyncResult{
		Changes:    changes,
		NextCursor: nextCursor,
		NextToken:  nextToken,
	}, nil
}

// Pull is a convenience form of Sync for callers that do not use SyncRequest.
func (s *SyncScope) Pull(accountID, deviceID string, token Token, cursor uint64) (SyncResult, error) {
	return s.Sync(SyncRequest{
		AccountID: accountID,
		DeviceID:  deviceID,
		Token:     token,
		Cursor:    cursor,
	})
}

func (s *SyncScope) checkDeviceLocked(accountID, deviceID string) error {
	device, ok := s.devices[deviceID]
	if !ok {
		return ErrDeviceNotRegistered
	}
	if device.accountID != accountID {
		return ErrDeviceScopeMismatch
	}
	if device.revoked {
		return ErrDeviceRevoked
	}
	if !device.approved {
		return ErrDeviceNotApproved
	}
	return nil
}

func (s *SyncScope) accountLocked(accountID string) *accountState {
	s.ensureInitLocked()
	account, ok := s.accounts[accountID]
	if !ok {
		account = &accountState{contacts: make(map[string]Contact)}
		s.accounts[accountID] = account
	}
	return account
}

func (s *SyncScope) ensureInitLocked() {
	if s.devices == nil {
		s.devices = make(map[string]*deviceRecord)
	}
	if s.tokens == nil {
		s.tokens = make(map[[sha256.Size]byte]*tokenRecord)
	}
	if s.accounts == nil {
		s.accounts = make(map[string]*accountState)
	}
}

func (s *SyncScope) newTokenLocked(accountID, deviceID string, cursor uint64) (Token, error) {
	token, key, err := newOpaqueToken()
	if err != nil {
		return "", err
	}
	for s.tokens[key] != nil {
		token, key, err = newOpaqueToken()
		if err != nil {
			return "", err
		}
	}
	s.tokens[key] = &tokenRecord{
		accountID: accountID,
		deviceID:  deviceID,
		cursor:    cursor,
	}
	return token, nil
}

func newOpaqueToken() (Token, [sha256.Size]byte, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [sha256.Size]byte{}, ErrTokenGeneration
	}
	value := base64.RawURLEncoding.EncodeToString(raw[:])
	return Token(value), hashToken(Token(value)), nil
}

func hashToken(token Token) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func changesAfter(changes []Change, cursor uint64) []Change {
	index := sort.Search(len(changes), func(i int) bool {
		return changes[i].Version > cursor
	})
	if index == len(changes) {
		return []Change{}
	}
	result := make([]Change, len(changes)-index)
	copy(result, changes[index:])
	return result
}

func cloneContact(contact Contact) Contact {
	contact.ID = strings.TrimSpace(contact.ID)
	return contact
}
