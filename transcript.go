package netsec

import (
	"crypto/sha256"
	"hash"
)

// HandshakeType is a TLS handshake message type, as carried in the first
// byte of a handshake message header.
type HandshakeType uint8

const (
	ClientHello         HandshakeType = 1
	ServerHello         HandshakeType = 2
	NewSessionTicket    HandshakeType = 4
	EndOfEarlyData      HandshakeType = 5
	EncryptedExtensions HandshakeType = 8
	Certificate         HandshakeType = 11
	CertificateRequest  HandshakeType = 13
	CertificateVerify   HandshakeType = 15
	Finished            HandshakeType = 20
	KeyUpdate           HandshakeType = 24
)

// Transcript is the running SHA-256 hash of the handshake messages. Only
// handshake messages are ever added; a compatibility ChangeCipherSpec
// record is a record-layer artifact and must never be written here, so
// that it cannot move the binding value a Finished is computed over.
type Transcript struct {
	h hash.Hash
}

// NewTranscript returns an empty handshake transcript.
func NewTranscript() *Transcript {
	return &Transcript{h: sha256.New()}
}

// Add appends one handshake message: the four-byte header (type plus
// uint24 length) followed by the message body.
func (t *Transcript) Add(typ HandshakeType, body []byte) {
	var hdr [4]byte
	hdr[0] = byte(typ)
	hdr[1] = byte(len(body) >> 16)
	hdr[2] = byte(len(body) >> 8)
	hdr[3] = byte(len(body))
	t.h.Write(hdr[:])
	t.h.Write(body)
}

// Sum returns the current transcript hash.
func (t *Transcript) Sum() []byte {
	return t.h.Sum(nil)
}
