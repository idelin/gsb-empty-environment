package netsec

import "errors"

var (
	// ErrInvalidDigest indicates a digest outside the supported sha256 form.
	ErrInvalidDigest = errors.New("invalid digest")
	// ErrInvalidRepository indicates an invalid registry origin or name.
	ErrInvalidRepository = errors.New("invalid repository")
	// ErrInvalidLayer indicates a malformed layer descriptor.
	ErrInvalidLayer = errors.New("invalid layer")
	// ErrTooManyURLs indicates a layer with more than four external URLs.
	ErrTooManyURLs = errors.New("too many layer URLs")
	// ErrURLPolicy indicates that a URL was rejected by host policy.
	ErrURLPolicy = errors.New("URL rejected by policy")
	// ErrHTTPStatus indicates a non-success or unsupported HTTP response.
	ErrHTTPStatus = errors.New("unexpected HTTP status")
	// ErrSizeMismatch indicates that the response length did not equal the
	// size declared by the manifest.
	ErrSizeMismatch = errors.New("layer size mismatch")
	// ErrDigestMismatch indicates that downloaded bytes did not match the
	// manifest digest.
	ErrDigestMismatch = errors.New("layer digest mismatch")
	// ErrTokenScope indicates that the host's repository token does not
	// authorize a pull of the requested digest from the exact source.
	ErrTokenScope = errors.New("repository token scope does not cover digest pull")
	// ErrUnsafeExternalTransport indicates that the external HTTP client
	// cannot be proven free of client certificates.
	ErrUnsafeExternalTransport = errors.New("unsafe external transport")
	// ErrClosed indicates use of a closed Fetcher.
	ErrClosed = errors.New("fetcher is closed")
	// ErrInvalidPlan indicates a malformed or host-inconsistent fetch plan.
	ErrInvalidPlan = errors.New("invalid layer fetch plan")
	// ErrContentEncoding indicates a response body transformed by HTTP
	// content coding, which cannot be compared with the compressed digest.
	ErrContentEncoding = errors.New("unsupported content encoding")
)
