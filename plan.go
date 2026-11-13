package netsec

import (
	"fmt"
	"mime"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ManifestRef is the manifest identity fixed by the image host. Layer URLs
// can never change the repository or manifest digest carried here.
type ManifestRef struct {
	Repository Repository
	Digest     Digest
}

// Validate reports whether the manifest identity is well formed.
func (m ManifestRef) Validate() error {
	if err := m.Repository.Validate(); err != nil {
		return err
	}
	if err := m.Digest.Validate(); err != nil {
		return err
	}
	return nil
}

// Layer is a manifest layer descriptor. Digest is the sha256 digest of the
// compressed layer bytes and Size is the compressed size. URLs contains zero
// to four HTTPS locations listed by the manifest.
type Layer struct {
	Digest    Digest
	Size      int64
	MediaType string
	URLs      []string
}

// Validate reports whether the layer descriptor is well formed.
func (l Layer) Validate() error {
	if err := l.Digest.Validate(); err != nil {
		return err
	}
	if l.Size < 0 {
		return fmt.Errorf("%w: negative size", ErrInvalidLayer)
	}
	if l.MediaType == "" || strings.ContainsAny(l.MediaType, "\x00\r\n") {
		return fmt.Errorf("%w: invalid media type", ErrInvalidLayer)
	}
	if _, _, err := mime.ParseMediaType(l.MediaType); err != nil {
		return fmt.Errorf("%w: invalid media type: %v", ErrInvalidLayer, err)
	}
	if len(l.URLs) > 4 {
		return fmt.Errorf("%w: got %d URLs, maximum is 4", ErrTooManyURLs, len(l.URLs))
	}
	for _, raw := range l.URLs {
		if raw == "" {
			return fmt.Errorf("%w: empty layer URL", ErrInvalidLayer)
		}
		if strings.ContainsAny(raw, "\x00\r\n\t ") {
			return fmt.Errorf("%w: layer URL contains whitespace or control characters", ErrInvalidLayer)
		}
		if strings.Contains(raw, "#") {
			return fmt.Errorf("%w: layer URL contains a fragment", ErrInvalidLayer)
		}
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("%w: invalid layer URL: %v", ErrInvalidLayer, err)
		}
		if !strings.EqualFold(u.Scheme, "https") {
			return fmt.Errorf("%w: layer URL must use https", ErrInvalidLayer)
		}
		if u.Opaque != "" || u.Host == "" {
			return fmt.Errorf("%w: layer URL has no hierarchical host", ErrInvalidLayer)
		}
		if u.User != nil || u.Fragment != "" || u.RawFragment != "" {
			return fmt.Errorf("%w: layer URL has unsafe components", ErrInvalidLayer)
		}
	}
	return nil
}

// SourceKind describes who owns a fetch source.
type SourceKind uint8

const (
	// SourceRepository is the exact repository that owns the manifest.
	SourceRepository SourceKind = iota
	// SourceExternal is an HTTPS URL listed by the manifest.
	SourceExternal
)

func (k SourceKind) String() string {
	switch k {
	case SourceRepository:
		return "repository"
	case SourceExternal:
		return "external"
	default:
		return "unknown"
	}
}

// Source is one ordered candidate in a layer fetch plan.
type Source struct {
	Kind SourceKind
	URL  *url.URL
}

func (s Source) String() string {
	if s.URL == nil {
		return ""
	}
	return s.URL.Redacted()
}

// Clone returns a source with a private URL copy.
func (s Source) Clone() Source {
	out := s
	if s.URL != nil {
		u := *s.URL
		out.URL = &u
	}
	return out
}

// LayerPlan is the host-fixed layer identity plus the ordered source list.
// When URLs are present, only those URLs are candidates. When the list is
// empty, the sole candidate is the exact repository blob URL.
type LayerPlan struct {
	Manifest ManifestRef
	Layer    Layer
	Sources  []Source
}

// Validate rechecks the plan without trusting manifest-provided fields.
func (p *LayerPlan) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil plan", ErrInvalidPlan)
	}
	if err := p.Manifest.Validate(); err != nil {
		return err
	}
	if err := p.Layer.Validate(); err != nil {
		return err
	}
	if len(p.Sources) == 0 {
		return fmt.Errorf("%w: plan has no sources", ErrInvalidPlan)
	}
	if len(p.Sources) > 4 {
		return fmt.Errorf("%w: plan has too many sources", ErrInvalidPlan)
	}
	repo, err := p.Manifest.Repository.canonical()
	if err != nil {
		return err
	}
	if len(p.Layer.URLs) == 0 {
		if len(p.Sources) != 1 || p.Sources[0].Kind != SourceRepository {
			return fmt.Errorf("%w: repository-only plan must have exactly one repository source", ErrInvalidPlan)
		}
		if !isRepositoryBlobURL(repo, p.Sources[0].URL, p.Layer.Digest) {
			return fmt.Errorf("%w: repository source is not the exact blob URL", ErrInvalidPlan)
		}
	} else if len(p.Sources) != len(p.Layer.URLs) {
		return fmt.Errorf("%w: source count does not match URL count", ErrInvalidPlan)
	}
	for i, src := range p.Sources {
		if src.URL == nil {
			return fmt.Errorf("%w: source %d has no URL", ErrInvalidPlan, i)
		}
		if !strings.EqualFold(src.URL.Scheme, "https") {
			return fmt.Errorf("%w: source %d is not https", ErrInvalidPlan, i)
		}
		if src.URL.User != nil || src.URL.Fragment != "" || src.URL.RawFragment != "" {
			return fmt.Errorf("%w: source %d has unsafe URL components", ErrInvalidPlan, i)
		}
	}
	return nil
}

// BuildLayerPlan validates a host-fixed manifest identity and a layer
// descriptor, then creates an ordered fetch plan. A layer listing at least one
// URL never implicitly gains a repository fallback.
func BuildLayerPlan(policy *ExternalURLPolicy, manifest ManifestRef, layer Layer) (*LayerPlan, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	repo, err := manifest.Repository.canonical()
	if err != nil {
		return nil, err
	}
	manifest.Repository = repo
	if err := layer.Validate(); err != nil {
		return nil, err
	}

	layerCopy := layer
	layerCopy.URLs = append([]string(nil), layer.URLs...)
	plan := &LayerPlan{Manifest: manifest, Layer: layerCopy}

	if len(layer.URLs) == 0 {
		u, err := repo.BlobURL(layer.Digest)
		if err != nil {
			return nil, err
		}
		plan.Sources = []Source{{Kind: SourceRepository, URL: u}}
		return plan, nil
	}
	for i, raw := range layer.URLs {
		if strings.ContainsAny(raw, "\x00\r\n\t ") {
			return nil, fmt.Errorf("%w: URL %d contains whitespace or control characters", ErrURLPolicy, i)
		}
		if strings.Contains(raw, "#") {
			return nil, fmt.Errorf("%w: URL %d contains a fragment", ErrURLPolicy, i)
		}
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: URL %d: %v", ErrURLPolicy, i, err)
		}
		if isRepositoryBlobURL(repo, u, layer.Digest) {
			if err := validateCommonHTTPSURL(u); err != nil {
				return nil, err
			}
			blobURL, err := repo.BlobURL(layer.Digest)
			if err != nil {
				return nil, err
			}
			blobURL.RawQuery = u.RawQuery
			blobURL.ForceQuery = u.ForceQuery
			plan.Sources = append(plan.Sources, Source{Kind: SourceRepository, URL: blobURL})
			continue
		}
		if isRepositoryOriginURL(repo, u) {
			if err := validateCommonHTTPSURL(u); err != nil {
				return nil, err
			}
			plan.Sources = append(plan.Sources, Source{Kind: SourceExternal, URL: cloneURL(u)})
			continue
		}
		if policy == nil {
			return nil, fmt.Errorf("%w: external layer URLs require a URL policy", ErrURLPolicy)
		}
		canonical, err := policy.ValidateURL(raw)
		if err != nil {
			return nil, fmt.Errorf("URL %d: %w", i, err)
		}
		plan.Sources = append(plan.Sources, Source{Kind: SourceExternal, URL: canonical})
	}
	return plan, nil
}

// BuildManifestPlans applies BuildLayerPlan to each layer in order.
func BuildManifestPlans(policy *ExternalURLPolicy, manifest ManifestRef, layers []Layer) ([]*LayerPlan, error) {
	plans := make([]*LayerPlan, 0, len(layers))
	for i, layer := range layers {
		plan, err := BuildLayerPlan(policy, manifest, layer)
		if err != nil {
			return nil, fmt.Errorf("layer %d: %w", i, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func isRepositoryBlobURL(repo Repository, u *url.URL, d Digest) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	expected, err := repo.BlobURL(d)
	if err != nil {
		return false
	}
	return sameOrigin(u, expected) && u.Path == expected.Path && u.EscapedPath() == expected.EscapedPath()
}

func isRepositoryOriginURL(repo Repository, u *url.URL) bool {
	if u == nil || !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	origin, err := repo.OriginURL()
	if err != nil {
		return false
	}
	return sameOrigin(u, origin)
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	ao, err := originKey(a)
	if err != nil {
		return false
	}
	bo, err := originKey(b)
	if err != nil {
		return false
	}
	return ao == bo
}

func originKey(u *url.URL) (string, error) {
	if u == nil || !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("%w: not https", ErrURLPolicy)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrURLPolicy)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	port, err := effectivePort(u.Port(), defaultHTTPSPort)
	if err != nil {
		return "", err
	}
	if port == defaultHTTPSPort {
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return "https://[" + ip.String() + "]", nil
		}
		return "https://" + host, nil
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func cloneURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	out := *u
	return &out
}
