package referrerfilter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPError carries the status information returned by a registry endpoint.
type HTTPError struct {
	Status     string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	message := "referrerfilter: registry request failed: " + e.Status
	if e.Body != "" {
		message += ": " + e.Body
	}
	return message
}

// statusError is the internal representation of a non-2xx response. It keeps
// the status code available so each API method can map 404 to its own
// sentinel (ErrReferrersUnsupported versus ErrManifestNotFound).
type statusError struct {
	statusCode int
	status     string
	body       string
}

func (e *statusError) Error() string {
	message := "referrerfilter: registry request failed: " + e.status
	if e.body != "" {
		message += ": " + e.body
	}
	return message
}

// HTTPRegistry is the Registry implementation for an OCI distribution
// registry reachable over HTTP(S).
type HTTPRegistry struct {
	// BaseURL is the registry root, for example https://registry.example.com.
	BaseURL string
	// HTTPClient performs requests. Defaults to http.DefaultClient when nil.
	HTTPClient *http.Client
	// Header is added to every request. Use it for Authorization headers.
	Header http.Header
}

func (r *HTTPRegistry) client() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}

// Referrers calls GET /v2/<name>/referrers/<digest>. A 404 means the endpoint
// or repository is absent; callers distinguish that through
// ErrReferrersUnsupported so the fallback tag can be tried.
func (r *HTTPRegistry) Referrers(ctx context.Context, repository string, subjectDigest Digest) ([]Descriptor, error) {
	path, err := url.JoinPath("/v2", repository, "referrers", subjectDigest.String())
	if err != nil {
		return nil, err
	}
	body, _, err := r.do(ctx, http.MethodGet, path)
	if err != nil {
		if statusErr, ok := err.(*statusError); ok && statusErr.statusCode == http.StatusNotFound {
			return nil, ErrReferrersUnsupported
		}
		if statusErr, ok := err.(*statusError); ok {
			return nil, &HTTPError{
				Status:     statusErr.status,
				StatusCode: statusErr.statusCode,
				Body:       statusErr.body,
			}
		}
		return nil, err
	}
	index, err := parseIndex(body)
	if err != nil {
		return nil, fmt.Errorf("referrerfilter: invalid referrers response: %w", err)
	}
	return index.Manifests, nil
}

// FetchManifest calls GET /v2/<name>/manifests/<digest>.
func (r *HTTPRegistry) FetchManifest(ctx context.Context, repository string, descriptorDigest Digest) ([]byte, error) {
	return r.fetchManifest(ctx, repository, descriptorDigest.String())
}

// FetchManifestByTag calls GET /v2/<name>/manifests/<tag>.
func (r *HTTPRegistry) FetchManifestByTag(ctx context.Context, repository, tag string) ([]byte, error) {
	if !validTag(tag) {
		return nil, fmt.Errorf("referrerfilter: invalid tag %q", tag)
	}
	return r.fetchManifest(ctx, repository, tag)
}

func (r *HTTPRegistry) fetchManifest(ctx context.Context, repository, reference string) ([]byte, error) {
	path, err := url.JoinPath("/v2", repository, "manifests", reference)
	if err != nil {
		return nil, err
	}
	body, contentType, err := r.do(ctx, http.MethodGet, path)
	if err != nil {
		if statusErr, ok := err.(*statusError); ok && statusErr.statusCode == http.StatusNotFound {
			return nil, ErrManifestNotFound
		}
		if statusErr, ok := err.(*statusError); ok {
			return nil, &HTTPError{
				Status:     statusErr.status,
				StatusCode: statusErr.statusCode,
				Body:       statusErr.body,
			}
		}
		return nil, err
	}
	mediaType := mediaTypeFromContentType(contentType)
	if mediaType != "" && !isAcceptedManifestType(mediaType) {
		return nil, fmt.Errorf("referrerfilter: unexpected manifest content type %q", mediaType)
	}
	return body, nil
}

func (r *HTTPRegistry) do(ctx context.Context, method, path string) ([]byte, string, error) {
	endpoint, err := url.JoinPath(r.BaseURL, path)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	for key, values := range r.Header {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}

	response, err := r.client().Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", &statusError{
			status:     response.Status,
			statusCode: response.StatusCode,
			body:       strings.TrimSpace(string(body)),
		}
	}
	return body, response.Header.Get("Content-Type"), nil
}

// maxResponseBytes bounds manifest bodies so a hostile registry cannot exhaust
// client memory on a single response.
const maxResponseBytes = 8 << 20

func mediaTypeFromContentType(contentType string) string {
	if contentType == "" {
		return ""
	}
	if semi := strings.IndexByte(contentType, ';'); semi >= 0 {
		contentType = contentType[:semi]
	}
	return strings.TrimSpace(contentType)
}

func isAcceptedManifestType(mediaType string) bool {
	switch mediaType {
	case MediaTypeOCIManifest, MediaTypeOCIIndex,
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1",
		"application/vnd.oci.image.index.v1":
		return true
	default:
		return false
	}
}

// validTag applies the distribution-spec tag grammar required for the
// sha256-<hex> fallback tag: [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}.
func validTag(tag string) bool {
	if tag == "" || len(tag) > 128 {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_':
		case i > 0 && (c == '.' || c == '-' || c == '_'):
		default:
			return false
		}
	}
	return true
}
