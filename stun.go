package netsec

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"unicode/utf8"
)

// STUN protocol constants from RFC 5389/8489 and TURN (RFC 8656).
const (
	STUNHeaderSize  = 20
	STUNMagicCookie = uint32(0x2112A442)

	STUNClassRequest    = uint8(0)
	STUNClassIndication = uint8(1)
	STUNClassSuccess    = uint8(2)
	STUNClassError      = uint8(3)

	STUNMethodBinding          = uint16(0x001)
	STUNMethodAllocate         = uint16(0x003)
	STUNMethodRefresh          = uint16(0x004)
	STUNMethodSendIndication   = uint16(0x006)
	STUNMethodDataIndication   = uint16(0x007)
	STUNMethodCreatePermission = uint16(0x008)
	STUNMethodChannelBind      = uint16(0x009)

	STUNAttrMappedAddress          = uint16(0x0001)
	STUNAttrUsername               = uint16(0x0006)
	STUNAttrMessageIntegrity       = uint16(0x0008)
	STUNAttrErrorCode              = uint16(0x0009)
	STUNAttrPeerAddress            = uint16(0x0012)
	STUNAttrRealm                  = uint16(0x0014)
	STUNAttrNonce                  = uint16(0x0015)
	STUNAttrXORPeerAddress         = uint16(0x0027)
	STUNAttrXORMappedAddress       = uint16(0x0020)
	STUNAttrSoftware               = uint16(0x8022)
	STUNAttrFingerprint            = uint16(0x8028)
	STUNAttrMessageIntegritySHA256 = uint16(0x001C)

	STUNAFIPv4 = uint8(0x01)
	STUNAFIPv6 = uint8(0x02)
)

// STUNAttribute is one type-length-value attribute.
type STUNAttribute struct {
	Type  uint16
	Value []byte
}

// STUNMessage is a parsed STUN message. Parsed messages retain the original
// bytes so MESSAGE-INTEGRITY can be checked exactly as transmitted.
type STUNMessage struct {
	Type          uint16
	TransactionID [12]byte
	Attributes    []STUNAttribute

	raw     []byte
	offsets []int
}

// STUNAddress is a decoded STUN address attribute.
type STUNAddress struct {
	IP   net.IP
	Port uint16
}

// STUNMessageType combines a method and class into the 16-bit message type.
func STUNMessageType(method uint16, class uint8) uint16 {
	method &= 0x0FFF
	encoded := (method & 0x000F) |
		((method & 0x0070) << 1) |
		((method & 0x0F80) << 2)
	return encoded |
		(uint16(class&0x01) << 4) |
		(uint16(class&0x02) << 7)
}

// STUNMessageMethod extracts the method from a message type.
func STUNMessageMethod(messageType uint16) uint16 {
	return (messageType & 0x000F) |
		((messageType & 0x00E0) >> 1) |
		((messageType & 0x3E00) >> 2)
}

// STUNMessageClass extracts the class from a message type.
func STUNMessageClass(messageType uint16) uint8 {
	return uint8(((messageType >> 4) & 0x01) | ((messageType >> 7) & 0x02))
}

// NewSTUNMessage creates an in-memory message for encoding.
func NewSTUNMessage(messageType uint16, transactionID [12]byte) *STUNMessage {
	return &STUNMessage{Type: messageType, TransactionID: transactionID}
}

// Add appends a copied attribute and returns the message for chaining.
func (m *STUNMessage) Add(attributeType uint16, value []byte) *STUNMessage {
	m.Attributes = append(m.Attributes, STUNAttribute{
		Type:  attributeType,
		Value: append([]byte(nil), value...),
	})
	return m
}

// AddString appends a UTF-8 string attribute.
func (m *STUNMessage) AddString(attributeType uint16, value string) *STUNMessage {
	return m.Add(attributeType, []byte(value))
}

// Encode serializes the message. When integrityKey is non-nil, a
// MESSAGE-INTEGRITY attribute computed with HMAC-SHA1 is appended.
func (m *STUNMessage) Encode(integrityKey []byte) ([]byte, error) {
	return EncodeSTUNMessage(m.Type, m.TransactionID, m.Attributes, integrityKey)
}

// ParseSTUNMessage parses one complete RFC 5389 STUN message. The message
// length must exactly match the header and the magic cookie must be present.
func ParseSTUNMessage(raw []byte) (*STUNMessage, error) {
	if len(raw) < STUNHeaderSize {
		return nil, fmt.Errorf("%w: shorter than the STUN header", ErrSTUNMessage)
	}
	if raw[0]&0xC0 != 0 {
		return nil, fmt.Errorf("%w: reserved message-type bits are set", ErrSTUNMessage)
	}

	messageLength := int(binary.BigEndian.Uint16(raw[2:4]))
	if messageLength%4 != 0 {
		return nil, fmt.Errorf("%w: message length is not a multiple of 4", ErrSTUNMessage)
	}
	messageEnd := STUNHeaderSize + messageLength
	if messageEnd != len(raw) {
		return nil, fmt.Errorf("%w: header length %d does not match %d bytes", ErrSTUNMessage, messageLength, len(raw)-STUNHeaderSize)
	}
	if binary.BigEndian.Uint32(raw[4:8]) != STUNMagicCookie {
		return nil, fmt.Errorf("%w: bad magic cookie", ErrSTUNMessage)
	}

	owned := append([]byte(nil), raw...)
	message := &STUNMessage{
		Type:       binary.BigEndian.Uint16(owned[0:2]),
		raw:        owned,
		offsets:    make([]int, 0, 8),
		Attributes: make([]STUNAttribute, 0, 8),
	}
	copy(message.TransactionID[:], owned[8:20])

	for offset := STUNHeaderSize; offset < messageEnd; {
		if offset+4 > messageEnd {
			return nil, fmt.Errorf("%w: truncated attribute header at offset %d", ErrSTUNMessage, offset)
		}
		attributeType := binary.BigEndian.Uint16(owned[offset : offset+2])
		attributeLength := int(binary.BigEndian.Uint16(owned[offset+2 : offset+4]))
		paddedLength := (attributeLength + 3) &^ 3
		if offset+4+paddedLength > messageEnd {
			return nil, fmt.Errorf("%w: attribute at offset %d overruns the message", ErrSTUNMessage, offset)
		}

		valueStart := offset + 4
		valueEnd := valueStart + attributeLength
		message.Attributes = append(message.Attributes, STUNAttribute{
			Type:  attributeType,
			Value: append([]byte(nil), owned[valueStart:valueEnd]...),
		})
		message.offsets = append(message.offsets, offset)
		offset += 4 + paddedLength
	}

	return message, nil
}

// Values returns copies of every attribute value of the requested type.
func (m *STUNMessage) Values(attributeType uint16) [][]byte {
	var values [][]byte
	for _, attribute := range m.Attributes {
		if attribute.Type == attributeType {
			values = append(values, append([]byte(nil), attribute.Value...))
		}
	}
	return values
}

// Value returns a copy of the first attribute of the requested type.
func (m *STUNMessage) Value(attributeType uint16) ([]byte, bool) {
	for _, attribute := range m.Attributes {
		if attribute.Type == attributeType {
			return append([]byte(nil), attribute.Value...), true
		}
	}
	return nil, false
}

// StringValue returns the first attribute of the requested type as a string.
func (m *STUNMessage) StringValue(attributeType uint16) (string, bool) {
	value, ok := m.Value(attributeType)
	if !ok {
		return "", false
	}
	return string(value), true
}

// Raw returns a copy of the original wire bytes, or nil for a message that
// was constructed in memory rather than parsed.
func (m *STUNMessage) Raw() []byte {
	return append([]byte(nil), m.raw...)
}

// VerifyIntegrity checks the single MESSAGE-INTEGRITY attribute using key.
// Attributes after MESSAGE-INTEGRITY are rejected unless they are FINGERPRINT,
// so callers cannot smuggle an unauthenticated PEER-ADDRESS past verification.
func (m *STUNMessage) VerifyIntegrity(key []byte) error {
	if len(m.raw) < STUNHeaderSize {
		return fmt.Errorf("%w: message was not parsed from wire bytes", ErrSTUNMessage)
	}

	integrityIndex := -1
	for index, attribute := range m.Attributes {
		if attribute.Type != STUNAttrMessageIntegrity {
			continue
		}
		if integrityIndex >= 0 {
			return fmt.Errorf("%w: duplicate MESSAGE-INTEGRITY", ErrSTUNAttribute)
		}
		integrityIndex = index
	}
	if integrityIndex < 0 {
		return ErrMissingIntegrity
	}

	for _, attribute := range m.Attributes[integrityIndex+1:] {
		if attribute.Type != STUNAttrFingerprint {
			return fmt.Errorf("%w: attribute type 0x%04x", ErrIntegrityOrder, attribute.Type)
		}
	}

	integrity := m.Attributes[integrityIndex]
	if len(integrity.Value) != sha1.Size {
		return fmt.Errorf("%w: MESSAGE-INTEGRITY length is %d", ErrSTUNAttribute, len(integrity.Value))
	}

	offset := m.offsets[integrityIndex]
	if offset < STUNHeaderSize || offset+4+sha1.Size > len(m.raw) {
		return fmt.Errorf("%w: MESSAGE-INTEGRITY offset is invalid", ErrSTUNMessage)
	}

	input := make([]byte, offset)
	copy(input, m.raw[:offset])
	// The HMAC input excludes the MESSAGE-INTEGRITY value but the header
	// length is set as if the attribute were present (RFC 5389 section 15.4).
	binary.BigEndian.PutUint16(input[2:4], uint16(offset+4))

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(input)
	if !hmac.Equal(mac.Sum(nil), integrity.Value) {
		return ErrIntegrityMismatch
	}
	return nil
}

// VerifyMessageIntegrity parses raw and checks MESSAGE-INTEGRITY with key.
func VerifyMessageIntegrity(raw []byte, key []byte) error {
	message, err := ParseSTUNMessage(raw)
	if err != nil {
		return err
	}
	return message.VerifyIntegrity(key)
}

// EncodeSTUNMessage serializes a STUN message. When integrityKey is non-nil,
// a MESSAGE-INTEGRITY attribute is appended and covered by the HMAC.
func EncodeSTUNMessage(messageType uint16, transactionID [12]byte, attributes []STUNAttribute, integrityKey []byte) ([]byte, error) {
	if messageType&0xC000 != 0 {
		return nil, fmt.Errorf("%w: reserved message-type bits are set", ErrSTUNMessage)
	}

	attributesLength := 0
	for _, attribute := range attributes {
		if attribute.Type == STUNAttrMessageIntegrity {
			return nil, fmt.Errorf("%w: supply MESSAGE-INTEGRITY through integrityKey", ErrSTUNAttribute)
		}
		if attribute.Type == STUNAttrFingerprint {
			return nil, fmt.Errorf("%w: FINGERPRINT encoding is not supported by this encoder", ErrSTUNAttribute)
		}
		if len(attribute.Value) > 0xFFFF {
			return nil, fmt.Errorf("%w: attribute 0x%04x is too long", ErrSTUNAttribute, attribute.Type)
		}
		attributesLength += 4 + ((len(attribute.Value) + 3) &^ 3)
		if attributesLength > 0xFFFF {
			return nil, fmt.Errorf("%w: attributes exceed STUN's length limit", ErrSTUNMessage)
		}
	}

	integrityLength := 0
	if integrityKey != nil {
		integrityLength = 4 + sha1.Size
	}
	totalLength := attributesLength + integrityLength
	if totalLength > 0xFFFF {
		return nil, fmt.Errorf("%w: message exceeds STUN's length limit", ErrSTUNMessage)
	}

	encoded := make([]byte, STUNHeaderSize+totalLength)
	binary.BigEndian.PutUint16(encoded[0:2], messageType)
	binary.BigEndian.PutUint16(encoded[2:4], uint16(totalLength))
	binary.BigEndian.PutUint32(encoded[4:8], STUNMagicCookie)
	copy(encoded[8:20], transactionID[:])

	offset := STUNHeaderSize
	for _, attribute := range attributes {
		binary.BigEndian.PutUint16(encoded[offset:offset+2], attribute.Type)
		binary.BigEndian.PutUint16(encoded[offset+2:offset+4], uint16(len(attribute.Value)))
		copy(encoded[offset+4:offset+4+len(attribute.Value)], attribute.Value)
		offset += 4 + ((len(attribute.Value) + 3) &^ 3)
	}

	if integrityKey != nil {
		mac := hmac.New(sha1.New, integrityKey)
		_, _ = mac.Write(encoded[:offset])
		sum := mac.Sum(nil)

		binary.BigEndian.PutUint16(encoded[offset:offset+2], STUNAttrMessageIntegrity)
		binary.BigEndian.PutUint16(encoded[offset+2:offset+4], sha1.Size)
		copy(encoded[offset+4:offset+4+sha1.Size], sum)
	}

	return encoded, nil
}

// DecodeAddress decodes a STUN address attribute. Both XOR and non-XOR
// address attributes are supported; transactionID is used for XOR-IPv6.
func DecodeAddress(attributeType uint16, value []byte, transactionID [12]byte) (STUNAddress, error) {
	xor, ok := addressAttributeIsXOR(attributeType)
	if !ok {
		return STUNAddress{}, fmt.Errorf("%w: attribute 0x%04x is not an address", ErrSTUNAttribute, attributeType)
	}
	if len(value) < 4 {
		return STUNAddress{}, fmt.Errorf("%w: address attribute is shorter than 4 bytes", ErrSTUNAttribute)
	}
	if value[1] != 0 {
		return STUNAddress{}, fmt.Errorf("%w: address reserved byte is non-zero", ErrSTUNAttribute)
	}

	port := binary.BigEndian.Uint16(value[2:4])
	if xor {
		port ^= 0x2112
	}

	switch value[0] {
	case STUNAFIPv4:
		if len(value) != 8 {
			return STUNAddress{}, fmt.Errorf("%w: IPv4 address length is %d", ErrSTUNAttribute, len(value))
		}
		ip := net.IP(append([]byte(nil), value[4:8]...))
		if xor {
			xorIPv4(ip)
		}
		return STUNAddress{IP: ip, Port: port}, nil
	case STUNAFIPv6:
		if len(value) != 20 {
			return STUNAddress{}, fmt.Errorf("%w: IPv6 address length is %d", ErrSTUNAttribute, len(value))
		}
		ip := net.IP(append([]byte(nil), value[4:20]...))
		if xor {
			xorIPv6(ip, transactionID)
		}
		return STUNAddress{IP: ip, Port: port}, nil
	default:
		return STUNAddress{}, fmt.Errorf("%w: unknown address family 0x%02x", ErrSTUNAttribute, value[0])
	}
}

// EncodeAddress encodes a MAPPED-ADDRESS, PEER-ADDRESS, XOR-MAPPED-ADDRESS, or
// XOR-PEER-ADDRESS attribute value.
func EncodeAddress(attributeType uint16, address STUNAddress, transactionID [12]byte) ([]byte, error) {
	xor, ok := addressAttributeIsXOR(attributeType)
	if !ok {
		return nil, fmt.Errorf("%w: attribute 0x%04x is not an address", ErrSTUNAttribute, attributeType)
	}

	port := address.Port
	if xor {
		port ^= 0x2112
	}

	var ip net.IP
	var family uint8
	var valueLength int
	if v4 := address.IP.To4(); v4 != nil {
		ip = append([]byte(nil), v4...)
		family = STUNAFIPv4
		valueLength = 8
	} else if v6 := address.IP.To16(); v6 != nil {
		ip = append([]byte(nil), v6...)
		family = STUNAFIPv6
		valueLength = 20
	} else {
		return nil, fmt.Errorf("%w: invalid address IP", ErrSTUNAttribute)
	}

	if xor {
		if family == STUNAFIPv4 {
			xorIPv4(ip)
		} else {
			xorIPv6(ip, transactionID)
		}
	}

	value := make([]byte, valueLength)
	value[0] = family
	binary.BigEndian.PutUint16(value[2:4], port)
	copy(value[4:], ip)
	return value, nil
}

// PeerAddresses returns every PEER-ADDRESS and XOR-PEER-ADDRESS in the
// message. Malformed peer attributes make the whole result invalid.
func (m *STUNMessage) PeerAddresses() ([]STUNAddress, error) {
	var addresses []STUNAddress
	for _, attribute := range m.Attributes {
		if attribute.Type != STUNAttrPeerAddress && attribute.Type != STUNAttrXORPeerAddress {
			continue
		}
		address, err := DecodeAddress(attribute.Type, attribute.Value, m.TransactionID)
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

// CheckPeers applies policy to every peer address in the message.
func (m *STUNMessage) CheckPeers(policy PeerPolicy) error {
	addresses, err := m.PeerAddresses()
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if err := policy.Check(address.IP); err != nil {
			return err
		}
	}
	return nil
}

// AuthenticateSTUN parses and authenticates a wire-format STUN request. The
// username is authenticated before its expiry, tenant, user, or peer claims
// are trusted.
func (v *Validator) AuthenticateSTUN(raw []byte) (*Credentials, error) {
	message, err := ParseSTUNMessage(raw)
	if err != nil {
		return nil, err
	}
	return v.AuthenticateMessage(message)
}

// AuthenticateMessage authenticates an already parsed STUN message. It
// requires exactly one USERNAME, verifies MESSAGE-INTEGRITY with the key
// derived from that username, then checks expiry, tenant policy, and peer
// addresses.
func (v *Validator) AuthenticateMessage(message *STUNMessage) (*Credentials, error) {
	if message == nil {
		return nil, fmt.Errorf("%w: nil message", ErrSTUNMessage)
	}

	usernames := message.Values(STUNAttrUsername)
	switch len(usernames) {
	case 0:
		return nil, ErrMissingUsername
	case 1:
	default:
		return nil, fmt.Errorf("%w: duplicate USERNAME", ErrSTUNAttribute)
	}
	if !utf8.Valid(usernames[0]) {
		return nil, fmt.Errorf("%w: USERNAME is not valid UTF-8", ErrUsernameFormat)
	}

	credentials, err := v.parse(string(usernames[0]))
	if err != nil {
		return nil, err
	}

	realms := message.Values(STUNAttrRealm)
	switch len(realms) {
	case 0:
		// Long-term credentials are challenged with REALM, but a message may
		// legitimately omit it after the server has fixed the realm. The
		// configured realm still participates in the key.
	case 1:
		if !hmac.Equal(realms[0], []byte(v.realm)) {
			return nil, fmt.Errorf("%w: message realm and validator realm differ", ErrRealmMismatch)
		}
	default:
		return nil, fmt.Errorf("%w: duplicate REALM", ErrSTUNAttribute)
	}

	if err := message.VerifyIntegrity(credentials.IntegrityKey); err != nil {
		return nil, err
	}
	if err := v.authorize(credentials); err != nil {
		return nil, err
	}
	if err := message.CheckPeers(v.peers); err != nil {
		return nil, err
	}

	return credentials, nil
}

func addressAttributeIsXOR(attributeType uint16) (xor bool, ok bool) {
	switch attributeType {
	case STUNAttrMappedAddress, STUNAttrPeerAddress:
		return false, true
	case STUNAttrXORMappedAddress, STUNAttrXORPeerAddress:
		return true, true
	default:
		return false, false
	}
}

func xorIPv4(ip net.IP) {
	ip[0] ^= 0x21
	ip[1] ^= 0x12
	ip[2] ^= 0xA4
	ip[3] ^= 0x42
}

func xorIPv6(ip net.IP, transactionID [12]byte) {
	ip[0] ^= 0x21
	ip[1] ^= 0x12
	ip[2] ^= 0xA4
	ip[3] ^= 0x42
	for index := range transactionID {
		ip[4+index] ^= transactionID[index]
	}
}
