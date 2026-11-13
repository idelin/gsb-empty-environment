package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Wire message type tags.
const (
	messageHello         byte = 1
	messageResponse      byte = 2
	messageInitiatorAuth byte = 3
	messageConfirmation  byte = 4
)

// MaxWireFieldLength bounds every length-prefixed wire field.
const MaxWireFieldLength = 1 << 20

// ErrMalformedMessage is returned by strict wire decoders.
var ErrMalformedMessage = errors.New("netsec: malformed message")

// Message is implemented by every handshake wire message.
type Message interface {
	MessageType() byte
}

// Hello is A's first message. It carries a name label and X. It never carries a
// public key: the receiving host resolves the label in its fixed registry.
type Hello struct {
	SessionID []byte
	Initiator string
	X         []byte
}

// MessageType implements Message.
func (*Hello) MessageType() byte { return messageHello }

// Response is B's message. It carries B's name label, Y, and B's Ed25519
// signature over the transcript digest.
type Response struct {
	SessionID []byte
	Responder string
	Y         []byte
	Signature []byte
}

// MessageType implements Message.
func (*Response) MessageType() byte { return messageResponse }

// InitiatorAuth is A's signature over the same transcript digest that B signed.
type InitiatorAuth struct {
	SessionID []byte
	Initiator string
	Signature []byte
}

// MessageType implements Message.
func (*InitiatorAuth) MessageType() byte { return messageInitiatorAuth }

// Role identifies the sender of a confirmation value.
type Role uint8

const (
	// RoleA is the initiator's confirmation role.
	RoleA Role = 1
	// RoleB is the responder's confirmation role.
	RoleB Role = 2
)

// IsValid reports whether r is a known role.
func (r Role) IsValid() bool { return r == RoleA || r == RoleB }

func (r Role) String() string {
	switch r {
	case RoleA:
		return "A"
	case RoleB:
		return "B"
	default:
		return fmt.Sprintf("role(%d)", uint8(r))
	}
}

// Confirmation is an HMAC over the role label and transcript digest.
type Confirmation struct {
	SessionID []byte
	Role      Role
	MAC       []byte
}

// MessageType implements Message.
func (*Confirmation) MessageType() byte { return messageConfirmation }

func cloneHello(h *Hello) *Hello {
	if h == nil {
		return nil
	}
	return &Hello{
		SessionID: append([]byte(nil), h.SessionID...),
		Initiator: h.Initiator,
		X:         append([]byte(nil), h.X...),
	}
}

func cloneResponse(r *Response) *Response {
	if r == nil {
		return nil
	}
	return &Response{
		SessionID: append([]byte(nil), r.SessionID...),
		Responder: r.Responder,
		Y:         append([]byte(nil), r.Y...),
		Signature: append([]byte(nil), r.Signature...),
	}
}

func cloneAuth(a *InitiatorAuth) *InitiatorAuth {
	if a == nil {
		return nil
	}
	return &InitiatorAuth{
		SessionID: append([]byte(nil), a.SessionID...),
		Initiator: a.Initiator,
		Signature: append([]byte(nil), a.Signature...),
	}
}

func cloneConfirmation(c *Confirmation) *Confirmation {
	if c == nil {
		return nil
	}
	return &Confirmation{
		SessionID: append([]byte(nil), c.SessionID...),
		Role:      c.Role,
		MAC:       append([]byte(nil), c.MAC...),
	}
}

// MarshalBinary encodes a Hello with strict length prefixes.
func (h *Hello) MarshalBinary() ([]byte, error) {
	if h == nil {
		return nil, ErrMalformedMessage
	}
	out := []byte{messageHello}
	out = appendLengthPrefixed(out, h.SessionID)
	out = appendLengthPrefixed(out, []byte(h.Initiator))
	out = appendLengthPrefixed(out, h.X)
	return out, nil
}

// UnmarshalBinary decodes a Hello.
func (h *Hello) UnmarshalBinary(data []byte) error {
	if h == nil {
		return ErrMalformedMessage
	}
	body, err := splitMessage(data, messageHello)
	if err != nil {
		return err
	}
	fields, err := parseFields(body, 3)
	if err != nil {
		return err
	}
	h.SessionID = fields[0]
	h.Initiator = string(fields[1])
	h.X = fields[2]
	return nil
}

// MarshalBinary encodes a Response with strict length prefixes.
func (r *Response) MarshalBinary() ([]byte, error) {
	if r == nil {
		return nil, ErrMalformedMessage
	}
	out := []byte{messageResponse}
	out = appendLengthPrefixed(out, r.SessionID)
	out = appendLengthPrefixed(out, []byte(r.Responder))
	out = appendLengthPrefixed(out, r.Y)
	out = appendLengthPrefixed(out, r.Signature)
	return out, nil
}

// UnmarshalBinary decodes a Response.
func (r *Response) UnmarshalBinary(data []byte) error {
	if r == nil {
		return ErrMalformedMessage
	}
	body, err := splitMessage(data, messageResponse)
	if err != nil {
		return err
	}
	fields, err := parseFields(body, 4)
	if err != nil {
		return err
	}
	r.SessionID = fields[0]
	r.Responder = string(fields[1])
	r.Y = fields[2]
	r.Signature = fields[3]
	return nil
}

// MarshalBinary encodes an InitiatorAuth with strict length prefixes.
func (a *InitiatorAuth) MarshalBinary() ([]byte, error) {
	if a == nil {
		return nil, ErrMalformedMessage
	}
	out := []byte{messageInitiatorAuth}
	out = appendLengthPrefixed(out, a.SessionID)
	out = appendLengthPrefixed(out, []byte(a.Initiator))
	out = appendLengthPrefixed(out, a.Signature)
	return out, nil
}

// UnmarshalBinary decodes an InitiatorAuth.
func (a *InitiatorAuth) UnmarshalBinary(data []byte) error {
	if a == nil {
		return ErrMalformedMessage
	}
	body, err := splitMessage(data, messageInitiatorAuth)
	if err != nil {
		return err
	}
	fields, err := parseFields(body, 3)
	if err != nil {
		return err
	}
	a.SessionID = fields[0]
	a.Initiator = string(fields[1])
	a.Signature = fields[2]
	return nil
}

// MarshalBinary encodes a Confirmation with strict length prefixes.
func (c *Confirmation) MarshalBinary() ([]byte, error) {
	if c == nil {
		return nil, ErrMalformedMessage
	}
	if !c.Role.IsValid() {
		return nil, ErrMalformedMessage
	}
	out := []byte{messageConfirmation}
	out = appendLengthPrefixed(out, c.SessionID)
	out = append(out, byte(c.Role))
	out = appendLengthPrefixed(out, c.MAC)
	return out, nil
}

// UnmarshalBinary decodes a Confirmation.
func (c *Confirmation) UnmarshalBinary(data []byte) error {
	if c == nil {
		return ErrMalformedMessage
	}
	body, err := splitMessage(data, messageConfirmation)
	if err != nil {
		return err
	}
	cursor := wireCursor{data: body}
	sid, err := cursor.field()
	if err != nil {
		return err
	}
	role, err := cursor.byte()
	if err != nil {
		return err
	}
	mac, err := cursor.field()
	if err != nil {
		return err
	}
	if cursor.remaining() != 0 {
		return ErrMalformedMessage
	}
	c.SessionID = sid
	c.Role = Role(role)
	c.MAC = mac
	if !c.Role.IsValid() {
		return ErrMalformedMessage
	}
	return nil
}

// EncodeMessage encodes any handshake message with its type tag.
func EncodeMessage(message Message) ([]byte, error) {
	switch typed := message.(type) {
	case *Hello:
		return typed.MarshalBinary()
	case *Response:
		return typed.MarshalBinary()
	case *InitiatorAuth:
		return typed.MarshalBinary()
	case *Confirmation:
		return typed.MarshalBinary()
	default:
		return nil, fmt.Errorf("%w: unsupported type %T", ErrMalformedMessage, message)
	}
}

// DecodeMessage decodes one complete handshake message and rejects trailing
// bytes.
func DecodeMessage(data []byte) (Message, error) {
	if len(data) == 0 {
		return nil, ErrMalformedMessage
	}
	switch data[0] {
	case messageHello:
		var message Hello
		if err := message.UnmarshalBinary(data); err != nil {
			return nil, err
		}
		return &message, nil
	case messageResponse:
		var message Response
		if err := message.UnmarshalBinary(data); err != nil {
			return nil, err
		}
		return &message, nil
	case messageInitiatorAuth:
		var message InitiatorAuth
		if err := message.UnmarshalBinary(data); err != nil {
			return nil, err
		}
		return &message, nil
	case messageConfirmation:
		var message Confirmation
		if err := message.UnmarshalBinary(data); err != nil {
			return nil, err
		}
		return &message, nil
	default:
		return nil, fmt.Errorf("%w: unknown type %d", ErrMalformedMessage, data[0])
	}
}

func splitMessage(data []byte, expected byte) ([]byte, error) {
	if len(data) < 1 || data[0] != expected {
		return nil, fmt.Errorf("%w: wrong type tag", ErrMalformedMessage)
	}
	return data[1:], nil
}

func parseFields(data []byte, count int) ([][]byte, error) {
	cursor := wireCursor{data: data}
	fields := make([][]byte, count)
	for i := range fields {
		field, err := cursor.field()
		if err != nil {
			return nil, err
		}
		fields[i] = field
	}
	if cursor.remaining() != 0 {
		return nil, fmt.Errorf("%w: trailing bytes", ErrMalformedMessage)
	}
	return fields, nil
}

type wireCursor struct {
	data   []byte
	offset int
}

func (c *wireCursor) remaining() int { return len(c.data) - c.offset }

func (c *wireCursor) byte() (byte, error) {
	if c.remaining() < 1 {
		return 0, ErrMalformedMessage
	}
	value := c.data[c.offset]
	c.offset++
	return value, nil
}

func (c *wireCursor) field() ([]byte, error) {
	if c.remaining() < 4 {
		return nil, fmt.Errorf("%w: missing length", ErrMalformedMessage)
	}
	length := int(binary.BigEndian.Uint32(c.data[c.offset : c.offset+4]))
	c.offset += 4
	if length < 0 || length > MaxWireFieldLength || c.remaining() < length {
		return nil, fmt.Errorf("%w: bad field length", ErrMalformedMessage)
	}
	value := append([]byte(nil), c.data[c.offset:c.offset+length]...)
	c.offset += length
	return value, nil
}
