package netsec

import (
	"errors"
	"fmt"
)

// 5GS NAS protocol discriminators (TS 24.501 section 9.2).
const (
	PD5GMM byte = 0x7E // 5GS mobility management
)

// Security header types for 5GMM messages (TS 24.501 section 9.3.1).
const (
	SHTPlainNAS              byte = 0x0 // plain 5GS NAS message, not security protected
	SHTIntegrityProtected    byte = 0x2 // integrity protected
	SHTIntegrityAndCiphered  byte = 0x3 // integrity protected and ciphered
	SHTIntegrityProtectedNew byte = 0x4 // integrity protected with new security context
)

// 5GMM message types (TS 24.501 section 9.7).
const (
	MsgRegistrationRequest  byte = 0x41
	MsgSecurityModeCommand  byte = 0x5D
	MsgSecurityModeReject   byte = 0x5E
	MsgSecurityModeComplete byte = 0x5F
)

// 5GMM information element identifiers (TS 24.501 section 9.11.2).
const (
	ieSelectedNASAlgs byte = 0x55 // Selected NAS security algorithms, TV (1 octet V)
	ieReplayedUECaps  byte = 0x2E // Replayed UE security capabilities, TLV
	ieEPSNASCaps      byte = 0x19 // EPS NAS security algorithms (nested/standalone), TLV
)

// Length (in octets) of the fixed prefix of an integrity protected, but not
// ciphered, 5GS NAS message (TS 24.501 section 9.2).
const protectedMessageHeaderLen = 7

// ErrTruncated is returned when a NAS message is shorter than required.
var ErrTruncated = errors.New("netsec: truncated NAS message")

// nasIE is one decoded information element: for TLV/TV IEs value holds the
// value part only (no IEI/length octets).
type nasIE struct {
	iei   byte
	value []byte
}

// ProtectedMessage is the decoded outer layer of a security protected 5GS NAS
// message (TS 24.501 section 9.2).
type ProtectedMessage struct {
	SecurityHeaderType byte
	SequenceNumber     byte
	MAC                [4]byte
	// Plaintext is the full plain 5GS NAS message whose integrity is covered by
	// the NAS-MAC, including its own extended protocol discriminator/PDU header.
	Plaintext []byte
}

// ParseProtectedMessage decodes an integrity protected, non-ciphered 5GS NAS
// message. Ciphered messages (security header type 3) are rejected because the
// receiver cannot validate them before a security context exists.
func ParseProtectedMessage(msg []byte) (*ProtectedMessage, error) {
	if len(msg) < protectedMessageHeaderLen {
		return nil, fmt.Errorf("%w: protected message needs at least %d octets, got %d",
			ErrTruncated, protectedMessageHeaderLen, len(msg))
	}
	if msg[0] != PD5GMM {
		return nil, fmt.Errorf("netsec: not a 5GMM message, protocol discriminator 0x%02X", msg[0])
	}
	sht := msg[1] & 0x0F
	if sht != SHTIntegrityProtected && sht != SHTIntegrityProtectedNew {
		return nil, fmt.Errorf("netsec: unsupported security header type 0x%X", sht)
	}
	pm := &ProtectedMessage{SecurityHeaderType: sht}
	copy(pm.MAC[:], msg[2:6])
	pm.SequenceNumber = msg[6]
	pm.Plaintext = append([]byte(nil), msg[protectedMessageHeaderLen:]...)
	if len(pm.Plaintext) == 0 {
		return nil, fmt.Errorf("%w: protected message carries no plaintext", ErrTruncated)
	}
	return pm, nil
}

// SecurityModeCommand holds the IEs of a plain NAS Security Mode Command.
type SecurityModeCommand struct {
	SelectedNEA byte // selected NAS ciphering algorithm (NEA0..NEA3)
	SelectedNIA byte // selected NAS integrity algorithm (NIA0..NIA3)
	// ReplayedUECapabilities is the raw value octets of the replayed UE security
	// capabilities IE (IEI 0x2E), including any nested EPS NAS security
	// algorithms. Bytes are retained verbatim for exact echo comparison.
	ReplayedUECapabilities []byte
}

// ParseSecurityModeCommand parses the plaintext body of a NAS Security Mode
// Command (TS 24.501 section 8.2.25).
func ParseSecurityModeCommand(plain []byte) (*SecurityModeCommand, error) {
	if len(plain) < 3 {
		return nil, fmt.Errorf("%w: SMC needs at least 3 octets, got %d", ErrTruncated, len(plain))
	}
	if plain[0] != PD5GMM {
		return nil, fmt.Errorf("netsec: SMC is not a 5GMM message, protocol discriminator 0x%02X", plain[0])
	}
	if plain[2] != MsgSecurityModeCommand {
		return nil, fmt.Errorf("netsec: not a Security Mode Command, message type 0x%02X", plain[2])
	}

	ies, err := walk5GMMIEs(plain[3:])
	if err != nil {
		return nil, err
	}
	smc := &SecurityModeCommand{}
	for _, ie := range ies {
		switch ie.iei {
		case ieSelectedNASAlgs:
			if len(ie.value) < 1 {
				return nil, errors.New("netsec: malformed selected NAS security algorithms IE")
			}
			smc.SelectedNEA = ie.value[0] & 0x07
			smc.SelectedNIA = (ie.value[0] >> 3) & 0x07
		case ieReplayedUECaps:
			smc.ReplayedUECapabilities = append([]byte(nil), ie.value...)
		}
	}
	return smc, nil
}

// ExtractUESecurityCapabilities locates the UE security capabilities IE
// (IEI 0x2E) inside a plain (unprotected) Registration Request and returns
// its value octets verbatim, including nested EPS NAS security algorithms.
// The returned slice is the exact byte sequence the UE sent and is the
// reference that a replayed UE security capabilities IE must match.
func ExtractUESecurityCapabilities(registrationRequest []byte) ([]byte, error) {
	if len(registrationRequest) < 4 {
		return nil, fmt.Errorf("%w: registration request needs at least 4 octets, got %d",
			ErrTruncated, len(registrationRequest))
	}
	if registrationRequest[0] != PD5GMM {
		return nil, fmt.Errorf("netsec: registration request is not a 5GMM message, protocol discriminator 0x%02X",
			registrationRequest[0])
	}
	if registrationRequest[2] != MsgRegistrationRequest {
		return nil, fmt.Errorf("netsec: not a Registration Request, message type 0x%02X",
			registrationRequest[2])
	}
	// The fixed 5GMM header ends with the half-octet ngKSI IE (octet 3); all
	// remaining IEs are TV/TLV/TLV-E and can be walked generically.
	ies, err := walk5GMMIEs(registrationRequest[4:])
	if err != nil {
		return nil, err
	}
	for _, ie := range ies {
		if ie.iei == ieReplayedUECaps {
			return append([]byte(nil), ie.value...), nil
		}
	}
	return nil, errors.New("netsec: registration request contains no UE security capabilities IE")
}

// walk5GMMIEs decodes a stream of 5GMM non-header IEs (TS 24.501 section 9.x),
// tolerating unknown IEs by following their length fields.
func walk5GMMIEs(buf []byte) ([]nasIE, error) {
	var ies []nasIE
	for pos := 0; pos < len(buf); {
		iei := buf[pos]
		// TLV-E: IEI with low nibble 0xF, two-octet length.
		if iei&0x0F == 0x0F {
			if pos+3 > len(buf) {
				return nil, fmt.Errorf("%w: truncated TLV-E IE 0x%02X", ErrTruncated, iei)
			}
			n := int(buf[pos+1])<<8 | int(buf[pos+2])
			start := pos + 3
			if start+n > len(buf) {
				return nil, fmt.Errorf("%w: truncated TLV-E IE 0x%02X value", ErrTruncated, iei)
			}
			ies = append(ies, nasIE{iei: iei, value: append([]byte(nil), buf[start:start+n]...)})
			pos = start + n
			continue
		}
		// Half-octet TV IEs (e.g. ngKSI 0xC0..0xC7) pack IEI+value into one
		// octet; they are skipped here.
		if iei&0xF0 == 0xC0 {
			pos++
			continue
		}
		// Fixed-length TV IEs are listed in tvLengths; everything else is a
		// one-octet-length TLV IE.
		if n, ok := tvLengths[iei]; ok {
			start := pos + 1
			if start+n > len(buf) {
				return nil, fmt.Errorf("%w: truncated TV IE 0x%02X", ErrTruncated, iei)
			}
			ies = append(ies, nasIE{iei: iei, value: append([]byte(nil), buf[start:start+n]...)})
			pos = start + n
			continue
		}
		if pos+2 > len(buf) {
			return nil, fmt.Errorf("%w: truncated TLV IE 0x%02X", ErrTruncated, iei)
		}
		n := int(buf[pos+1])
		start := pos + 2
		if start+n > len(buf) {
			return nil, fmt.Errorf("%w: truncated TLV IE 0x%02X value", ErrTruncated, iei)
		}
		ies = append(ies, nasIE{iei: iei, value: append([]byte(nil), buf[start:start+n]...)})
		pos = start + n
	}
	return ies, nil
}

// tvLengths maps TV IE identifiers to their fixed value length in octets
// (TS 24.501 tables 9.11.2.x). Only IEs relevant to SMC/registration
// validation are listed; unknown IEs fall back to TLV decoding.
var tvLengths = map[byte]int{
	ieSelectedNASAlgs: 1, // 0x55: type of ciphering + integrity algorithms
	0x10:              1, // 5GMM capability
	0x2F:              1, // 5GS update type
	0x14:              1, // 5GS registration type
	0x25:              1, // Additional 5GMM information
}
