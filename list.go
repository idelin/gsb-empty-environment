package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Object is one object returned by a listing.
type Object struct {
	Key          string            `json:"Key"`
	Size         int64             `json:"Size,omitempty"`
	ETag         string            `json:"ETag,omitempty"`
	LastModified string            `json:"LastModified,omitempty"`
	StorageClass string            `json:"StorageClass,omitempty"`
	Metadata     map[string]string `json:"Metadata,omitempty"`
}

// ObjectSource is the storage-facing listing contract used by List.
//
// ListObjects must return objects whose keys have prefix, strictly greater than
// after, in lexicographic order, and must treat prefix literally (no path
// normalization). It may return up to limit objects. An empty result means the
// source is exhausted for that prefix. The policy engine never exposes a key
// unless it separately authorizes GetObject on that exact key.
type ObjectSource interface {
	ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]Object, error)
}

// ListRequest is a secure bucket listing request.
type ListRequest struct {
	Bucket            string
	Prefix            string
	Delimiter         string
	MaxKeys           int
	ContinuationToken string
	Identity          Identity
	Conditions        ConditionValues
}

// ListResult is a filtered listing page. Only authorized object keys and
// common prefixes derived from authorized object keys are present.
type ListResult struct {
	Objects               []Object `json:"Objects"`
	CommonPrefixes        []string `json:"CommonPrefixes,omitempty"`
	IsTruncated           bool     `json:"IsTruncated"`
	NextContinuationToken string   `json:"NextContinuationToken,omitempty"`
	MaxKeys               int      `json:"MaxKeys"`
	Count                 int      `json:"Count"`
}

// Keys returns the authorized object keys in this page.
func (r ListResult) Keys() []string {
	out := make([]string, len(r.Objects))
	for i := range r.Objects {
		out[i] = r.Objects[i].Key
	}
	return out
}

type listTokenPayload struct {
	Version    string `json:"v"`
	Identity   string `json:"i"`
	Bucket     string `json:"b"`
	Prefix     string `json:"p"`
	Delimiter  string `json:"d"`
	Conditions string `json:"c,omitempty"`
	Scope      int    `json:"s"`
	After      string `json:"a,omitempty"`
	LastGroup  string `json:"g,omitempty"`
}

// List performs a bucket listing whose result is the intersection of:
//
//  1. an allowed ListBucket action for this principal and bucket;
//  2. an allowed GetObject action for every returned key, evaluated with the
//     same principal and condition context; and
//  3. the requested prefix and continuation position.
//
// A prefix, delimiter, or continuation token can only narrow that intersection.
// A client cannot use a prefix that escapes its granted key scope, obtain
// unauthorized CommonPrefixes, or replay a token under another principal,
// prefix, condition set, or policy version.
func (p *Policy) List(ctx context.Context, req ListRequest, source ObjectSource) (ListResult, error) {
	cp, err := p.snapshot()
	if err != nil {
		return emptyListResult(req), err
	}
	return cp.list(ctx, req, source)
}

func (cp *compiledPolicy) list(ctx context.Context, req ListRequest, source ObjectSource) (ListResult, error) {
	result := emptyListResult(req)
	if cp == nil {
		return result, ErrInvalidPolicy
	}
	if source == nil {
		return result, fmt.Errorf("%w: nil object source", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.Bucket) == "" {
		return result, fmt.Errorf("%w: missing bucket", ErrInvalidRequest)
	}
	if strings.ContainsRune(req.Bucket, '\x00') || strings.ContainsRune(req.Prefix, '\x00') {
		return result, fmt.Errorf("%w: key contains NUL", ErrInvalidRequest)
	}
	if unsafeObjectKey(req.Prefix) {
		// Treat path traversal in a listing prefix as an empty, non-expanded
		// scope rather than asking a storage backend that might normalize
		// paths to enumerate somewhere else.
		return result, nil
	}
	maxKeys, err := normalizeMaxKeys(req.MaxKeys)
	if err != nil {
		return result, err
	}
	req.MaxKeys = maxKeys
	result.MaxKeys = maxKeys

	listDecision := cp.authorize(Request{
		Action:            ActionListBucket,
		Bucket:            req.Bucket,
		Prefix:            req.Prefix,
		Delimiter:         req.Delimiter,
		MaxKeys:           maxKeys,
		ContinuationToken: req.ContinuationToken,
		Identity:          req.Identity,
		Conditions:        req.Conditions,
	}, nowFunc())
	if !listDecision.Allowed {
		cause := listDecision.Err
		if cause == nil {
			cause = ErrAccessDenied
		}
		return result, &AccessDeniedError{
			Action: ActionListBucket,
			Bucket: req.Bucket,
			Sid:    listDecision.Sid,
			Reason: listDecision.Reason,
			Cause:  cause,
		}
	}

	expected := listTokenPayload{
		Version:    cp.digest,
		Identity:   hashIdentity(req.Identity),
		Bucket:     req.Bucket,
		Prefix:     req.Prefix,
		Delimiter:  req.Delimiter,
		Conditions: hashConditions(req.Conditions),
	}
	state := expected
	if req.ContinuationToken != "" {
		decoded, err := cp.verifyListToken(req.ContinuationToken, expected)
		if err != nil {
			return result, err
		}
		state = decoded
	}

	readScopes := cp.readScopes(req.Identity, req.Bucket, nowFunc())
	if len(readScopes) == 0 {
		return result, nil
	}
	effectiveScopes := intersectListPrefixes(req.Prefix, readScopes)
	if len(effectiveScopes) == 0 {
		return result, nil
	}
	if state.Scope < 0 || state.Scope > len(effectiveScopes) {
		return result, fmt.Errorf("%w: token scope is out of range", ErrInvalidContinuationToken)
	}
	if state.Scope == len(effectiveScopes) {
		return result, nil
	}
	if state.After != "" && !strings.HasPrefix(state.After, effectiveScopes[state.Scope]) {
		return result, fmt.Errorf("%w: token position is outside the authorized prefix", ErrInvalidContinuationToken)
	}
	if state.LastGroup != "" && !strings.HasPrefix(state.LastGroup, req.Prefix) {
		return result, fmt.Errorf("%w: token group is outside the request prefix", ErrInvalidContinuationToken)
	}

	fetchSize := maxKeys
	if fetchSize < 100 {
		fetchSize = 100
	}
	if fetchSize > maxMaxKeys {
		fetchSize = maxMaxKeys
	}

	result.Objects = make([]Object, 0, maxKeys)
	result.CommonPrefixes = make([]string, 0)
	emittedGroups := make(map[string]struct{})
	if state.LastGroup != "" {
		// The group may span a page boundary. Do not emit it twice.
		emittedGroups[state.LastGroup] = struct{}{}
	}
	emittedObjects := make(map[string]struct{})
	entries := 0
	truncated := false

	for state.Scope < len(effectiveScopes) {
		if entries >= maxKeys {
			truncated = true
			break
		}
		scope := effectiveScopes[state.Scope]
		page, err := source.ListObjects(ctx, req.Bucket, scope, state.After, fetchSize)
		if err != nil {
			return emptyListResult(req), fmt.Errorf("netsec: list source: %w", err)
		}
		if len(page) == 0 {
			state.Scope++
			state.After = ""
			state.LastGroup = ""
			continue
		}

		stopped := false
		for _, object := range page {
			key := object.Key
			if key == "" || !strings.HasPrefix(key, scope) {
				return emptyListResult(req), fmt.Errorf(
					"%w: key %q is outside source prefix %q",
					ErrSourceContract, key, scope,
				)
			}
			if state.After != "" && key <= state.After {
				return emptyListResult(req), fmt.Errorf(
					"%w: key %q is not greater than previous key %q",
					ErrSourceContract, key, state.After,
				)
			}

			// Advance the raw source position even when the object is not
			// authorized. This prevents an unauthorized object from becoming
			// a permanent pagination obstacle.
			state.After = key

			group := commonPrefixForKey(req.Prefix, key, req.Delimiter)
			if group != "" {
				if _, seen := emittedGroups[group]; seen {
					continue
				}
				if !cp.objectAllowed(req, key, nowFunc()) {
					continue
				}
				if entries >= maxKeys {
					stopped = true
					break
				}
				result.CommonPrefixes = append(result.CommonPrefixes, group)
				emittedGroups[group] = struct{}{}
				state.LastGroup = group
				entries++
				if entries >= maxKeys {
					stopped = true
					break
				}
				continue
			}

			if !cp.objectAllowed(req, key, nowFunc()) {
				continue
			}
			if _, seen := emittedObjects[key]; seen {
				continue
			}
			if entries >= maxKeys {
				stopped = true
				break
			}
			result.Objects = append(result.Objects, object)
			emittedObjects[key] = struct{}{}
			entries++
			if entries >= maxKeys {
				stopped = true
				break
			}
		}
		if stopped {
			truncated = true
			break
		}
	}

	result.Count = len(result.Objects) + len(result.CommonPrefixes)
	if truncated && entries >= maxKeys {
		token, err := cp.signListToken(state)
		if err != nil {
			return emptyListResult(req), err
		}
		result.IsTruncated = true
		result.NextContinuationToken = token
	}
	return result, nil
}

func (cp *compiledPolicy) objectAllowed(req ListRequest, key string, now time.Time) bool {
	return cp.authorize(Request{
		Action:            ActionGetObject,
		Bucket:            req.Bucket,
		Key:               key,
		Prefix:            req.Prefix,
		Delimiter:         req.Delimiter,
		MaxKeys:           req.MaxKeys,
		ContinuationToken: req.ContinuationToken,
		Identity:          req.Identity,
		Conditions:        req.Conditions,
	}, now).Allowed
}

func (cp *compiledPolicy) readScopes(identity Identity, bucket string, now time.Time) []string {
	if identity.expired(now) {
		return nil
	}
	scopes := make([]string, 0)
	for index := range cp.statements {
		statement := &cp.statements[index]
		if statement.effect != EffectAllow {
			continue
		}
		if !statement.appliesToAction(ActionGetObject) {
			continue
		}
		if !statement.principals.matches(identity) {
			continue
		}
		if len(statement.resources) == 0 {
			// A NotResource-only statement otherwise covers all objects.
			scopes = append(scopes, "")
			continue
		}
		for _, pattern := range statement.resources {
			if !pattern.hasKey && !pattern.universal {
				continue
			}
			if !globMatch(pattern.bucket, bucket) {
				continue
			}
			scopes = append(scopes, pattern.fixedKeyPrefix())
		}
	}
	return normalizeScopes(scopes)
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	sort.Strings(scopes)
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if len(out) > 0 {
			last := out[len(out)-1]
			if scope == last || strings.HasPrefix(scope, last) {
				continue
			}
		}
		out = append(out, scope)
	}
	return out
}

func intersectListPrefixes(requestPrefix string, scopes []string) []string {
	effective := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		switch {
		case strings.HasPrefix(requestPrefix, scope):
			effective = append(effective, requestPrefix)
		case strings.HasPrefix(scope, requestPrefix):
			effective = append(effective, scope)
		}
	}
	return normalizeScopes(effective)
}

func commonPrefixForKey(requestPrefix, key, delimiter string) string {
	if delimiter == "" || !strings.HasPrefix(key, requestPrefix) {
		return ""
	}
	rest := key[len(requestPrefix):]
	index := strings.Index(rest, delimiter)
	if index < 0 {
		return ""
	}
	end := len(requestPrefix) + index + len(delimiter)
	return key[:end]
}

func normalizeMaxKeys(value int) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: max keys must not be negative", ErrInvalidRequest)
	}
	if value == 0 {
		return defaultMaxKeys, nil
	}
	if value > maxMaxKeys {
		return maxMaxKeys, nil
	}
	return value, nil
}

func emptyListResult(req ListRequest) ListResult {
	maxKeys := req.MaxKeys
	if maxKeys <= 0 || maxKeys > maxMaxKeys {
		maxKeys = defaultMaxKeys
	}
	return ListResult{
		Objects:        []Object{},
		CommonPrefixes: []string{},
		MaxKeys:        maxKeys,
	}
}

func hashIdentity(identity Identity) string {
	sum := sha256.Sum256([]byte(identity.fingerprint()))
	return hex.EncodeToString(sum[:])
}

func hashConditions(values ConditionValues) string {
	if len(values) == 0 {
		return ""
	}
	canonical := make(map[string][]string, len(values))
	for key, entries := range values {
		copied := append([]string(nil), entries...)
		sort.Strings(copied)
		canonical[key] = copied
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (cp *compiledPolicy) signListToken(state listTokenPayload) (string, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("%w: encode continuation token: %v", ErrInvalidContinuationToken, err)
	}
	payload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, cp.tokenKey[:])
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}

func (cp *compiledPolicy) verifyListToken(token string, expected listTokenPayload) (listTokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return listTokenPayload{}, fmt.Errorf("%w: malformed token", ErrInvalidContinuationToken)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return listTokenPayload{}, fmt.Errorf("%w: malformed payload", ErrInvalidContinuationToken)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return listTokenPayload{}, fmt.Errorf("%w: malformed signature", ErrInvalidContinuationToken)
	}
	mac := hmac.New(sha256.New, cp.tokenKey[:])
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return listTokenPayload{}, fmt.Errorf("%w: bad signature", ErrInvalidContinuationToken)
	}

	var decoded listTokenPayload
	if err := json.Unmarshal(payloadBytes, &decoded); err != nil {
		return listTokenPayload{}, fmt.Errorf("%w: malformed payload", ErrInvalidContinuationToken)
	}
	if decoded.Version != expected.Version ||
		decoded.Identity != expected.Identity ||
		decoded.Bucket != expected.Bucket ||
		decoded.Prefix != expected.Prefix ||
		decoded.Delimiter != expected.Delimiter ||
		decoded.Conditions != expected.Conditions {
		return listTokenPayload{}, fmt.Errorf("%w: token scope or policy does not match", ErrInvalidContinuationToken)
	}
	return decoded, nil
}

// MemorySource is a deterministic in-memory ObjectSource useful for gateways,
// tests, and local tooling.
type MemorySource struct {
	mu      sync.RWMutex
	objects map[string]map[string]Object
}

// NewMemorySource returns an empty in-memory source.
func NewMemorySource() *MemorySource {
	return &MemorySource{objects: make(map[string]map[string]Object)}
}

// Add adds or replaces an object.
func (m *MemorySource) Add(bucket string, object Object) *MemorySource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects == nil {
		m.objects = make(map[string]map[string]Object)
	}
	if m.objects[bucket] == nil {
		m.objects[bucket] = make(map[string]Object)
	}
	if object.Key == "" {
		return m
	}
	m.objects[bucket][object.Key] = object
	return m
}

// AddKey adds an object with only its key populated.
func (m *MemorySource) AddKey(bucket, key string) *MemorySource {
	return m.Add(bucket, Object{Key: key})
}

// Delete removes an object.
func (m *MemorySource) Delete(bucket, key string) *MemorySource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objects != nil && m.objects[bucket] != nil {
		delete(m.objects[bucket], key)
	}
	return m
}

// ListObjects implements ObjectSource.
func (m *MemorySource) ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]Object, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	if limit <= 0 {
		limit = defaultMaxKeys
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	byKey := m.objects[bucket]
	if len(byKey) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		if strings.HasPrefix(key, prefix) && key > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]Object, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out, nil
}
