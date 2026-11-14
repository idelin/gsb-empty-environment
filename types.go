package netsec

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// AccountID identifies a credential-vault account.
type AccountID string

// DeviceID identifies a device enrolled in an account.
type DeviceID string

// EntryID identifies an item in an account's credential store.
type EntryID string

// EnrollmentID identifies one admission request.
type EnrollmentID string

// RecoveryCode is a one-time account-recovery secret. Treat its printed
// form as sensitive.
type RecoveryCode string

// DeviceStatus is the admission state of a device.
type DeviceStatus uint8

const (
	// DevicePending means the device requested admission but has not been
	// approved.
	DevicePending DeviceStatus = iota
	// DeviceApproved means an existing device approved admission and supplied
	// a wrapped account key.
	DeviceApproved
	// DeviceRevoked means all access for the device has been withdrawn.
	DeviceRevoked
	// DeviceRejected means the admission request was explicitly rejected.
	DeviceRejected
)

func (s DeviceStatus) String() string {
	switch s {
	case DevicePending:
		return "pending"
	case DeviceApproved:
		return "approved"
	case DeviceRevoked:
		return "revoked"
	case DeviceRejected:
		return "rejected"
	default:
		return fmt.Sprintf("device-status(%d)", s)
	}
}

// EnrollmentStatus is the decision state of an admission request.
type EnrollmentStatus uint8

const (
	EnrollmentPending EnrollmentStatus = iota
	EnrollmentApproved
	EnrollmentRejected
)

func (s EnrollmentStatus) String() string {
	switch s {
	case EnrollmentPending:
		return "pending"
	case EnrollmentApproved:
		return "approved"
	case EnrollmentRejected:
		return "rejected"
	default:
		return fmt.Sprintf("enrollment-status(%d)", s)
	}
}

// NewDevice contains the public key material supplied by a joining device.
// The X25519 key is used to wrap the account key to that device; the
// optional Ed25519 key is retained as device identity metadata so the
// hosting service can authenticate future transport-level calls.
type NewDevice struct {
	ID                    DeviceID
	KeyAgreementPublicKey []byte
	SigningPublicKey      ed25519.PublicKey
}

// Device is a read-only view of a device's admission state.
type Device struct {
	ID                    DeviceID
	AccountID             AccountID
	Status                DeviceStatus
	Owner                 bool
	AllEntries            bool
	Scopes                []EntryID
	EnrollmentID          EnrollmentID
	ApprovedBy            DeviceID
	KeyAgreementPublicKey []byte
	SigningPublicKey      ed25519.PublicKey
	CreatedAt             time.Time
	ApprovedAt            time.Time
	RevokedAt             time.Time
}

// Enrollment is a read-only view of an admission request.
type Enrollment struct {
	ID           EnrollmentID
	AccountID    AccountID
	DeviceID     DeviceID
	Status       EnrollmentStatus
	RecoveryUsed bool
	CreatedAt    time.Time
	DecidedAt    time.Time
}

// Entry is a ciphertext entry returned by a sync operation. The caller
// decrypts it with the account key.
type Entry struct {
	ID         EntryID
	AccountID  AccountID
	Ciphertext []byte
	Metadata   []byte
	Version    uint64
	UpdatedAt  time.Time
}

// AlertType identifies a security-relevant admission event.
type AlertType string

const (
	// AlertRecoveryCodeUsed is emitted exactly when a recovery code is
	// successfully consumed.
	AlertRecoveryCodeUsed AlertType = "recovery_code_used"
	// AlertRecoveryCodeReuse is emitted when someone presents a recovery
	// code that was already consumed.
	AlertRecoveryCodeReuse AlertType = "recovery_code_reuse"
	// AlertDeviceApproved is emitted after a pending device is approved.
	AlertDeviceApproved AlertType = "device_approved"
	// AlertDeviceRejected is emitted when an enrollment is rejected.
	AlertDeviceRejected AlertType = "device_rejected"
	// AlertDeviceRevoked is emitted when a device is revoked.
	AlertDeviceRevoked AlertType = "device_revoked"
)

// Alert is a security event suitable for logging or forwarding to an
// account owner. It never contains a plaintext recovery code or account key.
type Alert struct {
	Type         AlertType
	At           time.Time
	AccountID    AccountID
	DeviceID     DeviceID
	EnrollmentID EnrollmentID
	Message      string
}

// DeviceSecrets is a convenience bundle returned by GenerateDevice. The
// private keys must remain on the joining device.
type DeviceSecrets struct {
	ID                     DeviceID
	SigningPrivateKey      ed25519.PrivateKey
	SigningPublicKey       ed25519.PublicKey
	KeyAgreementPrivateKey *ecdh.PrivateKey
	KeyAgreementPublicKey  []byte
}

// GenerateDevice creates fresh Ed25519 identity keys and an X25519 key
// agreement key for a new device. The hosting service only receives the
// public halves through Public.
func GenerateDevice(id DeviceID) (*DeviceSecrets, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty device id", ErrInvalidArgument)
	}
	signPub, signPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate signing key: %w", err)
	}
	ecdhPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate key-agreement key: %w", err)
	}
	return &DeviceSecrets{
		ID:                     id,
		SigningPrivateKey:      signPriv,
		SigningPublicKey:       signPub,
		KeyAgreementPrivateKey: ecdhPriv,
		KeyAgreementPublicKey:  ecdhPriv.PublicKey().Bytes(),
	}, nil
}

// Public returns the non-secret device material to submit for admission.
func (d *DeviceSecrets) Public() NewDevice {
	return NewDevice{
		ID:                    d.ID,
		KeyAgreementPublicKey: append([]byte(nil), d.KeyAgreementPublicKey...),
		SigningPublicKey:      append(ed25519.PublicKey(nil), d.SigningPublicKey...),
	}
}

func newOpaqueID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("netsec: generate id: %w", err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}
