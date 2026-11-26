package quicguard

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

// retryTokenVersion is the on-wire version of the sealed Retry token layout.
const retryTokenVersion byte = 0x01

// retryNonceSize is the GCM nonce length and also the replay key size.
const retryNonceSize = 12

// maxClockSkew bounds how far in the future an issued token may appear before
// it is rejected as invalid.
const maxClockSkew = 60 * time.Second

// retryIntegritySaltV1 is the per-version salt from RFC 9001 section 5.8.
var retryIntegritySaltV1 = [20]byte{
	0xef, 0xfc, 0x0f, 0x7e, 0x7e, 0xb8, 0xad, 0xf5, 0x10, 0x26,
	0x4c, 0x68, 0x2e, 0x8a, 0x8f, 0x7e, 0x31, 0x92, 0x97, 0x79,
}

// RetryTokenClaims is the authenticated content bound into a Retry token.
type RetryTokenClaims struct {
	// ClientIP is the source IP address the token is bound to.
	ClientIP netip.Addr
	// OriginalDCID is the DCID of the client's first Initial (RFC 9000 17.2.5).
	OriginalDCID []byte
	// RetrySCID is the server-chosen CID carried as the Retry Source CID.
	RetrySCID []byte
	// IssuedAt is the token creation time used for expiry evaluation.
	IssuedAt time.Time
}

func appendCIDField(buf []byte, cid []byte) ([]byte, error) {
	if len(cid) > maxConnectionIDLen {
		return nil, ErrConnectionIDTooLong
	}
	buf = append(buf, byte(len(cid)))
	return append(buf, cid...), nil
}

func encodeTokenPlaintext(c *RetryTokenClaims) ([]byte, error) {
	ip := c.ClientIP.AsSlice()
	if len(ip) > 255 {
		return nil, fmt.Errorf("%w: client IP", ErrMalformed)
	}
	buf := make([]byte, 0, 1+8+1+len(ip)+1+len(c.OriginalDCID)+1+len(c.RetrySCID))
	buf = append(buf, retryTokenVersion)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(c.IssuedAt.UnixNano()))
	buf = append(buf, ts[:]...)
	buf = append(buf, byte(len(ip)))
	buf = append(buf, ip...)
	var err error
	buf, err = appendCIDField(buf, c.OriginalDCID)
	if err != nil {
		return nil, err
	}
	buf, err = appendCIDField(buf, c.RetrySCID)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func readCIDField(b []byte, pos int) (cid []byte, next int, err error) {
	if pos >= len(b) {
		return nil, pos, ErrMalformed
	}
	length := int(b[pos])
	pos++
	if length > maxConnectionIDLen || pos+length > len(b) {
		return nil, pos, ErrMalformed
	}
	return b[pos : pos+length], pos + length, nil
}

func decodeTokenPlaintext(raw []byte) (*RetryTokenClaims, error) {
	pos := 0
	if len(raw) < 1+8+1 {
		return nil, ErrMalformed
	}
	if raw[pos] != retryTokenVersion {
		return nil, fmt.Errorf("%w: unknown token version", ErrMalformed)
	}
	pos++
	issued := time.Unix(0, int64(binary.BigEndian.Uint64(raw[pos:pos+8])))
	pos += 8

	ipLen := int(raw[pos])
	pos++
	if ipLen == 0 || pos+ipLen > len(raw) {
		return nil, ErrMalformed
	}
	ip, ok := netip.AddrFromSlice(raw[pos : pos+ipLen])
	if !ok {
		return nil, ErrMalformed
	}
	pos += ipLen

	origDCID, pos, err := readCIDField(raw, pos)
	if err != nil {
		return nil, err
	}
	retrySCID, pos, err := readCIDField(raw, pos)
	if err != nil {
		return nil, err
	}
	if pos != len(raw) {
		return nil, ErrMalformed
	}
	return &RetryTokenClaims{
		ClientIP:     ip,
		OriginalDCID: append([]byte(nil), origDCID...),
		RetrySCID:    append([]byte(nil), retrySCID...),
		IssuedAt:     issued,
	}, nil
}

// MintRetryToken seals the claims with AES-256-GCM under the guard's token key.
// The random 12-byte GCM nonce also serves as the anti-replay identifier.
func (g *Guard) MintRetryToken(claims *RetryTokenClaims) ([]byte, error) {
	if claims == nil {
		return nil, fmt.Errorf("%w: nil claims", ErrMalformed)
	}
	if !claims.ClientIP.IsValid() {
		return nil, fmt.Errorf("%w: invalid client IP", ErrMalformed)
	}
	plaintext, err := encodeTokenPlaintext(claims)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, retryNonceSize)
	if _, err := g.readRandom(nonce); err != nil {
		return nil, err
	}
	sealed := g.tokenAEAD.Seal(nil, nonce, plaintext, nil)
	token := make([]byte, 0, retryNonceSize+len(sealed))
	token = append(token, nonce...)
	token = append(token, sealed...)
	return token, nil
}

// VerifyRetryToken decrypts a Retry token, enforces the client-IP binding and
// lifetime, and consumes its nonce in the replay table. A token can verify
// successfully at most once; every later presentation returns ErrTokenReplay.
func (g *Guard) VerifyRetryToken(token []byte, clientIP netip.Addr) (*RetryTokenClaims, error) {
	if len(token) <= retryNonceSize {
		return nil, ErrTokenAEAD
	}
	nonce := token[:retryNonceSize]
	sealed := token[retryNonceSize:]
	plaintext, err := g.tokenAEAD.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrTokenAEAD
	}
	claims, err := decodeTokenPlaintext(plaintext)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTokenAEAD, err)
	}

	now := g.now()
	if now.After(claims.IssuedAt.Add(g.config.TokenTTL)) ||
		now.Add(maxClockSkew).Before(claims.IssuedAt) {
		return nil, ErrTokenExpired
	}
	if claims.ClientIP.Compare(clientIP) != 0 {
		return nil, ErrTokenWrongIP
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.isReplayLocked(nonce, claims.IssuedAt) {
		return nil, ErrTokenReplay
	}
	g.rememberNonceLocked(nonce, claims.IssuedAt)
	return claims, nil
}

func replayKey(nonce []byte) string {
	return string(nonce)
}

// isReplayLocked reports whether the nonce was previously consumed. Expired
// entries are swept opportunistically so the table cannot grow without bound.
func (g *Guard) isReplayLocked(nonce []byte, issuedAt time.Time) bool {
	nowNanos := g.now().UnixNano()
	for key, expiry := range g.replay {
		if nowNanos > expiry {
			delete(g.replay, key)
		}
	}
	expiry, ok := g.replay[replayKey(nonce)]
	return ok && nowNanos <= expiry
}

func (g *Guard) rememberNonceLocked(nonce []byte, issuedAt time.Time) {
	key := replayKey(nonce)
	g.replay[key] = issuedAt.Add(g.config.TokenTTL).UnixNano()
	if len(g.replay) <= g.config.MaxReplayEntries {
		return
	}
	// Memory bound: drop an unrelated, still-expired entry if the sweep left
	// the table over capacity. The freshly inserted nonce is never evicted.
	fresh := key
	for key, expiry := range g.replay {
		if key == fresh {
			continue
		}
		if g.now().UnixNano() > expiry {
			delete(g.replay, key)
			return
		}
	}
}

// retryAAD builds the pseudo packet used as Retry integrity AAD per RFC 9001
// section 5.8: a zero-length ODCID length prefix, the original DCID itself,
// followed by the Retry packet up to but excluding its integrity tag.
func retryAAD(originalDCID, retryPacketWithoutTag []byte) []byte {
	aad := make([]byte, 0, 1+len(originalDCID)+len(retryPacketWithoutTag))
	aad = append(aad, byte(len(originalDCID)))
	aad = append(aad, originalDCID...)
	aad = append(aad, retryPacketWithoutTag...)
	return aad
}

func (g *Guard) retryIntegrityAEAD(retrySCID []byte) (cipher.AEAD, []byte, error) {
	initialSecret := hkdfExtract(retryIntegritySaltV1[:], retrySCID)
	key := hkdfExpandLabel(initialSecret, "quic key", 16)
	iv := hkdfExpandLabel(initialSecret, "quic iv", 12)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	return aead, iv, nil
}

// BuildRetryPacket assembles a QUIC v1 Retry packet and appends its RFC 9001
// section 5.8 integrity tag. clientSCID is the Source CID of the client's
// first Initial and becomes the Retry packet's DCID; retrySCID is the new
// server CID. originalDCID is the DCID of the client's first Initial.
func (g *Guard) BuildRetryPacket(originalDCID, clientSCID, retrySCID, token []byte) ([]byte, error) {
	if len(originalDCID) > maxConnectionIDLen || len(clientSCID) > maxConnectionIDLen ||
		len(retrySCID) > maxConnectionIDLen {
		return nil, ErrConnectionIDTooLong
	}
	header := make([]byte, 0, 5+1+len(clientSCID)+1+len(retrySCID)+len(token))
	header = append(header, 0xf0) // long header, fixed bit, type Retry
	header = append(header, byte(Version1>>24), byte(Version1>>16), byte(Version1>>8), byte(Version1))
	header = append(header, byte(len(clientSCID)))
	header = append(header, clientSCID...)
	header = append(header, byte(len(retrySCID)))
	header = append(header, retrySCID...)
	header = append(header, token...)

	aead, iv, err := g.retryIntegrityAEAD(retrySCID)
	if err != nil {
		return nil, err
	}
	tag := aead.Seal(nil, iv, nil, retryAAD(originalDCID, header))
	return append(header, tag...), nil
}

// VerifyRetryPacket validates the 16-byte Retry integrity tag of a received
// Retry packet against the expected original DCID and returns its parsed
// header. Any bit flip or DCID mismatch yields ErrRetryIntegrity.
func (g *Guard) VerifyRetryPacket(packet, originalDCID []byte) (*Header, error) {
	h, err := ParsePacket(packet, 0)
	if err != nil {
		return nil, err
	}
	if h.Type != TypeRetry {
		return nil, fmt.Errorf("%w: not a Retry packet", ErrMalformed)
	}
	if len(packet) < 16 {
		return nil, ErrRetryIntegrity
	}
	gotTag := packet[len(packet)-16:]
	headerPart := packet[:len(packet)-16]

	aead, iv, err := g.retryIntegrityAEAD(h.SCID)
	if err != nil {
		return nil, err
	}
	expectedTag := aead.Seal(nil, iv, nil, retryAAD(originalDCID, headerPart))
	if subtle.ConstantTimeCompare(gotTag, expectedTag) != 1 {
		return nil, ErrRetryIntegrity
	}
	return h, nil
}
