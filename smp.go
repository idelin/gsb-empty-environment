// Package netsec implements BLE SMP pairing method selection for a Go host
// stack. It derives the association model from both peers' IO capabilities,
// OOB flags and AuthReq bits (MITM 0x04, SC 0x08), enforces a caller
// required security level, and rejects short encryption keys.
package netsec

import "fmt"

// IO capability values as carried in the SMP Pairing Request/Response.
const (
	IODisplayOnly     uint8 = 0x00
	IODisplayYesNo    uint8 = 0x01
	IOKeyboardOnly    uint8 = 0x02
	IONoInputNoOutput uint8 = 0x03
	IOKeyboardDisplay uint8 = 0x04
)

// AuthReq bit masks (SMP Pairing Request/Response AuthReq field).
const (
	AuthReqBonding  uint8 = 0x01
	AuthReqMITM     uint8 = 0x04
	AuthReqSC       uint8 = 0x08
	AuthReqKeypress uint8 = 0x10
	AuthReqCT2      uint8 = 0x20
)

// SMP pairing failed reason codes (Core Spec, Vol 3, Part H 3.5.5).
const (
	ErrCodePasskeyEntryFailed  uint8 = 0x01
	ErrCodeOOBNotAvailable     uint8 = 0x02
	ErrCodeAuthRequirements    uint8 = 0x03
	ErrCodeConfirmValueFailed  uint8 = 0x04
	ErrCodePairingNotSupported uint8 = 0x05
	ErrCodeEncryptionKeySize   uint8 = 0x06
)

// PairingError is returned when the pairing request cannot satisfy the
// local security policy. Code is the SMP "Pairing Failed" reason the host
// should send back to the peer.
type PairingError struct {
	Code   uint8
	Reason string
}

func (e *PairingError) Error() string {
	return fmt.Sprintf("smp: pairing rejected (0x%02x): %s", e.Code, e.Reason)
}

// Method is the selected association model.
type Method int

const (
	MethodJustWorks Method = iota
	MethodPasskeyEntry
	MethodNumericComparison
	MethodOutOfBand
)

func (m Method) String() string {
	switch m {
	case MethodJustWorks:
		return "Just Works"
	case MethodPasskeyEntry:
		return "Passkey Entry"
	case MethodNumericComparison:
		return "Numeric Comparison"
	case MethodOutOfBand:
		return "Out Of Band"
	}
	return "unknown"
}

// SecurityLevel is the BLE security mode 1 level a pairing achieves.
type SecurityLevel int

const (
	// SecurityNone is level 1: no security.
	SecurityNone SecurityLevel = iota + 1
	// SecurityUnauthenticated is level 2: unauthenticated pairing with
	// encryption (e.g. Just Works).
	SecurityUnauthenticated
	// SecurityAuthenticated is level 3: authenticated pairing with
	// encryption (legacy authenticated pairing).
	SecurityAuthenticated
	// SecuritySecureConnections is level 4: authenticated LE Secure
	// Connections pairing with encryption.
	SecuritySecureConnections
)

func (l SecurityLevel) String() string {
	switch l {
	case SecurityNone:
		return "no security"
	case SecurityUnauthenticated:
		return "unauthenticated encryption"
	case SecurityAuthenticated:
		return "authenticated encryption"
	case SecuritySecureConnections:
		return "authenticated secure connections"
	}
	return "unknown"
}

// Features describes one side of the pairing exchange as announced in its
// Pairing Request/Response PDU.
type Features struct {
	IOCapability uint8
	// OOB is the OOB data flag: this side has out-of-band data available.
	OOB bool
	// AuthReq carries the raw AuthReq octet (MITM 0x04, SC 0x08, ...).
	AuthReq uint8
	// MaxKeySize is the maximum encryption key size in octets (7..16).
	MaxKeySize uint8
}

func (f Features) mitm() bool { return f.AuthReq&AuthReqMITM != 0 }
func (f Features) sc() bool   { return f.AuthReq&AuthReqSC != 0 }

// Policy is the local security policy enforced by SelectMethod.
type Policy struct {
	// RequiredLevel is the minimum acceptable security level. If the
	// derived method cannot reach it, the pairing is rejected with the
	// Authentication Requirements error (0x03).
	RequiredLevel SecurityLevel
	// MinKeySize is the minimum acceptable encryption key size in octets.
	// Zero defaults to 7, the Bluetooth minimum.
	MinKeySize uint8
}

// Result is the outcome of a successful SelectMethod call.
type Result struct {
	Method Method
	// Level is the security level the pairing will achieve.
	Level SecurityLevel
	// SecureConnections reports whether LE Secure Connections is used.
	// It requires the SC bit from both peers; a peer clearing it (e.g. a
	// downgrade attacker) forces legacy pairing.
	SecureConnections bool
	// KeySize is the negotiated maximum encryption key size in octets.
	KeySize uint8
}

const (
	minKeySize uint8 = 7
	maxKeySize uint8 = 16
)

// SelectMethod picks the association model for an SMP pairing request and
// enforces the local security policy. local and remote are the two peers'
// announced features; the result is independent of which side initiated.
func SelectMethod(local, remote Features, pol Policy) (Result, error) {
	sc := local.sc() && remote.sc()
	method := selectMethod(local, remote, sc)
	level := methodLevel(method, sc)

	if pol.RequiredLevel > level {
		return Result{}, &PairingError{
			Code: ErrCodeAuthRequirements,
			Reason: fmt.Sprintf("derived method %q reaches %s, but policy requires %s",
				method, level, pol.RequiredLevel),
		}
	}

	minKey := pol.MinKeySize
	if minKey == 0 {
		minKey = minKeySize
	}
	keySize := local.MaxKeySize
	if remote.MaxKeySize < keySize {
		keySize = remote.MaxKeySize
	}
	if keySize > maxKeySize {
		keySize = maxKeySize
	}
	if keySize < minKey {
		return Result{}, &PairingError{
			Code:   ErrCodeEncryptionKeySize,
			Reason: fmt.Sprintf("negotiated key size %d octets below minimum %d", keySize, minKey),
		}
	}

	return Result{
		Method:            method,
		Level:             level,
		SecureConnections: sc,
		KeySize:           keySize,
	}, nil
}

// selectMethod implements the IO capability mapping of Core Spec Vol 3,
// Part H, Table 2.8. OOB takes precedence; without a MITM request from
// either side the pairing degenerates to Just Works.
func selectMethod(local, remote Features, sc bool) Method {
	if local.OOB || remote.OOB {
		return MethodOutOfBand
	}
	if !local.mitm() && !remote.mitm() {
		return MethodJustWorks
	}
	return ioMethod(local.IOCapability, remote.IOCapability, sc)
}

func ioMethod(a, b uint8, sc bool) Method {
	if a == IONoInputNoOutput || b == IONoInputNoOutput {
		return MethodJustWorks
	}
	// Numeric Comparison exists only in LE Secure Connections and needs
	// a Yes/No confirmation on both sides.
	if sc && yesNoCapable(a) && yesNoCapable(b) {
		return MethodNumericComparison
	}
	if passkeyPossible(a, b) {
		return MethodPasskeyEntry
	}
	return MethodJustWorks
}

func displayCapable(io uint8) bool {
	return io == IODisplayOnly || io == IODisplayYesNo || io == IOKeyboardDisplay
}

func keyboardCapable(io uint8) bool {
	return io == IOKeyboardOnly || io == IOKeyboardDisplay
}

func yesNoCapable(io uint8) bool {
	return io == IODisplayYesNo || io == IOKeyboardDisplay
}

// passkeyPossible reports whether the IO pair can run Passkey Entry: one
// side displays and the other inputs, or both input.
func passkeyPossible(a, b uint8) bool {
	return (displayCapable(a) && keyboardCapable(b)) ||
		(keyboardCapable(a) && displayCapable(b)) ||
		(keyboardCapable(a) && keyboardCapable(b))
}

// methodLevel maps the association model to the security mode 1 level it
// achieves. OOB is assumed to run over an authenticated OOB channel.
func methodLevel(m Method, sc bool) SecurityLevel {
	switch m {
	case MethodJustWorks:
		return SecurityUnauthenticated
	case MethodPasskeyEntry, MethodOutOfBand:
		if sc {
			return SecuritySecureConnections
		}
		return SecurityAuthenticated
	case MethodNumericComparison:
		return SecuritySecureConnections
	}
	return SecurityNone
}
