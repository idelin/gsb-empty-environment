package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	// TicketVersion is the only ticket wire format understood by this package.
	TicketVersion byte = 1

	ticketHeaderSize = 6
	ticketMACSize    = 32
	maxTicketSize    = 1 << 20
	minSecretSize    = 32
)

var ticketDomain = []byte("arrow-flight-do-get-ticket\x00")

var (
	ErrMalformed          = errors.New("netsec: malformed flight ticket")
	ErrTruncated          = errors.New("netsec: truncated flight ticket")
	ErrTicketTooLarge     = errors.New("netsec: flight ticket is too large")
	ErrUnsupportedTicket  = errors.New("netsec: unsupported flight ticket version")
	ErrUnknownKey         = errors.New("netsec: unknown flight ticket key")
	ErrBadMAC             = errors.New("netsec: invalid flight ticket MAC")
	ErrExpired            = errors.New("netsec: flight ticket expired")
	ErrPrincipalMismatch  = errors.New("netsec: flight ticket principal mismatch")
	ErrDescriptorMismatch = errors.New(
		"netsec: flight ticket descriptor mismatch",
	)
	ErrEndpointMismatch  = errors.New("netsec: flight ticket endpoint mismatch")
	ErrPartitionMismatch = errors.New("netsec: flight ticket partition mismatch")
	ErrInvalidKey        = errors.New("netsec: invalid flight ticket key")
	ErrInvalidBinding    = errors.New("netsec: invalid flight ticket binding")
)

// DescriptorType identifies the kind of Arrow Flight descriptor.
type DescriptorType byte

const (
	DescriptorUnknown DescriptorType = 0
	DescriptorPath    DescriptorType = 1
	DescriptorCommand DescriptorType = 2
)

// Descriptor is the flight descriptor the ticket authorizes. It mirrors the
// Arrow Flight descriptor fields so GetFlightInfo and DoGet can bind their
// request to the ticket without importing the Arrow SDK.
type Descriptor struct {
	Type    DescriptorType
	Path    []string
	Command []byte
}

// Binding is the complete authorization scope of a ticket. Every field is
// authenticated by the MAC and must match the request that consumes the
// ticket.
type Binding struct {
	Principal  string
	Descriptor Descriptor
	Endpoint   string
	Partition  int64
}

// Claims is the authenticated content of a ticket.
type Claims struct {
	Principal  string
	Descriptor Descriptor
	Endpoint   string
	ExpiresAt  time.Time
	Partition  int64
	KeyID      byte
}

// Binding returns the authorization scope carried by the claims.
func (c Claims) Binding() Binding {
	return Binding{
		Principal:  c.Principal,
		Descriptor: cloneDescriptor(c.Descriptor),
		Endpoint:   c.Endpoint,
		Partition:  c.Partition,
	}
}

// Key is an HMAC key with an identifier written into each ticket. During a
// rotation, the current key is used for signing and the previous key remains
// accepted for verification.
type Key struct {
	ID     byte
	Secret []byte
}

// Keyring holds the signing key followed by accepted older keys.
type Keyring struct {
	keys []Key
}

// NewKeyring creates a keyring from the current signing key and zero or more
// previous keys. Key identifiers must be unique and secrets must contain at
// least 32 bytes.
func NewKeyring(current Key, previous ...Key) (*Keyring, error) {
	keys := make([]Key, 0, 1+len(previous))
	seen := make(map[byte]struct{}, 1+len(previous))

	for _, key := range append([]Key{current}, previous...) {
		if len(key.Secret) < minSecretSize {
			return nil, fmt.Errorf(
				"%w: key %d secret must be at least %d bytes",
				ErrInvalidKey, key.ID, minSecretSize,
			)
		}
		if _, ok := seen[key.ID]; ok {
			return nil, fmt.Errorf("%w: duplicate key id %d", ErrInvalidKey, key.ID)
		}
		seen[key.ID] = struct{}{}
		keys = append(keys, Key{ID: key.ID, Secret: bytes.Clone(key.Secret)})
	}

	return &Keyring{keys: keys}, nil
}

// NewRandomKey creates a 256-bit HMAC key with the supplied identifier.
func NewRandomKey(id byte) (Key, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return Key{}, fmt.Errorf("%w: generate key: %v", ErrInvalidKey, err)
	}
	return Key{ID: id, Secret: secret}, nil
}

// Issue creates a ticket for the binding using the current signing key.
func (k *Keyring) Issue(binding Binding, expiresAt time.Time) ([]byte, error) {
	if k == nil || len(k.keys) == 0 {
		return nil, fmt.Errorf("%w: empty keyring", ErrInvalidKey)
	}
	if err := validateBinding(binding); err != nil {
		return nil, err
	}
	if expiresAt.IsZero() {
		return nil, fmt.Errorf("%w: zero expiration", ErrInvalidBinding)
	}
	if len(binding.Principal) > math.MaxUint32 || len(binding.Endpoint) > math.MaxUint32 {
		return nil, ErrTicketTooLarge
	}

	descriptor, err := encodeDescriptor(binding.Descriptor)
	if err != nil {
		return nil, err
	}

	payload := make([]byte, 0, 128+len(binding.Principal)+len(binding.Endpoint)+len(descriptor))
	payload = appendSizedBytes(payload, []byte(binding.Principal))
	payload = appendSizedBytes(payload, descriptor)
	payload = appendSizedBytes(payload, []byte(binding.Endpoint))
	payload = appendInt64(payload, expiresAt.UnixNano())
	payload = appendInt64(payload, binding.Partition)

	if len(payload) > maxTicketSize-ticketHeaderSize-ticketMACSize {
		return nil, ErrTicketTooLarge
	}

	key := k.keys[0]
	header := make([]byte, ticketHeaderSize)
	header[0] = TicketVersion
	header[1] = key.ID
	binary.BigEndian.PutUint32(header[2:6], uint32(len(payload)))

	raw := make([]byte, ticketHeaderSize+len(payload)+ticketMACSize)
	copy(raw, header)
	copy(raw[ticketHeaderSize:], payload)
	tag := sign(key.Secret, header, payload)
	copy(raw[ticketHeaderSize+len(payload):], tag)

	return raw, nil
}

// Verify authenticates a ticket as of the current time and checks that its
// complete binding matches expected.
func (k *Keyring) Verify(raw []byte, expected Binding) (Claims, error) {
	return k.VerifyAt(raw, expected, time.Now())
}

// VerifyAt is Verify with an explicit clock, which is useful for services that
// share a clock with their ticket issuer and for deterministic tests.
func (k *Keyring) VerifyAt(raw []byte, expected Binding, now time.Time) (Claims, error) {
	if err := validateBinding(expected); err != nil {
		return Claims{}, err
	}

	claims, err := k.verifyContextAt(raw, expected.Principal, expected.Endpoint, now)
	if err != nil {
		return Claims{}, err
	}

	if !descriptorEqual(claims.Descriptor, expected.Descriptor) {
		return Claims{}, ErrDescriptorMismatch
	}
	if claims.Partition != expected.Partition {
		return Claims{}, ErrPartitionMismatch
	}

	return claims, nil
}

// VerifyRequest authenticates a ticket for a DoGet request, where the request
// carries only the ticket. It checks the authenticated principal, current
// endpoint, MAC, and expiration, then returns the ticket's authenticated
// descriptor and partition for the service to use.
func (k *Keyring) VerifyRequest(raw []byte, principal, endpoint string) (Claims, error) {
	return k.VerifyRequestAt(raw, principal, endpoint, time.Now())
}

// VerifyRequestAt is VerifyRequest with an explicit clock.
func (k *Keyring) VerifyRequestAt(
	raw []byte,
	principal string,
	endpoint string,
	now time.Time,
) (Claims, error) {
	return k.verifyContextAt(raw, principal, endpoint, now)
}

func (k *Keyring) verifyContextAt(
	raw []byte,
	principal string,
	endpoint string,
	now time.Time,
) (Claims, error) {
	if principal == "" || endpoint == "" {
		return Claims{}, fmt.Errorf("%w: empty request context", ErrInvalidBinding)
	}

	claims, err := k.openAt(raw, now)
	if err != nil {
		return Claims{}, err
	}
	if !constantTimeStringEqual(claims.Principal, principal) {
		return Claims{}, ErrPrincipalMismatch
	}
	if !constantTimeStringEqual(claims.Endpoint, endpoint) {
		return Claims{}, ErrEndpointMismatch
	}
	return claims, nil
}

func (k *Keyring) openAt(raw []byte, now time.Time) (Claims, error) {
	if k == nil || len(k.keys) == 0 {
		return Claims{}, fmt.Errorf("%w: empty keyring", ErrInvalidKey)
	}
	if len(raw) < ticketHeaderSize+ticketMACSize {
		return Claims{}, ErrTruncated
	}
	if len(raw) > maxTicketSize {
		return Claims{}, ErrTicketTooLarge
	}

	version := raw[0]
	if version != TicketVersion {
		return Claims{}, fmt.Errorf("%w: %d", ErrUnsupportedTicket, version)
	}

	payloadLen := binary.BigEndian.Uint32(raw[2:6])
	if payloadLen > maxTicketSize-ticketHeaderSize-ticketMACSize {
		return Claims{}, ErrTicketTooLarge
	}
	wantLen := ticketHeaderSize + int(payloadLen) + ticketMACSize
	if len(raw) < wantLen {
		return Claims{}, ErrTruncated
	}
	if len(raw) > wantLen {
		return Claims{}, ErrMalformed
	}

	payload := raw[ticketHeaderSize : wantLen-ticketMACSize]
	tag := raw[wantLen-ticketMACSize:]
	header := raw[:ticketHeaderSize]

	secret, ok := k.lookup(raw[1])
	if !ok {
		return Claims{}, fmt.Errorf("%w: %d", ErrUnknownKey, raw[1])
	}
	if !hmac.Equal(tag, sign(secret, header, payload)) {
		return Claims{}, ErrBadMAC
	}

	claims, err := decodePayload(payload)
	if err != nil {
		return Claims{}, err
	}
	claims.KeyID = raw[1]
	if !now.Before(claims.ExpiresAt) {
		return Claims{}, fmt.Errorf("%w: expired at %s", ErrExpired, claims.ExpiresAt.Format(time.RFC3339Nano))
	}
	return claims, nil
}

func (k *Keyring) lookup(id byte) ([]byte, bool) {
	for _, key := range k.keys {
		if key.ID == id {
			return key.Secret, true
		}
	}
	return nil, false
}

func validateBinding(binding Binding) error {
	if binding.Principal == "" {
		return fmt.Errorf("%w: empty principal", ErrInvalidBinding)
	}
	if binding.Endpoint == "" {
		return fmt.Errorf("%w: empty endpoint", ErrInvalidBinding)
	}
	if _, err := encodeDescriptor(binding.Descriptor); err != nil {
		return err
	}
	return nil
}

func encodeDescriptor(descriptor Descriptor) ([]byte, error) {
	if descriptor.Type > DescriptorCommand {
		return nil, fmt.Errorf(
			"%w: descriptor type %d", ErrInvalidBinding, descriptor.Type,
		)
	}
	if len(descriptor.Path) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: too many descriptor paths", ErrInvalidBinding)
	}

	out := []byte{byte(descriptor.Type)}
	out = appendUint32(out, uint32(len(descriptor.Path)))
	for _, path := range descriptor.Path {
		if len(path) > math.MaxUint32 {
			return nil, fmt.Errorf("%w: descriptor path is too long", ErrInvalidBinding)
		}
		out = appendSizedBytes(out, []byte(path))
	}
	if len(descriptor.Command) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: descriptor command is too long", ErrInvalidBinding)
	}
	out = appendUint32(out, uint32(len(descriptor.Command)))
	out = append(out, descriptor.Command...)
	return out, nil
}

func decodePayload(payload []byte) (Claims, error) {
	principal, rest, err := readSizedBytes(payload)
	if err != nil {
		return Claims{}, err
	}
	encodedDescriptor, rest, err := readSizedBytes(rest)
	if err != nil {
		return Claims{}, err
	}
	endpoint, rest, err := readSizedBytes(rest)
	if err != nil {
		return Claims{}, err
	}
	if len(rest) != 16 {
		if len(rest) < 16 {
			return Claims{}, ErrTruncated
		}
		return Claims{}, ErrMalformed
	}

	expiresAt := time.Unix(0, int64(binary.BigEndian.Uint64(rest[0:8]))).UTC()
	partition := int64(binary.BigEndian.Uint64(rest[8:16]))
	descriptor, err := decodeDescriptor(encodedDescriptor)
	if err != nil {
		return Claims{}, err
	}
	if string(principal) == "" || string(endpoint) == "" {
		return Claims{}, fmt.Errorf("%w: empty authenticated field", ErrMalformed)
	}

	return Claims{
		Principal:  string(principal),
		Descriptor: descriptor,
		Endpoint:   string(endpoint),
		ExpiresAt:  expiresAt,
		Partition:  partition,
	}, nil
}

func decodeDescriptor(encoded []byte) (Descriptor, error) {
	if len(encoded) < 1 {
		return Descriptor{}, ErrTruncated
	}
	typ := DescriptorType(encoded[0])
	if typ > DescriptorCommand {
		return Descriptor{}, fmt.Errorf("%w: descriptor type %d", ErrMalformed, typ)
	}
	rest := encoded[1:]

	countBytes, rest, err := readUint32(rest)
	if err != nil {
		return Descriptor{}, err
	}
	count := countBytes
	if uint64(count) > uint64(len(rest)) {
		return Descriptor{}, ErrTruncated
	}
	paths := make([]string, 0, int(count))
	for i := uint32(0); i < count; i++ {
		var path []byte
		path, rest, err = readSizedBytes(rest)
		if err != nil {
			return Descriptor{}, err
		}
		paths = append(paths, string(path))
	}
	command, rest, err := readSizedBytes(rest)
	if err != nil {
		return Descriptor{}, err
	}
	if len(rest) != 0 {
		return Descriptor{}, ErrMalformed
	}

	return Descriptor{
		Type:    typ,
		Path:    paths,
		Command: bytes.Clone(command),
	}, nil
}

func descriptorEqual(a, b Descriptor) bool {
	encodedA, err := encodeDescriptor(a)
	if err != nil {
		return false
	}
	encodedB, err := encodeDescriptor(b)
	if err != nil {
		return false
	}
	if len(encodedA) != len(encodedB) {
		return false
	}
	return subtle.ConstantTimeCompare(encodedA, encodedB) == 1
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	paths := make([]string, len(descriptor.Path))
	copy(paths, descriptor.Path)
	return Descriptor{
		Type:    descriptor.Type,
		Path:    paths,
		Command: bytes.Clone(descriptor.Command),
	}
}

func sign(secret, header, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(ticketDomain)
	mac.Write(header)
	mac.Write(payload)
	return mac.Sum(nil)
}

func appendUint32(dst []byte, value uint32) []byte {
	return append(dst, byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
}

func appendInt64(dst []byte, value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	return append(dst, encoded[:]...)
}

func appendSizedBytes(dst, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func readUint32(src []byte) (uint32, []byte, error) {
	if len(src) < 4 {
		return 0, nil, ErrTruncated
	}
	return binary.BigEndian.Uint32(src[:4]), src[4:], nil
}

func readSizedBytes(src []byte) ([]byte, []byte, error) {
	length, rest, err := readUint32(src)
	if err != nil {
		return nil, nil, err
	}
	if uint64(length) > uint64(len(rest)) {
		return nil, nil, ErrTruncated
	}
	return rest[:length], rest[length:], nil
}

func constantTimeStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
