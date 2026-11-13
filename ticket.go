package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

const (
	ticketMagic    = "NETS"
	ticketVersion  = 1
	ticketNonceLen = 12
	ticketKeyLen   = 32
)

// TicketData is the plaintext state sealed inside a resumption ticket.

// It deliberately contains no extended_master_secret flag: the flag for a
// resumed session is authoritative only in the server-side SessionStore, so
// stripping it from the ticket removes any way for a peer to tamper with EMS
// negotiation during resumption.
type TicketData struct {
	SessionID []byte
}

// TicketCipher seals and opens resumption tickets with an authenticated
// AES-256-GCM construction. The key ID lets operators rotate keys while still
// accepting tickets minted by an older key.
type TicketCipher struct {
	key   []byte
	keyID byte
	aead  cipher.AEAD
}

// NewTicketCipher builds a TicketCipher from a 32-byte AES-256 key and the
// key ID advertised on every minted ticket.
func NewTicketCipher(key []byte, keyID byte) (*TicketCipher, error) {
	if len(key) != ticketKeyLen {
		return nil, fmt.Errorf("netsec: ticket key must be %d bytes, got %d", ticketKeyLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TicketCipher{
		key:   append([]byte(nil), key...),
		keyID: keyID,
		aead:  aead,
	}, nil
}

// Seal returns an authenticated, encrypted ticket carrying data.
func (c *TicketCipher) Seal(data *TicketData) ([]byte, error) {
	if data == nil || len(data.SessionID) == 0 {
		return nil, ErrTicketInvalid
	}
	nonce := make([]byte, ticketNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	plaintext := make([]byte, 2+len(data.SessionID))
	binary.BigEndian.PutUint16(plaintext, uint16(len(data.SessionID)))
	copy(plaintext[2:], data.SessionID)

	sealed := c.aead.Seal(nil, nonce, plaintext, nil)

	ticket := make([]byte, 0, len(ticketMagic)+3+ticketNonceLen+len(sealed))
	ticket = append(ticket, ticketMagic...)
	ticket = append(ticket, ticketVersion, c.keyID, ticketNonceLen)
	ticket = append(ticket, nonce...)
	ticket = append(ticket, sealed...)
	return ticket, nil
}

// Open authenticates and decrypts a ticket previously produced by Seal. Any
// truncation, version mismatch or authentication failure yields
// ErrTicketInvalid.
func (c *TicketCipher) Open(ticket []byte) (*TicketData, error) {
	headerLen := len(ticketMagic) + 3
	if len(ticket) < headerLen+ticketNonceLen {
		return nil, ErrTicketInvalid
	}
	if string(ticket[:len(ticketMagic)]) != ticketMagic {
		return nil, ErrTicketInvalid
	}
	if ticket[len(ticketMagic)] != ticketVersion {
		return nil, ErrTicketInvalid
	}
	if ticket[len(ticketMagic)+1] != c.keyID {
		return nil, ErrTicketInvalid
	}
	nonceLen := int(ticket[len(ticketMagic)+2])
	if nonceLen != ticketNonceLen || len(ticket) < headerLen+nonceLen {
		return nil, ErrTicketInvalid
	}
	nonce := ticket[headerLen : headerLen+nonceLen]
	sealed := ticket[headerLen+nonceLen:]

	plaintext, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrTicketInvalid
	}
	if len(plaintext) < 2 {
		return nil, ErrTicketInvalid
	}
	idLen := int(binary.BigEndian.Uint16(plaintext))
	if 2+idLen != len(plaintext) {
		return nil, ErrTicketInvalid
	}
	return &TicketData{SessionID: append([]byte(nil), plaintext[2:]...)}, nil
}
