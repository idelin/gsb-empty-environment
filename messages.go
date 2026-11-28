package wireguard

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// WireGuard message types.
const (
	MessageHandshakeInitiation uint32 = 1
	MessageHandshakeResponse   uint32 = 2
	MessageCookieReply         uint32 = 3
	MessageTransportData       uint32 = 4
)

// Common aliases used by other WireGuard implementations.
const (
	MessageInitiationType  = MessageHandshakeInitiation
	MessageResponseType    = MessageHandshakeResponse
	MessageCookieReplyType = MessageCookieReply
	MessageTransportType   = MessageTransportData
)

// WireGuard wire message sizes.
const (
	MessageInitiationSize      = 148
	MessageResponseSize        = 92
	MessageCookieReplySize     = 64
	MessageTransportHeaderSize = 16
	MessageTransportMinSize    = MessageTransportHeaderSize + poly1305TagSize
	CookieSize                 = 16
	MACSize                    = 16
)

const (
	handshakeInitiationSize = MessageInitiationSize
	handshakeResponseSize   = MessageResponseSize
	cookieReplySize         = MessageCookieReplySize
	transportHeaderSize     = MessageTransportHeaderSize

	initiationMAC1Offset = 116
	initiationMAC2Offset = 132
	responseMAC1Offset   = 60
	responseMAC2Offset   = 76
)

var (
	// ErrInvalidMessage means a packet is malformed or has the wrong size.
	ErrInvalidMessage = errors.New("wireguard: invalid message")
	// ErrAuthentication means a MAC or AEAD tag did not verify.
	ErrAuthentication = errors.New("wireguard: authentication failed")
	// ErrReplay means a handshake timestamp was not strictly increasing.
	ErrReplay = errors.New("wireguard: replayed handshake timestamp")
	// ErrUnknownSession means no key slot matches the receiver index.
	ErrUnknownSession = errors.New("wireguard: unknown session index")
	// ErrNoSession means there is no current key usable for sending.
	ErrNoSession = errors.New("wireguard: no current session")
	// ErrSessionExpired means a session hit a hard reject limit and was zeroed.
	ErrSessionExpired = errors.New("wireguard: session expired")
	// ErrReplayWindow means a transport counter was a duplicate or too old.
	ErrReplayWindow = errors.New("wireguard: transport replay rejected")
	// ErrHandshakeUnconfirmed means a responder tried to send on Next before
	// receiving the initiator's first authenticated transport packet.
	ErrHandshakeUnconfirmed = errors.New("wireguard: handshake response not yet confirmed")
	// ErrUnexpectedHandshake means a response did not match a pending handshake.
	ErrUnexpectedHandshake = errors.New("wireguard: unexpected handshake message")
)

func messageType(pkt []byte) (uint32, error) {
	if len(pkt) < 4 {
		return 0, ErrInvalidMessage
	}
	return binary.LittleEndian.Uint32(pkt[0:4]), nil
}

func mac1Key(suite Suite, pub [32]byte) [32]byte {
	return suite.Hash([]byte(labelMAC1), pub[:])
}

func cookieKey(suite Suite, pub [32]byte) [32]byte {
	return suite.Hash([]byte(labelCookie), pub[:])
}

func checkMAC1(suite Suite, pkt []byte, offset int, pub [32]byte) bool {
	if len(pkt) < offset+16 {
		return false
	}
	key := mac1Key(suite, pub)
	expected := suite.MAC(key[:], pkt[:offset])
	return subtle.ConstantTimeCompare(expected[:], pkt[offset:offset+16]) == 1
}

func checkMAC2(suite Suite, pkt []byte, offset int, cookie [16]byte) bool {
	if len(pkt) < offset+16 {
		return false
	}
	expected := suite.MAC(cookie[:], pkt[:offset])
	return subtle.ConstantTimeCompare(expected[:], pkt[offset:offset+16]) == 1
}

type handshakeInitiation struct {
	Sender             uint32
	Ephemeral          [32]byte
	EncryptedStatic    [48]byte
	EncryptedTimestamp [28]byte
	MAC1               [16]byte
	MAC2               [16]byte
}

func parseHandshakeInitiation(pkt []byte) (handshakeInitiation, error) {
	var m handshakeInitiation
	if len(pkt) != handshakeInitiationSize {
		return m, ErrInvalidMessage
	}
	if binary.LittleEndian.Uint32(pkt[0:4]) != MessageHandshakeInitiation {
		return m, ErrInvalidMessage
	}
	m.Sender = binary.LittleEndian.Uint32(pkt[4:8])
	copy(m.Ephemeral[:], pkt[8:40])
	copy(m.EncryptedStatic[:], pkt[40:88])
	copy(m.EncryptedTimestamp[:], pkt[88:116])
	copy(m.MAC1[:], pkt[116:132])
	copy(m.MAC2[:], pkt[132:148])
	return m, nil
}

func (m *handshakeInitiation) marshal() []byte {
	pkt := make([]byte, handshakeInitiationSize)
	binary.LittleEndian.PutUint32(pkt[0:4], MessageHandshakeInitiation)
	binary.LittleEndian.PutUint32(pkt[4:8], m.Sender)
	copy(pkt[8:40], m.Ephemeral[:])
	copy(pkt[40:88], m.EncryptedStatic[:])
	copy(pkt[88:116], m.EncryptedTimestamp[:])
	copy(pkt[116:132], m.MAC1[:])
	copy(pkt[132:148], m.MAC2[:])
	return pkt
}

type handshakeResponse struct {
	Sender           uint32
	Receiver         uint32
	Ephemeral        [32]byte
	EncryptedNothing [16]byte
	MAC1             [16]byte
	MAC2             [16]byte
}

func parseHandshakeResponse(pkt []byte) (handshakeResponse, error) {
	var m handshakeResponse
	if len(pkt) != handshakeResponseSize {
		return m, ErrInvalidMessage
	}
	if binary.LittleEndian.Uint32(pkt[0:4]) != MessageHandshakeResponse {
		return m, ErrInvalidMessage
	}
	m.Sender = binary.LittleEndian.Uint32(pkt[4:8])
	m.Receiver = binary.LittleEndian.Uint32(pkt[8:12])
	copy(m.Ephemeral[:], pkt[12:44])
	copy(m.EncryptedNothing[:], pkt[44:60])
	copy(m.MAC1[:], pkt[60:76])
	copy(m.MAC2[:], pkt[76:92])
	return m, nil
}

func (m *handshakeResponse) marshal() []byte {
	pkt := make([]byte, handshakeResponseSize)
	binary.LittleEndian.PutUint32(pkt[0:4], MessageHandshakeResponse)
	binary.LittleEndian.PutUint32(pkt[4:8], m.Sender)
	binary.LittleEndian.PutUint32(pkt[8:12], m.Receiver)
	copy(pkt[12:44], m.Ephemeral[:])
	copy(pkt[44:60], m.EncryptedNothing[:])
	copy(pkt[60:76], m.MAC1[:])
	copy(pkt[76:92], m.MAC2[:])
	return pkt
}

type cookieReply struct {
	Receiver uint32
	Nonce    [24]byte
	Cookie   [32]byte
}

func parseCookieReply(pkt []byte) (cookieReply, error) {
	var m cookieReply
	if len(pkt) != cookieReplySize {
		return m, ErrInvalidMessage
	}
	if binary.LittleEndian.Uint32(pkt[0:4]) != MessageCookieReply {
		return m, ErrInvalidMessage
	}
	m.Receiver = binary.LittleEndian.Uint32(pkt[4:8])
	copy(m.Nonce[:], pkt[8:32])
	copy(m.Cookie[:], pkt[32:64])
	return m, nil
}

func (m *cookieReply) marshal() []byte {
	pkt := make([]byte, cookieReplySize)
	binary.LittleEndian.PutUint32(pkt[0:4], MessageCookieReply)
	binary.LittleEndian.PutUint32(pkt[4:8], m.Receiver)
	copy(pkt[8:32], m.Nonce[:])
	copy(pkt[32:64], m.Cookie[:])
	return pkt
}

type transportPacket struct {
	Receiver uint32
	Counter  uint64
	Body     []byte
}

func parseTransport(pkt []byte) (transportPacket, error) {
	var m transportPacket
	if len(pkt) < transportHeaderSize+poly1305TagSize {
		return m, ErrInvalidMessage
	}
	if binary.LittleEndian.Uint32(pkt[0:4]) != MessageTransportData {
		return m, ErrInvalidMessage
	}
	m.Receiver = binary.LittleEndian.Uint32(pkt[4:8])
	m.Counter = binary.LittleEndian.Uint64(pkt[8:16])
	m.Body = pkt[16:]
	return m, nil
}

func marshalTransport(receiver uint32, counter uint64, body []byte) []byte {
	pkt := make([]byte, transportHeaderSize+len(body))
	binary.LittleEndian.PutUint32(pkt[0:4], MessageTransportData)
	binary.LittleEndian.PutUint32(pkt[4:8], receiver)
	binary.LittleEndian.PutUint64(pkt[8:16], counter)
	copy(pkt[16:], body)
	return pkt
}

func transportAD(pkt []byte) []byte {
	return pkt[:transportHeaderSize]
}

func buildCookieReply(suite Suite, senderPub [32]byte, receiverIndex uint32, cookie [16]byte, receivedMAC1 [16]byte, r io.Reader) ([]byte, error) {
	var m cookieReply
	m.Receiver = receiverIndex
	if _, err := io.ReadFull(r, m.Nonce[:]); err != nil {
		return nil, err
	}
	key := cookieKey(suite, senderPub)
	encrypted, err := suite.SealX(key, m.Nonce, cookie[:], receivedMAC1[:])
	if err != nil {
		return nil, fmt.Errorf("wireguard: sealing cookie reply: %w", err)
	}
	copy(m.Cookie[:], encrypted)
	return m.marshal(), nil
}

// MessageKind classifies a WireGuard packet.
type MessageKind uint32

const (
	KindInvalid MessageKind = iota
	KindHandshakeInitiation
	KindHandshakeResponse
	KindCookieReply
	KindTransport
)

// ClassifyMessage identifies a packet by its type field.
func ClassifyMessage(pkt []byte) MessageKind {
	t, err := messageType(pkt)
	if err != nil {
		return KindInvalid
	}
	switch t {
	case MessageHandshakeInitiation:
		return KindHandshakeInitiation
	case MessageHandshakeResponse:
		return KindHandshakeResponse
	case MessageCookieReply:
		return KindCookieReply
	case MessageTransportData:
		return KindTransport
	default:
		return KindInvalid
	}
}
