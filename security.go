package netsec

import (
	"errors"
	"fmt"
)

// SecurityLevel is the GlobalPlatform secure-channel security level bit mask.
// C-MAC protects command integrity; C-DECRYPTION is only meaningful when
// commands are also integrity protected.
type SecurityLevel uint8

const (
	LevelNone SecurityLevel = 0x00

	// C-MAC requests command integrity protection.
	C_MAC SecurityLevel = 0x01
	// C_DECRYPTION requests command confidentiality protection.
	C_DECRYPTION SecurityLevel = 0x02

	LevelCMAC        = C_MAC
	LevelCDecryption = C_DECRYPTION
	LevelFull        = C_MAC | C_DECRYPTION

	CMAC        = C_MAC
	CDecryption = C_DECRYPTION
)

// Common aliases for callers that do not use the bit names directly.
const (
	SecurityLevelNone        = LevelNone
	SecurityLevelCMAC        = LevelCMAC
	SecurityLevelCDecryption = LevelCDecryption
	SecurityLevelFull        = LevelFull
)

// Valid reports whether l contains only defined security-level bits.
func (l SecurityLevel) Valid() bool {
	return l&^(C_MAC|C_DECRYPTION) == 0
}

// HasMAC reports whether command integrity protection is enabled.
func (l SecurityLevel) HasMAC() bool {
	return l&C_MAC != 0
}

// HasDecryption reports whether command confidentiality protection is enabled.
func (l SecurityLevel) HasDecryption() bool {
	return l&C_DECRYPTION != 0
}

// Has reports whether l enables every bit in required.
func (l SecurityLevel) Has(required SecurityLevel) bool {
	return l&required == required
}

func (l SecurityLevel) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelCMAC:
		return "C-MAC"
	case LevelCDecryption:
		return "C-DECRYPTION"
	case LevelFull:
		return "C-MAC+C-DECRYPTION"
	default:
		return fmt.Sprintf("security-level(0x%02x)", uint8(l))
	}
}

// MACFunc computes a command MAC and returns the next MAC chaining value.
//
// counter is the command sequence counter supplied by the dispatcher.
// chainingValue is the value returned by the preceding successful command.
// apdu is the command APDU as supplied to VerifyCommand or Dispatch, excluding
// the received MAC.
//
// Implementations must bind counter, chainingValue, and apdu into the returned
// MAC; otherwise a captured MAC could be reused with a different counter or
// command body.
//
// The callback is injected by the host so that session-key and algorithm
// choices remain outside this state package.
type MACFunc func(counter uint32, chainingValue, apdu []byte) ([]byte, error)

var (
	ErrNoMACFunction          = errors.New("netsec: no MAC function configured")
	ErrNotAuthenticated       = errors.New("netsec: secure channel is not authenticated")
	ErrChannelInvalid         = errors.New("netsec: secure channel is invalid")
	ErrInvalidSecurityLevel   = errors.New("netsec: invalid security level")
	ErrSecurityLevelDowngrade = errors.New("netsec: security level downgrade")
	ErrInsufficientSecurity   = errors.New("netsec: insufficient command security")
	ErrMalformedAPDU          = errors.New("netsec: malformed APDU")
	ErrMACMismatch            = errors.New("netsec: command MAC mismatch")
	ErrMACGeneration          = errors.New("netsec: command MAC generation failed")
	ErrCounterMismatch        = errors.New("netsec: command counter mismatch")
	ErrCounterExhausted       = errors.New("netsec: command counter exhausted")

	// ErrReplay and ErrCounterRollback both match ErrCounterMismatch with
	// errors.Is, while still allowing callers to distinguish the cases.
	ErrReplay          = fmt.Errorf("%w: replay detected", ErrCounterMismatch)
	ErrCounterRollback = fmt.Errorf("%w: counter rolled back", ErrReplay)
	ErrCounterGap      = fmt.Errorf("%w: counter gap", ErrCounterMismatch)
)

// Compatibility aliases with names commonly used in host integrations.
var (
	ErrAuthenticationFailed  = ErrMACMismatch
	ErrInvalidMAC            = ErrMACMismatch
	ErrSecurityDowngrade     = ErrSecurityLevelDowngrade
	ErrSecurityLevelRequired = ErrInsufficientSecurity
	ErrCounterReplay         = ErrReplay
)

type invalidChannelError struct {
	cause error
}

func (e *invalidChannelError) Error() string {
	if e.cause == nil {
		return ErrChannelInvalid.Error()
	}
	return fmt.Sprintf("%s: %v", ErrChannelInvalid, e.cause)
}

func (e *invalidChannelError) Unwrap() error {
	return e.cause
}

func (e *invalidChannelError) Is(target error) bool {
	return target == ErrChannelInvalid
}
