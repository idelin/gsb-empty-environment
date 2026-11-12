package referrerfilter

import (
	"context"
	"errors"
)

// ErrReferrersUnsupported signals that the registry does not implement the
// Referrers API and the sha256-<hex> fallback tag must be consulted.
var ErrReferrersUnsupported = sentinelError("referrerfilter: referrers API not supported by registry")

// ErrManifestNotFound signals that a manifest lookup returned not found.
var ErrManifestNotFound = sentinelError("referrerfilter: manifest not found")

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// FilterAndVerify fetches and filters the referrers of subjectDigest in
// repository. Every descriptor passes the allow list, digest integrity and
// subject-binding checks before verifier is invoked. The returned Result
// accounts for every descriptor the registry offered, including duplicates,
// which are verified once and reported again as rejected on repeats.
func FilterAndVerify(
	ctx context.Context,
	registry Registry,
	repository string,
	subjectDigest string,
	allowed AllowList,
	verifier Verifier,
) (*Result, error) {
	subject, err := ParseDigest(subjectDigest)
	if err != nil {
		return nil, err
	}

	descriptors, err := registry.Referrers(ctx, repository, subject)
	result := &Result{}
	if err != nil {
		if !errors.Is(err, ErrReferrersUnsupported) {
			return nil, err
		}
		fallbackDescriptors, fallbackErr := fallbackReferrers(ctx, registry, repository, subject)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		descriptors = fallbackDescriptors
		result.FallbackUsed = true
	}

	seen := make(map[string]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		// The allow list is checked before any network work so disallowed
		// artifacts can never influence what the verifier sees.
		if !allowed.Allows(descriptor.ArtifactType) {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonArtifactTypeNotAllowed,
			})
			continue
		}

		digest, digestErr := ParseDigest(descriptor.Digest.String())
		if digestErr != nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonBadDigest,
				Err:        digestErr,
			})
			continue
		}
		descriptor.Digest = digest

		if _, dup := seen[digest.String()]; dup {
			// A repeated digest describes content already processed; fetching
			// or verifying it again adds no security and amplifies registry
			// traffic, so the duplicate is rejected deterministically.
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonDuplicateDigest,
				Err:        sentinelError("referrerfilter: duplicate descriptor digest " + digest.String()),
			})
			continue
		}
		seen[digest.String()] = struct{}{}

		raw, fetchErr := registry.FetchManifest(ctx, repository, digest)
		if fetchErr != nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonFetchFailed,
				Err:        fetchErr,
			})
			continue
		}

		if descriptor.Size > 0 && descriptor.Size != int64(len(raw)) {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonSizeMismatch,
			})
			continue
		}

		actualDigest, digestErr := digestBytes(digest.Algorithm(), raw)
		if digestErr != nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonDigestMismatch,
				Err:        digestErr,
			})
			continue
		}
		if actualDigest.String() != digest.String() {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonDigestMismatch,
				Err:        sentinelError("referrerfilter: content digest " + actualDigest.String() + " != descriptor digest " + digest.String()),
			})
			continue
		}

		manifest, parseErr := parseImageManifest(raw)
		if parseErr != nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonMalformedManifest,
				Err:        parseErr,
			})
			continue
		}

		if manifest.Subject == nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonSubjectMissing,
			})
			continue
		}
		if manifest.Subject.Digest.String() != subject.String() {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonSubjectMismatch,
				Err:        sentinelError("referrerfilter: manifest subject " + manifest.Subject.Digest.String() + " != queried subject " + subject.String()),
			})
			continue
		}

		if verifyErr := verifier.Verify(ctx, descriptor, raw); verifyErr != nil {
			result.Rejected = append(result.Rejected, Rejection{
				Descriptor: descriptor,
				Reason:     ReasonVerifyFailed,
				Err:        verifyErr,
			})
			continue
		}

		result.Verified = append(result.Verified, VerifiedReference{
			Descriptor:  descriptor,
			RawManifest: raw,
		})
	}

	return result, nil
}

// fallbackReferrers implements the distribution-spec fallback: pull the tag
// sha256-<hex>, require an image index, and read its manifest entries.
func fallbackReferrers(ctx context.Context, registry Registry, repository string, subject Digest) ([]Descriptor, error) {
	if subject.Algorithm() != algSHA256 {
		return nil, sentinelError("referrerfilter: fallback tag is only defined for sha256 subjects, got " + subject.Algorithm())
	}
	tag := algSHA256 + "-" + subject.Hex()
	raw, err := registry.FetchManifestByTag(ctx, repository, tag)
	if err != nil {
		return nil, err
	}
	index, err := parseIndex(raw)
	if err != nil {
		return nil, err
	}
	if index.MediaType != "" && index.MediaType != MediaTypeOCIIndex {
		return nil, sentinelError("referrerfilter: fallback tag " + tag + " holds " + index.MediaType + ", not an image index")
	}
	return index.Manifests, nil
}
