package netsec

import (
	"crypto/hmac"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	CodeAccessRequest   = 1
	CodeAccessAccept    = 2
	CodeAccessReject    = 3
	CodeAccessChallenge = 11

	AttrMessageAuthenticator = 80

	headerSize                    = 20
	authenticatorSize             = 16
	messageAuthenticatorValueSize = 16
	messageAuthenticatorAttrSize  = 2 + messageAuthenticatorValueSize
	maxPacketSize                 = 4096
)

var (
	ErrMalformedPacket              = errors.New("radius: malformed packet")
	ErrUnexpectedCode               = errors.New("radius: unexpected packet code")
	ErrMessageAuthenticatorMissing  = errors.New("radius: Message-Authenticator missing")
	ErrMessageAuthenticatorNotFirst = errors.New(
		"radius: Message-Authenticator is not the first Access-Request attribute",
	)
	ErrMessageAuthenticatorDuplicate = errors.New("radius: duplicate Message-Authenticator")
	ErrMessageAuthenticatorLength    = errors.New(
		"radius: Message-Authenticator value is not 16 bytes",
	)
	ErrMessageAuthenticatorMismatch = errors.New("radius: Message-Authenticator mismatch")
	ErrInvalidRequestAuthenticator  = errors.New("radius: invalid request authenticator")
)

// Attribute is a decoded RADIUS attribute. Value points into the parsed packet
// and must be treated as read-only.
type Attribute struct {
	Type  uint8
	Value []byte

	valueOffset int
}

// Packet is a decoded RADIUS packet. Attributes outside the packet's Length
// field are treated as padding and ignored.
type Packet struct {
	Code          uint8
	Identifier    uint8
	Length        uint16
	Authenticator [authenticatorSize]byte
	Attributes    []Attribute

	raw []byte
}

// Raw returns the packet bytes covered by the RADIUS Length field. The
// returned slice must be treated as read-only.
func (p *Packet) Raw() []byte {
	return p.raw
}

// Parse decodes a RADIUS packet. RADIUS permits unused trailing octets in a
// datagram; they are ignored and do not participate in authentication.
func Parse(packet []byte) (*Packet, error) {
	if len(packet) < headerSize {
		return nil, fmt.Errorf("%w: shorter than the 20-byte header", ErrMalformedPacket)
	}

	length := int(binary.BigEndian.Uint16(packet[2:4]))
	if length < headerSize || length > maxPacketSize {
		return nil, fmt.Errorf("%w: invalid Length %d", ErrMalformedPacket, length)
	}
	if length > len(packet) {
		return nil, fmt.Errorf(
			"%w: Length %d exceeds datagram size %d",
			ErrMalformedPacket, length, len(packet),
		)
	}

	attrs, err := parseAttributes(packet[headerSize:length])
	if err != nil {
		return nil, err
	}

	p := &Packet{
		Code:       packet[0],
		Identifier: packet[1],
		Length:     uint16(length),
		Attributes: attrs,
		raw:        packet[:length],
	}
	copy(p.Authenticator[:], packet[4:headerSize])
	return p, nil
}

func parseAttributes(attrs []byte) ([]Attribute, error) {
	parsed := make([]Attribute, 0, 8)
	for offset := 0; offset < len(attrs); {
		if offset+2 > len(attrs) {
			return nil, fmt.Errorf("%w: truncated attribute header", ErrMalformedPacket)
		}

		typ := attrs[offset]
		length := int(attrs[offset+1])
		if length < 2 {
			return nil, fmt.Errorf(
				"%w: attribute type %d has invalid length %d",
				ErrMalformedPacket, typ, length,
			)
		}
		if offset+length > len(attrs) {
			return nil, fmt.Errorf(
				"%w: attribute type %d overruns packet",
				ErrMalformedPacket, typ,
			)
		}

		parsed = append(parsed, Attribute{
			Type:        typ,
			Value:       attrs[offset+2 : offset+length],
			valueOffset: headerSize + offset + 2,
		})
		offset += length
	}
	return parsed, nil
}

// VerifyRequest validates an Access-Request. The Message-Authenticator MUST be
// the first attribute and its HMAC-MD5 MUST cover the complete request with the
// Message-Authenticator value zeroed. Missing, duplicate, misplaced, truncated,
// or mismatched values are rejected.
func VerifyRequest(packet, secret []byte) (*Packet, error) {
	p, err := Parse(packet)
	if err != nil {
		return nil, err
	}
	if p.Code != CodeAccessRequest {
		return nil, fmt.Errorf("%w: got %d, want Access-Request", ErrUnexpectedCode, p.Code)
	}
	if len(p.Attributes) == 0 {
		return nil, ErrMessageAuthenticatorMissing
	}

	first := p.Attributes[0]
	if first.Type != AttrMessageAuthenticator {
		for _, attr := range p.Attributes[1:] {
			if attr.Type == AttrMessageAuthenticator {
				return nil, ErrMessageAuthenticatorNotFirst
			}
		}
		return nil, ErrMessageAuthenticatorMissing
	}

	for _, attr := range p.Attributes[1:] {
		if attr.Type == AttrMessageAuthenticator {
			return nil, ErrMessageAuthenticatorDuplicate
		}
	}
	if len(first.Value) != messageAuthenticatorValueSize {
		return nil, fmt.Errorf(
			"%w: got %d bytes",
			ErrMessageAuthenticatorLength, len(first.Value),
		)
	}

	expected := computeMessageAuthenticator(p, secret, nil, first.valueOffset)
	if !hmac.Equal(expected, first.Value) {
		return nil, ErrMessageAuthenticatorMismatch
	}
	return p, nil
}

// VerifyResponse validates a response's Message-Authenticator. Per RFC 3579,
// the response MAC is computed with the authenticator from the corresponding
// Access-Request in the Authenticator header field and the
// Message-Authenticator value zeroed.
func VerifyResponse(packet, requestAuthenticator, secret []byte) (*Packet, error) {
	p, err := Parse(packet)
	if err != nil {
		return nil, err
	}
	if !isResponseCode(p.Code) {
		return nil, fmt.Errorf("%w: got response code %d", ErrUnexpectedCode, p.Code)
	}
	if len(requestAuthenticator) != authenticatorSize {
		return nil, ErrInvalidRequestAuthenticator
	}

	attr, err := singleMessageAuthenticator(p)
	if err != nil {
		return nil, err
	}

	expected := computeMessageAuthenticator(
		p, secret, requestAuthenticator, attr.valueOffset,
	)
	if !hmac.Equal(expected, attr.Value) {
		return nil, ErrMessageAuthenticatorMismatch
	}
	return p, nil
}

// SignResponse fills in the response Message-Authenticator using the request
// authenticator, then computes the normal RADIUS Response Authenticator. The
// response must already contain exactly one 16-byte Message-Authenticator
// attribute.
func SignResponse(packet, requestAuthenticator, secret []byte) error {
	p, err := Parse(packet)
	if err != nil {
		return err
	}
	if !isResponseCode(p.Code) {
		return fmt.Errorf("%w: got response code %d", ErrUnexpectedCode, p.Code)
	}
	if len(requestAuthenticator) != authenticatorSize {
		return ErrInvalidRequestAuthenticator
	}

	attr, err := singleMessageAuthenticator(p)
	if err != nil {
		return err
	}

	// RFC 3579: the response MAC uses the Access-Request authenticator in the
	// header, not the response authenticator being constructed below.
	expected := computeMessageAuthenticator(
		p, secret, requestAuthenticator, attr.valueOffset,
	)
	copy(packet[attr.valueOffset:attr.valueOffset+messageAuthenticatorValueSize], expected)

	// RFC 2865 Response Authenticator:
	// MD5(Code, Identifier, Length, Request Authenticator, Attributes, Secret).
	responseAuthInput := make([]byte, len(p.raw)+len(secret))
	copy(responseAuthInput, p.raw)
	copy(responseAuthInput[4:headerSize], requestAuthenticator)
	copy(responseAuthInput[len(p.raw):], secret)
	responseAuth := md5.Sum(responseAuthInput)
	copy(packet[4:headerSize], responseAuth[:])
	return nil
}

// NewResponse builds and signs an Access-Accept, Access-Reject, or
// Access-Challenge. The Message-Authenticator is placed at the front of the
// attribute list.
func NewResponse(
	code, identifier byte,
	requestAuthenticator, attributes, secret []byte,
) ([]byte, error) {
	if !isResponseCode(code) {
		return nil, fmt.Errorf("%w: got response code %d", ErrUnexpectedCode, code)
	}
	if len(requestAuthenticator) != authenticatorSize {
		return nil, ErrInvalidRequestAuthenticator
	}

	length := headerSize + messageAuthenticatorAttrSize + len(attributes)
	if length > maxPacketSize {
		return nil, fmt.Errorf("%w: packet length %d exceeds %d", ErrMalformedPacket, length, maxPacketSize)
	}

	packet := make([]byte, length)
	packet[0] = code
	packet[1] = identifier
	binary.BigEndian.PutUint16(packet[2:4], uint16(length))
	packet[headerSize] = AttrMessageAuthenticator
	packet[headerSize+1] = messageAuthenticatorAttrSize
	copy(packet[headerSize+messageAuthenticatorAttrSize:], attributes)

	if err := SignResponse(packet, requestAuthenticator, secret); err != nil {
		return nil, err
	}
	return packet, nil
}

func isResponseCode(code uint8) bool {
	switch code {
	case CodeAccessAccept, CodeAccessReject, CodeAccessChallenge:
		return true
	default:
		return false
	}
}

func singleMessageAuthenticator(p *Packet) (Attribute, error) {
	var found *Attribute
	for i := range p.Attributes {
		if p.Attributes[i].Type != AttrMessageAuthenticator {
			continue
		}
		if found != nil {
			return Attribute{}, ErrMessageAuthenticatorDuplicate
		}
		found = &p.Attributes[i]
	}
	if found == nil {
		return Attribute{}, ErrMessageAuthenticatorMissing
	}
	if len(found.Value) != messageAuthenticatorValueSize {
		return Attribute{}, fmt.Errorf(
			"%w: got %d bytes",
			ErrMessageAuthenticatorLength, len(found.Value),
		)
	}
	return *found, nil
}

func computeMessageAuthenticator(
	p *Packet,
	secret, authenticator []byte,
	valueOffset int,
) []byte {
	input := make([]byte, len(p.raw))
	copy(input, p.raw)
	if authenticator != nil {
		copy(input[4:headerSize], authenticator)
	}
	clear(input[valueOffset : valueOffset+messageAuthenticatorValueSize])

	mac := hmac.New(md5.New, secret)
	_, _ = mac.Write(input)
	return mac.Sum(nil)
}
