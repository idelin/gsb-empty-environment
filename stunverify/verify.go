// Package stunverify validates STUN messages for ICE and TURN services.
//
// The verifier performs the structural checks from RFC 5389/RFC 8489 and
// verifies the long-term-credential MESSAGE-INTEGRITY (and optional
// MESSAGE-INTEGRITY-SHA256 / FINGERPRINT) attributes.
//
// Security model: attributes located after MESSAGE-INTEGRITY are not covered
// by the integrity check. Only MESSAGE-INTEGRITY-SHA256 (0x001C) and
// FINGERPRINT (0x8028) are permitted there. Any other attribute found after
// MESSAGE-INTEGRITY (for example an XOR-PEER-ADDRESS appended by an attacker
// to redirect relayed traffic) is stripped from the trusted attribute set so
// it can never enter business parsing or override an earlier same-named
// attribute. It is reported in Message.Ignored; Verify rejects it outright.
package stunverify

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Well-known STUN attribute types handled by the verifier.
const (
	AttrMessageIntegrity       uint16 = 0x0008
	AttrMessageIntegritySHA256 uint16 = 0x001C
	AttrFingerprint            uint16 = 0x8028

	// MagicCookie is the fixed STUN magic cookie (RFC 5389).
	MagicCookie uint32 = 0x2112A442

	fingerprintXOR uint32 = 0x5354554E
	headerLen             = 20
)

// Structural and cryptographic validation errors.
var (
	ErrShortMessage         = errors.New("stunverify: message shorter than 20-byte header")
	ErrBadMagicCookie       = errors.New("stunverify: magic cookie mismatch")
	ErrBadMessageLength     = errors.New("stunverify: header length is not a multiple of 4")
	ErrLengthMismatch       = errors.New("stunverify: header length does not equal attribute payload length")
	ErrAttributeOverflow    = errors.New("stunverify: attribute header or value runs past message end")
	ErrBadPadding           = errors.New("stunverify: attribute padding bytes must be zero")
	ErrDuplicateIntegrity   = errors.New("stunverify: MESSAGE-INTEGRITY appears more than once")
	ErrDuplicateSHA256      = errors.New("stunverify: MESSAGE-INTEGRITY-SHA256 appears more than once")
	ErrDuplicateFingerprint = errors.New("stunverify: FINGERPRINT appears more than once")
	ErrBadIntegrityLength   = errors.New("stunverify: MESSAGE-INTEGRITY must be 20 bytes")
	ErrBadSHA256Length      = errors.New("stunverify: MESSAGE-INTEGRITY-SHA256 must be 32 bytes")
	ErrBadFingerprintLength = errors.New("stunverify: FINGERPRINT must be 4 bytes")
	ErrFingerprintNotLast   = errors.New("stunverify: FINGERPRINT must be the last attribute")
	ErrMissingIntegrity     = errors.New("stunverify: message has no MESSAGE-INTEGRITY attribute")
	ErrIntegrityMismatch    = errors.New("stunverify: MESSAGE-INTEGRITY HMAC-SHA1 mismatch")
	ErrSHA256Mismatch       = errors.New("stunverify: MESSAGE-INTEGRITY-SHA256 HMAC-SHA256 mismatch")
	ErrFingerprintMismatch  = errors.New("stunverify: FINGERPRINT CRC-32 mismatch")
	ErrAttributeAfterMI     = errors.New("stunverify: attribute after MESSAGE-INTEGRITY is not MESSAGE-INTEGRITY-SHA256 or FINGERPRINT")
)

// Attribute is a single validated STUN type-length-value attribute.
//
// Value contains only the declared attribute value; STUN padding bytes are
// never included. The slice is a copy owned by the caller and may be retained.
type Attribute struct {
	Type  uint16
	Value []byte
}

// Message is the result of validating a STUN message.
type Message struct {
	Type          uint16
	TransactionID []byte // always 12 bytes

	// Attributes contains every trusted attribute. Attributes placed after
	// MESSAGE-INTEGRITY that are not MESSAGE-INTEGRITY-SHA256 or FINGERPRINT
	// are excluded here and reported in Ignored instead.
	Attributes []Attribute

	// Ignored lists attributes stripped because they appeared after
	// MESSAGE-INTEGRITY without being integrity-protected. Verify treats their
	// presence as an error; VerifyAndParse only exposes them for auditing.
	Ignored []Attribute

	HasIntegrity       bool
	HasIntegritySHA256 bool
	HasFingerprint     bool
}

// Method extracts the STUN method from the message type (RFC 5389 section 6).
func (m *Message) Method() uint16 {
	return m.Type&0x000F | m.Type&0x00E0 | m.Type&0x0F00
}

// Class extracts the STUN class (request/response/indication/error) from the
// message type (RFC 5389 section 6).
func (m *Message) Class() uint16 {
	return m.Type&0x0010 | m.Type&0x0100
}

// Attribute returns the value of the first trusted attribute matching typ and
// whether it was present. Attributes stripped from the unprotected tail are
// never returned here.
func (m *Message) Attribute(typ uint16) ([]byte, bool) {
	for i := range m.Attributes {
		if m.Attributes[i].Type == typ {
			return m.Attributes[i].Value, true
		}
	}
	return nil, false
}

// Verify fully validates raw using the supplied long-term credential key.
//
// On success the returned Message carries only trusted, integrity-protected
// attributes (plus the optional integrity/fingerprint attributes). Any
// structural violation, duplicate MESSAGE-INTEGRITY/FINGERPRINT, bad padding,
// length overflow, failed HMAC/CRC check or unprotected trailing attribute is
// reported as an error.
func Verify(raw, key []byte) (*Message, error) {
	return verify(raw, key, true)
}

// VerifyAndParse behaves like Verify but, instead of rejecting a message that
// carries extra attributes after MESSAGE-INTEGRITY, strips those attributes
// from the trusted set and reports them in Message.Ignored. All other checks,
// including the HMAC and FINGERPRINT checks, are still enforced.
func VerifyAndParse(raw, key []byte) (*Message, error) {
	return verify(raw, key, false)
}

func verify(raw, key []byte, strict bool) (*Message, error) {
	if len(raw) < headerLen {
		return nil, ErrShortMessage
	}

	msgType := binary.BigEndian.Uint16(raw[0:2])
	declaredLen := int(binary.BigEndian.Uint16(raw[2:4]))

	if binary.BigEndian.Uint32(raw[4:8]) != MagicCookie {
		return nil, ErrBadMagicCookie
	}
	if declaredLen%4 != 0 {
		return nil, ErrBadMessageLength
	}
	if len(raw) != headerLen+declaredLen {
		return nil, ErrLengthMismatch
	}

	msg := &Message{
		Type:          msgType,
		TransactionID: append([]byte(nil), raw[8:20]...),
		Attributes:    make([]Attribute, 0, 8),
	}

	body := raw[headerLen:]
	pos := 0
	afterIntegrity := false

	for pos < len(body) {
		// TLV header: 2-byte type + 2-byte length.
		if pos+4 > len(body) {
			return nil, ErrAttributeOverflow
		}
		attrType := binary.BigEndian.Uint16(body[pos : pos+2])
		attrLen := int(binary.BigEndian.Uint16(body[pos+2 : pos+4]))

		valueStart := pos + 4
		valueEnd := valueStart + attrLen
		if valueEnd > len(body) {
			return nil, ErrAttributeOverflow
		}

		// Values are padded to a 4-byte boundary with zero bytes.
		pad := (4 - attrLen%4) % 4
		padEnd := valueEnd + pad
		if padEnd > len(body) {
			return nil, ErrAttributeOverflow
		}
		for _, b := range body[valueEnd:padEnd] {
			if b != 0 {
				return nil, ErrBadPadding
			}
		}

		value := append([]byte(nil), body[valueStart:valueEnd]...)
		attrOffset := pos // offset of the TLV header relative to the body

		switch attrType {
		case AttrMessageIntegrity:
			if afterIntegrity {
				return nil, ErrDuplicateIntegrity
			}
			if attrLen != sha1.Size {
				return nil, ErrBadIntegrityLength
			}
			input := integrityInput(raw, attrOffset, attrLen)
			mac := hmac.New(sha1.New, key)
			mac.Write(input)
			if !hmac.Equal(value, mac.Sum(nil)) {
				return nil, ErrIntegrityMismatch
			}
			afterIntegrity = true
			msg.HasIntegrity = true

		case AttrMessageIntegritySHA256:
			if !afterIntegrity {
				// Before MESSAGE-INTEGRITY it is an ordinary attribute and is
				// covered by the SHA-1 integrity check.
				msg.Attributes = append(msg.Attributes, Attribute{Type: attrType, Value: value})
				break
			}
			if msg.HasIntegritySHA256 {
				return nil, ErrDuplicateSHA256
			}
			if attrLen != sha256.Size {
				return nil, ErrBadSHA256Length
			}
			input := integrityInput(raw, attrOffset, attrLen)
			mac := hmac.New(sha256.New, key)
			mac.Write(input)
			if !hmac.Equal(value, mac.Sum(nil)) {
				return nil, ErrSHA256Mismatch
			}
			msg.HasIntegritySHA256 = true

		case AttrFingerprint:
			if msg.HasFingerprint {
				return nil, ErrDuplicateFingerprint
			}
			if attrLen != 4 {
				return nil, ErrBadFingerprintLength
			}
			// FINGERPRINT must be the final attribute of the message.
			if padEnd != len(body) {
				return nil, ErrFingerprintNotLast
			}
			input := integrityInput(raw, attrOffset, attrLen)
			got := binary.BigEndian.Uint32(value)
			want := crc32.ChecksumIEEE(input) ^ fingerprintXOR
			if got != want {
				return nil, ErrFingerprintMismatch
			}
			msg.HasFingerprint = true

		default:
			if afterIntegrity {
				// Unprotected trailing attribute: never let it enter business
				// parsing or override an earlier same-named attribute.
				msg.Ignored = append(msg.Ignored, Attribute{Type: attrType, Value: value})
				if strict {
					return nil, ErrAttributeAfterMI
				}
			} else {
				msg.Attributes = append(msg.Attributes, Attribute{Type: attrType, Value: value})
			}
		}

		pos = padEnd
	}

	if !msg.HasIntegrity {
		return nil, ErrMissingIntegrity
	}

	return msg, nil
}

// integrityInput rebuilds the bytes that feed an HMAC or CRC check for the
// attribute whose TLV header starts at bodyOffset inside the attribute body.
//
// The check input runs from the message start through the attribute's own
// type and length fields (i.e. up to, but not including, its value), with the
// 16-bit message-length field recomputed so that it counts every byte up to
// the end of the attribute being checked.
func integrityInput(raw []byte, bodyOffset, attrLen int) []byte {
	input := make([]byte, headerLen+bodyOffset+4)
	copy(input, raw[:headerLen+bodyOffset+4])

	newLen := uint16(bodyOffset + 4 + attrLen)
	binary.BigEndian.PutUint16(input[2:4], newLen)
	return input
}

// PaddingLen reports how many zero padding bytes follow an attribute value of
// the given length so that each TLV record stays 4-byte aligned.
func PaddingLen(valueLen int) int {
	return (4 - valueLen%4) % 4
}
