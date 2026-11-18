package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrSignatureRequired is returned when policy requires a signature but
	// the attribute is a digest.
	ErrSignatureRequired = errors.New("netsec: signature required for path")
	// ErrUntrustedKeyID is returned when a signature was made by a key that
	// is not in the policy's trusted set.
	ErrUntrustedKeyID = errors.New("netsec: untrusted IMA signing key")
	// ErrNotRegular is returned for non-regular files, which are not valid
	// execution or mapping targets for this evaluator.
	ErrNotRegular = errors.New("netsec: not a regular file")
	// ErrFileChanged is returned when a file changed repeatedly while it was
	// being evaluated.
	ErrFileChanged = errors.New("netsec: file changed during evaluation")
	// ErrNilEvaluator is returned when a method is called on a nil evaluator.
	ErrNilEvaluator = errors.New("netsec: nil evaluator")
)

// Result describes the outcome of an evaluation. A result is allowed only
// when Allowed is true and Evaluate returned a nil error.
type Result struct {
	Path            string
	Allowed         bool
	Type            AttrType
	Algorithm       Algorithm
	KeyID           uint32
	SignatureLength uint16
	// Cached is true when the result came from the evaluator's cache rather
	// than from a fresh attribute read.
	Cached bool
}

type evaluatorCacheKey struct {
	path string
	attr string
}

type evaluatorCacheEntry struct {
	fingerprint fileFingerprint
	result      Result
	err         error
}

// Evaluator applies an IMA appraisal policy to files. It is safe for
// concurrent use.
type Evaluator struct {
	// Policy is the appraisal policy. Construct the evaluator with
	// NewEvaluator and use SetPolicy to change it after use.
	Policy Policy

	// XAttrName overrides the security attribute name. The default is
	// DefaultXAttrName ("security.ima").
	XAttrName string

	mu         sync.Mutex
	cache      map[evaluatorCacheKey]evaluatorCacheEntry
	generation uint64
}

// NewEvaluator returns an evaluator for policy. The trusted-key set is copied
// so later changes to the caller's map cannot silently weaken a running
// evaluator.
func NewEvaluator(policy Policy) *Evaluator {
	return &Evaluator{
		Policy: copyPolicy(policy),
		cache:  make(map[evaluatorCacheKey]evaluatorCacheEntry),
	}
}

// New is an alias for NewEvaluator.
func New(policy Policy) *Evaluator { return NewEvaluator(policy) }

// SetPolicy replaces the evaluator policy and drops every cached decision.
func (e *Evaluator) SetPolicy(policy Policy) {
	if e == nil {
		return
	}
	policy = copyPolicy(policy)

	e.mu.Lock()
	e.Policy = policy
	e.cache = make(map[evaluatorCacheKey]evaluatorCacheEntry)
	e.generation++
	e.mu.Unlock()
}

func copyPolicy(policy Policy) Policy {
	trusted := make(map[uint32]bool, len(policy.TrustedKeyIDs))
	for keyID, ok := range policy.TrustedKeyIDs {
		if ok {
			trusted[keyID] = true
		}
	}
	policy.TrustedKeyIDs = trusted
	return policy
}

func (e *Evaluator) attributeName() string {
	if e != nil && e.XAttrName != "" {
		return e.XAttrName
	}
	return DefaultXAttrName
}

// Evaluate reads and evaluates path's IMA attribute. A nil error means the
// file is allowed. Policy denials and malformed attributes are returned as
// errors so an allow decision can never be mistaken for a denial.
//
// Callers should invoke Evaluate immediately before execve or mmap. The cache
// is validated against the file's inode, size, mode, mtime and ctime on every
// call, so a write through this path or through a hard link invalidates the
// previous result.
func (e *Evaluator) Evaluate(path string) (Result, error) {
	if e == nil {
		return Result{Path: path}, ErrNilEvaluator
	}
	if path == "" {
		return Result{Path: path}, fmt.Errorf("%w: empty path", ErrMalformedAttribute)
	}

	policy, generation := e.snapshotPolicy()
	key := e.cacheKey(path)
	var lastErr error

	// A writer racing with evaluation is retried a bounded number of times.
	// The cached entry is keyed by the final, stable fingerprint.
	for attempt := 0; attempt < 3; attempt++ {
		before, err := os.Stat(path)
		if err != nil {
			return Result{Path: path}, fmt.Errorf("netsec: stat %q: %w", path, err)
		}
		if !before.Mode().IsRegular() {
			return Result{Path: path}, fmt.Errorf("%w: %q", ErrNotRegular, path)
		}
		fingerprint, ok := fingerprintOf(before)
		if !ok {
			return Result{Path: path}, fmt.Errorf("%w: cannot fingerprint %q", ErrFileChanged, path)
		}

		if result, err, hit := e.cacheLookup(key, fingerprint); hit {
			result.Path = path
			result.Cached = true
			return result, err
		}

		raw, err := readXAttr(path, key.attr)
		if err != nil {
			result := Result{Path: path}
			if errors.Is(err, ErrNoAttribute) {
				err = fmt.Errorf("netsec: %q: %w", path, err)
				e.cacheStore(generation, key, fingerprint, result, err)
				return result, err
			}
			return result, fmt.Errorf("netsec: read %s on %q: %w", key.attr, path, err)
		}

		after, err := os.Stat(path)
		if err != nil {
			return Result{Path: path}, fmt.Errorf("netsec: stat %q: %w", path, err)
		}
		afterFingerprint, ok := fingerprintOf(after)
		if !ok {
			return Result{Path: path}, fmt.Errorf("%w: cannot fingerprint %q", ErrFileChanged, path)
		}
		if afterFingerprint != fingerprint {
			lastErr = fmt.Errorf("%w: %q", ErrFileChanged, path)
			continue
		}

		result, evalErr := e.evaluateAttribute(policy, path, raw)
		result.Path = path
		result.Cached = false
		// evaluateAttribute performs no I/O: its result is deterministic for
		// this attribute and policy, so denials can be cached too.
		e.cacheStore(generation, key, fingerprint, result, evalErr)
		return result, evalErr
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("%w: %q", ErrFileChanged, path)
	}
	return Result{Path: path}, lastErr
}

// EvaluateAttribute applies the policy to an already-read attribute. It does
// not access the filesystem or the cache.
func (e *Evaluator) EvaluateAttribute(path string, raw []byte) (Result, error) {
	if e == nil {
		return Result{Path: path}, ErrNilEvaluator
	}
	policy, _ := e.snapshotPolicy()
	result, err := e.evaluateAttribute(policy, path, raw)
	result.Path = path
	return result, err
}

func (e *Evaluator) evaluateAttribute(policy Policy, path string, raw []byte) (Result, error) {
	result := Result{Path: path}
	if len(raw) == 0 {
		return result, fmt.Errorf("%w: empty value", ErrMalformedAttribute)
	}

	attrType := AttrType(raw[0])
	result.Type = attrType

	// Check policy before parsing the payload. A digest-only attribute is
	// never an acceptable substitute on a path that requires a signature,
	// even if the digest itself is well formed.
	if policy.RequiresSignature(path) && attrType != TypeSignature {
		return result, fmt.Errorf("%w: %q carries a %s attribute",
			ErrSignatureRequired, path, attrType)
	}

	attr, err := Parse(raw)
	if err != nil {
		return result, err
	}
	result.Type = attr.Type
	result.Algorithm = attr.Algorithm
	result.KeyID = attr.KeyID
	result.SignatureLength = attr.SignatureLength

	if attr.Type == TypeSignature {
		if !policy.TrustsKeyID(attr.KeyID) {
			return result, fmt.Errorf("%w: keyid 0x%08x", ErrUntrustedKeyID, attr.KeyID)
		}
		result.Allowed = true
		return result, nil
	}

	// Digest-only attributes are allowed only when the path does not require
	// a signature. Parse has already enforced a known algorithm and an exact
	// digest length.
	result.Allowed = true
	return result, nil
}

func (e *Evaluator) snapshotPolicy() (Policy, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Policy, e.generation
}

// Check returns nil only when path is allowed. Call it before execve or mmap.
func (e *Evaluator) Check(path string) error {
	_, err := e.Evaluate(path)
	return err
}

// BeforeExec is an explicit alias for Check for execution call sites.
func (e *Evaluator) BeforeExec(path string) error { return e.Check(path) }

// BeforeMmap is an explicit alias for Check for mapping call sites.
func (e *Evaluator) BeforeMmap(path string) error { return e.Check(path) }

// Allow reports whether path is currently allowed.
func (e *Evaluator) Allow(path string) bool {
	result, err := e.Evaluate(path)
	return err == nil && result.Allowed
}

func (e *Evaluator) cacheKey(path string) evaluatorCacheKey {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = filepath.Clean(path)
	}
	return evaluatorCacheKey{
		path: filepath.Clean(absolute),
		attr: e.attributeName(),
	}
}

func (e *Evaluator) cacheLookup(key evaluatorCacheKey, fingerprint fileFingerprint) (Result, error, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	entry, ok := e.cache[key]
	if !ok {
		return Result{}, nil, false
	}
	if entry.fingerprint != fingerprint {
		delete(e.cache, key)
		return Result{}, nil, false
	}
	return entry.result, entry.err, true
}

func (e *Evaluator) cacheStore(generation uint64, key evaluatorCacheKey, fingerprint fileFingerprint, result Result, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if generation != e.generation {
		return
	}
	if e.cache == nil {
		e.cache = make(map[evaluatorCacheKey]evaluatorCacheEntry)
	}
	result.Cached = false
	e.cache[key] = evaluatorCacheEntry{
		fingerprint: fingerprint,
		result:      result,
		err:         err,
	}
}

// Invalidate drops the cached result for path.
func (e *Evaluator) Invalidate(path string) {
	if e == nil {
		return
	}
	key := e.cacheKey(path)
	e.mu.Lock()
	delete(e.cache, key)
	e.mu.Unlock()
}

// InvalidateAll drops every cached result.
func (e *Evaluator) InvalidateAll() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.cache = make(map[evaluatorCacheKey]evaluatorCacheEntry)
	e.mu.Unlock()
}

// CacheSize reports the number of cached decisions.
func (e *Evaluator) CacheSize() int {
	if e == nil {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cache)
}

// Evaluate applies policy to path without retaining a cache.
func Evaluate(path string, policy Policy) (Result, error) {
	return NewEvaluator(policy).Evaluate(path)
}

// EvaluateAttribute applies policy to an already-read attribute without
// retaining a cache.
func EvaluateAttribute(path string, raw []byte, policy Policy) (Result, error) {
	return NewEvaluator(policy).EvaluateAttribute(path, raw)
}

// Check applies policy to path without retaining a cache.
func Check(path string, policy Policy) error {
	_, err := Evaluate(path, policy)
	return err
}
