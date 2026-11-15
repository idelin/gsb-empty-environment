package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Allowlist is the set of OCI artifactType values that may be passed to a
// signature or attestation verifier. A nil Allowlist allows nothing.
type Allowlist map[string]struct{}

// NewAllowlist constructs an allowlist from exact artifactType values. Empty
// entries are ignored.
func NewAllowlist(artifactTypes ...string) Allowlist {
	allowlist := make(Allowlist, len(artifactTypes))
	for _, artifactType := range artifactTypes {
		if artifactType != "" {
			allowlist[artifactType] = struct{}{}
		}
	}
	return allowlist
}

// Allows reports whether artifactType is in the allowlist.
func (allowlist Allowlist) Allows(artifactType string) bool {
	if artifactType == "" {
		return false
	}
	_, ok := allowlist[artifactType]
	return ok
}

// Rejection records one descriptor that was not eligible for verification.
type Rejection struct {
	Descriptor Descriptor
	Reason     string
	Err        error
}

func (rejection Rejection) Error() string {
	if rejection.Err != nil {
		return rejection.Reason + ": " + rejection.Err.Error()
	}
	return rejection.Reason
}

// Result contains the descriptors that passed filtering and diagnostics for
// every descriptor that was excluded.
type Result struct {
	Accepted []Descriptor
	Rejected []Rejection
}

type processor struct {
	ctx       context.Context
	subject   digestValue
	allowlist Allowlist
	fetcher   ManifestFetcher
	verifier  VerifyFunc

	fetched map[string]fetchedManifest
	seen    map[string]struct{}
	result  Result
	fatal   error
}

type fetchedManifest struct {
	content []byte
	err     error
}

// FilterReferrers filters a referrers API descriptor list. Only descriptors
// whose artifactType is allowlisted, whose fetched manifest hashes to the
// descriptor digest, and whose subject equals subject are returned. Invalid
// entries are skipped; an error is returned only for invalid arguments,
// cancellation, or a failing fetcher/verifier context.
func FilterReferrers(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
) ([]Descriptor, error) {
	result, err := FilterReferrersDetailed(ctx, subject, descriptors, allowlist, fetcher, nil)
	return result.Accepted, err
}

// FilterReferrersDetailed is FilterReferrers with rejection diagnostics.
func FilterReferrersDetailed(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	subjectDigest, err := parseDigest(subject)
	if err != nil {
		return Result{}, fmt.Errorf("netsec: subject digest: %w", err)
	}
	if fetcher == nil {
		return Result{}, errors.New("netsec: nil manifest fetcher")
	}

	p := newProcessor(ctx, subjectDigest, allowlist, fetcher, verifier)
	p.process(descriptors)
	return p.result, p.fatal
}

// FilterAndVerify filters descriptors and invokes verifier only for accepted
// entries. Entries rejected before verification, and entries whose verifier
// rejects them, are not returned.
func FilterAndVerify(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	if verifier == nil {
		return Result{}, errors.New("netsec: nil verifier")
	}
	return FilterReferrersDetailed(ctx, subject, descriptors, allowlist, fetcher, verifier)
}

// FilterReferrersAndVerify is an alias for FilterAndVerify.
func FilterReferrersAndVerify(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	return FilterAndVerify(ctx, subject, descriptors, allowlist, fetcher, verifier)
}

// FilterFallbackIndex applies the same checks to a legacy fallback index
// fetched from the sha256-<hex> tag.
func FilterFallbackIndex(
	ctx context.Context,
	subject string,
	indexContent []byte,
	allowlist Allowlist,
	fetcher ManifestFetcher,
) ([]Descriptor, error) {
	result, err := FilterFallbackIndexDetailed(ctx, subject, indexContent, allowlist, fetcher, nil)
	return result.Accepted, err
}

// FilterFallbackIndexDetailed is FilterFallbackIndex with rejection
// diagnostics.
func FilterFallbackIndexDetailed(
	ctx context.Context,
	subject string,
	indexContent []byte,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	subjectDigest, err := parseDigest(subject)
	if err != nil {
		return Result{}, fmt.Errorf("netsec: subject digest: %w", err)
	}
	if fetcher == nil {
		return Result{}, errors.New("netsec: nil manifest fetcher")
	}
	index, err := parseIndex(indexContent)
	if err != nil {
		return Result{}, err
	}

	p := newProcessor(ctx, subjectDigest, allowlist, fetcher, verifier)
	p.process(index.Manifests)
	return p.result, p.fatal
}

// FallbackTag returns the legacy referrers tag for subject, for example
// sha256:abcd... becomes sha256-abcd...
func FallbackTag(subject string) (string, error) {
	subjectDigest, err := parseDigest(subject)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(subjectDigest.String(), ":", "-"), nil
}

// FetchFallbackIndex fetches and returns the raw fallback index manifest.
func FetchFallbackIndex(
	ctx context.Context,
	subject string,
	fetcher ManifestFetcher,
) ([]byte, error) {
	if fetcher == nil {
		return nil, errors.New("netsec: nil manifest fetcher")
	}
	tag, err := FallbackTag(subject)
	if err != nil {
		return nil, err
	}
	return fetcher.FetchManifest(ctx, tag)
}

// FilterFallback fetches the legacy fallback index and filters its descriptors.
func FilterFallback(
	ctx context.Context,
	subject string,
	allowlist Allowlist,
	fetcher ManifestFetcher,
) ([]Descriptor, error) {
	indexContent, err := FetchFallbackIndex(ctx, subject, fetcher)
	if err != nil {
		return nil, err
	}
	return FilterFallbackIndex(ctx, subject, indexContent, allowlist, fetcher)
}

// FilterFallbackAndVerify fetches the legacy fallback index, filters it, and
// invokes verifier only for accepted descriptors.
func FilterFallbackAndVerify(
	ctx context.Context,
	subject string,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	indexContent, err := FetchFallbackIndex(ctx, subject, fetcher)
	if err != nil {
		return Result{}, err
	}
	return FilterFallbackIndexDetailed(ctx, subject, indexContent, allowlist, fetcher, verifier)
}

// FilterReferrersWithFallback filters referrers when present. If the referrers
// list is empty (for example because the registry does not implement the
// referrers API), it falls back to the sha256-<hex> index tag.
func FilterReferrersWithFallback(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
) ([]Descriptor, error) {
	result, err := FilterReferrersWithFallbackDetailed(ctx, subject, descriptors, allowlist, fetcher, nil)
	return result.Accepted, err
}

// FilterReferrersWithFallbackDetailed is FilterReferrersWithFallback with
// rejection diagnostics and an optional verifier.
func FilterReferrersWithFallbackDetailed(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) (Result, error) {
	if len(descriptors) > 0 {
		return FilterReferrersDetailed(ctx, subject, descriptors, allowlist, fetcher, verifier)
	}
	return FilterFallbackAndVerify(ctx, subject, allowlist, fetcher, verifier)
}

// FilterSources applies filtering to an optional referrers response and an
// optional fallback index, deduplicating descriptors by digest across both.
func FilterSources(
	ctx context.Context,
	subject string,
	descriptors []Descriptor,
	fallbackIndex []byte,
	allowlist Allowlist,
	fetcher ManifestFetcher,
) (Result, error) {
	subjectDigest, err := parseDigest(subject)
	if err != nil {
		return Result{}, fmt.Errorf("netsec: subject digest: %w", err)
	}
	if fetcher == nil {
		return Result{}, errors.New("netsec: nil manifest fetcher")
	}

	p := newProcessor(ctx, subjectDigest, allowlist, fetcher, nil)
	p.process(descriptors)
	if len(fallbackIndex) > 0 {
		index, err := parseIndex(fallbackIndex)
		if err != nil {
			return Result{}, err
		}
		p.process(index.Manifests)
	}
	return p.result, p.fatal
}

func newProcessor(
	ctx context.Context,
	subject digestValue,
	allowlist Allowlist,
	fetcher ManifestFetcher,
	verifier VerifyFunc,
) *processor {
	return &processor{
		ctx:       ctx,
		subject:   subject,
		allowlist: allowlist,
		fetcher:   fetcher,
		verifier:  verifier,
		fetched:   make(map[string]fetchedManifest),
		seen:      make(map[string]struct{}),
	}
}

func (p *processor) process(descriptors []Descriptor) {
	for _, descriptor := range descriptors {
		if err := p.ctx.Err(); err != nil {
			p.fatal = err
			return
		}
		p.processOne(descriptor)
		if p.fatal != nil {
			return
		}
	}
}

func (p *processor) processOne(descriptor Descriptor) {
	digest, err := parseDigest(descriptor.Digest)
	if err != nil {
		p.reject(descriptor, "invalid descriptor digest", err)
		return
	}
	key := digest.String()
	if _, ok := p.seen[key]; ok {
		p.reject(descriptor, "duplicate descriptor digest", nil)
		return
	}

	// A descriptor may omit artifactType (notably in legacy fallback indexes),
	// in which case the digest-verified manifest supplies it. If the descriptor
	// does claim a type, reject it before fetching anything else.
	if descriptor.ArtifactType != "" && !p.allowlist.Allows(descriptor.ArtifactType) {
		p.reject(descriptor, "artifactType is not allowlisted", nil)
		return
	}

	content, err := p.fetch(key)
	if err != nil {
		if p.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			p.fatal = err
			return
		}
		p.reject(descriptor, "fetch manifest", err)
		return
	}
	if err := VerifyDigest(key, content); err != nil {
		p.reject(descriptor, "manifest content does not match descriptor digest", err)
		return
	}
	if descriptor.Size > 0 && int64(len(content)) != descriptor.Size {
		p.reject(descriptor, "manifest size does not match descriptor", nil)
		return
	}

	manifest, err := parseManifest(content)
	if err != nil {
		p.reject(descriptor, "invalid manifest", err)
		return
	}
	if manifest.Subject == nil {
		p.reject(descriptor, "manifest has no subject", nil)
		return
	}
	manifestSubject, err := canonicalDigest(manifest.Subject.Digest)
	if err != nil {
		p.reject(descriptor, "manifest has invalid subject digest", err)
		return
	}
	if manifestSubject != p.subject.String() {
		p.reject(descriptor, "manifest subject does not match queried subject", nil)
		return
	}

	manifestType := manifest.ArtifactType
	if manifestType != "" {
		if !p.allowlist.Allows(manifestType) {
			p.reject(descriptor, "manifest artifactType is not allowlisted", nil)
			return
		}
		if descriptor.ArtifactType != "" && descriptor.ArtifactType != manifestType {
			p.reject(descriptor, "descriptor and manifest artifactType differ", nil)
			return
		}
	} else if descriptor.ArtifactType == "" {
		p.reject(descriptor, "manifest has no artifactType", nil)
		return
	}

	accepted := descriptor
	if accepted.ArtifactType == "" {
		accepted.ArtifactType = manifestType
	}
	if p.verifier != nil {
		if err := p.verifier(p.ctx, accepted, bytes.Clone(content)); err != nil {
			if p.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				p.fatal = err
				return
			}
			p.reject(descriptor, "verification failed", err)
			return
		}
	}

	p.seen[key] = struct{}{}
	p.result.Accepted = append(p.result.Accepted, accepted)
}

func (p *processor) fetch(digest string) ([]byte, error) {
	if cached, ok := p.fetched[digest]; ok {
		return cached.content, cached.err
	}
	content, err := p.fetcher.FetchManifest(p.ctx, digest)
	p.fetched[digest] = fetchedManifest{content: content, err: err}
	return content, err
}

func (p *processor) reject(descriptor Descriptor, reason string, err error) {
	p.result.Rejected = append(p.result.Rejected, Rejection{
		Descriptor: descriptor,
		Reason:     reason,
		Err:        err,
	})
}
