package netsec

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// BeginEnrollment records a pending admission request. The request has no
// access until an existing approved device calls ApproveEnrollment or
// ApproveEnrollmentAll. The requesting device's private keys remain on the
// device; only public keys are stored here.
func (s *Service) BeginEnrollment(accountID AccountID, newDevice NewDevice) (*Enrollment, error) {
	s.ensureInitialized()
	if err := validateNewDevice(newDevice); err != nil {
		return nil, err
	}
	enrollmentID, err := newOpaqueID("enr_")
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	var events []Alert
	defer func() { s.finishLocked(events) }()

	acct, exists := s.accounts[accountID]
	if !exists {
		if _, registered := s.devices[newDevice.ID]; registered {
			return nil, fmt.Errorf("%w: device %q belongs to another account", ErrCrossAccount, newDevice.ID)
		}
		return nil, fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}
	enr, _, err := s.beginEnrollmentLocked(acct, EnrollmentID(enrollmentID), newDevice, false)
	if err != nil {
		return nil, err
	}
	view := enrollmentView(enr)
	return &view, nil
}

// BeginRecoveryEnrollment consumes a one-time recovery code and records a
// pending admission request. Consumption and request creation happen under
// one lock, so a code can create at most one request even under concurrent
// attempts. Successful use always emits AlertRecoveryCodeUsed.
//
// A recovery code does not grant access: the request still needs approval
// from an existing device and an account-key envelope wrapped to the new
// device.
func (s *Service) BeginRecoveryEnrollment(accountID AccountID, code RecoveryCode, newDevice NewDevice) (*Enrollment, error) {
	s.ensureInitialized()
	if err := validateNewDevice(newDevice); err != nil {
		return nil, err
	}
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return nil, fmt.Errorf("%w", ErrRecoveryCodeInvalid)
	}
	enrollmentID, err := newOpaqueID("enr_")
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	var events []Alert
	defer func() { s.finishLocked(events) }()

	acct, exists := s.accounts[accountID]
	if !exists {
		if _, registered := s.devices[newDevice.ID]; registered {
			return nil, fmt.Errorf("%w: device %q belongs to another account", ErrCrossAccount, newDevice.ID)
		}
		return nil, fmt.Errorf("%w: %q", ErrAccountNotFound, accountID)
	}

	digest := s.recoveryDigestLocked(accountID, normalized)
	record, exists := acct.recovery[digest]
	if !exists {
		return nil, fmt.Errorf("%w", ErrRecoveryCodeInvalid)
	}
	now := s.now()
	if record.used {
		events = append(events, Alert{
			Type:      AlertRecoveryCodeReuse,
			At:        now,
			AccountID: accountID,
			DeviceID:  newDevice.ID,
			Message:   "a previously consumed recovery code was presented again",
		})
		s.recordAlertLocked(events[len(events)-1])
		return nil, fmt.Errorf("%w", ErrRecoveryCodeUsed)
	}

	enr, _, err := s.beginEnrollmentLocked(acct, EnrollmentID(enrollmentID), newDevice, true)
	if err != nil {
		return nil, err
	}

	record.used = true
	record.usedAt = now
	record.usedBy = newDevice.ID
	usedAlert := Alert{
		Type:         AlertRecoveryCodeUsed,
		At:           now,
		AccountID:    accountID,
		DeviceID:     newDevice.ID,
		EnrollmentID: enr.id,
		Message:      "recovery code consumed for a pending device admission",
	}
	events = append(events, usedAlert)
	s.recordAlertLocked(usedAlert)

	view := enrollmentView(enr)
	return &view, nil
}

// BeginEnrollmentLocked is the shared insertion path. The caller must hold
// s.mu for writing and must have checked that acct exists.
func (s *Service) beginEnrollmentLocked(acct *account, enrollmentID EnrollmentID, newDevice NewDevice, recoveryUsed bool) (*enrollment, *device, error) {
	if _, exists := s.devices[newDevice.ID]; exists {
		return nil, nil, fmt.Errorf("%w: %q", ErrDeviceAlreadyExists, newDevice.ID)
	}
	if _, exists := s.enrollments[enrollmentID]; exists {
		return nil, nil, fmt.Errorf("%w: duplicate enrollment id", ErrInvalidArgument)
	}

	now := s.now()
	dev := &device{
		id:                    newDevice.ID,
		accountID:             acct.id,
		keyAgreementPublicKey: append([]byte(nil), newDevice.KeyAgreementPublicKey...),
		signingPublicKey:      append(ed25519.PublicKey(nil), newDevice.SigningPublicKey...),
		status:                DevicePending,
		enrollmentID:          enrollmentID,
		createdAt:             now,
	}
	enr := &enrollment{
		id:           enrollmentID,
		accountID:    acct.id,
		deviceID:     newDevice.ID,
		status:       EnrollmentPending,
		recoveryUsed: recoveryUsed,
		createdAt:    now,
	}
	acct.devices[newDevice.ID] = dev
	s.devices[newDevice.ID] = dev
	s.enrollments[enrollmentID] = enr
	return enr, dev, nil
}

// ApproveEnrollment approves a pending device, stores the account-key
// envelope wrapped to that device, and grants an explicit set of EntryIDs.
// An empty scope grants no entries. The approver must be an active, approved
// device in the same account and cannot approve itself.
func (s *Service) ApproveEnrollment(accountID AccountID, approverID DeviceID, enrollmentID EnrollmentID, wrappedAccountKey []byte, entryScopes []EntryID) error {
	return s.approveEnrollment(accountID, approverID, enrollmentID, wrappedAccountKey, entryScopes, false)
}

// ApproveEnrollmentAll is the explicit form of approval that grants a
// device access to all current and future entries in the account.
func (s *Service) ApproveEnrollmentAll(accountID AccountID, approverID DeviceID, enrollmentID EnrollmentID, wrappedAccountKey []byte) error {
	return s.approveEnrollment(accountID, approverID, enrollmentID, wrappedAccountKey, nil, true)
}

func (s *Service) approveEnrollment(accountID AccountID, approverID DeviceID, enrollmentID EnrollmentID, wrappedAccountKey []byte, entryScopes []EntryID, allEntries bool) error {
	s.ensureInitialized()
	if err := validateWrappedKey(wrappedAccountKey); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidWrappedKey, err)
	}

	s.mu.Lock()
	var events []Alert
	defer func() { s.finishLocked(events) }()

	acct, approver, err := s.authorizeLocked(accountID, approverID)
	if err != nil {
		return err
	}
	enr, exists := s.enrollments[enrollmentID]
	if !exists || enr.accountID != accountID {
		return fmt.Errorf("%w: %q", ErrEnrollmentNotFound, enrollmentID)
	}
	if enr.status != EnrollmentPending {
		return fmt.Errorf("%w: %q is %s", ErrEnrollmentNotPending, enrollmentID, enr.status)
	}
	target, exists := acct.devices[enr.deviceID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrDeviceNotFound, enr.deviceID)
	}
	if target.status != DevicePending {
		return fmt.Errorf("%w: %q is %s", ErrDeviceNotApproved, enr.deviceID, target.status)
	}
	if approverID == target.id {
		return fmt.Errorf("%w: device cannot approve itself", ErrApprovalRequired)
	}

	scopes, err := normalizeScopes(entryScopes)
	if err != nil {
		return err
	}
	if allEntries && !approver.allEntries {
		return fmt.Errorf("%w: approver cannot grant all entries", ErrScopeDenied)
	}
	if !approver.allEntries {
		for id := range scopes {
			if _, allowed := approver.scopes[id]; !allowed {
				return fmt.Errorf("%w: approver cannot grant %q", ErrScopeDenied, id)
			}
		}
	}
	now := s.now()
	target.status = DeviceApproved
	target.wrappedKey = append([]byte(nil), wrappedAccountKey...)
	target.scopes = scopes
	target.allEntries = allEntries
	target.approvedBy = approverID
	target.approvedAt = now
	enr.status = EnrollmentApproved
	enr.decidedAt = now

	event := Alert{
		Type:         AlertDeviceApproved,
		At:           now,
		AccountID:    accountID,
		DeviceID:     target.id,
		EnrollmentID: enrollmentID,
		Message:      "new device approved and account key envelope installed",
	}
	events = append(events, event)
	s.recordAlertLocked(event)
	return nil
}

// RejectEnrollment rejects a pending request. The rejected device remains
// known to the service (so its identifier cannot be silently reused) but
// has no access.
func (s *Service) RejectEnrollment(accountID AccountID, actorID DeviceID, enrollmentID EnrollmentID) error {
	s.ensureInitialized()
	s.mu.Lock()
	var events []Alert
	defer func() { s.finishLocked(events) }()

	acct, _, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return err
	}
	enr, exists := s.enrollments[enrollmentID]
	if !exists || enr.accountID != accountID {
		return fmt.Errorf("%w: %q", ErrEnrollmentNotFound, enrollmentID)
	}
	if enr.status != EnrollmentPending {
		return fmt.Errorf("%w: %q is %s", ErrEnrollmentNotPending, enrollmentID, enr.status)
	}
	target, exists := acct.devices[enr.deviceID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrDeviceNotFound, enr.deviceID)
	}
	now := s.now()
	target.status = DeviceRejected
	target.wrappedKey = nil
	target.scopes = nil
	target.allEntries = false
	enr.status = EnrollmentRejected
	enr.decidedAt = now
	event := Alert{
		Type:         AlertDeviceRejected,
		At:           now,
		AccountID:    accountID,
		DeviceID:     target.id,
		EnrollmentID: enrollmentID,
		Message:      "pending device admission rejected",
	}
	events = append(events, event)
	s.recordAlertLocked(event)
	return nil
}

// IssueRecoveryCode creates a 160-bit one-time recovery code for an
// account. Only the account owner may issue codes. The service stores only
// a keyed digest; the returned code must be shown once to the user.
func (s *Service) IssueRecoveryCode(accountID AccountID, actorID DeviceID) (RecoveryCode, error) {
	s.ensureInitialized()
	code, err := generateRecoveryCode()
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	acct, actor, err := s.authorizeLocked(accountID, actorID)
	if err != nil {
		return "", err
	}
	if !actor.owner {
		return "", fmt.Errorf("%w: only the account owner may issue recovery codes", ErrApprovalRequired)
	}
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return "", fmt.Errorf("%w: generated recovery code failed validation", ErrInvalidArgument)
	}
	digest := s.recoveryDigestLocked(accountID, normalized)
	if _, exists := acct.recovery[digest]; exists {
		return "", fmt.Errorf("%w: recovery code collision", ErrInvalidArgument)
	}
	acct.recovery[digest] = &recoveryRecord{
		digest:    digest,
		createdAt: s.now(),
		createdBy: actorID,
	}
	return code, nil
}

func generateRecoveryCode() (RecoveryCode, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate recovery code: %w", err)
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	groups := make([]string, 0, 4)
	for i := 0; i < len(encoded); i += 8 {
		groups = append(groups, encoded[i:i+8])
	}
	return RecoveryCode("RC-" + strings.Join(groups, "-")), nil
}

func normalizeRecoveryCode(code RecoveryCode) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(string(code)) {
		if unicode.IsSpace(r) || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	normalized := b.String()
	if len(normalized) == 34 && strings.HasPrefix(normalized, "RC") {
		normalized = normalized[2:]
	}
	if len(normalized) != 32 {
		return ""
	}
	for _, r := range normalized {
		if (r < 'A' || r > 'Z') && (r < '2' || r > '7') {
			return ""
		}
	}
	return normalized
}

func (s *Service) recoveryDigestLocked(accountID AccountID, normalized string) [32]byte {
	mac := hmac.New(sha256.New, s.recoveryKey[:])
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(accountID)))
	mac.Write(length[:])
	mac.Write([]byte(accountID))
	mac.Write([]byte{0})
	mac.Write([]byte(normalized))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func normalizeScopes(entryScopes []EntryID) (map[EntryID]struct{}, error) {
	scopes := make(map[EntryID]struct{}, len(entryScopes))
	for _, id := range entryScopes {
		if id == "" {
			return nil, fmt.Errorf("%w: empty entry scope", ErrInvalidArgument)
		}
		scopes[id] = struct{}{}
	}
	return scopes, nil
}

func scopeList(scopes map[EntryID]struct{}) []EntryID {
	out := make([]EntryID, 0, len(scopes))
	for id := range scopes {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
