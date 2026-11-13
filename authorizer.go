package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Digest is an image content digest such as "sha256:0123...".
//
// Digests identify image content. Tags are deliberately not accepted as cache
// keys because a tag can be moved to different content.
type Digest string

// CredentialHash is a stable, non-reversible hash of a pull credential.
//
// The authorizer compares hashes and never stores the original credential.
// Callers may use their own hashing scheme, or HashCredential for a helper.
type CredentialHash string

// PullPolicy controls whether the local cache can satisfy a pull.
type PullPolicy uint8

const (
	// IfNotPresent permits a cached digest to be used after authorization.
	IfNotPresent PullPolicy = iota
	// Always requires the caller to pull from the source on every request.
	Always
)

// String implements fmt.Stringer.
func (p PullPolicy) String() string {
	switch p {
	case IfNotPresent:
		return "IfNotPresent"
	case Always:
		return "Always"
	default:
		return fmt.Sprintf("PullPolicy(%d)", uint8(p))
	}
}

// Request describes one image pull authorization check.
type Request struct {
	// Reference is the repository and optional tag being pulled. It is passed
	// to the verifier for diagnostics/source lookup and is never used as a
	// cache key.
	Reference string

	// Digest is the resolved content digest. Callers must resolve a tag to the
	// digest before constructing the request; a tag alone is not a digest.
	Digest Digest

	// Credential is the hash of the credential that will be used for this
	// pull. It is required for private images, even when the digest is cached.
	Credential CredentialHash

	// Anonymous marks an image that the caller knows is publicly pullable.
	// Anonymous pulls do not require or record a credential hash.
	Anonymous bool

	// Cached reports whether the node's image store currently has the digest.
	// The authorizer does not infer physical cache presence from its
	// authorization records.
	Cached bool

	// Policy selects the image pull policy. The zero value is IfNotPresent.
	Policy PullPolicy
}

// SourceRequest is supplied to a VerifyFunc after a cache authorization is
// missing. The verifier should ask the registry (or equivalent authority)
// whether the credential may pull this digest.
type SourceRequest struct {
	Reference  string
	Digest     Digest
	Credential CredentialHash
	Anonymous  bool
	Policy     PullPolicy
}

// VerifyFunc validates a pull credential against the source.
//
// It returns true when the credential is authorized for the digest. A false
// result is a normal authorization denial. A non-nil error means the source
// could not be consulted or the verification failed; the error is returned to
// the caller.
type VerifyFunc func(context.Context, SourceRequest) (bool, error)

// Decision is the result of an authorization check.
type Decision uint8

const (
	// Denied means the request may not use the cache or proceed with the pull.
	Denied Decision = iota
	// CacheHit means the caller may use the locally cached digest.
	CacheHit
	// SourceRequired means the caller must pull the digest from its source.
	SourceRequired
)

// String implements fmt.Stringer.
func (d Decision) String() string {
	switch d {
	case Denied:
		return "Denied"
	case CacheHit:
		return "CacheHit"
	case SourceRequired:
		return "SourceRequired"
	default:
		return fmt.Sprintf("Decision(%d)", uint8(d))
	}
}

var (
	// ErrInvalidDigest indicates that a request did not contain a content
	// digest in algorithm:hex form.
	ErrInvalidDigest = errors.New("netsec: digest must be an algorithm:hex content digest")
	// ErrMissingCredential indicates a private pull without a credential hash.
	ErrMissingCredential = errors.New("netsec: private image requires a credential hash")
	// ErrInvalidPolicy indicates an unsupported pull policy.
	ErrInvalidPolicy = errors.New("netsec: unsupported pull policy")
	// ErrNoVerifier indicates that source validation was required but no
	// verifier was supplied.
	ErrNoVerifier = errors.New("netsec: credential verifier is required")
)

// Authorizer stores digest-keyed cache authorizations. It is safe for
// concurrent use. All methods may be called from multiple goroutines.
type Authorizer struct {
	mu sync.Mutex

	// records maps a digest to the credential hashes that have been
	// authorized for it.
	records map[Digest]map[CredentialHash]struct{}

	// byCredential is a reverse index so revocation can remove one hash from
	// every digest without scanning the whole cache.
	byCredential map[CredentialHash]map[Digest]struct{}

	// credentialEpoch is incremented for every revocation of a hash. An
	// authorization that started before a revocation captures the old epoch
	// and must not write its result back into the cache.
	credentialEpoch map[CredentialHash]uint64

	// digestEpoch is incremented whenever a digest is forgotten. It prevents
	// an in-flight validation from recreating an entry after eviction.
	digestEpoch map[Digest]uint64
}

// New returns an empty Authorizer.
func New() *Authorizer {
	return &Authorizer{
		records:         make(map[Digest]map[CredentialHash]struct{}),
		byCredential:    make(map[CredentialHash]map[Digest]struct{}),
		credentialEpoch: make(map[CredentialHash]uint64),
		digestEpoch:     make(map[Digest]uint64),
	}
}

// Authorize checks whether req may use the cached digest.
//
// On a cache hit whose credential is not already authorized, verify is called
// against the source. If verification succeeds, the hash is added to the
// digest's authorization set; because the image is already cached, the
// decision is CacheHit, not SourceRequired. On a cache miss, an authorized
// credential still requires a source pull and the decision is SourceRequired.
//
// Always policy bypasses the cache and always returns SourceRequired for a
// successful (or anonymous) request. Private Always requests call verify on
// every invocation, even when the hash is already recorded.
//
// A nil verifier is only acceptable when no source validation is needed:
// anonymous requests, cache hits with an already-authorized hash, or source
// pulls whose hash is already authorized.
func (a *Authorizer) Authorize(ctx context.Context, req Request, verify VerifyFunc) (Decision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(req); err != nil {
		return Denied, err
	}
	if err := ctx.Err(); err != nil {
		return Denied, err
	}

	switch req.Policy {
	case Always:
		if req.Anonymous {
			return SourceRequired, nil
		}
		return a.validateAndRecord(ctx, req, verify, SourceRequired)

	case IfNotPresent:
		if req.Anonymous {
			if req.Cached {
				return CacheHit, nil
			}
			return SourceRequired, nil
		}

		if a.isAuthorized(req.Digest, req.Credential) {
			if req.Cached {
				return CacheHit, nil
			}
			return SourceRequired, nil
		}

		decision := SourceRequired
		if req.Cached {
			decision = CacheHit
		}
		return a.validateAndRecord(ctx, req, verify, decision)

	default:
		return Denied, ErrInvalidPolicy
	}
}

// Revoke removes cred from every digest authorization record and returns the
// number of digest records that contained it. It also invalidates any
// validation that started before this call, preventing an in-flight pull from
// restoring the revoked hash.
//
// A later validation that starts after Revoke may establish a new
// authorization if the source still accepts the credential.
func (a *Authorizer) Revoke(cred CredentialHash) int {
	if cred == "" {
		return 0
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.initLocked()
	a.credentialEpoch[cred]++

	digests := a.byCredential[cred]
	removed := 0
	for digest := range digests {
		record := a.records[digest]
		if record == nil {
			continue
		}
		if _, ok := record[cred]; ok {
			delete(record, cred)
			removed++
		}
		if len(record) == 0 {
			delete(a.records, digest)
		}
	}
	delete(a.byCredential, cred)

	return removed
}

// Forget removes the authorization record for digest, for example after the
// image store evicts the content. It returns whether a record existed.
func (a *Authorizer) Forget(digest Digest) bool {
	if digest == "" {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.initLocked()
	a.digestEpoch[digest]++

	record, ok := a.records[digest]
	if !ok {
		return false
	}
	for cred := range record {
		digests := a.byCredential[cred]
		delete(digests, digest)
		if len(digests) == 0 {
			delete(a.byCredential, cred)
		}
	}
	delete(a.records, digest)
	return true
}

// IsAuthorized reports whether cred is currently authorized for digest.
// It is intended for diagnostics, tests, and persistence checks.
func (a *Authorizer) IsAuthorized(digest Digest, cred CredentialHash) bool {
	if digest == "" || cred == "" {
		return false
	}
	return a.isAuthorized(digest, cred)
}

// HashCredential hashes a credential for storage in the authorization cache.
// Callers that already have a stable non-reversible credential identifier may
// supply it directly instead.
func HashCredential(credential string) CredentialHash {
	sum := sha256.Sum256([]byte("netsec-credential-v1\x00" + credential))
	return CredentialHash("sha256:" + hex.EncodeToString(sum[:]))
}

func (a *Authorizer) isAuthorized(digest Digest, cred CredentialHash) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.records[digest][cred]
	return ok
}

// validateAndRecord calls the source verifier outside the lock and records the
// credential only if no revocation or eviction raced with the validation.
func (a *Authorizer) validateAndRecord(ctx context.Context, req Request, verify VerifyFunc, decision Decision) (Decision, error) {
	if verify == nil {
		return Denied, ErrNoVerifier
	}

	credentialEpoch, digestEpoch := a.epochs(req.Digest, req.Credential)

	ok, err := verify(ctx, SourceRequest{
		Reference:  req.Reference,
		Digest:     req.Digest,
		Credential: req.Credential,
		Anonymous:  req.Anonymous,
		Policy:     req.Policy,
	})
	if err != nil {
		return Denied, fmt.Errorf("netsec: verify credential: %w", err)
	}
	if !ok {
		// An authoritative source denial invalidates any older cache
		// authorization for this digest and credential.
		a.removeAuthorization(req.Digest, req.Credential)
		return Denied, nil
	}
	if err := ctx.Err(); err != nil {
		return Denied, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.initLocked()
	if a.credentialEpoch[req.Credential] != credentialEpoch ||
		a.digestEpoch[req.Digest] != digestEpoch {
		return Denied, nil
	}

	record := a.records[req.Digest]
	if record == nil {
		record = make(map[CredentialHash]struct{})
		a.records[req.Digest] = record
	}
	record[req.Credential] = struct{}{}

	digests := a.byCredential[req.Credential]
	if digests == nil {
		digests = make(map[Digest]struct{})
		a.byCredential[req.Credential] = digests
	}
	digests[req.Digest] = struct{}{}

	return decision, nil
}

func (a *Authorizer) epochs(digest Digest, cred CredentialHash) (credential, content uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credentialEpoch[cred], a.digestEpoch[digest]
}

func (a *Authorizer) initLocked() {
	if a.records == nil {
		a.records = make(map[Digest]map[CredentialHash]struct{})
	}
	if a.byCredential == nil {
		a.byCredential = make(map[CredentialHash]map[Digest]struct{})
	}
	if a.credentialEpoch == nil {
		a.credentialEpoch = make(map[CredentialHash]uint64)
	}
	if a.digestEpoch == nil {
		a.digestEpoch = make(map[Digest]uint64)
	}
}

func (a *Authorizer) removeAuthorization(digest Digest, cred CredentialHash) {
	a.mu.Lock()
	defer a.mu.Unlock()

	record := a.records[digest]
	if record == nil {
		return
	}
	delete(record, cred)
	if len(record) == 0 {
		delete(a.records, digest)
	}

	digests := a.byCredential[cred]
	delete(digests, digest)
	if len(digests) == 0 {
		delete(a.byCredential, cred)
	}
}

func validateRequest(req Request) error {
	if !validDigest(req.Digest) {
		return ErrInvalidDigest
	}
	if req.Policy != IfNotPresent && req.Policy != Always {
		return ErrInvalidPolicy
	}
	if !req.Anonymous && req.Credential == "" {
		return ErrMissingCredential
	}
	return nil
}

// validDigest accepts content digests of the form algorithm:hex. It rejects
// ordinary repository:tag strings and bare tags, which are not content
// identities.
func validDigest(digest Digest) bool {
	s := string(digest)
	colon := strings.IndexByte(s, ':')
	if colon <= 0 || colon == len(s)-1 {
		return false
	}
	for _, r := range s[:colon] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '-' && r != '.' {
			return false
		}
	}
	for _, r := range s[colon+1:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}
