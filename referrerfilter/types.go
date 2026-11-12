package referrerfilter

import (
	"context"
	"encoding/json"
)

// Media types used by the OCI image and distribution specifications.
const (
	MediaTypeOCIManifest      = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex         = "application/vnd.oci.image.index.v1+json"
	MediaTypeReferrersResults = "application/vnd.oci.image.index.v1+json"
)

// Descriptor describes a registry-stored manifest. The fields are unmarshaled
// directly from untrusted API responses and therefore never trusted without
// validation.
type Descriptor struct {
	MediaType    string            `json:"mediaType,omitempty"`
	Digest       Digest            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// Subject is the manifest field that binds an artifact to another manifest.
type Subject struct {
	MediaType    string            `json:"mediaType,omitempty"`
	Digest       Digest            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// imageManifest is the subset of application/vnd.oci.image.manifest.v1+json
// that is needed to validate referrers. Unknown fields are discarded.
type imageManifest struct {
	MediaType    string            `json:"mediaType,omitempty"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Subject      *Subject          `json:"subject,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
}

// indexManifest is the subset of application/vnd.oci.image.index.v1+json used
// by both Referrers API responses and fallback-tag manifests.
type indexManifest struct {
	MediaType   string            `json:"mediaType,omitempty"`
	Manifests   []Descriptor      `json:"manifests"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// AllowList is the set of artifact types a verification client is willing to
// consume. The zero value rejects everything, so callers must list the
// signature and attestation types they actually verify.
type AllowList struct {
	artifactTypes map[string]struct{}
}

// NewAllowList builds an AllowList from the permitted artifact type strings.
func NewAllowList(artifactTypes ...string) AllowList {
	allowed := make(map[string]struct{}, len(artifactTypes))
	for _, artifactType := range artifactTypes {
		if artifactType == "" {
			continue
		}
		allowed[artifactType] = struct{}{}
	}
	return AllowList{artifactTypes: allowed}
}

// Allows reports whether the artifact type is permitted.
func (a AllowList) Allows(artifactType string) bool {
	_, ok := a.artifactTypes[artifactType]
	return ok
}

// Verifier consumes a descriptor that has already passed every structural and
// binding check in this package. Implementations perform signature or
// attestation verification. FilterAndVerify guarantees this is never called
// for a filtered-out or invalid descriptor.
type Verifier interface {
	Verify(ctx context.Context, descriptor Descriptor, rawManifest []byte) error
}

// VerifierFunc adapts a plain function to the Verifier interface.
type VerifierFunc func(ctx context.Context, descriptor Descriptor, rawManifest []byte) error

// Verify implements Verifier.
func (f VerifierFunc) Verify(ctx context.Context, descriptor Descriptor, rawManifest []byte) error {
	return f(ctx, descriptor, rawManifest)
}

// VerifiedReference is a descriptor that survived all gates and was accepted
// by the Verifier.
type VerifiedReference struct {
	Descriptor  Descriptor
	RawManifest []byte
}

// Rejection reasons are stable strings suitable for metrics and tests. They
// intentionally do not leak manifest bytes.
const (
	// ReasonArtifactTypeNotAllowed: descriptor's artifactType is absent from
	// the allow list.
	ReasonArtifactTypeNotAllowed = "artifact_type_not_allowed"
	// ReasonBadDigest: descriptor digest is missing or malformed.
	ReasonBadDigest = "bad_digest"
	// ReasonFetchFailed: the registry could not return the manifest.
	ReasonFetchFailed = "fetch_failed"
	// ReasonDigestMismatch: fetched bytes do not hash to the descriptor digest.
	ReasonDigestMismatch = "digest_mismatch"
	// ReasonSizeMismatch: fetched byte count differs from the descriptor size.
	ReasonSizeMismatch = "size_mismatch"
	// ReasonMalformedManifest: bytes are not a JSON image manifest.
	ReasonMalformedManifest = "malformed_manifest"
	// ReasonSubjectMissing: the manifest carries no subject field.
	ReasonSubjectMissing = "subject_missing"
	// ReasonSubjectMismatch: the manifest subject is not the queried digest.
	ReasonSubjectMismatch = "subject_mismatch"
	// ReasonVerifyFailed: the Verifier rejected an otherwise valid reference.
	ReasonVerifyFailed = "verify_failed"
	// ReasonDuplicateDigest: an earlier descriptor with the same digest was
	// already processed.
	ReasonDuplicateDigest = "duplicate_digest"
)

// Rejection records why a descriptor was kept away from successful
// verification.
type Rejection struct {
	Descriptor Descriptor
	Reason     string
	Err        error
}

// Result is the outcome of filtering one subject's referrers.
type Result struct {
	// Verified contains every descriptor accepted by the Verifier, in the
	// order the registry first presented it.
	Verified []VerifiedReference
	// Rejected contains every descriptor that failed a gate or verification.
	Rejected []Rejection
	// FallbackUsed reports whether the sha256-<hex> tag replaced the
	// Referrers API.
	FallbackUsed bool
}

// Registry abstracts the two registry operations FilterAndVerify needs.
// HTTPRegistry provides the real implementation; tests use fakes.
type Registry interface {
	// Referrers returns the descriptors attached to subjectDigest. A nil
	// slice without error means no referrers were found.
	Referrers(ctx context.Context, repository string, subjectDigest Digest) ([]Descriptor, error)
	// FetchManifest returns the raw bytes of the manifest at descriptorDigest.
	FetchManifest(ctx context.Context, repository string, descriptorDigest Digest) ([]byte, error)
	// FetchManifestByTag returns the raw bytes of a manifest referenced by a
	// tag, notably the sha256-<hex> fallback tag.
	FetchManifestByTag(ctx context.Context, repository, tag string) ([]byte, error)
}

// parseImageManifest rejects anything that is not a JSON object carrying the
// image-manifest shape.
func parseImageManifest(raw []byte) (*imageManifest, error) {
	var manifest imageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// parseIndex parses a Referrers API response or a fallback-tag index.
func parseIndex(raw []byte) (*indexManifest, error) {
	var index indexManifest
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, err
	}
	return &index, nil
}
