package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// HashLen is the SHA-256 output length in bytes.
const HashLen = sha256.Size

// MaxOutputLen is the largest key a single derivation may produce:
// 255 HMAC-SHA256 blocks, as mandated by RFC 5869 section 2.3.
const MaxOutputLen = 255 * HashLen

// Purpose labels identify which subsystem-specific key is being derived.
type Purpose uint32

// Built-in purposes registered on every new Service.
const (
	PurposeSessionMAC    Purpose = 1
	PurposeLogEncryption Purpose = 2
	PurposeTokenSigning  Purpose = 3
)

var (
	ErrPurposeNotRegistered = errors.New("netsec: purpose is not registered with the key service")
	ErrPurposeNotAllowed    = errors.New("netsec: purpose is not allowed for this handle")
	ErrHandleRevoked        = errors.New("netsec: handle has been revoked")
	ErrOutputTooLong        = errors.New("netsec: requested output length exceeds 255*HashLen")
	ErrInvalidOutputLen     = errors.New("netsec: output length must be positive")
	ErrFieldTooLong         = errors.New("netsec: identity field exceeds 65535 bytes")
	ErrInvalidIKM           = errors.New("netsec: IKM must not be empty")
	ErrInvalidPRK           = errors.New("netsec: PRK must be exactly HashLen bytes")
)

// Service holds one pseudorandom key (PRK) shared by every derivation, plus
// the registry of purpose labels handles are permitted to request.
// A Service is safe for concurrent use.
type Service struct {
	mu       sync.RWMutex
	prk      []byte
	purposes map[Purpose]struct{}
	nextID   uint64
}

// NewServiceFromIKM extracts a PRK from ikm via HKDF-Extract. An empty or
// nil salt is replaced by HashLen zero bytes, as required by RFC 5869 2.2.
func NewServiceFromIKM(ikm, salt []byte) (*Service, error) {
	if len(ikm) == 0 {
		return nil, ErrInvalidIKM
	}
	return newService(extract(ikm, salt)), nil
}

// NewServiceFromPRK wraps an already-extracted PRK. The slice is copied so
// later mutation by the caller cannot affect the service.
func NewServiceFromPRK(prk []byte) (*Service, error) {
	if len(prk) != HashLen {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidPRK, len(prk))
	}
	return newService(append([]byte(nil), prk...)), nil
}

func newService(prk []byte) *Service {
	svc := &Service{
		prk:      prk,
		purposes: make(map[Purpose]struct{}),
	}
	for _, purpose := range []Purpose{PurposeSessionMAC, PurposeLogEncryption, PurposeTokenSigning} {
		svc.purposes[purpose] = struct{}{}
	}
	return svc
}

// RegisterPurpose adds a purpose label to the service registry. Purpose 0 is
// reserved and cannot be registered.
func (s *Service) RegisterPurpose(purpose Purpose) error {
	if purpose == 0 {
		return fmt.Errorf("%w: purpose 0 is reserved", ErrPurposeNotRegistered)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purposes[purpose] = struct{}{}
	return nil
}

func (s *Service) isRegistered(purpose Purpose) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.purposes[purpose]
	return ok
}

// IssueHandle mints a capability handle for owner that may only request the
// listed purposes. Every requested purpose must already be registered with
// the service. Duplicate purposes in allowed are collapsed.
func (s *Service) IssueHandle(owner string, allowed []Purpose) (*Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allow := make(map[Purpose]struct{}, len(allowed))
	for _, purpose := range allowed {
		if _, ok := s.purposes[purpose]; !ok {
			return nil, fmt.Errorf("%w: purpose %d", ErrPurposeNotRegistered, purpose)
		}
		allow[purpose] = struct{}{}
	}
	s.nextID++
	return &Handle{
		id:      s.nextID,
		owner:   owner,
		svc:     s,
		allowed: allow,
	}, nil
}

// Handle is a per-caller derivation capability. It can only derive keys for
// the purposes registered at issuance, and every derivation fails once the
// handle is revoked.
type Handle struct {
	mu      sync.RWMutex
	id      uint64
	owner   string
	svc     *Service
	allowed map[Purpose]struct{}
	revoked bool
}

// Revoke invalidates the handle. It waits for in-flight derivations to
// finish; after it returns, all subsequent Derive calls fail.
func (h *Handle) Revoke() {
	h.mu.Lock()
	h.revoked = true
	h.mu.Unlock()
}

// Revoked reports whether the handle has been revoked.
func (h *Handle) Revoked() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.revoked
}

// Owner returns the owner identifier supplied when the handle was issued.
func (h *Handle) Owner() string { return h.owner }

// Derive produces an outLen-byte key for purpose/version and the caller's
// identity fields. Raw info strings are deliberately not part of the API:
// the HKDF info input is always built internally as length-prefixed TLV
// records so callers cannot collide with another subsystem's context.
func (h *Handle) Derive(purpose Purpose, version uint16, identityFields [][]byte, outLen int) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.revoked {
		return nil, ErrHandleRevoked
	}
	if outLen <= 0 {
		return nil, ErrInvalidOutputLen
	}
	if outLen > MaxOutputLen {
		return nil, fmt.Errorf("%w: requested %d bytes", ErrOutputTooLong, outLen)
	}
	if !h.svc.isRegistered(purpose) {
		return nil, fmt.Errorf("%w: purpose %d", ErrPurposeNotRegistered, purpose)
	}
	if _, ok := h.allowed[purpose]; !ok {
		return nil, fmt.Errorf("%w: purpose %d", ErrPurposeNotAllowed, purpose)
	}
	info, err := buildInfo(purpose, version, identityFields)
	if err != nil {
		return nil, err
	}
	return expand(h.svc.prk, info, outLen)
}

// buildInfo serializes the derivation context as TLV records. Every record,
// including the purpose number and the version, is prefixed with its length
// encoded as a two-byte big-endian uint16. The prefixes make the encoding
// unambiguous: shifting the boundary between adjacent fields always yields a
// different byte string.
func buildInfo(purpose Purpose, version uint16, identityFields [][]byte) ([]byte, error) {
	var buf bytes.Buffer
	var purposeBuf [4]byte
	binary.BigEndian.PutUint32(purposeBuf[:], uint32(purpose))
	if err := writeTLV(&buf, purposeBuf[:]); err != nil {
		return nil, err
	}
	var versionBuf [2]byte
	binary.BigEndian.PutUint16(versionBuf[:], version)
	if err := writeTLV(&buf, versionBuf[:]); err != nil {
		return nil, err
	}
	for _, field := range identityFields {
		if len(field) > 0xffff {
			return nil, fmt.Errorf("%w: field is %d bytes", ErrFieldTooLong, len(field))
		}
		if err := writeTLV(&buf, field); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func writeTLV(buf *bytes.Buffer, value []byte) error {
	var lengthBuf [2]byte
	binary.BigEndian.PutUint16(lengthBuf[:], uint16(len(value)))
	if _, err := buf.Write(lengthBuf[:]); err != nil {
		return err
	}
	_, err := buf.Write(value)
	return err
}

// extract implements HKDF-Extract per RFC 5869 section 2.2. A missing salt
// is a string of HashLen zero bytes.
func extract(ikm, salt []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, HashLen)
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// expand implements HKDF-Expand per RFC 5869 section 2.3. outLen must be
// positive and must not exceed 255*HashLen.
func expand(prk, info []byte, outLen int) ([]byte, error) {
	if outLen <= 0 {
		return nil, ErrInvalidOutputLen
	}
	blockCount := (outLen + HashLen - 1) / HashLen
	if blockCount > 255 {
		return nil, fmt.Errorf("%w: requested %d bytes", ErrOutputTooLong, outLen)
	}
	okm := make([]byte, 0, blockCount*HashLen)
	var previous []byte
	for counter := 1; counter <= blockCount; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{byte(counter)})
		previous = mac.Sum(nil)
		okm = append(okm, previous...)
	}
	return okm[:outLen], nil
}
