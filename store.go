package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// HeaderCacheKeyDigest is the default response header in which an origin
	// returns the digest of the cache key it used.
	HeaderCacheKeyDigest = "X-Cache-Key-Digest"

	// HeaderNormalizationVersion is the default header used to compare the
	// request and response normalization versions.
	HeaderNormalizationVersion = "X-Cache-Key-Version"
)

var (
	// ErrNotCacheable means the response must not enter a shared cache.
	ErrNotCacheable = errors.New("netsec: response is not cacheable by a shared cache")

	// ErrMissingKeyDigest means the origin did not return a cache-key digest.
	ErrMissingKeyDigest = errors.New("netsec: origin did not return a cache-key digest")

	// ErrKeyDigestMismatch means the origin's digest does not match the request.
	ErrKeyDigestMismatch = errors.New("netsec: origin cache-key digest mismatch")

	// ErrNormalizationVersionMismatch means the request and response were
	// normalized using different versions.
	ErrNormalizationVersionMismatch = errors.New("netsec: normalization version mismatch")

	// ErrBodyTooLarge means the response exceeded StoreOptions.MaxBodyBytes.
	ErrBodyTooLarge = errors.New("netsec: response body exceeds store limit")
)

// OriginMetadata is authenticated metadata returned by the origin for a
// response. KeyDigest must identify the canonical request key that the origin
// used. NormalizationVersion, when non-empty, must match the edge's version.
type OriginMetadata struct {
	KeyDigest            string
	NormalizationVersion string
}

// StoreOptions controls the response store.
type StoreOptions struct {
	// MaxEntries limits the number of stored variants. Zero uses 4096.
	// A negative value disables the limit.
	MaxEntries int

	// MaxBodyBytes limits a stored response body. Zero uses 8 MiB.
	MaxBodyBytes int64

	// DefaultTTL applies when a response has no explicit freshness lifetime.
	// Zero uses one hour.
	DefaultTTL time.Duration

	// Now is injectable for tests. Nil uses time.Now.
	Now func() time.Time

	// DigestHeader is the response header carrying the origin key digest.
	// Empty uses X-Cache-Key-Digest.
	DigestHeader string

	// VersionHeader is the request and response header carrying the
	// normalization version. Empty uses X-Cache-Key-Version.
	VersionHeader string
}

// CacheHit is a response returned by Lookup.
type CacheHit struct {
	Response  *http.Response
	Key       Key
	BaseKey   Key
	Vary      []string
	StoredAt  time.Time
	ExpiresAt time.Time
}

type cacheEntry struct {
	fullID          string
	key             Key
	baseKey         Key
	variantDigest   [32]byte
	vary            []string
	header          http.Header
	body            []byte
	status          string
	statusCode      int
	proto           string
	protoMajor      int
	protoMinor      int
	requestVersion  string
	responseVersion string
	storedAt        time.Time
	expiresAt       time.Time
}

// Store is a concurrency-safe in-memory shared response cache. Stored bodies
// and headers are cloned on insertion and on every hit.
type Store struct {
	builder *KeyBuilder
	options StoreOptions

	mu      sync.RWMutex
	entries map[string]*cacheEntry
	byBase  map[string][]*cacheEntry
}

// NewStore creates a response store. If more than one options value is passed,
// the first is used.
func NewStore(builder *KeyBuilder, options ...StoreOptions) *Store {
	if builder == nil {
		builder = NewKeyBuilder(KeyConfig{})
	}

	config := StoreOptions{}
	if len(options) > 0 {
		config = options[0]
	}
	if config.MaxEntries == 0 {
		config.MaxEntries = 4096
	}
	if config.MaxBodyBytes == 0 {
		config.MaxBodyBytes = 8 << 20
	}
	if config.DefaultTTL == 0 {
		config.DefaultTTL = time.Hour
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.DigestHeader == "" {
		config.DigestHeader = HeaderCacheKeyDigest
	}
	if config.VersionHeader == "" {
		config.VersionHeader = HeaderNormalizationVersion
	}

	return &Store{
		builder: builder,
		options: config,
		entries: make(map[string]*cacheEntry),
		byBase:  make(map[string][]*cacheEntry),
	}
}

// KeyBuilder returns the store's key builder.
func (s *Store) KeyBuilder() *KeyBuilder {
	if s == nil {
		return nil
	}
	return s.builder
}

// Put verifies the origin digest and stores resp for req in the builder's
// default identity. An omitted or empty digest falls back to the configured
// response header. A missing digest is rejected: unverified writes never enter
// the shared cache.
func (s *Store) Put(req *http.Request, resp *http.Response, originDigest ...string) error {
	if s == nil {
		return errors.New("netsec: nil store")
	}
	return s.PutFor(req, s.builder.Identity(), resp, originDigest...)
}

// PutFor is Put for an explicit site and tenant identity.
func (s *Store) PutFor(req *http.Request, identity Identity, resp *http.Response, originDigest ...string) error {
	digest := ""
	if len(originDigest) > 0 {
		digest = originDigest[0]
	}
	return s.PutForWithMetadata(req, identity, resp, OriginMetadata{KeyDigest: digest})
}

// PutWithMetadata stores a response and verifies all supplied origin metadata.
func (s *Store) PutWithMetadata(req *http.Request, resp *http.Response, metadata OriginMetadata) error {
	if s == nil {
		return errors.New("netsec: nil store")
	}
	return s.PutForWithMetadata(req, s.builder.Identity(), resp, metadata)
}

// PutForWithMetadata is PutWithMetadata for an explicit tenant identity.
func (s *Store) PutForWithMetadata(req *http.Request, identity Identity, resp *http.Response, metadata OriginMetadata) error {
	if s == nil {
		return errors.New("netsec: nil store")
	}
	if req == nil || resp == nil {
		return errors.New("netsec: nil request or response")
	}
	if requestBypassesCache(req) {
		return ErrNotCacheable
	}

	key, err := s.builder.BuildFor(req, identity)
	if err != nil {
		return err
	}

	requestVersion := s.versionFromHeader(req.Header)
	if requestVersion != "" && requestVersion != key.Version {
		return fmt.Errorf("%w: request=%q edge=%q", ErrNormalizationVersionMismatch, requestVersion, key.Version)
	}

	header := canonicalHeaderClone(resp.Header)
	if !responseStatusCacheable(resp.StatusCode) {
		return ErrNotCacheable
	}
	vary, wildcard, err := parseVary(header)
	if err != nil {
		return err
	}
	if wildcard {
		return ErrNotCacheable
	}

	now := s.options.Now()
	ttl, cacheable := sharedResponsePolicy(header, now, s.options.DefaultTTL)
	if !cacheable {
		return ErrNotCacheable
	}

	responseVersion := metadata.NormalizationVersion
	if headerVersion := s.versionFromHeader(header); headerVersion != "" {
		if responseVersion != "" && responseVersion != headerVersion {
			return fmt.Errorf("%w: metadata=%q response=%q", ErrNormalizationVersionMismatch, responseVersion, headerVersion)
		}
		responseVersion = headerVersion
	}
	if responseVersion == "" {
		responseVersion = key.Version
	}
	if responseVersion != key.Version {
		return fmt.Errorf("%w: response=%q edge=%q", ErrNormalizationVersionMismatch, responseVersion, key.Version)
	}
	if requestVersion != "" && responseVersion != "" && requestVersion != responseVersion {
		return fmt.Errorf("%w: request=%q response=%q", ErrNormalizationVersionMismatch, requestVersion, responseVersion)
	}

	digestValue, err := s.selectDigest(header, metadata.KeyDigest)
	if err != nil {
		return err
	}
	expectedDigest, err := parseDigest(digestValue)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrKeyDigestMismatch, err)
	}
	if subtle.ConstantTimeCompare(expectedDigest[:], key.Digest[:]) != 1 {
		return ErrKeyDigestMismatch
	}

	body, err := s.readBody(resp)
	if err != nil {
		return err
	}

	variantKey := variantKey(key, vary, req.Header)
	entry := &cacheEntry{
		fullID:          key.Hex() + "." + variantKey.Hex(),
		key:             variantKey,
		baseKey:         key,
		variantDigest:   variantKey.Digest,
		vary:            append([]string(nil), vary...),
		header:          header,
		body:            body,
		status:          responseStatus(resp),
		statusCode:      resp.StatusCode,
		proto:           responseProto(resp),
		protoMajor:      responseProtoMajor(resp),
		protoMinor:      responseProtoMinor(resp),
		requestVersion:  key.Version,
		responseVersion: responseVersion,
		storedAt:        now,
		expiresAt:       now.Add(ttl),
	}
	s.insert(entry)
	return nil
}

// PutResponse verifies metadata from resp's headers and uses resp.Request.
func (s *Store) PutResponse(resp *http.Response) error {
	if resp == nil || resp.Request == nil {
		return errors.New("netsec: response has no request")
	}
	return s.Put(resp.Request, resp, "")
}

// Lookup returns the matching variant and a fresh response object. The response
// body is an independent bytes-backed reader.
func (s *Store) Lookup(req *http.Request) (CacheHit, bool) {
	if s == nil {
		return CacheHit{}, false
	}
	return s.LookupFor(req, s.builder.Identity())
}

// LookupFor is Lookup for an explicit site and tenant identity.
func (s *Store) LookupFor(req *http.Request, identity Identity) (CacheHit, bool) {
	if s == nil || req == nil || requestBypassesCache(req) {
		return CacheHit{}, false
	}

	key, err := s.builder.BuildFor(req, identity)
	if err != nil {
		return CacheHit{}, false
	}
	requestVersion := s.versionFromHeader(req.Header)
	if requestVersion != "" && requestVersion != key.Version {
		return CacheHit{}, false
	}

	now := s.options.Now()
	s.mu.RLock()
	candidates := append([]*cacheEntry(nil), s.byBase[key.Hex()]...)
	s.mu.RUnlock()

	for _, entry := range candidates {
		if entry == nil || !now.Before(entry.expiresAt) {
			continue
		}
		if entry.requestVersion != key.Version || entry.responseVersion != key.Version {
			continue
		}
		candidateVariant := variantKey(key, entry.vary, req.Header)
		if candidateVariant.Digest != entry.variantDigest {
			continue
		}
		if !responseHeaderStillShareable(entry.header) {
			continue
		}

		return CacheHit{
			Response:  entry.response(req, now),
			Key:       entry.key,
			BaseKey:   entry.baseKey,
			Vary:      append([]string(nil), entry.vary...),
			StoredAt:  entry.storedAt,
			ExpiresAt: entry.expiresAt,
		}, true
	}
	return CacheHit{}, false
}

// Get is Lookup with only the response returned.
func (s *Store) Get(req *http.Request) (*http.Response, bool) {
	hit, ok := s.Lookup(req)
	if !ok {
		return nil, false
	}
	return hit.Response, true
}

// GetFor is LookupFor with only the response returned.
func (s *Store) GetFor(req *http.Request, identity Identity) (*http.Response, bool) {
	hit, ok := s.LookupFor(req, identity)
	if !ok {
		return nil, false
	}
	return hit.Response, true
}

// Len returns the number of stored variants, including not-yet-evicted stale
// entries.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Purge removes every stored variant.
func (s *Store) Purge() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]*cacheEntry)
	s.byBase = make(map[string][]*cacheEntry)
}

func (s *Store) selectDigest(header http.Header, explicit string) (string, error) {
	headerDigest := firstHeaderValueAny(header,
		s.options.DigestHeader,
		HeaderCacheKeyDigest,
		"Cache-Key-Digest",
		"X-Origin-Key-Digest",
	)
	if explicit == "" && headerDigest == "" {
		return "", ErrMissingKeyDigest
	}
	if explicit != "" && headerDigest != "" {
		explicitBytes, explicitErr := parseDigest(explicit)
		headerBytes, headerErr := parseDigest(headerDigest)
		if explicitErr != nil || headerErr != nil || explicitBytes != headerBytes {
			return "", fmt.Errorf("%w: explicit and response header digests differ", ErrKeyDigestMismatch)
		}
	}
	if explicit != "" {
		return explicit, nil
	}
	return headerDigest, nil
}

func (s *Store) versionFromHeader(header http.Header) string {
	return firstHeaderValueAny(header,
		s.options.VersionHeader,
		HeaderNormalizationVersion,
		"X-Cache-Normalization-Version",
		"X-Normalization-Version",
	)
}

func (s *Store) readBody(resp *http.Response) ([]byte, error) {
	if resp.Body == nil {
		return nil, nil
	}

	limit := s.options.MaxBodyBytes
	if limit < 0 {
		body, err := io.ReadAll(resp.Body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("netsec: read response body: %w", err)
		}
		return body, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(body)) > limit {
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
		return nil, ErrBodyTooLarge
	}
	if err != nil {
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
		return nil, fmt.Errorf("netsec: read response body: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func (s *Store) insert(entry *cacheEntry) {
	now := s.options.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeExpiredLocked(now)

	// A resource that changes its Vary scope invalidates older variants;
	// retaining them could serve data selected under the wrong header policy.
	for _, existing := range append([]*cacheEntry(nil), s.byBase[entry.baseKey.Hex()]...) {
		if !sameStrings(existing.vary, entry.vary) {
			s.removeEntryLocked(existing)
		}
	}
	if existing := s.entries[entry.fullID]; existing != nil {
		s.removeEntryLocked(existing)
	}

	if s.options.MaxEntries > 0 {
		for len(s.entries) >= s.options.MaxEntries {
			s.removeOldestLocked()
		}
	}

	s.entries[entry.fullID] = entry
	baseID := entry.baseKey.Hex()
	s.byBase[baseID] = append(s.byBase[baseID], entry)
}

func (s *Store) removeExpiredLocked(now time.Time) {
	for _, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			s.removeEntryLocked(entry)
		}
	}
}

func (s *Store) removeOldestLocked() {
	var oldest *cacheEntry
	for _, entry := range s.entries {
		if oldest == nil || entry.storedAt.Before(oldest.storedAt) {
			oldest = entry
		}
	}
	if oldest != nil {
		s.removeEntryLocked(oldest)
	}
}

func (s *Store) removeEntryLocked(entry *cacheEntry) {
	if entry == nil {
		return
	}
	delete(s.entries, entry.fullID)

	baseID := entry.baseKey.Hex()
	variants := s.byBase[baseID]
	filtered := variants[:0]
	for _, candidate := range variants {
		if candidate != entry {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		delete(s.byBase, baseID)
		return
	}
	s.byBase[baseID] = filtered
}

func (e *cacheEntry) response(req *http.Request, now time.Time) *http.Response {
	header := e.header.Clone()
	age := int64(now.Sub(e.storedAt) / time.Second)
	if age < 0 {
		age = 0
	}
	header.Set("Age", strconv.FormatInt(age, 10))
	header.Set("Content-Length", strconv.Itoa(len(e.body)))

	return &http.Response{
		Status:        e.status,
		StatusCode:    e.statusCode,
		Proto:         e.proto,
		ProtoMajor:    e.protoMajor,
		ProtoMinor:    e.protoMinor,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(e.body)),
		ContentLength: int64(len(e.body)),
		Request:       req,
	}
}

func variantKey(base Key, vary []string, header http.Header) Key {
	preimage := make([]byte, 0, 128)
	preimage = append(preimage, "netsec-shared-cache-variant\x00"...)
	preimage = appendString(preimage, base.Hex())
	preimage = appendString(preimage, base.Version)
	preimage = appendString(preimage, strconv.Itoa(len(vary)))
	for _, name := range vary {
		preimage = appendHeader(preimage, name, header)
	}

	digest := sha256.Sum256(preimage)
	return Key{
		Digest:           digest,
		NormalizedTarget: base.NormalizedTarget,
		NormalizedQuery:  base.NormalizedQuery,
		Version:          base.Version,
	}
}

func parseVary(header http.Header) ([]string, bool, error) {
	seen := make(map[string]struct{})
	var vary []string

	values, present := headerValues(header, "Vary")
	if !present {
		return nil, false, nil
	}
	for _, value := range values {
		for _, name := range splitHTTPList(value) {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if name == "*" {
				return nil, true, nil
			}
			if !validHeaderName(name) {
				return nil, false, fmt.Errorf("%w: invalid Vary header name %q", ErrNotCacheable, name)
			}
			name = strings.ToLower(name)
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			vary = append(vary, name)
		}
	}
	sort.Strings(vary)
	return vary, false, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		character := name[i]
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)):
		default:
			return false
		}
	}
	return true
}

func parseDigest(value string) ([32]byte, error) {
	var digest [32]byte
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"`)
	lower := strings.ToLower(value)
	for _, prefix := range []string{"sha256:", "sha256=", "sha-256:", "sha-256="} {
		if strings.HasPrefix(lower, prefix) {
			value = value[len(prefix):]
			break
		}
	}
	value = strings.TrimSpace(value)

	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == len(digest) {
		copy(digest[:], decoded)
		return digest, nil
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == len(digest) {
			copy(digest[:], decoded)
			return digest, nil
		}
	}
	return digest, errors.New("digest must be a SHA-256 hex or base64 value")
}

func responseStatus(resp *http.Response) string {
	if resp.Status != "" {
		return resp.Status
	}
	text := http.StatusText(resp.StatusCode)
	if text == "" {
		return strconv.Itoa(resp.StatusCode)
	}
	return strconv.Itoa(resp.StatusCode) + " " + text
}

func responseProto(resp *http.Response) string {
	if resp.Proto != "" {
		return resp.Proto
	}
	return "HTTP/1.1"
}

func responseProtoMajor(resp *http.Response) int {
	if resp.ProtoMajor != 0 {
		return resp.ProtoMajor
	}
	return 1
}

func responseProtoMinor(resp *http.Response) int {
	if resp.ProtoMinor != 0 {
		return resp.ProtoMinor
	}
	return 1
}

func responseHeaderStillShareable(header http.Header) bool {
	if hasHeader(header, "Set-Cookie") || hasHeader(header, "Set-Cookie2") {
		return false
	}
	cacheControl := parseCacheControl(headerValuesNoPresence(header, "Cache-Control"))
	for _, directive := range []string{"private", "no-store", "no-cache"} {
		if _, ok := cacheControl[directive]; ok {
			return false
		}
	}
	if strings.EqualFold(firstHeaderValue(header, "Pragma"), "no-cache") {
		return false
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
