package netsec

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
)

// TenantID identifies the tenant that owns an SNI and its dictionaries.
type TenantID string

// DictionaryID identifies a compression dictionary. Zero is reserved to mean
// "no dictionary".
type DictionaryID uint16

// NoDictionary selects the dictionary-less compression path.
const NoDictionary DictionaryID = 0

// Dictionary is a tenant-scoped compression dictionary.
type Dictionary struct {
	ID     DictionaryID
	Tenant TenantID
	SNI    string
	Data   []byte
}

type dictionaryRecord struct {
	dictionary Dictionary
}

// DictionaryRegistry stores dictionaries and binds every dictionary ID to
// exactly one tenant and SNI. Global ID uniqueness means that a cache key of
// (algorithm, dictionary ID, chain hash) cannot alias dictionaries belonging to
// different tenants.
type DictionaryRegistry struct {
	mutex       sync.RWMutex
	byID        map[DictionaryID]dictionaryRecord
	tenantBySNI map[string]TenantID
}

// NewDictionaryRegistry returns an empty registry.
func NewDictionaryRegistry() *DictionaryRegistry {
	return &DictionaryRegistry{
		byID:        make(map[DictionaryID]dictionaryRecord),
		tenantBySNI: make(map[string]TenantID),
	}
}

func normalizeSNI(sni string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(sni)), ".")
}

// Register adds a dictionary for tenant and SNI. Dictionary IDs are globally
// unique: reusing an ID for a different tenant, SNI, or dictionary value is
// rejected. Registering an identical record again is idempotent.
func (registry *DictionaryRegistry) Register(tenant TenantID, sni string, id DictionaryID, data []byte) error {
	if registry == nil {
		return ErrDictionaryNotFound
	}
	if tenant == "" {
		return fmt.Errorf("%w: empty tenant", ErrDictionaryNotFound)
	}
	normalizedSNI := normalizeSNI(sni)
	if normalizedSNI == "" {
		return fmt.Errorf("%w: empty SNI", ErrDictionaryNotFound)
	}
	if id == NoDictionary {
		return fmt.Errorf("%w: dictionary ID 0 is reserved", ErrDictionaryIDConflict)
	}
	if len(data) == 0 {
		return fmt.Errorf("%w: empty dictionary", ErrDictionaryNotFound)
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.byID == nil {
		registry.byID = make(map[DictionaryID]dictionaryRecord)
	}
	if registry.tenantBySNI == nil {
		registry.tenantBySNI = make(map[string]TenantID)
	}

	if existingTenant, ok := registry.tenantBySNI[normalizedSNI]; ok && existingTenant != tenant {
		return fmt.Errorf("%w: %s", ErrSNIConflict, normalizedSNI)
	}

	if existing, ok := registry.byID[id]; ok {
		if existing.dictionary.Tenant == tenant &&
			existing.dictionary.SNI == normalizedSNI &&
			bytes.Equal(existing.dictionary.Data, data) {
			return nil
		}
		return fmt.Errorf("%w: ID %d", ErrDictionaryIDConflict, id)
	}

	registry.byID[id] = dictionaryRecord{
		dictionary: Dictionary{
			ID:     id,
			Tenant: tenant,
			SNI:    normalizedSNI,
			Data:   append([]byte(nil), data...),
		},
	}
	registry.tenantBySNI[normalizedSNI] = tenant
	return nil
}

// TenantForSNI returns the tenant bound to sni.
func (registry *DictionaryRegistry) TenantForSNI(sni string) (TenantID, bool) {
	if registry == nil {
		return "", false
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	tenant, ok := registry.tenantBySNI[normalizeSNI(sni)]
	return tenant, ok
}

// Lookup returns the dictionary only when tenant and SNI both own id. An empty
// tenant is derived from the registry's SNI binding. A dictionary ID registered
// to another tenant or SNI is rejected with ErrCrossTenantDictionary.
func (registry *DictionaryRegistry) Lookup(tenant TenantID, sni string, id DictionaryID) (Dictionary, error) {
	if registry == nil {
		return Dictionary{}, ErrDictionaryNotFound
	}
	normalizedSNI := normalizeSNI(sni)

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	if tenant == "" {
		tenant = registry.tenantBySNI[normalizedSNI]
	}
	record, ok := registry.byID[id]
	if !ok {
		return Dictionary{}, fmt.Errorf("%w: ID %d", ErrDictionaryNotFound, id)
	}
	if record.dictionary.Tenant != tenant || record.dictionary.SNI != normalizedSNI {
		return Dictionary{}, fmt.Errorf("%w: ID %d", ErrCrossTenantDictionary, id)
	}
	return cloneDictionary(record.dictionary), nil
}

// SelectBySNI derives the tenant from sni and then looks up id. This models a
// server that trusts only the SNI from the ClientHello and has no separate
// tenant parameter.
func (registry *DictionaryRegistry) SelectBySNI(sni string, id DictionaryID) (Dictionary, error) {
	if registry == nil {
		return Dictionary{}, ErrDictionaryNotFound
	}
	normalizedSNI := normalizeSNI(sni)

	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	record, ok := registry.byID[id]
	if !ok {
		return Dictionary{}, fmt.Errorf("%w: ID %d", ErrDictionaryNotFound, id)
	}
	tenant, ok := registry.tenantBySNI[normalizedSNI]
	if !ok || tenant != record.dictionary.Tenant || record.dictionary.SNI != normalizedSNI {
		return Dictionary{}, fmt.Errorf("%w: ID %d", ErrCrossTenantDictionary, id)
	}
	return cloneDictionary(record.dictionary), nil
}

func cloneDictionary(dictionary Dictionary) Dictionary {
	dictionary.Data = append([]byte(nil), dictionary.Data...)
	return dictionary
}
