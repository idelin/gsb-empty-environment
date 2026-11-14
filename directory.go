package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Query requests one key by complete fingerprint and user identifier. ShortID
// is an optional display hint. A query without Fingerprint is never sufficient
// to resolve a key, regardless of how few candidates exist.
type Query struct {
	Fingerprint string `json:"fingerprint"`
	UserID      string `json:"user_id"`
	ShortID     string `json:"short_id,omitempty"`
}

// Identity is an alias for Query.
type Identity = Query

// LookupRequest is an alias for Query.
type LookupRequest = Query

// Revocation is a signed revocation certificate. UserID empty revokes all
// bindings of the identified key.
type Revocation struct {
	Fingerprint string    `json:"fingerprint"`
	UserID      string    `json:"user_id,omitempty"`
	Algorithm   Algorithm `json:"algorithm,omitempty"`
	Signature   []byte    `json:"signature"`
}

// Directory is a concurrency-safe, in-memory key directory. It never uses a
// short identifier as an index for authoritative lookups.
type Directory struct {
	mu sync.RWMutex

	byFingerprint map[string]*directoryEntry

	revokedKeys     map[string]revocationState
	revokedBindings map[string]revocationState
}

// KeyDirectory is an alias for Directory.
type KeyDirectory = Directory

type revocationState struct {
	fingerprint string
	userID      string
	algorithm   Algorithm
	signature   []byte
}

type normalizedRecord struct {
	record          KeyRecord
	fingerprint     string
	keyID           string
	der             []byte
	shortIDs        map[string]struct{}
	bindings        map[string][]Binding
	revoked         bool
	revocationState *revocationState
}

type directoryEntry struct {
	normalizedRecord
}

// NewDirectory returns an empty key directory.
func NewDirectory() *Directory {
	return &Directory{
		byFingerprint:   make(map[string]*directoryEntry),
		revokedKeys:     make(map[string]revocationState),
		revokedBindings: make(map[string]revocationState),
	}
}

// New returns an empty key directory.
func New() *Directory {
	return NewDirectory()
}

// NewKeyDirectory returns an empty key directory.
func NewKeyDirectory() *Directory {
	return NewDirectory()
}

// Import validates and adds a record. It accepts a KeyRecord or *KeyRecord.
//
// The full fingerprint must be derived from the record's public key, every
// binding must carry a valid self-signature, and a claimed revocation must
// carry a valid revocation certificate. Publishing a different key under an
// already-used full fingerprint returns ErrFingerprintCollision.
func (d *Directory) Import(value any) error {
	if d == nil {
		return fmt.Errorf("%w: nil directory", ErrNotFound)
	}
	record, err := coerceRecord(value)
	if err != nil {
		return err
	}
	normalized, err := normalizeRecord(record)
	if err != nil {
		if errors.Is(err, ErrFingerprintMismatch) {
			if claimed, normalizeErr := NormalizeFingerprint(record.Fingerprint); normalizeErr == nil {
				d.mu.RLock()
				occupied := d.byFingerprint[claimed] != nil
				d.mu.RUnlock()
				if occupied {
					return fmt.Errorf("%w: %w", ErrFingerprintCollision, err)
				}
			}
		}
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureMaps()

	existing := d.byFingerprint[normalized.fingerprint]
	if existing != nil {
		if !bytes.Equal(existing.der, normalized.der) {
			return fmt.Errorf("%w: %s", ErrFingerprintCollision, normalized.fingerprint)
		}
		mergeBindings(&existing.record, normalized.record)
		for userID, bindings := range normalized.bindings {
			existing.bindings[userID] = appendUniqueBindings(existing.bindings[userID], bindings...)
		}
		existing.revoked = existing.revoked || normalized.revoked
		addShortIDs(existing.shortIDs, normalized.shortIDs)
	} else {
		copied := *normalized
		existing = &directoryEntry{normalizedRecord: copied}
		d.byFingerprint[normalized.fingerprint] = existing
	}

	if existing.revocationState != nil {
		d.storeRevocation(existing.revocationState)
	}
	if normalized.revocationState != nil {
		d.storeRevocation(normalized.revocationState)
	}
	return nil
}

// Add is an alias for Import.
func (d *Directory) Add(value any) error {
	return d.Import(value)
}

// AddKey is an alias for Import.
func (d *Directory) AddKey(value any) error {
	return d.Import(value)
}

// ImportKey is an alias for Import.
func (d *Directory) ImportKey(value any) error {
	return d.Import(value)
}

// Register is an alias for Import.
func (d *Directory) Register(value any) error {
	return d.Import(value)
}

// Lookup resolves a key. It accepts either a Query value or two strings
// (fingerprint, userID), optionally followed by a short-identifier hint.
//
// Resolution always requires the complete fingerprint and user identifier.
// A short identifier alone returns ErrFingerprintRequired; a colliding short
// identifier never resolves to a key.
func (d *Directory) Lookup(args ...any) (*KeyRecord, error) {
	if d == nil {
		return nil, fmt.Errorf("%w: nil directory", ErrNotFound)
	}
	query, err := coerceQuery(args...)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query.Fingerprint) == "" {
		if strings.TrimSpace(query.ShortID) != "" {
			return nil, d.lookupByShortID(query.ShortID, query.UserID)
		}
		if strings.TrimSpace(query.UserID) != "" {
			candidates, _, err := d.findByUserID(query.UserID)
			if err != nil {
				return nil, err
			}
			if len(candidates) > 1 {
				return nil, ErrMultipleResults
			}
			if len(candidates) == 1 {
				return nil, ErrFingerprintRequired
			}
			return nil, ErrNotFound
		}
		return nil, ErrFingerprintRequired
	}

	fingerprint, err := NormalizeFingerprint(query.Fingerprint)
	if err != nil {
		return nil, err
	}
	userID := normalizeUserID(query.UserID)
	if userID == "" {
		return nil, ErrUserIDRequired
	}
	hint := NormalizeShortID(query.ShortID)

	d.mu.RLock()
	defer d.mu.RUnlock()
	entry := d.byFingerprint[fingerprint]
	if entry == nil {
		return nil, ErrNotFound
	}
	result, err := d.verifiedBinding(entry, fingerprint, userID, hint)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// LookupBy resolves a key by complete fingerprint and user identifier.
func (d *Directory) LookupBy(fingerprint, userID string) (*KeyRecord, error) {
	return d.Lookup(fingerprint, userID)
}

// LookupByFingerprint is an alias for LookupBy.
func (d *Directory) LookupByFingerprint(fingerprint, userID string) (*KeyRecord, error) {
	return d.Lookup(fingerprint, userID)
}

// Resolve is a typed form of Lookup.
func (d *Directory) Resolve(query Query) (*KeyRecord, error) {
	return d.Lookup(query)
}

// Query is an alias for Lookup.
func (d *Directory) Query(args ...any) (*KeyRecord, error) {
	return d.Lookup(args...)
}

// LookupByShortID refuses to use a short identifier as an authorization token.
// It exists so callers can classify collisions and missing hints explicitly:
// a unique candidate still returns ErrFingerprintRequired, while two or more
// candidates return ErrShortIDCollision.
func (d *Directory) LookupByShortID(args ...string) error {
	if d == nil {
		return fmt.Errorf("%w: nil directory", ErrNotFound)
	}
	if len(args) == 0 || len(args) > 2 {
		return ErrFingerprintRequired
	}
	userID := ""
	if len(args) == 2 {
		userID = args[1]
	}
	return d.lookupByShortID(args[0], userID)
}

func (d *Directory) lookupByShortID(shortID, userID string) error {
	hint := NormalizeShortID(shortID)
	if hint == "" {
		return ErrFingerprintRequired
	}
	userID = normalizeUserID(userID)

	d.mu.RLock()
	defer d.mu.RUnlock()

	matches := 0
	seen := make(map[string]struct{})
	for fingerprint, entry := range d.byFingerprint {
		if !entry.acceptsShortID(hint) {
			continue
		}
		if _, duplicate := seen[entry.keyID]; duplicate {
			continue
		}
		if userID == "" {
			for candidateUserID := range entry.bindings {
				if d.entryRevoked(entry, candidateUserID) {
					continue
				}
				if _, err := d.verifiedBinding(entry, fingerprint, candidateUserID, ""); err == nil {
					seen[entry.keyID] = struct{}{}
					matches++
					break
				}
			}
			continue
		}
		if d.entryRevoked(entry, userID) || len(entry.bindings[userID]) == 0 {
			continue
		}
		if _, err := d.verifiedBinding(entry, fingerprint, userID, ""); err == nil {
			seen[entry.keyID] = struct{}{}
			matches++
		}
	}
	switch {
	case matches == 0:
		return ErrNotFound
	case matches > 1:
		return ErrShortIDCollision
	default:
		return ErrFingerprintRequired
	}
}

// FindByUserID returns every active, verified key bound to userID. The result
// is not authoritative for encryption: it may be empty, or it may contain
// several keys. Use Lookup with a complete fingerprint before encrypting.
func (d *Directory) FindByUserID(userID string) ([]*KeyRecord, error) {
	if d == nil {
		return nil, fmt.Errorf("%w: nil directory", ErrNotFound)
	}
	userID = normalizeUserID(userID)
	if userID == "" {
		return nil, ErrUserIDRequired
	}
	records, _, err := d.findByUserID(userID)
	return records, err
}

// LookupByUserID classifies a user-only query. It never returns a key: even a
// single candidate lacks the required full fingerprint. Multiple candidates
// return ErrMultipleResults.
func (d *Directory) LookupByUserID(userID string) (*KeyRecord, error) {
	records, err := d.FindByUserID(userID)
	if err != nil {
		return nil, err
	}
	switch len(records) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return nil, ErrFingerprintRequired
	default:
		return nil, ErrMultipleResults
	}
}

func (d *Directory) findByUserID(userID string) ([]*KeyRecord, map[string]struct{}, error) {
	userID = normalizeUserID(userID)
	if userID == "" {
		return nil, nil, ErrUserIDRequired
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	keyIDs := make(map[string]struct{})
	var records []*KeyRecord
	for fingerprint, entry := range d.byFingerprint {
		if len(entry.bindings[userID]) == 0 || d.entryRevoked(entry, userID) {
			continue
		}
		record, err := d.verifiedBinding(entry, fingerprint, userID, "")
		if err != nil {
			continue
		}
		if _, seen := keyIDs[entry.keyID]; seen {
			continue
		}
		keyIDs[entry.keyID] = struct{}{}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Fingerprint < records[j].Fingerprint
	})
	return records, keyIDs, nil
}

func (d *Directory) verifiedBinding(entry *directoryEntry, fingerprint, userID, hint string) (*KeyRecord, error) {
	if entry == nil {
		return nil, ErrNotFound
	}
	if d.entryRevoked(entry, userID) {
		return nil, fmt.Errorf("%w: %s", ErrRevoked, fingerprint)
	}

	bindings := entry.bindings[userID]
	if len(bindings) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUserMismatch, userID)
	}

	var accepted []Binding
	for _, binding := range bindings {
		if err := VerifyBinding(entry.record.PublicKey, fingerprint, userID, binding.Signature, binding.Algorithm); err != nil {
			continue
		}
		accepted = append(accepted, cloneBinding(binding))
	}
	if len(accepted) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnverifiedBinding, userID)
	}

	if hint != "" {
		if !entry.acceptsShortID(hint) {
			return nil, fmt.Errorf("%w: got %s", ErrShortIDMismatch, hint)
		}
	}

	result := cloneRecord(entry.record)
	result.Fingerprint = fingerprint
	result.UserID = userID
	result.Bindings = accepted
	result.BindingSignature = accepted[0].Signature
	result.BindingAlgorithm = accepted[0].Algorithm
	result.Verified = true
	result.Revoked = false
	result.RevocationSignature = nil
	result.RevocationAlgorithm = ""
	result.RevocationUserID = ""
	return &result, nil
}

func (entry *directoryEntry) acceptsShortID(hint string) bool {
	if hint == "" {
		return true
	}
	if _, ok := entry.shortIDs[hint]; ok {
		return true
	}
	if len(hint) >= 4 && strings.HasSuffix(entry.fingerprint, hint) {
		return true
	}
	if len(hint) >= 4 && strings.HasPrefix(entry.fingerprint, hint) {
		return true
	}
	return false
}

func (d *Directory) entryRevoked(entry *directoryEntry, userID string) bool {
	if _, ok := d.revokedKeys[entry.keyID]; ok {
		return true
	}
	if _, ok := d.revokedBindings[entry.keyID+"\x00"+userID]; ok {
		return true
	}
	return false
}

// Revoke applies a signed revocation certificate. It accepts a Revocation value
// or (fingerprint, userID, signature) with an optional Algorithm argument.
// A revoked key cannot be made active again by re-importing the old record.
func (d *Directory) Revoke(args ...any) error {
	if d == nil {
		return fmt.Errorf("%w: nil directory", ErrNotFound)
	}
	request, err := coerceRevocation(args...)
	if err != nil {
		return err
	}
	fingerprint, err := NormalizeFingerprint(request.Fingerprint)
	if err != nil {
		return err
	}
	userID := normalizeUserID(request.UserID)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureMaps()
	entry := d.byFingerprint[fingerprint]
	if entry == nil {
		return ErrNotFound
	}
	if userID != "" && len(entry.bindings[userID]) == 0 {
		return ErrUserMismatch
	}
	if err := VerifyRevocation(entry.record.PublicKey, fingerprint, userID, request.Signature, request.Algorithm); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRevocation, err)
	}
	state := revocationState{
		fingerprint: fingerprint,
		userID:      userID,
		algorithm:   request.Algorithm,
		signature:   append([]byte(nil), request.Signature...),
	}
	d.storeRevocation(&state)
	return nil
}

// ApplyRevocation is an alias for Revoke with a Revocation value.
func (d *Directory) ApplyRevocation(revocation Revocation) error {
	return d.Revoke(revocation)
}

// RevokeKey is an alias for Revoke.
func (d *Directory) RevokeKey(args ...any) error {
	return d.Revoke(args...)
}

// IsRevoked reports whether the complete fingerprint has a key-wide revocation.
func (d *Directory) IsRevoked(fingerprint string) bool {
	if d == nil {
		return false
	}
	normalized, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry := d.byFingerprint[normalized]
	if entry == nil {
		return false
	}
	_, ok := d.revokedKeys[entry.keyID]
	return ok
}

// IsBindingRevoked reports whether userID is revoked for the complete
// fingerprint.
func (d *Directory) IsBindingRevoked(fingerprint, userID string) bool {
	if d == nil {
		return false
	}
	normalized, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return false
	}
	userID = normalizeUserID(userID)
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry := d.byFingerprint[normalized]
	if entry == nil {
		return false
	}
	if _, ok := d.revokedKeys[entry.keyID]; ok {
		return true
	}
	_, ok := d.revokedBindings[entry.keyID+"\x00"+userID]
	return ok
}

func (d *Directory) storeRevocation(state *revocationState) {
	if state == nil {
		return
	}
	normalized, err := NormalizeFingerprint(state.fingerprint)
	if err != nil {
		return
	}
	entry := d.byFingerprint[normalized]
	if entry == nil {
		return
	}
	if state.userID == "" {
		d.revokedKeys[entry.keyID] = *state
		entry.revoked = true
		return
	}
	d.revokedBindings[entry.keyID+"\x00"+state.userID] = *state
}

func (d *Directory) ensureMaps() {
	if d.byFingerprint == nil {
		d.byFingerprint = make(map[string]*directoryEntry)
	}
	if d.revokedKeys == nil {
		d.revokedKeys = make(map[string]revocationState)
	}
	if d.revokedBindings == nil {
		d.revokedBindings = make(map[string]revocationState)
	}
}

func normalizeRecord(record *KeyRecord) (*normalizedRecord, error) {
	if record == nil {
		return nil, fmt.Errorf("%w: nil record", ErrInvalidPublicKey)
	}
	key, der, err := normalizePublicKey(record.PublicKey)
	if err != nil {
		return nil, err
	}

	fingerprint := record.Fingerprint
	if strings.TrimSpace(fingerprint) == "" {
		fingerprint, err = Fingerprint(key)
		if err != nil {
			return nil, err
		}
	} else {
		fingerprint, err = NormalizeFingerprint(fingerprint)
		if err != nil {
			return nil, err
		}
	}
	fingerprintSet, err := FingerprintSet(key)
	if err != nil {
		return nil, err
	}
	if _, ok := fingerprintSet[fingerprint]; !ok {
		return nil, fmt.Errorf("%w: claimed %s", ErrFingerprintMismatch, fingerprint)
	}

	keyID, err := Fingerprint(key)
	if err != nil {
		return nil, err
	}

	shortIDs := make(map[string]struct{})
	if hint := NormalizeShortID(record.ShortID); hint != "" {
		if len(hint) > fingerprintLenSHA256 {
			return nil, fmt.Errorf("%w: overlong hint", ErrShortIDMismatch)
		}
		shortIDs[hint] = struct{}{}
	}
	derived, err := DerivedShortID(fingerprint)
	if err != nil {
		return nil, err
	}
	shortIDs[derived] = struct{}{}

	bindings, userID, err := normalizeBindings(record, key, fingerprint)
	if err != nil {
		return nil, err
	}

	normalized := KeyRecord{
		Fingerprint:      fingerprint,
		ShortID:          effectiveShortID(record.ShortID, derived),
		UserID:           userID,
		PublicKey:        key,
		Bindings:         flattenBindings(bindings),
		RevocationUserID: normalizeUserID(record.RevocationUserID),
		Verified:         true,
	}

	var revocation *revocationState
	if record.Revoked || len(record.RevocationSignature) > 0 {
		revocationUserID := normalized.RevocationUserID
		switch {
		case record.RevocationAll:
			revocationUserID = ""
		case revocationUserID == "":
			revocationUserID = userID
		}
		verifyErr := VerifyRevocation(key, fingerprint, revocationUserID, record.RevocationSignature, record.RevocationAlgorithm)
		if verifyErr != nil && revocationUserID != "" {
			// A publisher may omit RevocationAll while supplying a
			// key-wide certificate. Both forms are signed by the key.
			if keyWideErr := VerifyRevocation(key, fingerprint, "", record.RevocationSignature, record.RevocationAlgorithm); keyWideErr == nil {
				revocationUserID = ""
				verifyErr = nil
			}
		}
		if verifyErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRevocation, verifyErr)
		}
		normalized.Revoked = true
		normalized.RevocationSignature = append([]byte(nil), record.RevocationSignature...)
		normalized.RevocationAlgorithm = record.RevocationAlgorithm
		normalized.RevocationUserID = revocationUserID
		normalized.RevocationAll = revocationUserID == ""
		revocation = &revocationState{
			fingerprint: fingerprint,
			userID:      revocationUserID,
			algorithm:   record.RevocationAlgorithm,
			signature:   append([]byte(nil), record.RevocationSignature...),
		}
	}

	result := &normalizedRecord{
		record:          normalized,
		fingerprint:     fingerprint,
		keyID:           keyID,
		der:             append([]byte(nil), der...),
		shortIDs:        shortIDs,
		bindings:        bindings,
		revoked:         normalized.Revoked,
		revocationState: revocation,
	}
	return result, nil
}

func normalizeBindings(record *KeyRecord, key any, fingerprint string) (map[string][]Binding, string, error) {
	bindings := make(map[string][]Binding)
	for _, binding := range record.Bindings {
		userID := normalizeUserID(binding.UserID)
		if userID == "" {
			return nil, "", ErrUserIDRequired
		}
		bindingFingerprint := binding.Fingerprint
		if strings.TrimSpace(bindingFingerprint) == "" {
			bindingFingerprint = fingerprint
		} else {
			normalizedFingerprint, err := NormalizeFingerprint(bindingFingerprint)
			if err != nil {
				return nil, "", err
			}
			bindingFingerprint = normalizedFingerprint
		}
		if bindingFingerprint != fingerprint {
			return nil, "", fmt.Errorf("%w: binding fingerprint %s", ErrFingerprintMismatch, bindingFingerprint)
		}
		if err := VerifyBinding(key, fingerprint, userID, binding.Signature, binding.Algorithm); err != nil {
			return nil, "", fmt.Errorf("%w: %v", ErrUnverifiedBinding, err)
		}
		binding.UserID = userID
		binding.Fingerprint = fingerprint
		binding.Verified = true
		bindings[userID] = appendUniqueBindings(bindings[userID], binding)
	}

	topUserID := normalizeUserID(record.UserID)
	if topUserID != "" {
		covered := len(bindings[topUserID]) > 0
		if len(record.BindingSignature) == 0 {
			if !covered {
				return nil, "", fmt.Errorf("%w: %s", ErrUnverifiedBinding, topUserID)
			}
		} else {
			if err := VerifyBinding(key, fingerprint, topUserID, record.BindingSignature, record.BindingAlgorithm); err != nil {
				return nil, "", fmt.Errorf("%w: %v", ErrUnverifiedBinding, err)
			}
			bindings[topUserID] = appendUniqueBindings(bindings[topUserID], Binding{
				UserID:      topUserID,
				Fingerprint: fingerprint,
				Algorithm:   record.BindingAlgorithm,
				Signature:   append([]byte(nil), record.BindingSignature...),
				Verified:    true,
			})
		}
	}

	if len(bindings) == 0 {
		if topUserID == "" {
			return nil, "", ErrUserIDRequired
		}
		return nil, "", fmt.Errorf("%w: %s", ErrUnverifiedBinding, topUserID)
	}

	firstUserID := topUserID
	if firstUserID == "" {
		firstUserID = sortedBindingUserIDs(bindings)[0]
	}
	return bindings, firstUserID, nil
}

func flattenBindings(bindings map[string][]Binding) []Binding {
	var result []Binding
	for _, userID := range sortedBindingUserIDs(bindings) {
		result = append(result, bindings[userID]...)
	}
	return result
}

func sortedBindingUserIDs(bindings map[string][]Binding) []string {
	userIDs := make([]string, 0, len(bindings))
	for userID := range bindings {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	return userIDs
}

func appendUniqueBindings(existing []Binding, additions ...Binding) []Binding {
	for _, addition := range additions {
		duplicate := false
		for _, present := range existing {
			if present.UserID == addition.UserID && bytes.Equal(present.Signature, addition.Signature) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, cloneBinding(addition))
		}
	}
	return existing
}

func mergeBindings(target *KeyRecord, source KeyRecord) {
	target.Bindings = appendUniqueBindings(target.Bindings, source.Bindings...)
}

func addShortIDs(target map[string]struct{}, source map[string]struct{}) {
	for shortID := range source {
		target[shortID] = struct{}{}
	}
}

func effectiveShortID(supplied, derived string) string {
	if normalized := NormalizeShortID(supplied); normalized != "" {
		return normalized
	}
	return derived
}

func cloneBinding(binding Binding) Binding {
	binding.Signature = append([]byte(nil), binding.Signature...)
	return binding
}

func cloneRecord(record KeyRecord) KeyRecord {
	record.Bindings = append([]Binding(nil), record.Bindings...)
	for i := range record.Bindings {
		record.Bindings[i] = cloneBinding(record.Bindings[i])
	}
	record.BindingSignature = append([]byte(nil), record.BindingSignature...)
	record.RevocationSignature = append([]byte(nil), record.RevocationSignature...)
	return record
}

func coerceRecord(value any) (*KeyRecord, error) {
	switch typed := value.(type) {
	case KeyRecord:
		copied := cloneRecord(typed)
		return &copied, nil
	case *KeyRecord:
		if typed == nil {
			return nil, fmt.Errorf("%w: nil record", ErrInvalidPublicKey)
		}
		copied := cloneRecord(*typed)
		return &copied, nil
	default:
		return nil, fmt.Errorf("%w: expected KeyRecord, got %T", ErrInvalidPublicKey, value)
	}
}

func coerceQuery(args ...any) (Query, error) {
	if len(args) == 1 {
		switch typed := args[0].(type) {
		case Query:
			return typed, nil
		case *Query:
			if typed == nil {
				return Query{}, ErrFingerprintRequired
			}
			return *typed, nil
		default:
			return Query{}, fmt.Errorf("%w: expected Query, got %T", ErrFingerprintRequired, args[0])
		}
	}
	if len(args) == 2 || len(args) == 3 {
		fingerprint, ok := args[0].(string)
		if !ok {
			return Query{}, fmt.Errorf("%w: fingerprint must be a string", ErrFingerprintRequired)
		}
		userID, ok := args[1].(string)
		if !ok {
			return Query{}, fmt.Errorf("%w: user identifier must be a string", ErrUserIDRequired)
		}
		query := Query{Fingerprint: fingerprint, UserID: userID}
		if len(args) == 3 {
			shortID, ok := args[2].(string)
			if !ok {
				return Query{}, fmt.Errorf("%w: short identifier must be a string", ErrShortIDMismatch)
			}
			query.ShortID = shortID
		}
		return query, nil
	}
	return Query{}, ErrFingerprintRequired
}

func coerceRevocation(args ...any) (Revocation, error) {
	if len(args) == 1 {
		switch typed := args[0].(type) {
		case Revocation:
			return typed, nil
		case *Revocation:
			if typed == nil {
				return Revocation{}, ErrInvalidRevocation
			}
			return *typed, nil
		case KeyRecord:
			return revocationFromRecord(typed), nil
		case *KeyRecord:
			if typed == nil {
				return Revocation{}, ErrInvalidRevocation
			}
			return revocationFromRecord(*typed), nil
		default:
			return Revocation{}, fmt.Errorf("%w: expected Revocation, got %T", ErrInvalidRevocation, args[0])
		}
	}
	if len(args) == 3 || len(args) == 4 {
		fingerprint, ok := args[0].(string)
		if !ok {
			return Revocation{}, fmt.Errorf("%w: fingerprint must be a string", ErrInvalidFingerprint)
		}
		userID, ok := args[1].(string)
		if !ok {
			return Revocation{}, fmt.Errorf("%w: user identifier must be a string", ErrUserIDRequired)
		}
		signature, ok := args[2].([]byte)
		if !ok {
			return Revocation{}, fmt.Errorf("%w: signature must be []byte", ErrInvalidRevocation)
		}
		revocation := Revocation{Fingerprint: fingerprint, UserID: userID, Signature: signature}
		if len(args) == 4 {
			switch algorithm := args[3].(type) {
			case Algorithm:
				revocation.Algorithm = algorithm
			case string:
				revocation.Algorithm = Algorithm(algorithm)
			default:
				return Revocation{}, fmt.Errorf("%w: algorithm must be Algorithm", ErrInvalidRevocation)
			}
		}
		return revocation, nil
	}
	return Revocation{}, ErrInvalidRevocation
}

func revocationFromRecord(record KeyRecord) Revocation {
	userID := record.RevocationUserID
	if userID == "" && !record.RevocationAll {
		userID = record.UserID
	}
	return Revocation{
		Fingerprint: record.Fingerprint,
		UserID:      userID,
		Algorithm:   record.RevocationAlgorithm,
		Signature:   record.RevocationSignature,
	}
}
