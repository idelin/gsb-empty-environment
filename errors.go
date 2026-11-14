package netsec

import "errors"

var (
	// ErrInvalidReference means a tag or digest reference cannot be used.
	ErrInvalidReference = errors.New("invalid image reference")

	// ErrInvalidDigest means a digest is malformed or uses an unknown algorithm.
	ErrInvalidDigest = errors.New("invalid digest")

	// ErrUnsupportedDigestAlgorithm means the digest algorithm is not implemented.
	ErrUnsupportedDigestAlgorithm = errors.New("unsupported digest algorithm")

	// ErrDigestAlgorithmMismatch means one chain contains more than one digest algorithm.
	ErrDigestAlgorithmMismatch = errors.New("digest algorithm mismatch in verified chain")

	// ErrDigestMismatch means content does not hash to its descriptor digest.
	ErrDigestMismatch = errors.New("content digest mismatch")

	// ErrSizeMismatch means content length does not match its descriptor.
	ErrSizeMismatch = errors.New("content size mismatch")

	// ErrBlobNotFound means a referenced blob could not be obtained.
	ErrBlobNotFound = errors.New("blob not found")

	// ErrFetchFailed means the content source failed while fetching a blob.
	ErrFetchFailed = errors.New("blob fetch failed")

	// ErrInvalidManifest means an OCI/Docker JSON document is malformed.
	ErrInvalidManifest = errors.New("invalid image manifest")

	// ErrUnsupportedMediaType means the document type is not an OCI image or index.
	ErrUnsupportedMediaType = errors.New("unsupported media type")

	// ErrMediaTypeMismatch means a descriptor and its content disagree on media type.
	ErrMediaTypeMismatch = errors.New("manifest media type mismatch")

	// ErrNotImageManifest means an artifact, signature, or attestation cannot be run.
	ErrNotImageManifest = errors.New("manifest is not a runnable image")

	// ErrInvalidPlatform means the requested platform is incomplete.
	ErrInvalidPlatform = errors.New("invalid platform")

	// ErrPlatformNotFound means no runnable child matches the requested platform.
	ErrPlatformNotFound = errors.New("platform not found")

	// ErrAmbiguousPlatform means a request with an omitted OS version matches multiple children.
	ErrAmbiguousPlatform = errors.New("platform selection is ambiguous")

	// ErrDuplicatePlatform means an index contains duplicate runnable platforms.
	ErrDuplicatePlatform = errors.New("duplicate platform in image index")

	// ErrMissingLayer means a layer referenced by the verified manifest is unavailable.
	ErrMissingLayer = errors.New("image layer is missing")

	// ErrLayerCountMismatch means the config and manifest disagree on layer count.
	ErrLayerCountMismatch = errors.New("layer count does not match image config")

	// ErrConfigPlatformMismatch means the config contradicts its selected platform.
	ErrConfigPlatformMismatch = errors.New("image config platform mismatch")

	// ErrTagResolutionFailed means a tag could not be mapped to a digest.
	ErrTagResolutionFailed = errors.New("tag resolution failed")
)
