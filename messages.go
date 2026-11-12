package netsec

import (
	"encoding/binary"
	"fmt"
)

// NegotiateMessage is the parsed Type 1 NTLM message.
type NegotiateMessage struct {
	Raw            []byte
	Flags          uint32
	VersionPresent bool
	Domain         string
	Workstation    string
}

// ChallengeMessage is the parsed Type 2 NTLM message.
type ChallengeMessage struct {
	Raw             []byte
	Flags           uint32
	ServerChallenge [8]byte
	TargetName      string
	TargetInfo      AVList
	TargetInfoRaw   []byte
	VersionPresent  bool
}

// AuthenticateMessage is the parsed Type 3 NTLM message.
type AuthenticateMessage struct {
	Raw                 []byte
	Flags               uint32
	LMResponse          []byte
	NTResponse          []byte
	DomainName          []byte
	UserName            []byte
	Workstation         []byte
	EncryptedSessionKey []byte
	VersionPresent      bool
	MIC                 []byte
	MICOffset           int
	MICPresent          bool
	NTLMv2TargetInfo    AVList
	micOffsets          []int
}

// ParseNegotiateMessage parses an NTLM NEGOTIATE_MESSAGE.
func ParseNegotiateMessage(raw []byte) (*NegotiateMessage, error) {
	if err := checkMessage(raw, MessageTypeNegotiate); err != nil {
		return nil, err
	}
	if len(raw) < 32 {
		return nil, validationError(ErrMalformedMessage, "NEGOTIATE message is only %d bytes, want at least 32", len(raw))
	}
	flags := binary.LittleEndian.Uint32(raw[12:16])
	// The Version field is only informational here. Accept peers that
	// advertise VERSION but omit the eight bytes, since the MIC is
	// calculated over the received bytes regardless of this field.
	version := flags&NegotiateFlagVersion != 0 && len(raw) >= 40

	domainBuf, err := readSecurityBuffer(raw, 16)
	if err != nil {
		return nil, err
	}
	domain, err := domainBuf.bytes(raw)
	if err != nil {
		return nil, err
	}
	workBuf, err := readSecurityBuffer(raw, 24)
	if err != nil {
		return nil, err
	}
	workstation, err := workBuf.bytes(raw)
	if err != nil {
		return nil, err
	}
	domainName, _ := decodeAVString(domain)
	workstationName, _ := decodeAVString(workstation)

	return &NegotiateMessage{
		Raw:            raw,
		Flags:          flags,
		VersionPresent: version,
		Domain:         domainName,
		Workstation:    workstationName,
	}, nil
}

// ParseChallengeMessage parses an NTLM CHALLENGE_MESSAGE.
func ParseChallengeMessage(raw []byte) (*ChallengeMessage, error) {
	if err := checkMessage(raw, MessageTypeChallenge); err != nil {
		return nil, err
	}
	if len(raw) < 48 {
		return nil, validationError(ErrMalformedMessage, "CHALLENGE message is only %d bytes, want at least 48", len(raw))
	}

	flags := binary.LittleEndian.Uint32(raw[20:24])
	version := flags&NegotiateFlagVersion != 0
	if version && len(raw) < 56 {
		return nil, validationError(ErrMalformedMessage, "CHALLENGE version field is truncated")
	}
	headerEnd := 48
	if version {
		headerEnd = 56
	}

	challenge := &ChallengeMessage{
		Raw:            raw,
		Flags:          flags,
		VersionPresent: version,
	}
	copy(challenge.ServerChallenge[:], raw[24:32])

	targetBuf, err := readSecurityBuffer(raw, 12)
	if err != nil {
		return nil, err
	}
	if targetBuf.Length > 0 && int(targetBuf.Offset) < headerEnd {
		return nil, validationError(ErrMalformedMessage, "CHALLENGE target name overlaps the message header")
	}
	target, err := targetBuf.bytes(raw)
	if err != nil {
		return nil, err
	}
	if len(target) > 0 {
		name, err := decodeAVString(target)
		if err != nil {
			return nil, validationError(ErrMalformedMessage, "CHALLENGE target name: %v", err)
		}
		challenge.TargetName = name
	}

	infoBuf, err := readSecurityBuffer(raw, 40)
	if err != nil {
		return nil, err
	}
	if infoBuf.Length > 0 && int(infoBuf.Offset) < headerEnd {
		return nil, validationError(ErrMalformedMessage, "CHALLENGE target info overlaps the message header")
	}
	info, err := infoBuf.bytes(raw)
	if err != nil {
		return nil, err
	}
	challenge.TargetInfoRaw = info
	if len(info) > 0 {
		challenge.TargetInfo, err = ParseAVPairs(info)
		if err != nil {
			return nil, err
		}
	}
	return challenge, nil
}

// ParseAuthenticateMessage parses an NTLM AUTHENTICATE_MESSAGE.
func ParseAuthenticateMessage(raw []byte) (*AuthenticateMessage, error) {
	if err := checkMessage(raw, MessageTypeAuthenticate); err != nil {
		return nil, err
	}
	if len(raw) < 64 {
		return nil, validationError(ErrMalformedMessage, "AUTHENTICATE message is only %d bytes, want at least 64", len(raw))
	}

	message := &AuthenticateMessage{
		Raw:   raw,
		Flags: binary.LittleEndian.Uint32(raw[60:64]),
	}
	message.VersionPresent = message.Flags&NegotiateFlagVersion != 0
	headerEnd := 64
	if message.VersionPresent {
		headerEnd = 72
		if len(raw) < headerEnd {
			return nil, validationError(ErrMalformedMessage, "AUTHENTICATE version field is truncated")
		}
	}

	var err error
	if message.LMResponse, err = parseAuthBuffer(raw, 12, headerEnd); err != nil {
		return nil, err
	}
	if message.NTResponse, err = parseAuthBuffer(raw, 20, headerEnd); err != nil {
		return nil, err
	}
	if message.DomainName, err = parseAuthBuffer(raw, 28, headerEnd); err != nil {
		return nil, err
	}
	if message.UserName, err = parseAuthBuffer(raw, 36, headerEnd); err != nil {
		return nil, err
	}
	if message.Workstation, err = parseAuthBuffer(raw, 44, headerEnd); err != nil {
		return nil, err
	}
	if message.EncryptedSessionKey, err = parseAuthBuffer(raw, 52, headerEnd); err != nil {
		return nil, err
	}

	// The MIC immediately follows the optional Version field. A
	// payload that starts inside that 16-byte slot means there is no
	// MIC and the message was built with a compact 64/72-byte header.
	message.micOffsets = plausibleMICOffsets(raw, headerEnd)
	if len(message.micOffsets) > 0 {
		message.MICPresent = true
		message.MICOffset = message.micOffsets[0]
		message.MIC = raw[message.MICOffset : message.MICOffset+16]
	}

	if len(message.NTResponse) >= 16 {
		blob := message.NTResponse[16:]
		if len(blob) >= 28 && blob[0] == 1 && blob[1] == 1 {
			message.NTLMv2TargetInfo, err = ParseAVPairs(blob[28:])
			if err != nil {
				return nil, err
			}
		}
	}

	return message, nil
}

func plausibleMICOffsets(raw []byte, preferred int) []int {
	payloadStart := firstPayloadOffset(raw, 64)
	candidates := []int{preferred, 64, 72}
	seen := make(map[int]bool, len(candidates))
	var offsets []int
	for _, offset := range candidates {
		if seen[offset] {
			continue
		}
		seen[offset] = true
		if offset+16 > len(raw) {
			continue
		}
		if payloadStart > 0 && payloadStart < offset+16 {
			continue
		}
		offsets = append(offsets, offset)
	}
	return offsets
}

func parseAuthBuffer(raw []byte, fieldOffset, headerEnd int) ([]byte, error) {
	buf, err := readSecurityBuffer(raw, fieldOffset)
	if err != nil {
		return nil, err
	}
	if buf.Length > 0 && int(buf.Offset) < headerEnd {
		return nil, validationError(ErrMalformedMessage, "AUTHENTICATE payload overlaps the message header")
	}
	return buf.bytes(raw)
}

func firstPayloadOffset(raw []byte, headerEnd int) int {
	offsets := []int{12, 20, 28, 36, 44, 52}
	first := 0
	for _, offset := range offsets {
		buf, err := readSecurityBuffer(raw, offset)
		if err != nil || buf.Length == 0 {
			continue
		}
		start := int(buf.Offset)
		if start < headerEnd {
			continue
		}
		if first == 0 || start < first {
			first = start
		}
	}
	return first
}

// User returns the decoded AUTHENTICATE user name. It is intentionally
// not used to reserialize the message.
func (m *AuthenticateMessage) User() string {
	name, err := decodeAVString(m.UserName)
	if err != nil {
		return string(m.UserName)
	}
	return name
}

// Domain returns the AUTHENTICATE domain name as a string.
func (m *AuthenticateMessage) Domain() string {
	name, err := decodeAVString(m.DomainName)
	if err != nil {
		return string(m.DomainName)
	}
	return name
}

// WorkstationName returns the AUTHENTICATE workstation name.
func (m *AuthenticateMessage) WorkstationName() string {
	name, err := decodeAVString(m.Workstation)
	if err != nil {
		return string(m.Workstation)
	}
	return name
}

// String is useful in diagnostics without exposing key material.
func (m *ChallengeMessage) String() string {
	return fmt.Sprintf("challenge{type:2 flags:0x%08x spn:%q avpairs:%d}", m.Flags, m.TargetName, len(m.TargetInfo))
}
