package ctap2auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"unicode/utf8"
)

// pinUvAuthToken permission bits (FIDO CTAP §5.3, pinUvAuthToken permissions).
type Permission uint8

const (
	PermMakeCredential Permission = 1 << 0 // mc
	PermGetAssertion   Permission = 1 << 1 // ga
	PermCredentialMgmt Permission = 1 << 2 // cm
	PermBioEnrollment  Permission = 1 << 3 // be
)

const (
	// MaxPINRetries is the CTAP2 default pinRetries budget.
	MaxPINRetries = 8
	// MaxAuthFailuresPerPowerUp triggers CTAP2_ERR_PIN_AUTH_BLOCKED.
	MaxAuthFailuresPerPowerUp = 3
	minPINCodePoints          = 4
	maxPINCodePoints          = 63
	tokenSizeBytes            = 32
)

// PinUvAuthToken is an issued, permission-scoped and optionally RP-bound token.
type PinUvAuthToken struct {
	Bytes       []byte
	Permissions Permission
	RPID        string
}

// PINManager implements the CTAP2 PIN retry/lock state machine and pinUvAuth
// token bookkeeping for one authenticator. It is safe for concurrent use.
type PINManager struct {
	mu sync.Mutex

	pinSet      bool
	pinHash     []byte
	pinRetries  int
	authFails   int // consecutive pinUvAuthParam failures since power-up
	authBlocked bool
	tokens      map[string]*PinUvAuthToken
}

// NewPINManager returns a manager with no PIN provisioned and the default
// retry budget. SetPin must succeed before tokens can be issued.
func NewPINManager() *PINManager {
	return &PINManager{
		pinRetries: MaxPINRetries,
		tokens:     make(map[string]*PinUvAuthToken),
	}
}

// PinHash returns SHA-256 over the UTF-8 PIN bytes, the value the platform
// AES-CBC encrypts into the pinHashEnc ClientPIN parameter.
func PinHash(pin string) []byte {
	sum := sha256.Sum256([]byte(pin))
	out := make([]byte, len(sum))
	copy(out, sum[:])
	return out
}

// SetPin provisions or replaces the PIN. CTAP2 requires 4-63 Unicode code
// points. Provisioning resets the retry budget, clears the power-up failure
// state and revokes every outstanding token.
func (m *PINManager) SetPin(pin string) error {
	runes := utf8.RuneCountInString(pin)
	if runes < minPINCodePoints || runes > maxPINCodePoints {
		return ErrPinPolicyViolation
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pinHash = PinHash(pin)
	m.pinSet = true
	m.pinRetries = MaxPINRetries
	m.authFails = 0
	m.authBlocked = false
	m.tokens = make(map[string]*PinUvAuthToken)
	return nil
}

// Retries returns the remaining pinRetries budget (0 means permanently blocked).
func (m *PINManager) Retries() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinRetries
}

// PinBlocked reports whether pinRetries reached zero (permanent lock).
func (m *PINManager) PinBlocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinSet && m.pinRetries == 0
}

// AuthBlocked reports the power-up scoped CTAP2_ERR_PIN_AUTH_BLOCKED state.
func (m *PINManager) AuthBlocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authBlocked
}

// PowerCycle models an authenticator power cycle: the three-strike
// pinUvAuthParam block and in-memory tokens are cleared, while the persistent
// pinRetries budget and a permanent PIN block survive.
func (m *PINManager) PowerCycle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authFails = 0
	m.authBlocked = false
	m.tokens = make(map[string]*PinUvAuthToken)
}

func (m *PINManager) recordAuthFailure() error {
	m.authFails++
	if m.authFails >= MaxAuthFailuresPerPowerUp {
		m.authBlocked = true
		return ErrPinAuthBlocked
	}
	return ErrPinAuthInvalid
}

// IssuePinUvAuthToken validates pinHashEnc against the provisioned PIN and, on
// success, mints a token carrying the requested permissions and optional RP
// binding. pinHashEnc must be AES-256-CBC (IV||ciphertext) produced with a
// random IV under the session AES key.
//
// A cryptographic failure (bad IV/ciphertext) counts toward the three-strike
// power-up block; a wrong PIN decrements pinRetries and permanently locks at
// zero.
func (m *PINManager) IssuePinUvAuthToken(session *AuthSession, permissions Permission, rpID string, pinHashEnc []byte) (*PinUvAuthToken, error) {
	if permissions == 0 {
		return nil, ErrMissingParameter
	}
	if permissions&(PermMakeCredential|PermGetAssertion) != 0 && rpID == "" {
		return nil, ErrMissingParameter
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.pinSet {
		return nil, ErrPinNotSet
	}
	if m.pinRetries == 0 {
		return nil, ErrPinBlocked
	}
	if m.authBlocked {
		return nil, ErrPinAuthBlocked
	}

	gotHash, err := session.Decrypt(pinHashEnc)
	if err != nil {
		return nil, m.recordAuthFailure()
	}
	if !hmac.Equal(gotHash, m.pinHash) {
		m.pinRetries--
		if m.pinRetries == 0 {
			return nil, ErrPinBlocked
		}
		return nil, ErrPinInvalid
	}

	// Successful authentication clears the consecutive power-up failure count.
	m.authFails = 0

	tokenBytes := make([]byte, tokenSizeBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := &PinUvAuthToken{Bytes: tokenBytes, Permissions: permissions, RPID: rpID}
	m.tokens[hex.EncodeToString(tokenBytes)] = token
	return token, nil
}

// VerifyPinUvAuthParam checks HMAC-SHA-256(message) keyed by the token. A
// failure increments the power-up counter and blocks at three consecutive
// failures; a success resets it. Protocol One (16-byte) tags are rejected.
func (m *PINManager) VerifyPinUvAuthParam(tokenBytes, message, authParam []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.authBlocked {
		return false, ErrPinAuthBlocked
	}

	token, ok := m.tokens[hex.EncodeToString(tokenBytes)]
	if !ok {
		return false, m.recordAuthFailure()
	}
	mac := hmac.New(sha256.New, token.Bytes)
	mac.Write(message)
	expected := mac.Sum(nil)

	if len(authParam) == 16 {
		return false, ErrProtocolOneDowngrade
	}
	if len(authParam) != macSizeBytes || !hmac.Equal(expected, authParam) {
		return false, m.recordAuthFailure()
	}
	m.authFails = 0
	return true, nil
}

// AssertPermission enforces the token permission mask and RP binding. MC/GA are
// RP-scoped and must match the caller's rpID exactly; CM/BE are management
// permissions without an RP scope.
func (m *PINManager) AssertPermission(tokenBytes []byte, want Permission, rpID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	token, ok := m.tokens[hex.EncodeToString(tokenBytes)]
	if !ok {
		return ErrUnknownToken
	}
	if token.Permissions&want != want {
		return ErrPermissionDenied
	}
	if want&(PermMakeCredential|PermGetAssertion) != 0 {
		if token.RPID == "" {
			return ErrTokenNotRPBound
		}
		if rpID == "" {
			return ErrMissingParameter
		}
		if token.RPID != rpID {
			return ErrRPIDMismatch
		}
	}
	return nil
}

// RevokeToken removes a token, e.g. after use or a management reset.
func (m *PINManager) RevokeToken(tokenBytes []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, hex.EncodeToString(tokenBytes))
}
