package netsec

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sync"
	"time"
)

// Service is an in-memory admission and sync-policy component. A hosting
// credential-vault service owns one Service per deployment and calls it
// after authenticating the transport identity of the calling device.
//
// All methods are safe for concurrent use. A Service must not be copied
// after first use. The zero value is ready to use.
type Service struct {
	mu sync.RWMutex

	initOnce    sync.Once
	now         func() time.Time
	recoveryKey [32]byte

	accounts    map[AccountID]*account
	devices     map[DeviceID]*device
	enrollments map[EnrollmentID]*enrollment

	alerts       []Alert
	alertHandler func(Alert)
}

const maxAlertHistory = 4096

type account struct {
	id       AccountID
	devices  map[DeviceID]*device
	entries  map[EntryID]*entryRecord
	recovery map[[32]byte]*recoveryRecord
}

type device struct {
	id                    DeviceID
	accountID             AccountID
	keyAgreementPublicKey []byte
	signingPublicKey      ed25519.PublicKey
	status                DeviceStatus
	owner                 bool
	allEntries            bool
	scopes                map[EntryID]struct{}
	wrappedKey            []byte
	enrollmentID          EnrollmentID
	approvedBy            DeviceID
	createdAt             time.Time
	approvedAt            time.Time
	revokedAt             time.Time
}

type enrollment struct {
	id           EnrollmentID
	accountID    AccountID
	deviceID     DeviceID
	status       EnrollmentStatus
	recoveryUsed bool
	createdAt    time.Time
	decidedAt    time.Time
}

type recoveryRecord struct {
	digest    [32]byte
	used      bool
	createdAt time.Time
	createdBy DeviceID
	usedAt    time.Time
	usedBy    DeviceID
}

type entryRecord struct {
	id         EntryID
	accountID  AccountID
	ciphertext []byte
	metadata   []byte
	version    uint64
	updatedAt  time.Time
}

// NewService creates an empty admission service. It panics only if the
// operating system cannot provide cryptographic randomness.
func NewService() *Service {
	s := &Service{}
	s.ensureInitialized()
	return s
}

// New is an alias for NewService.
func New() *Service {
	return NewService()
}

// ensureInitialized makes the zero value of Service usable. It is safe to
// call concurrently and is a no-op after the first call.
func (s *Service) ensureInitialized() {
	s.initOnce.Do(func() {
		if s.now == nil {
			s.now = time.Now
		}
		if s.accounts == nil {
			s.accounts = make(map[AccountID]*account)
		}
		if s.devices == nil {
			s.devices = make(map[DeviceID]*device)
		}
		if s.enrollments == nil {
			s.enrollments = make(map[EnrollmentID]*enrollment)
		}
		if _, err := rand.Read(s.recoveryKey[:]); err != nil {
			panic(fmt.Sprintf("netsec: initialize recovery key: %v", err))
		}
	})
}

// CreateAccount creates an account whose first device is already approved.
// The first device is the account owner and has implicit access to all
// entries.
func (s *Service) CreateAccount(owner NewDevice) (AccountID, error) {
	id, err := newOpaqueID("acct_")
	if err != nil {
		return "", err
	}
	accountID := AccountID(id)
	if err := s.CreateAccountWithID(accountID, owner); err != nil {
		return "", err
	}
	return accountID, nil
}

// CreateAccountWithID creates an account with a caller-selected identifier.
// It is useful when the vault service stores its own stable account IDs.
func (s *Service) CreateAccountWithID(accountID AccountID, owner NewDevice) error {
	if accountID == "" {
		return fmt.Errorf("%w: empty account id", ErrInvalidArgument)
	}
	if err := validateNewDevice(owner); err != nil {
		return err
	}

	s.ensureInitialized()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.accounts[accountID]; exists {
		return fmt.Errorf("%w: %q", ErrAccountAlreadyExists, accountID)
	}
	if _, exists := s.devices[owner.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDeviceAlreadyExists, owner.ID)
	}

	now := s.now()
	acct := &account{
		id:       accountID,
		devices:  make(map[DeviceID]*device),
		entries:  make(map[EntryID]*entryRecord),
		recovery: make(map[[32]byte]*recoveryRecord),
	}
	ownerDevice := &device{
		id:                    owner.ID,
		accountID:             accountID,
		keyAgreementPublicKey: append([]byte(nil), owner.KeyAgreementPublicKey...),
		signingPublicKey:      append(ed25519.PublicKey(nil), owner.SigningPublicKey...),
		status:                DeviceApproved,
		owner:                 true,
		allEntries:            true,
		createdAt:             now,
		approvedAt:            now,
	}
	acct.devices[owner.ID] = ownerDevice
	s.accounts[accountID] = acct
	s.devices[owner.ID] = ownerDevice
	return nil
}

// PutEntry stores an opaque ciphertext entry in an account. The calling
// device must be approved and active in that same account. Storing an
// existing EntryID creates a new version.
func (s *Service) PutEntry(accountID AccountID, actorID DeviceID, entryID EntryID, ciphertext, metadata []byte) error {
	if entryID == "" {
		return fmt.Errorf("%w: empty entry id", ErrInvalidArgument)
	}
	if len(ciphertext) == 0 {
		return fmt.Errorf("%w: empty ciphertext", ErrInvalidArgument)
	}

	s.ensureInitialized()
	s.mu.Lock()
	defer s.mu.Unlock()

	acct, dev, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return err
	}
	if !dev.allEntries {
		if _, allowed := dev.scopes[entryID]; !allowed {
			return fmt.Errorf("%w: %q", ErrScopeDenied, entryID)
		}
	}

	now := s.now()
	record, exists := acct.entries[entryID]
	if !exists {
		record = &entryRecord{
			id:        entryID,
			accountID: accountID,
			version:   1,
		}
		acct.entries[entryID] = record
	} else {
		record.version++
	}
	record.ciphertext = append([]byte(nil), ciphertext...)
	record.metadata = append([]byte(nil), metadata...)
	record.updatedAt = now
	return nil
}

// WrappedAccountKey returns the encrypted account-key envelope that the
// approving device stored for deviceID. Only approved, active devices can
// retrieve their envelope. Removing the envelope from the service on
// revocation also prevents later retrieval.
func (s *Service) WrappedAccountKey(accountID AccountID, deviceID DeviceID) ([]byte, error) {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, dev, err := s.authorizeLocked(accountID, deviceID)
	if err != nil {
		return nil, err
	}
	if len(dev.wrappedKey) == 0 {
		return nil, fmt.Errorf("%w: no wrapped key", ErrDeviceNotApproved)
	}
	return append([]byte(nil), dev.wrappedKey...), nil
}

// Device returns a snapshot of one device's state.
func (s *Service) Device(accountID AccountID, deviceID DeviceID) (Device, error) {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, dev, err := s.lookupDeviceLocked(accountID, deviceID)
	if err != nil {
		return Device{}, err
	}
	return deviceView(dev), nil
}

// EnrollmentInfo returns a snapshot of one enrollment request.
func (s *Service) EnrollmentInfo(accountID AccountID, enrollmentID EnrollmentID) (Enrollment, error) {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	enr, exists := s.enrollments[enrollmentID]
	if !exists || enr.accountID != accountID {
		return Enrollment{}, fmt.Errorf("%w: %q", ErrEnrollmentNotFound, enrollmentID)
	}
	return enrollmentView(enr), nil
}

// Alerts returns a copy of the security-alert history. History is bounded
// to maxAlertHistory entries; handlers still receive every event.
func (s *Service) Alerts() []Alert {
	s.ensureInitialized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Alert, len(s.alerts))
	copy(out, s.alerts)
	return out
}

// SetAlertHandler installs a callback invoked synchronously after the
// service lock is released. Passing nil clears the handler. Alert history
// is always retained regardless of the handler.
func (s *Service) SetAlertHandler(handler func(Alert)) {
	s.ensureInitialized()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alertHandler = handler
}

// RevokeDevice permanently withdraws a device's access. An owner may revoke
// any device in the account, and any approved device may revoke itself.
// Revocation removes the wrapped key and item scopes.
func (s *Service) RevokeDevice(accountID AccountID, actorID, targetID DeviceID) error {
	s.ensureInitialized()
	s.mu.Lock()
	var events []Alert
	defer func() { s.finishLocked(events) }()

	acct, actor, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return err
	}
	target, exists := acct.devices[targetID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrDeviceNotFound, targetID)
	}
	if target.status == DeviceRevoked {
		return nil
	}
	if actorID != targetID && !actor.owner {
		return fmt.Errorf("%w: only the account owner may revoke another device", ErrApprovalRequired)
	}
	if target.status != DeviceApproved {
		return fmt.Errorf("%w: %q", ErrDeviceNotApproved, targetID)
	}

	now := s.now()
	target.status = DeviceRevoked
	target.revokedAt = now
	target.wrappedKey = nil
	target.scopes = nil
	target.allEntries = false
	event := Alert{
		Type:      AlertDeviceRevoked,
		At:        now,
		AccountID: accountID,
		DeviceID:  targetID,
		Message:   "device revoked; sync and key retrieval are disabled",
	}
	events = append(events, event)
	s.recordAlertLocked(event)
	return nil
}

// finishLocked must be deferred while s.mu is held. It snapshots the alert
// handler, releases the write lock, then dispatches events without holding
// the service lock.
func (s *Service) finishLocked(events []Alert) {
	handler := s.alertHandler
	s.mu.Unlock()
	dispatchAlerts(handler, events)
}

func dispatchAlerts(handler func(Alert), events []Alert) {
	if handler == nil {
		return
	}
	for _, event := range events {
		handler(event)
	}
}

func (s *Service) recordAlertLocked(event Alert) {
	if len(s.alerts) >= maxAlertHistory {
		copy(s.alerts, s.alerts[1:])
		s.alerts[len(s.alerts)-1] = event
		return
	}
	s.alerts = append(s.alerts, event)
}

func (s *Service) authorizeLocked(accountID AccountID, deviceID DeviceID) (*account, *device, error) {
	acct, dev, err := s.lookupDeviceLocked(accountID, deviceID)
	if err != nil {
		return nil, nil, err
	}
	switch dev.status {
	case DeviceRevoked:
		return nil, nil, fmt.Errorf("%w: %q", ErrDeviceRevoked, deviceID)
	case DeviceApproved:
		return acct, dev, nil
	default:
		return nil, nil, fmt.Errorf("%w: %q", ErrDeviceNotApproved, deviceID)
	}
}

func (s *Service) lookupDeviceLocked(accountID AccountID, deviceID DeviceID) (*account, *device, error) {
	acct, exists := s.accounts[accountID]
	if !exists {
		if other, ok := s.devices[deviceID]; ok && other.accountID != accountID {
			return nil, nil, fmt.Errorf("%w: device %q does not belong to account %q", ErrCrossAccount, deviceID, accountID)
		}
		return nil, nil, fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	dev, exists := acct.devices[deviceID]
	if !exists {
		if other, ok := s.devices[deviceID]; ok && other.accountID != accountID {
			return nil, nil, fmt.Errorf("%w: device %q does not belong to account %q", ErrCrossAccount, deviceID, accountID)
		}
		return nil, nil, fmt.Errorf("%w: %q", ErrDeviceNotFound, deviceID)
	}
	if dev.accountID != accountID {
		return nil, nil, fmt.Errorf("%w: device %q does not belong to account %q", ErrCrossAccount, deviceID, accountID)
	}
	return acct, dev, nil
}

func validateNewDevice(dev NewDevice) error {
	if dev.ID == "" {
		return fmt.Errorf("%w: empty device id", ErrInvalidArgument)
	}
	if len(dev.KeyAgreementPublicKey) != keyAgreementKeySize {
		return fmt.Errorf("%w: key agreement public key must be %d bytes", ErrInvalidArgument, keyAgreementKeySize)
	}
	if _, err := ecdh.X25519().NewPublicKey(dev.KeyAgreementPublicKey); err != nil {
		return fmt.Errorf("%w: key agreement public key: %v", ErrInvalidArgument, err)
	}
	if len(dev.SigningPublicKey) != 0 && len(dev.SigningPublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: signing public key must be %d bytes", ErrInvalidArgument, ed25519.PublicKeySize)
	}
	return nil
}

func deviceView(dev *device) Device {
	return Device{
		ID:                    dev.id,
		AccountID:             dev.accountID,
		Status:                dev.status,
		Owner:                 dev.owner,
		AllEntries:            dev.allEntries,
		Scopes:                scopeList(dev.scopes),
		EnrollmentID:          dev.enrollmentID,
		ApprovedBy:            dev.approvedBy,
		KeyAgreementPublicKey: append([]byte(nil), dev.keyAgreementPublicKey...),
		SigningPublicKey:      append(ed25519.PublicKey(nil), dev.signingPublicKey...),
		CreatedAt:             dev.createdAt,
		ApprovedAt:            dev.approvedAt,
		RevokedAt:             dev.revokedAt,
	}
}

func enrollmentView(enr *enrollment) Enrollment {
	return Enrollment{
		ID:           enr.id,
		AccountID:    enr.accountID,
		DeviceID:     enr.deviceID,
		Status:       enr.status,
		RecoveryUsed: enr.recoveryUsed,
		CreatedAt:    enr.createdAt,
		DecidedAt:    enr.decidedAt,
	}
}
