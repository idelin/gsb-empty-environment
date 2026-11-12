package netsec

import "encoding/binary"

// FC-SP AUTH_ELS fixed header and TLV layout. All multi-byte integers are
// big-endian; TLV values are padded to 4-byte word boundaries.
const (
	authELSHeaderLen = 16
	tlvHeaderLen     = 4
)

// AUTH_ELS command codes (FC-LS).
const (
	CmdAuthET   = 0x90 // Authentication Exchange Transaction
	CmdAuthDone = 0x91 // transaction completion (success/failure)
	CmdAuthRJ   = 0x92 // reject
)

// Authentication protocol identifiers.
const (
	ProtoDHCHAP = 0x01
)

// AUTH_ELS status codes carried in the fixed header.
const (
	StatusSuccess          = 0
	StatusNotAuthenticated = 1
	StatusBadCredentials   = 2
	StatusInProgress       = 3
	StatusFailure          = 4
)

// DH-CHAP TLV parameter types (FC-SW-6 tables 76-77).
const (
	ParamAuthProto      uint8 = 1
	ParamHashFunction   uint8 = 2
	ParamDHGroup        uint8 = 3
	ParamSecret         uint8 = 4
	ParamCHAPIdentifier uint8 = 5
	ParamChallenge      uint8 = 6
	ParamDHPublicValue  uint8 = 7
	ParamCHAPResponse   uint8 = 8
	ParamLocalName      uint8 = 9
)

// Param is one DH-CHAP TLV.
type Param struct {
	Type  uint8
	Value []byte
}

// AuthMessage is the parsed form of one AUTH_ELS frame.
type AuthMessage struct {
	Command uint8
	Status  uint8
	// Proto is only meaningful for the first message of an exchange.
	Proto  uint8
	Params []Param
}

func wordPadded(n int) int { return (n + 3) &^ 3 }

// MarshalAuthMessage encodes an AUTH_ELS frame, including word padding on
// every TLV. Padding bytes are zeroed.
func MarshalAuthMessage(m AuthMessage) ([]byte, error) {
	bodyLen := authELSHeaderLen
	for _, p := range m.Params {
		if len(p.Value) > 0xFFFF {
			return nil, ErrInvalidMessage
		}
		bodyLen += tlvHeaderLen + wordPadded(len(p.Value))
	}
	buf := make([]byte, bodyLen)
	buf[0] = m.Command
	buf[1] = m.Status
	binary.BigEndian.PutUint32(buf[4:8], uint32(bodyLen-authELSHeaderLen))
	buf[8] = m.Proto

	off := authELSHeaderLen
	for _, p := range m.Params {
		buf[off] = p.Type
		buf[off+1] = 0 // reserved
		binary.BigEndian.PutUint16(buf[off+2:off+4], uint16(len(p.Value)))
		off += tlvHeaderLen
		off += copy(buf[off:], p.Value)
		off += wordPadded(len(p.Value)) - len(p.Value) // skip padding
	}
	return buf, nil
}

// ParseAuthMessage decodes an AUTH_ELS frame. Non-zero padding and truncated
// TLVs are rejected so a crafted frame cannot smuggle ambiguous data.
func ParseAuthMessage(raw []byte) (AuthMessage, error) {
	if len(raw) < authELSHeaderLen {
		return AuthMessage{}, ErrInvalidMessage
	}
	m := AuthMessage{
		Command: raw[0],
		Status:  raw[1],
		Proto:   raw[8],
	}
	declared := int(binary.BigEndian.Uint32(raw[4:8]))
	if declared != len(raw)-authELSHeaderLen {
		return AuthMessage{}, ErrInvalidMessage
	}

	off := authELSHeaderLen
	for off < len(raw) {
		if len(raw)-off < tlvHeaderLen {
			return AuthMessage{}, ErrInvalidMessage
		}
		ptype := raw[off]
		vlen := int(binary.BigEndian.Uint16(raw[off+2 : off+4]))
		off += tlvHeaderLen
		padded := wordPadded(vlen)
		if vlen == 0 || off+padded > len(raw) {
			return AuthMessage{}, ErrInvalidMessage
		}
		for _, pad := range raw[off+vlen : off+padded] {
			if pad != 0 {
				return AuthMessage{}, ErrInvalidMessage
			}
		}
		value := make([]byte, vlen)
		copy(value, raw[off:off+vlen])
		m.Params = append(m.Params, Param{Type: ptype, Value: value})
		off += padded
	}
	return m, nil
}

// Param returns the first TLV of the given type and whether it was present.
func (m AuthMessage) Param(ptype uint8) (Param, bool) {
	for _, p := range m.Params {
		if p.Type == ptype {
			return p, true
		}
	}
	return Param{}, false
}

// HashOffers extracts the ordered hash identifier list from a TLV.
func (m AuthMessage) HashOffers() ([]HashID, bool) {
	p, ok := m.Param(ParamHashFunction)
	if !ok {
		return nil, false
	}
	out := make([]HashID, len(p.Value))
	for i, b := range p.Value {
		out[i] = HashID(b)
	}
	return out, true
}

// DHGroupOffers extracts the ordered DH group identifier list from a TLV.
func (m AuthMessage) DHGroupOffers() ([]DHGroupID, bool) {
	p, ok := m.Param(ParamDHGroup)
	if !ok {
		return nil, false
	}
	out := make([]DHGroupID, len(p.Value))
	for i, b := range p.Value {
		out[i] = DHGroupID(b)
	}
	return out, true
}

// DHPublicValue extracts the peer public value TLV.
func (m AuthMessage) DHPublicValue() ([]byte, bool) {
	p, ok := m.Param(ParamDHPublicValue)
	return p.Value, ok
}

// GetChallenge extracts the challenge TLV.
func (m AuthMessage) GetChallenge() ([]byte, bool) {
	p, ok := m.Param(ParamChallenge)
	return p.Value, ok
}

// GetCHAPResponse extracts the CHAP response TLV.
func (m AuthMessage) GetCHAPResponse() ([]byte, bool) {
	p, ok := m.Param(ParamCHAPResponse)
	return p.Value, ok
}

func (m AuthMessage) count(ptype uint8) int {
	n := 0
	for _, p := range m.Params {
		if p.Type == ptype {
			n++
		}
	}
	return n
}
