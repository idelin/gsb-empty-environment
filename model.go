package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

const (
	// ExtendedProtocolDiscriminator5GMM identifies a 5GS mobility management
	// NAS message (TS 24.501, table 9.2).
	ExtendedProtocolDiscriminator5GMM byte = 0x7e

	// Security header types defined for 5GS NAS messages (TS 24.501, table
	// 9.3.1).
	SecurityHeaderPlain                            byte = 0x00
	SecurityHeaderIntegrityProtected               byte = 0x01
	SecurityHeaderIntegrityProtectedCiphered       byte = 0x02
	SecurityHeaderIntegrityProtectedNewContext     byte = 0x03
	SecurityHeaderIntegrityProtectedCipheredNewCtx byte = 0x04
	SecurityHeaderMask                             byte = 0x0f

	// 5GMM message types used by the security mode control procedure.
	MessageTypeRegistrationRequest  byte = 0x41
	MessageTypeSecurityModeCommand  byte = 0x5d
	MessageTypeSecurityModeComplete byte = 0x5e
	MessageTypeSecurityModeReject   byte = 0x5f

	// IEIUESecurityCapability is the type-3 IEI of the UE security capability
	// IE (TS 24.501, clause 9.11.3.54).
	IEIUESecurityCapability byte = 0x2e

	// RegistrationTypeEmergency is the 5GS registration type value for an
	// emergency registration (TS 24.501, clause 9.11.3.7).
	RegistrationTypeEmergency byte = 0x04
	registrationTypeMask      byte = 0x07
)

const (
	// IntegrityAlgorithm5GIA0 is the null 5G NAS integrity algorithm.
	IntegrityAlgorithm5GIA0 byte = 0x00
	// IntegrityAlgorithm5GIA1 is 128-5G-IA1 (SNOW 3G).
	IntegrityAlgorithm5GIA1 byte = 0x01
	// IntegrityAlgorithm5GIA2 is 128-5G-IA2 (AES-CMAC).
	IntegrityAlgorithm5GIA2 byte = 0x02
	// IntegrityAlgorithm5GIA3 is 128-5G-IA3 (ZUC).
	IntegrityAlgorithm5GIA3 byte = 0x03

	// CipheringAlgorithm5GEA0 is the null 5G NAS ciphering algorithm.
	CipheringAlgorithm5GEA0 byte = 0x00
)

var (
	// ErrMalformedMessage indicates that a NAS message or IE cannot be
	// decoded safely.
	ErrMalformedMessage = errors.New("malformed NAS message")

	// ErrUnsupportedIntegrityAlgorithm indicates that the package has no
	// built-in implementation for the selected integrity algorithm.
	ErrUnsupportedIntegrityAlgorithm = errors.New("unsupported NAS integrity algorithm")
)

// UESecurityCapability is the decoded form of the UE security capability IE.
// Raw always contains the exact IE value octets, which are what the network
// must replay unchanged in the Security Mode Command.
type UESecurityCapability struct {
	Raw []byte

	// EA5G and IA5G are the bitmap octets for 5G ciphering and integrity
	// algorithms. Bit 8 represents algorithm 0, bit 7 algorithm 1, etc.
	EA5G byte
	IA5G byte

	// EEAEPS and EIAEPS are the corresponding EPS bitmap octets. They are
	// zero when the terminal did not include EPS capabilities.
	EEAEPS byte
	EIAEPS byte
}

// ParseUESecurityCapability decodes the value octets of the UE security
// capability IE. At least the 5G EA and 5G IA octets are required.
func ParseUESecurityCapability(raw []byte) (UESecurityCapability, error) {
	if len(raw) < 2 {
		return UESecurityCapability{}, fmt.Errorf("%w: UE security capability needs at least two octets", ErrMalformedMessage)
	}
	if len(raw) > 255 {
		return UESecurityCapability{}, fmt.Errorf("%w: UE security capability is too long", ErrMalformedMessage)
	}

	capability := UESecurityCapability{
		Raw:  append([]byte(nil), raw...),
		EA5G: raw[0],
		IA5G: raw[1],
	}
	if len(raw) >= 4 {
		capability.EEAEPS = raw[2]
		capability.EIAEPS = raw[3]
	}
	return capability, nil
}

// Supports5GEA reports whether the UE advertised the selected 5G ciphering
// algorithm.
func (c UESecurityCapability) Supports5GEA(algorithm byte) bool {
	return supportsBitmapAlgorithm(c.EA5G, algorithm)
}

// Supports5GIA reports whether the UE advertised the selected 5G integrity
// algorithm.
func (c UESecurityCapability) Supports5GIA(algorithm byte) bool {
	return supportsBitmapAlgorithm(c.IA5G, algorithm)
}

func supportsBitmapAlgorithm(bitmap, algorithm byte) bool {
	if algorithm > 7 {
		return false
	}
	return bitmap&(0x80>>algorithm) != 0
}

// RegistrationRequest contains the security-relevant fields of a 5G
// Registration Request.
type RegistrationRequest struct {
	Raw []byte

	RegistrationType byte
	Emergency        bool

	// UESecurityCapabilities is the exact value of the UE security capability
	// IE, copied from the Registration Request bytes.
	UESecurityCapabilities []byte
}

// ParseRegistrationRequest extracts the registration type and the raw UE
// security capability octets from an unprotected 5G Registration Request.
//
// The input must be a complete NAS PDU beginning with the extended protocol
// discriminator. A ciphered Registration Request cannot be inspected without
// first deciphering it; use a decoded RegistrationRequest in that case.
func ParseRegistrationRequest(raw []byte) (*RegistrationRequest, error) {
	if len(raw) < 5 {
		return nil, fmt.Errorf("%w: Registration Request is too short", ErrMalformedMessage)
	}
	if raw[0] != ExtendedProtocolDiscriminator5GMM {
		return nil, fmt.Errorf("%w: unexpected extended protocol discriminator 0x%02x", ErrMalformedMessage, raw[0])
	}
	if raw[1]&SecurityHeaderMask != SecurityHeaderPlain || raw[1]&^SecurityHeaderMask != 0 {
		return nil, fmt.Errorf("%w: Registration Request must be plain NAS", ErrMalformedMessage)
	}
	if raw[2] != MessageTypeRegistrationRequest {
		return nil, fmt.Errorf("%w: unexpected message type 0x%02x", ErrMalformedMessage, raw[2])
	}

	body := raw[3:]
	if len(body) < 4 {
		return nil, fmt.Errorf("%w: Registration Request has no mandatory identity", ErrMalformedMessage)
	}

	registrationType := body[0] & registrationTypeMask
	// body[1] is ngKSI.
	identityLength := int(body[2])
	identityEnd := 3 + identityLength
	if identityEnd > len(body) {
		return nil, fmt.Errorf("%w: truncated 5GS mobile identity", ErrMalformedMessage)
	}

	capabilityIndex, capabilityLength, ok := findCapabilityIE(body, identityEnd)
	if !ok {
		return nil, fmt.Errorf("%w: UE security capability IE is missing", ErrMalformedMessage)
	}

	capability := append([]byte(nil), body[capabilityIndex+2:capabilityIndex+2+capabilityLength]...)
	if _, err := ParseUESecurityCapability(capability); err != nil {
		return nil, err
	}

	return &RegistrationRequest{
		Raw:                    append([]byte(nil), raw...),
		RegistrationType:       registrationType,
		Emergency:              registrationType == RegistrationTypeEmergency,
		UESecurityCapabilities: capability,
	}, nil
}

// NewRegistrationRequest builds the decoded request view used by validation.
// It is useful when the caller already decoded or deciphered the request.
func NewRegistrationRequest(emergency bool, capabilities []byte) (*RegistrationRequest, error) {
	capability, err := ParseUESecurityCapability(capabilities)
	if err != nil {
		return nil, err
	}
	return &RegistrationRequest{
		Emergency:              emergency,
		UESecurityCapabilities: capability.Raw,
	}, nil
}

// findCapabilityIE walks the optional part of a Registration Request looking
// for UE security capability. Registration Requests are attacker-controlled,
// so each candidate is bounds-checked and must have a plausible capability
// length before it is accepted.
func findCapabilityIE(body []byte, start int) (index, length int, ok bool) {
	for index = start; index+3 <= len(body); index++ {
		if body[index] != IEIUESecurityCapability {
			continue
		}
		length = int(body[index+1])
		if length < 2 || length > 8 {
			continue
		}
		if index+2+length > len(body) {
			continue
		}
		return index, length, true
	}
	return 0, 0, false
}

// SecurityModeCommand is the decoded form of a 5GS NAS Security Mode Command.
type SecurityModeCommand struct {
	Raw []byte

	// SecurityHeaderType is the outer security header type. A valid initial
	// Security Mode Command uses SecurityHeaderIntegrityProtectedNewContext.
	SecurityHeaderType byte

	MessageAuthenticationCode []byte
	SequenceNumber            byte

	// MessageType is the inner plain NAS message type.
	MessageType byte
	// PlainNAS is the inner plain NAS message, beginning with its extended
	// protocol discriminator.
	PlainNAS []byte

	SelectedCipheringAlgorithm byte
	SelectedIntegrityAlgorithm byte

	ReplayedUESecurityCapabilities []byte
}

// ParseSecurityModeCommand decodes an integrity-protected 5GS NAS Security
// Mode Command.
//
// The outer header layout is EPD | security header type | MAC (4 octets) |
// sequence number | plain NAS message. The plain NAS message is EPD | plain
// security header | message type | IEs.
func ParseSecurityModeCommand(raw []byte) (*SecurityModeCommand, error) {
	if len(raw) < 10 {
		return nil, fmt.Errorf("%w: Security Mode Command is too short", ErrMalformedMessage)
	}
	if raw[0] != ExtendedProtocolDiscriminator5GMM {
		return nil, fmt.Errorf("%w: unexpected extended protocol discriminator 0x%02x", ErrMalformedMessage, raw[0])
	}
	if raw[1]&^SecurityHeaderMask != 0 {
		return nil, fmt.Errorf("%w: spare security header bits are not zero", ErrMalformedMessage)
	}

	plainNAS := raw[7:]
	if plainNAS[0] != ExtendedProtocolDiscriminator5GMM {
		return nil, fmt.Errorf("%w: inner extended protocol discriminator is 0x%02x", ErrMalformedMessage, plainNAS[0])
	}
	if plainNAS[1]&SecurityHeaderMask != SecurityHeaderPlain || plainNAS[1]&^SecurityHeaderMask != 0 {
		return nil, fmt.Errorf("%w: inner NAS message must be plain", ErrMalformedMessage)
	}
	if plainNAS[2] != MessageTypeSecurityModeCommand {
		return nil, fmt.Errorf("%w: unexpected inner message type 0x%02x", ErrMalformedMessage, plainNAS[2])
	}

	body := plainNAS[3:]
	if len(body) < 4 {
		return nil, fmt.Errorf("%w: Security Mode Command has incomplete mandatory IEs", ErrMalformedMessage)
	}

	selectedAlgorithmOctet := body[0]
	// body[1] is ngKSI. The replayed UE security capability IE is mandatory
	// and follows the two fixed fields.
	if body[2] != IEIUESecurityCapability {
		return nil, fmt.Errorf("%w: replayed UE security capability IE is missing", ErrMalformedMessage)
	}
	capabilityLength := int(body[3])
	if capabilityLength < 2 || capabilityLength > 8 {
		return nil, fmt.Errorf("%w: invalid replayed UE security capability length %d", ErrMalformedMessage, capabilityLength)
	}
	capabilityEnd := 4 + capabilityLength
	if capabilityEnd > len(body) {
		return nil, fmt.Errorf("%w: truncated replayed UE security capability", ErrMalformedMessage)
	}

	command := &SecurityModeCommand{
		Raw:                            append([]byte(nil), raw...),
		SecurityHeaderType:             raw[1] & SecurityHeaderMask,
		MessageAuthenticationCode:      append([]byte(nil), raw[2:6]...),
		SequenceNumber:                 raw[6],
		MessageType:                    MessageTypeSecurityModeCommand,
		PlainNAS:                       append([]byte(nil), plainNAS...),
		SelectedCipheringAlgorithm:     selectedAlgorithmOctet >> 4,
		SelectedIntegrityAlgorithm:     selectedAlgorithmOctet & 0x0f,
		ReplayedUESecurityCapabilities: append([]byte(nil), body[4:capabilityEnd]...),
	}
	if _, err := ParseUESecurityCapability(command.ReplayedUESecurityCapabilities); err != nil {
		return nil, err
	}
	return command, nil
}

func capabilitiesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
