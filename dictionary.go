package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// DictionaryID is an opaque identifier for one of the server's agreed
// certificate-compression dictionaries.
type DictionaryID = string

const (
	// ExtensionCompressCertificateDictionary is the extension used by this
	// package to carry a client's requested dictionary IDs. Its body is:
	//
	//	uint16 ids_length;
	//	opaque id<1..255> ids[ids_length];
	//
	// The server still decides whether a requested ID belongs to the tenant
	// selected by its SNI. It never uses a dictionary chosen by the client
	// directly.
	ExtensionCompressCertificateDictionary uint16 = 0xFE01

	// MaxDictionaryIDLength is the largest dictionary ID that fits in the
	// extension vector.
	MaxDictionaryIDLength = 255
	// MaxDictionarySize bounds server-configured dictionary memory. zlib
	// dictionaries are further limited to their 32 KiB history window by the
	// codec.
	MaxDictionarySize = 1 << 20
)

var (
	ErrUnknownDictionary     = errors.New("netsec: unknown dictionary")
	ErrCrossTenantDictionary = errors.New("netsec: dictionary belongs to another tenant")
	ErrInvalidDictionary     = errors.New("netsec: invalid dictionary")
	ErrDictionaryImmutable   = errors.New("netsec: dictionary contents are immutable")
)

// CanonicalSNI normalizes the DNS name used as the tenant boundary. DNS names
// are case-insensitive and may carry a trailing root dot.
func CanonicalSNI(sni string) string {
	return strings.TrimSuffix(strings.ToLower(sni), ".")
}

// Dictionary is an immutable dictionary selected for one tenant.
type Dictionary struct {
	ID      DictionaryID
	Tenant  string
	Content []byte
}

// DictionaryStore holds dictionaries scoped by SNI tenant. The same ID may
// exist for different tenants, but Select only resolves an ID within the
// caller's tenant.
type DictionaryStore struct {
	mu       sync.RWMutex
	byTenant map[string]map[DictionaryID][]byte
	owners   map[DictionaryID]map[string]struct{}
}

// NewDictionaryStore returns an empty store.
func NewDictionaryStore() *DictionaryStore {
	return &DictionaryStore{
		byTenant: make(map[string]map[DictionaryID][]byte),
		owners:   make(map[DictionaryID]map[string]struct{}),
	}
}

// Register installs a dictionary for tenant. Re-registering the same ID with
// identical bytes is idempotent; changing it is rejected so cached compressed
// certificates cannot become stale.
func (s *DictionaryStore) Register(tenant string, id DictionaryID, content []byte) error {
	tenant = CanonicalSNI(tenant)
	if s == nil {
		return fmt.Errorf("%w: nil store", ErrInvalidDictionary)
	}
	if tenant == "" {
		return fmt.Errorf("%w: empty tenant", ErrInvalidDictionary)
	}
	if id == "" || len(id) > MaxDictionaryIDLength {
		return fmt.Errorf("%w: dictionary ID length %d is outside [1,%d]", ErrInvalidDictionary, len(id), MaxDictionaryIDLength)
	}
	if len(content) > MaxDictionarySize {
		return fmt.Errorf("%w: dictionary is %d bytes, maximum is %d", ErrInvalidDictionary, len(content), MaxDictionarySize)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byTenant == nil {
		s.byTenant = make(map[string]map[DictionaryID][]byte)
	}
	if s.owners == nil {
		s.owners = make(map[DictionaryID]map[string]struct{})
	}
	tenantDictionaries := s.byTenant[tenant]
	if tenantDictionaries == nil {
		tenantDictionaries = make(map[DictionaryID][]byte)
		s.byTenant[tenant] = tenantDictionaries
	}
	if existing, ok := tenantDictionaries[id]; ok {
		if string(existing) != string(content) {
			return fmt.Errorf("%w: %q for tenant %q", ErrDictionaryImmutable, id, tenant)
		}
		return nil
	}
	tenantDictionaries[id] = append([]byte(nil), content...)
	owners := s.owners[id]
	if owners == nil {
		owners = make(map[string]struct{})
		s.owners[id] = owners
	}
	owners[tenant] = struct{}{}
	return nil
}

// Select resolves id only inside the tenant identified by sni. If the ID
// exists for another tenant, ErrCrossTenantDictionary is returned even when
// the current tenant also has no such ID.
func (s *DictionaryStore) Select(sni string, id DictionaryID) (Dictionary, error) {
	tenant := CanonicalSNI(sni)
	if s == nil {
		return Dictionary{}, fmt.Errorf("%w: nil store", ErrUnknownDictionary)
	}
	if tenant == "" {
		return Dictionary{}, fmt.Errorf("%w: empty SNI", ErrUnknownDictionary)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if content, ok := s.byTenant[tenant][id]; ok {
		return Dictionary{ID: id, Tenant: tenant, Content: append([]byte(nil), content...)}, nil
	}

	for otherTenant := range s.owners[id] {
		if otherTenant != tenant {
			return Dictionary{}, fmt.Errorf("%w: %q requested by %q", ErrCrossTenantDictionary, id, tenant)
		}
	}
	return Dictionary{}, fmt.Errorf("%w: %q for %q", ErrUnknownDictionary, id, tenant)
}

// Owns reports whether id belongs to tenant. It does not return dictionary
// contents.
func (s *DictionaryStore) Owns(tenant string, id DictionaryID) bool {
	tenant = CanonicalSNI(tenant)
	if s == nil || tenant == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byTenant[tenant][id]
	return ok
}

// ParseDictionaryExtension parses the body of the agreed dictionary extension.
func ParseDictionaryExtension(data []byte) ([]DictionaryID, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: dictionary extension is too short", ErrMalformedExtension)
	}
	total := int(binary.BigEndian.Uint16(data[:2]))
	if total != len(data)-2 {
		return nil, fmt.Errorf("%w: dictionary extension length mismatch", ErrMalformedExtension)
	}

	ids := make([]DictionaryID, 0)
	for offset := 2; offset < len(data); {
		idLength := int(data[offset])
		offset++
		if idLength == 0 {
			return nil, fmt.Errorf("%w: empty dictionary ID", ErrMalformedExtension)
		}
		if offset+idLength > len(data) {
			return nil, fmt.Errorf("%w: truncated dictionary ID", ErrMalformedExtension)
		}
		ids = append(ids, string(data[offset:offset+idLength]))
		offset += idLength
	}
	return ids, nil
}

// MarshalDictionaryExtension encodes the agreed dictionary extension body.
func MarshalDictionaryExtension(ids []DictionaryID) ([]byte, error) {
	total := 0
	for _, id := range ids {
		if id == "" || len(id) > MaxDictionaryIDLength {
			return nil, fmt.Errorf("%w: dictionary ID length %d is outside [1,%d]", ErrMalformedExtension, len(id), MaxDictionaryIDLength)
		}
		total += 1 + len(id)
		if total > MaxCompressedCertificateLength {
			return nil, fmt.Errorf("%w: dictionary extension exceeds %d bytes", ErrMalformedExtension, MaxCompressedCertificateLength)
		}
	}
	data := make([]byte, 2, 2+total)
	binary.BigEndian.PutUint16(data[:2], uint16(total))
	for _, id := range ids {
		data = append(data, byte(len(id)))
		data = append(data, id...)
	}
	return data, nil
}

// ClientOffer is the compression-relevant subset of a ClientHello. It is kept
// separate from the wire so the handshake code can call the negotiator after
// it has parsed and authenticated the SNI.
type ClientOffer struct {
	SNI           string
	Algorithms    []Algorithm
	DictionaryIDs []DictionaryID
}

// ParseClientOffer parses the two extension bodies. Either body may be nil,
// meaning the client did not send that extension.
func ParseClientOffer(sni string, compressCertificate, dictionaryExtension []byte) (ClientOffer, error) {
	offer := ClientOffer{SNI: CanonicalSNI(sni)}
	var err error
	if compressCertificate != nil {
		offer.Algorithms, err = ParseCompressCertificateExtension(compressCertificate)
		if err != nil {
			return ClientOffer{}, err
		}
	}
	if dictionaryExtension != nil {
		offer.DictionaryIDs, err = ParseDictionaryExtension(dictionaryExtension)
		if err != nil {
			return ClientOffer{}, err
		}
	}
	return offer, nil
}
