package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// SecurityHeaderLen is the wire length of the UADP SecurityHeader as
// scoped by this package: 4-byte SecurityTokenId followed by an 8-byte
// MessageNonce (4-byte random prefix + 4-byte big-endian counter).
const SecurityHeaderLen = 12

// SecurityHeader carries the fields of a UADP NetworkMessage security
// header that the admission gate needs to inspect.
type SecurityHeader struct {
	SecurityTokenId uint32
	// MessageNonce is the raw 8-byte nonce: NoncePrefix || NonceCounter.
	MessageNonce [8]byte
}

// NoncePrefix returns the 4-byte random prefix of the message nonce.
// It must remain constant for the lifetime of a security token.
func (h SecurityHeader) NoncePrefix() [4]byte {
	var prefix [4]byte
	copy(prefix[:], h.MessageNonce[:4])
	return prefix
}

// NonceCounter returns the big-endian counter portion of the nonce.
func (h SecurityHeader) NonceCounter() uint32 {
	return binary.BigEndian.Uint32(h.MessageNonce[4:8])
}

// NewSecurityHeader builds a header from its logical parts.
func NewSecurityHeader(tokenId uint32, prefix [4]byte, counter uint32) SecurityHeader {
	var h SecurityHeader
	h.SecurityTokenId = tokenId
	copy(h.MessageNonce[:4], prefix[:])
	binary.BigEndian.PutUint32(h.MessageNonce[4:8], counter)
	return h
}

// ErrSecurityHeaderShort is returned when a buffer cannot hold a full
// security header.
var ErrSecurityHeaderShort = errors.New("netsec: buffer too short for security header")

// ParseSecurityHeader decodes a security header from the front of buf,
// returning the header and the remaining payload.
func ParseSecurityHeader(buf []byte) (SecurityHeader, []byte, error) {
	if len(buf) < SecurityHeaderLen {
		return SecurityHeader{}, nil, ErrSecurityHeaderShort
	}
	var h SecurityHeader
	h.SecurityTokenId = binary.BigEndian.Uint32(buf[0:4])
	copy(h.MessageNonce[:], buf[4:12])
	return h, buf[SecurityHeaderLen:], nil
}

// AppendEncode appends the wire encoding of h to dst.
func (h SecurityHeader) AppendEncode(dst []byte) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], h.SecurityTokenId)
	dst = append(dst, tmp[:]...)
	dst = append(dst, h.MessageNonce[:]...)
	return dst
}

// Admission errors. All are returned wrapped with context; use
// errors.Is to match.
var (
	// ErrTokenRejected: the token id is neither the current nor the
	// previous token.
	ErrTokenRejected = errors.New("netsec: security token id not accepted")
	// ErrTokenTooOld: the token id predates the previous token
	// (grandparent or older) and is not the current token.
	ErrTokenTooOld = errors.New("netsec: security token id older than previous token")
	// ErrNonceReplay: the nonce counter is not strictly greater than
	// the highest counter already seen for this token and publisher.
	ErrNonceReplay = errors.New("netsec: nonce counter replay or rollback")
	// ErrNoncePrefixChanged: the random nonce prefix changed within a
	// single token lifetime, which indicates a forged message.
	ErrNoncePrefixChanged = errors.New("netsec: nonce prefix changed within token lifetime")
	// ErrVerificationFailed: signature or decryption verification
	// failed; no admission state was advanced.
	ErrVerificationFailed = errors.New("netsec: message verification failed")
)

func wrapf(err error, format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, err)...)
}
