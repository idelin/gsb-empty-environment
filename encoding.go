package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Domain-separated prefixes make signatures and hashes computed for different
// message classes non-interchangeable.
const (
	authCanonicalPrefix    = "OFFLINEAUTH-AUTH-V1\x00"
	receiptCanonicalPrefix = "OFFLINEAUTH-RECEIPT-V1\x00"
	genesisChainPrefix     = "OFFLINEAUTH-GENESIS-V1\x00"
)

// Wire format magic values distinguish token and receipt envelopes.
const (
	tokenWireMagic   = "OA-TKN1"
	receiptWireMagic = "OA-RCP1"
)

// putLenString writes a length-prefixed UTF-8 byte string.
func putLenString(buf []byte, pos int, value string) (int, error) {
	n := len(value)
	if n > 0xffffffff {
		return 0, errors.New("offlineauth: field too long")
	}
	if pos+4+n > len(buf) {
		return 0, errors.New("offlineauth: encoding buffer overflow")
	}
	binary.BigEndian.PutUint32(buf[pos:pos+4], uint32(n))
	copy(buf[pos+4:pos+4+n], value)
	return pos + 4 + n, nil
}

// getLenString reads a length-prefixed UTF-8 byte string.
func getLenString(buf []byte, pos int) (string, int, error) {
	if pos+4 > len(buf) {
		return "", 0, ErrInvalidToken
	}
	n := int(binary.BigEndian.Uint32(buf[pos : pos+4]))
	pos += 4
	if n < 0 || pos+n > len(buf) {
		return "", 0, ErrInvalidToken
	}
	return string(buf[pos : pos+n]), pos + n, nil
}

func putInt64(buf []byte, pos int, value int64) (int, error) {
	if pos+8 > len(buf) {
		return 0, errors.New("offlineauth: encoding buffer overflow")
	}
	binary.BigEndian.PutUint64(buf[pos:pos+8], uint64(value))
	return pos + 8, nil
}

func getInt64(buf []byte, pos int) (int64, int, error) {
	if pos+8 > len(buf) {
		return 0, 0, ErrInvalidToken
	}
	value := int64(binary.BigEndian.Uint64(buf[pos : pos+8]))
	return value, pos + 8, nil
}

func putUint64(buf []byte, pos int, value uint64) (int, error) {
	if pos+8 > len(buf) {
		return 0, errors.New("offlineauth: encoding buffer overflow")
	}
	binary.BigEndian.PutUint64(buf[pos:pos+8], value)
	return pos + 8, nil
}

func getUint64(buf []byte, pos int) (uint64, int, error) {
	if pos+8 > len(buf) {
		return 0, 0, ErrInvalidToken
	}
	return binary.BigEndian.Uint64(buf[pos : pos+8]), pos + 8, nil
}

func putBytes(buf []byte, pos int, value []byte) (int, error) {
	n := len(value)
	if n > 0xffffffff {
		return 0, errors.New("offlineauth: field too long")
	}
	if pos+4+n > len(buf) {
		return 0, errors.New("offlineauth: encoding buffer overflow")
	}
	binary.BigEndian.PutUint32(buf[pos:pos+4], uint32(n))
	copy(buf[pos+4:pos+4+n], value)
	return pos + 4 + n, nil
}

func getBytes(buf []byte, pos int) ([]byte, int, error) {
	if pos+4 > len(buf) {
		return nil, 0, ErrInvalidToken
	}
	n := int(binary.BigEndian.Uint32(buf[pos : pos+4]))
	pos += 4
	if n < 0 || pos+n > len(buf) {
		return nil, 0, ErrInvalidToken
	}
	out := make([]byte, n)
	copy(out, buf[pos:pos+n])
	return out, pos + n, nil
}

// canonicalSize reports the encoded length of the fixed authorization layout.
func authCanonicalSize(a Authorization) int {
	size := len(authCanonicalPrefix)
	for _, field := range []string{a.AuthID, a.StationID, a.ConnectorID, a.AccountID} {
		size += 4 + len(field)
	}
	return size + 3*8 // Quota, ValidFrom, ValidUntil
}

// canonicalAuth produces the deterministic byte representation that is signed
// by the backend and digested for local storage.
func canonicalAuth(a Authorization) []byte {
	buf := make([]byte, authCanonicalSize(a))
	pos := copy(buf, authCanonicalPrefix)
	pos, _ = putLenString(buf, pos, a.AuthID)
	pos, _ = putLenString(buf, pos, a.StationID)
	pos, _ = putLenString(buf, pos, a.ConnectorID)
	pos, _ = putLenString(buf, pos, a.AccountID)
	pos, _ = putInt64(buf, pos, a.Quota)
	pos, _ = putInt64(buf, pos, a.ValidFrom)
	pos, _ = putInt64(buf, pos, a.ValidUntil)
	return buf
}

func receiptCanonicalSize(r Receipt) int {
	size := len(receiptCanonicalPrefix)
	for _, field := range []string{r.AuthID, r.Digest, r.StationID, r.ConnectorID, r.AccountID} {
		size += 4 + len(field)
	}
	size += 3 * 8 // Amount, ConsumedAt, Seq
	return size + 4 + len(r.PrevHash)
}

// canonicalReceipt produces the deterministic byte representation signed by
// the station and verified by the backend.
func canonicalReceipt(r Receipt) []byte {
	buf := make([]byte, receiptCanonicalSize(r))
	pos := copy(buf, receiptCanonicalPrefix)
	pos, _ = putLenString(buf, pos, r.AuthID)
	pos, _ = putLenString(buf, pos, r.Digest)
	pos, _ = putLenString(buf, pos, r.StationID)
	pos, _ = putLenString(buf, pos, r.ConnectorID)
	pos, _ = putLenString(buf, pos, r.AccountID)
	pos, _ = putInt64(buf, pos, r.Amount)
	pos, _ = putInt64(buf, pos, r.ConsumedAt)
	pos, _ = putUint64(buf, pos, r.Seq)
	pos, _ = putBytes(buf, pos, r.PrevHash)
	return buf
}

// AuthDigest returns the hex-encoded SHA-256 digest of the canonical
// authorization. This is the only per-authorization identifier persisted on
// the station alongside its remaining-quota counter.
func AuthDigest(a Authorization) string {
	sum := sha256.Sum256(canonicalAuth(a))
	return hexEncode(sum[:])
}

// ReceiptHead returns the hash-chain head produced by chaining a receipt onto
// PrevHash. The head commits to the receipt's full signed content.
func ReceiptHead(r Receipt) []byte {
	h := sha256.New()
	h.Write(r.PrevHash)
	h.Write(canonicalReceipt(r))
	sum := h.Sum(nil)
	out := make([]byte, len(sum))
	copy(out, sum)
	return out
}

// GenesisHead derives the initial hash-chain head for a station so every
// chain starts from a station-specific, non-reusable value.
func GenesisHead(stationID string) []byte {
	h := sha256.New()
	h.Write([]byte(genesisChainPrefix))
	h.Write([]byte(stationID))
	sum := h.Sum(nil)
	out := make([]byte, len(sum))
	copy(out, sum)
	return out
}

// EncodeToken serializes a signed authorization token for transport.
func EncodeToken(t SignedToken) ([]byte, error) {
	if len(t.Signature) != ed25519.SignatureSize {
		return nil, ErrInvalidKey
	}
	body := canonicalAuth(t.Auth)
	buf := make([]byte, len(tokenWireMagic)+4+len(body)+4+len(t.Signature))
	pos := copy(buf, tokenWireMagic)
	pos, _ = putBytes(buf, pos, body)
	pos, _ = putBytes(buf, pos, t.Signature)
	return buf, nil
}

// DecodeToken parses a signed authorization token without verifying it.
func DecodeToken(wire []byte) (SignedToken, error) {
	if len(wire) < len(tokenWireMagic) || string(wire[:len(tokenWireMagic)]) != tokenWireMagic {
		return SignedToken{}, ErrInvalidToken
	}
	pos := len(tokenWireMagic)
	body, pos, err := getBytes(wire, pos)
	if err != nil {
		return SignedToken{}, err
	}
	sig, pos, err := getBytes(wire, pos)
	if err != nil {
		return SignedToken{}, err
	}
	if len(sig) != ed25519.SignatureSize {
		return SignedToken{}, ErrBadSignature
	}
	if pos != len(wire) {
		return SignedToken{}, ErrInvalidToken
	}
	auth, err := decodeCanonicalAuth(body)
	if err != nil {
		return SignedToken{}, err
	}
	return SignedToken{Auth: auth, Signature: sig}, nil
}

func decodeCanonicalAuth(body []byte) (Authorization, error) {
	if len(body) < len(authCanonicalPrefix) ||
		string(body[:len(authCanonicalPrefix)]) != authCanonicalPrefix {
		return Authorization{}, ErrInvalidToken
	}
	pos := len(authCanonicalPrefix)
	var a Authorization
	var err error
	if a.AuthID, pos, err = getLenString(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.StationID, pos, err = getLenString(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.ConnectorID, pos, err = getLenString(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.AccountID, pos, err = getLenString(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.Quota, pos, err = getInt64(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.ValidFrom, pos, err = getInt64(body, pos); err != nil {
		return Authorization{}, err
	}
	if a.ValidUntil, pos, err = getInt64(body, pos); err != nil {
		return Authorization{}, err
	}
	if pos != len(body) {
		return Authorization{}, ErrInvalidToken
	}
	return a, nil
}

// EncodeReceipt serializes a signed consumption receipt for transport.
func EncodeReceipt(r SignedReceipt) ([]byte, error) {
	if len(r.Signature) != ed25519.SignatureSize {
		return nil, ErrInvalidKey
	}
	body := canonicalReceipt(r.Receipt)
	buf := make([]byte, len(receiptWireMagic)+4+len(body)+4+len(r.Signature))
	pos := copy(buf, receiptWireMagic)
	pos, _ = putBytes(buf, pos, body)
	pos, _ = putBytes(buf, pos, r.Signature)
	return buf, nil
}

// DecodeReceipt parses a signed consumption receipt without verifying it.
func DecodeReceipt(wire []byte) (SignedReceipt, error) {
	if len(wire) < len(receiptWireMagic) || string(wire[:len(receiptWireMagic)]) != receiptWireMagic {
		return SignedReceipt{}, ErrInvalidReceipt
	}
	pos := len(receiptWireMagic)
	body, pos, err := getBytes(wire, pos)
	if err != nil {
		return SignedReceipt{}, ErrInvalidReceipt
	}
	sig, pos, err := getBytes(wire, pos)
	if err != nil {
		return SignedReceipt{}, ErrInvalidReceipt
	}
	if len(sig) != ed25519.SignatureSize {
		return SignedReceipt{}, ErrBadSignature
	}
	if pos != len(wire) {
		return SignedReceipt{}, ErrInvalidReceipt
	}
	rec, err := decodeCanonicalReceipt(body)
	if err != nil {
		return SignedReceipt{}, err
	}
	return SignedReceipt{Receipt: rec, Signature: sig}, nil
}

func decodeCanonicalReceipt(body []byte) (Receipt, error) {
	if len(body) < len(receiptCanonicalPrefix) ||
		string(body[:len(receiptCanonicalPrefix)]) != receiptCanonicalPrefix {
		return Receipt{}, ErrInvalidReceipt
	}
	pos := len(receiptCanonicalPrefix)
	var r Receipt
	var err error
	if r.AuthID, pos, err = getLenString(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.Digest, pos, err = getLenString(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.StationID, pos, err = getLenString(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.ConnectorID, pos, err = getLenString(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.AccountID, pos, err = getLenString(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.Amount, pos, err = getInt64(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.ConsumedAt, pos, err = getInt64(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.Seq, pos, err = getUint64(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if r.PrevHash, pos, err = getBytes(body, pos); err != nil {
		return Receipt{}, ErrInvalidReceipt
	}
	if pos != len(body) {
		return Receipt{}, ErrInvalidReceipt
	}
	return r, nil
}

func hexEncode(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[2*i] = digits[b>>4]
		out[2*i+1] = digits[b&0x0f]
	}
	return string(out)
}
