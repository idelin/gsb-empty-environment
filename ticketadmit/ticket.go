package ticketadmit

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// envelopeVersion is the current wire envelope version.
const envelopeVersion byte = 1

// magic identifies tickets produced by this package.
var envelopeMagic = [4]byte{'T', 'A', 'D', 'M'}

// fixedHeaderSize is magic(4) + version(1) + generation(4) + ageAdd(4) +
// nonce(12).
const fixedHeaderSize = 4 + 1 + 4 + 4 + nonceSize

// ErrUnknownTicket is returned when a ticket cannot be authenticated with
// either deployed key generation, or is not a ticketadmit ticket at all.
// Callers must treat it as "no resumable session" and continue with a full
// handshake; it must never cause the TLS handshake to fail.
var ErrUnknownTicket = errors.New("ticketadmit: ticket cannot be decrypted by either key generation")

// Ticket is a sealed session ticket together with the metadata the caller
// needs for resumption admission.
type Ticket struct {
	// Raw is the opaque, authenticated wire ticket handed to the client.
	Raw []byte

	// Claims are the plaintext binding claims sealed in Raw.
	Claims Claims

	// AgeAdd is the ticket_age_add value (RFC 8446 section 4.2.11.2)
	// associated with the ticket, carried in the unauthenticated envelope
	// header. Clients obfuscate the ticket age with it.
	AgeAdd uint32

	// KeyGeneration is the generation used to seal the ticket.
	KeyGeneration uint32
}

// Seal authenticates and encrypts the claims with the current key.
func (r *KeyRing) Seal(claims Claims) (*Ticket, error) {
	plaintext, err := claims.marshal()
	if err != nil {
		return nil, err
	}

	var ageAddBytes [4]byte
	if _, err := rand.Read(ageAddBytes[:]); err != nil {
		return nil, err
	}
	ageAdd := binary.BigEndian.Uint32(ageAddBytes[:])

	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}

	header := make([]byte, fixedHeaderSize)
	off := 0
	copy(header[off:], envelopeMagic[:])
	off += len(envelopeMagic)
	header[off] = envelopeVersion
	off++
	binary.BigEndian.PutUint32(header[off:], r.current.Generation)
	off += 4
	binary.BigEndian.PutUint32(header[off:], ageAdd)
	off += 4
	copy(header[off:], nonce[:])

	// The full header (including the key generation and nonce) is
	// authenticated data: an attacker cannot retarget a ticket at another key
	// generation or swap nonces without being detected.
	ciphertext := r.current.aead.Seal(nil, nonce[:], plaintext, header)

	raw := make([]byte, 0, len(header)+len(ciphertext))
	raw = append(raw, header...)
	raw = append(raw, ciphertext...)

	return &Ticket{
		Raw:           raw,
		Claims:        claims,
		AgeAdd:        ageAdd,
		KeyGeneration: r.current.Generation,
	}, nil
}

// Open authenticates a ticket, trying the current key generation first and
// then the previous one. It returns ErrUnknownTicket when neither generation
// can authenticate the ticket, including tickets sealed by a retired
// three-generations-old key. A ticket that decrypts but contains malformed
// claims returns ErrMalformedTicket.
func (r *KeyRing) Open(raw []byte) (*Ticket, error) {
	if len(raw) < fixedHeaderSize {
		return nil, ErrUnknownTicket
	}
	header := raw[:fixedHeaderSize]
	off := 0
	var magic [4]byte
	copy(magic[:], header[off:])
	off += len(magic)
	if magic != envelopeMagic {
		return nil, ErrUnknownTicket
	}
	if header[off] != envelopeVersion {
		return nil, ErrUnknownTicket
	}
	off++
	generation := binary.BigEndian.Uint32(header[off:])
	off += 4
	ageAdd := binary.BigEndian.Uint32(header[off:])
	off += 4
	nonce := header[off : off+nonceSize]

	key := r.lookup(generation)
	if key == nil {
		return nil, ErrUnknownTicket
	}

	plaintext, err := key.aead.Open(nil, nonce, raw[fixedHeaderSize:], header)
	if err != nil {
		// Authentication failure: do not reveal which stage failed. Treat it
		// exactly like an unknown generation so the caller falls back to a
		// full handshake.
		return nil, ErrUnknownTicket
	}

	claims, err := unmarshalClaims(plaintext)
	if err != nil {
		return nil, err
	}
	return &Ticket{
		Raw:           append([]byte(nil), raw...),
		Claims:        claims,
		AgeAdd:        ageAdd,
		KeyGeneration: generation,
	}, nil
}
