package netsec

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ticketFormatVersion = 1
	ticketNonceSize     = 12
	ticketHeaderSize    = 4 + 1 + 8 + ticketIDSize + ticketNonceSize
	ticketMaxBytes      = 64 * 1024
	ticketTagSize       = 16
)

var ticketMagic = [4]byte{'E', 'T', 'K', '1'}

// Ticket is the opaque AEAD-sealed authorization token handed to the wireless
// controller. Only the AAA service can open it.
type Ticket struct {
	Raw       []byte
	ID        TicketID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Bytes returns a copy of the opaque ticket bytes.
func (t Ticket) Bytes() []byte {
	return append([]byte(nil), t.Raw...)
}

type ticketPayload struct {
	FormatVersion int       `json:"v"`
	TicketID      string    `json:"id"`
	KeyGeneration uint64    `json:"key_generation"`
	IssuedAt      time.Time `json:"issued_at"`
	ExpiresAt     time.Time `json:"expires_at"`

	OuterEAPIdentity      string `json:"outer_eap_identity"`
	InnerIdentity         string `json:"inner_identity"`
	CertificateIdentity   string `json:"certificate_identity"`
	CertificateSubject    string `json:"certificate_subject"`
	ServerVirtualIdentity string `json:"server_virtual_identity"`

	AccessPoint       AccessPoint `json:"access_point"`
	Policy            Policy      `json:"policy"`
	PolicyFingerprint string      `json:"policy_fingerprint"`

	ClientLeafFingerprint    string `json:"client_leaf_fingerprint"`
	ClientChainFingerprint   string `json:"client_chain_fingerprint"`
	CertValidationGeneration uint64 `json:"cert_validation_generation"`
	RevocationEpoch          uint64 `json:"revocation_epoch"`

	TLSVersion         uint16 `json:"tls_version"`
	TLSCipherSuite     uint16 `json:"tls_cipher_suite"`
	MSKIdentity        []byte `json:"msk_identity"`
	ChannelBindingType string `json:"channel_binding_type"`
	ChannelBinding     []byte `json:"channel_binding"`

	Grant Grant `json:"grant"`
}

func sealTicket(payload ticketPayload, keyring *Keyring, random io.Reader) (Ticket, error) {
	if keyring == nil {
		return Ticket{}, fmt.Errorf("%w: nil keyring", ErrInvalidArgument)
	}
	if random == nil {
		random = rand.Reader
	}

	var id TicketID
	if _, err := io.ReadFull(random, id[:]); err != nil {
		return Ticket{}, fmt.Errorf("netsec: generate ticket ID: %w", err)
	}
	if id.isZero() {
		return Ticket{}, fmt.Errorf("%w: generated zero ticket ID", ErrInvalidArgument)
	}

	generation, master := keyring.snapshot()
	payload.FormatVersion = ticketFormatVersion
	payload.TicketID = id.String()
	payload.KeyGeneration = generation
	payload.IssuedAt = cloneTime(payload.IssuedAt)
	payload.ExpiresAt = cloneTime(payload.ExpiresAt)
	if err := validateTicketPayload(payload, payload.IssuedAt, 0); err != nil {
		return Ticket{}, fmt.Errorf("netsec: refusing to seal invalid ticket: %w", err)
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return Ticket{}, fmt.Errorf("netsec: marshal ticket: %w", err)
	}

	var nonce [ticketNonceSize]byte
	if _, err := io.ReadFull(random, nonce[:]); err != nil {
		return Ticket{}, fmt.Errorf("netsec: generate ticket nonce: %w", err)
	}
	aead, err := ticketAEAD(master, generation, id, nonce)
	if err != nil {
		return Ticket{}, err
	}

	var header [ticketHeaderSize]byte
	copy(header[0:4], ticketMagic[:])
	header[4] = ticketFormatVersion
	binary.BigEndian.PutUint64(header[5:13], generation)
	copy(header[13:29], id[:])
	copy(header[29:41], nonce[:])

	sealed := aead.Seal(nil, nonce[:], plaintext, ticketAAD(generation, id))
	raw := make([]byte, 0, len(header)+len(sealed))
	raw = append(raw, header[:]...)
	raw = append(raw, sealed...)
	if len(raw) > ticketMaxBytes {
		return Ticket{}, fmt.Errorf("%w: sealed ticket exceeds maximum size", ErrInvalidArgument)
	}

	return Ticket{
		Raw:       raw,
		ID:        id,
		IssuedAt:  payload.IssuedAt,
		ExpiresAt: payload.ExpiresAt,
	}, nil
}

func openTicket(raw []byte, keyring *Keyring, now time.Time, maxLifetime time.Duration) (ticketPayload, error) {
	var zero ticketPayload
	if keyring == nil {
		return zero, fmt.Errorf("%w: nil keyring", ErrInvalidArgument)
	}
	if len(raw) < ticketHeaderSize+ticketTagSize {
		return zero, fmt.Errorf("%w: ticket is too short", ErrTicketInvalid)
	}
	if len(raw) > ticketMaxBytes {
		return zero, fmt.Errorf("%w: ticket is too large", ErrTicketInvalid)
	}
	if !bytes.Equal(raw[0:4], ticketMagic[:]) {
		return zero, fmt.Errorf("%w: bad ticket magic", ErrTicketInvalid)
	}
	if raw[4] != ticketFormatVersion {
		return zero, fmt.Errorf("%w: unsupported ticket version %d", ErrTicketInvalid, raw[4])
	}

	generation := binary.BigEndian.Uint64(raw[5:13])
	var id TicketID
	copy(id[:], raw[13:29])
	if id.isZero() {
		return zero, fmt.Errorf("%w: zero ticket ID", ErrTicketInvalid)
	}
	var nonce [ticketNonceSize]byte
	copy(nonce[:], raw[29:41])

	activeGeneration, master := keyring.snapshot()
	if generation != activeGeneration {
		return zero, fmt.Errorf("%w: ticket generation %d, active generation %d", ErrTicketKeyRotated, generation, activeGeneration)
	}

	aead, err := ticketAEAD(master, generation, id, nonce)
	if err != nil {
		return zero, err
	}
	plaintext, err := aead.Open(nil, nonce[:], raw[ticketHeaderSize:], ticketAAD(generation, id))
	if err != nil {
		return zero, fmt.Errorf("%w: authentication failed", ErrTicketInvalid)
	}

	var payload ticketPayload
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return zero, fmt.Errorf("%w: decode ticket payload: %v", ErrTicketInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return zero, err
	}
	if payload.FormatVersion != ticketFormatVersion {
		return zero, fmt.Errorf("%w: payload version mismatch", ErrTicketInvalid)
	}
	if payload.KeyGeneration != generation {
		return zero, fmt.Errorf("%w: payload key generation mismatch", ErrTicketInvalid)
	}
	payloadID, err := parseTicketID(payload.TicketID)
	if err != nil || payloadID != id {
		return zero, fmt.Errorf("%w: payload ticket ID mismatch", ErrTicketInvalid)
	}

	if err := validateTicketPayload(payload, now, maxLifetime); err != nil {
		return zero, err
	}
	return payload, nil
}

func validateTicketPayload(payload ticketPayload, now time.Time, maxLifetime time.Duration) error {
	if payload.IssuedAt.IsZero() || payload.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: missing ticket timestamps", ErrTicketInvalid)
	}
	if !payload.ExpiresAt.After(payload.IssuedAt) {
		return fmt.Errorf("%w: invalid ticket lifetime", ErrTicketInvalid)
	}
	if maxLifetime > 0 && payload.ExpiresAt.Sub(payload.IssuedAt) > maxLifetime {
		return fmt.Errorf("%w: ticket lifetime exceeds configured maximum", ErrTicketInvalid)
	}
	if now.Before(payload.IssuedAt) {
		return fmt.Errorf("%w: ticket is not yet valid", ErrTicketInvalid)
	}
	if !now.Before(payload.ExpiresAt) {
		return ErrTicketExpired
	}

	payload.Policy = payload.Policy.normalized()
	if err := payload.Policy.validate(); err != nil {
		return err
	}
	if payload.PolicyFingerprint != policyFingerprint(payload.Policy) {
		return fmt.Errorf("%w: policy fingerprint mismatch", ErrTicketInvalid)
	}
	if err := payload.AccessPoint.validate(); err != nil {
		return err
	}
	if payload.AccessPoint.DomainID != payload.Policy.DomainID || payload.AccessPoint.SSID != payload.Policy.SSID {
		return fmt.Errorf("%w: ticket access point is outside its policy domain", ErrTicketInvalid)
	}
	if !payload.Policy.permitsAP(payload.AccessPoint) {
		return fmt.Errorf("%w: ticket access point is not permitted by its policy", ErrTicketInvalid)
	}

	if payload.OuterEAPIdentity == "" {
		return fmt.Errorf("%w: missing outer EAP identity", ErrTicketInvalid)
	}
	if payload.InnerIdentity == "" || IsAnonymousIdentity(payload.InnerIdentity) {
		return fmt.Errorf("%w: invalid inner identity", ErrTicketInvalid)
	}
	if payload.CertificateIdentity == "" || IsAnonymousIdentity(payload.CertificateIdentity) {
		return fmt.Errorf("%w: invalid certificate identity", ErrTicketInvalid)
	}
	if payload.InnerIdentity != payload.CertificateIdentity {
		return fmt.Errorf("%w: inner identity is not the certificate subject", ErrTicketInvalid)
	}
	if payload.ServerVirtualIdentity == "" || !payload.Policy.permitsServer(payload.ServerVirtualIdentity) {
		return fmt.Errorf("%w: invalid server virtual identity", ErrTicketInvalid)
	}
	if !validHexDigest(payload.ClientLeafFingerprint) || !validHexDigest(payload.ClientChainFingerprint) {
		return fmt.Errorf("%w: invalid certificate fingerprint", ErrTicketInvalid)
	}

	if !payload.Policy.permitsTLS(payload.TLSVersion, payload.TLSCipherSuite) {
		return fmt.Errorf("%w: ticket TLS parameters are not permitted by policy", ErrTicketInvalid)
	}
	if !validBindingValue(payload.MSKIdentity) {
		return fmt.Errorf("%w: invalid MSK identity", ErrTicketInvalid)
	}
	if strings.TrimSpace(payload.ChannelBindingType) == "" || !validBindingValue(payload.ChannelBinding) {
		return fmt.Errorf("%w: invalid issued channel binding", ErrTicketInvalid)
	}

	grant, err := payload.Grant.normalized()
	if err != nil {
		return err
	}
	payload.Grant = grant
	if payload.Grant.DomainID != payload.Policy.DomainID {
		return fmt.Errorf("%w: grant domain mismatch", ErrTicketInvalid)
	}
	if payload.Grant.Principal != payload.CertificateIdentity {
		return fmt.Errorf("%w: grant principal is not the certificate subject", ErrTicketInvalid)
	}
	if !payload.Policy.permitsGrant(payload.Grant) {
		return fmt.Errorf("%w: frozen grant is not permitted by policy", ErrTicketInvalid)
	}
	return nil
}

func ticketAEAD(master [ticketKeySize]byte, generation uint64, id TicketID, nonce [ticketNonceSize]byte) (cipher.AEAD, error) {
	info := fmt.Sprintf("eap-tls-resume-ticket/v1|generation=%d|id=%s", generation, id.String())
	key, err := hkdf.Key(sha256.New, master[:], nonce[:], info, ticketKeySize)
	if err != nil {
		return nil, fmt.Errorf("netsec: derive ticket key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("netsec: create ticket cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create ticket AEAD: %w", err)
	}
	return aead, nil
}

func ticketAAD(generation uint64, id TicketID) []byte {
	out := make([]byte, 0, 4+1+8+ticketIDSize)
	out = append(out, ticketMagic[:]...)
	out = append(out, ticketFormatVersion)
	var gen [8]byte
	binary.BigEndian.PutUint64(gen[:], generation)
	out = append(out, gen[:]...)
	out = append(out, id[:]...)
	return out
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("%w: trailing ticket data: %v", ErrTicketInvalid, err)
	}
	return fmt.Errorf("%w: trailing ticket data", ErrTicketInvalid)
}

func validHexDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validBindingValue(v []byte) bool {
	if len(v) == 0 || len(v) > 128 {
		return false
	}
	for _, b := range v {
		if b != 0 {
			return true
		}
	}
	return false
}
