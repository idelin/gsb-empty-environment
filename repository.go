package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Repository identifies the registry source that owns a manifest. Origin is a
// canonical https origin and Name is the repository path, for example
// "team/application".
type Repository struct {
	Origin string
	Name   string
}

// NewRepository validates and canonicalizes a repository.
func NewRepository(origin, name string) (Repository, error) {
	r := Repository{Origin: origin, Name: name}
	return r.canonical()
}

// Validate reports whether r is a canonical, supported repository.
func (r Repository) Validate() error {
	_, err := r.canonical()
	return err
}

// Source returns the exact host-fixed repository source.
func (r Repository) Source() string {
	c, err := r.canonical()
	if err != nil {
		return ""
	}
	return c.Origin + "/" + c.Name
}

// BlobPath returns the distribution API path for a repository blob.
func (r Repository) BlobPath(d Digest) (string, error) {
	c, err := r.canonical()
	if err != nil {
		return "", err
	}
	if err := d.Validate(); err != nil {
		return "", err
	}
	return "/v2/" + c.Name + "/blobs/" + string(d), nil
}

// BlobURL returns the only valid in-repository URL for a layer blob.
func (r Repository) BlobURL(d Digest) (*url.URL, error) {
	c, err := r.canonical()
	if err != nil {
		return nil, err
	}
	blobPath, err := c.BlobPath(d)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(c.Origin)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRepository, err)
	}
	u.Path = blobPath
	return u, nil
}

// OriginURL returns the canonical repository origin.
func (r Repository) OriginURL() (*url.URL, error) {
	c, err := r.canonical()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(c.Origin)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRepository, err)
	}
	return u, nil
}

func (r Repository) canonical() (Repository, error) {
	origin, err := canonicalOrigin(r.Origin, true)
	if err != nil {
		return Repository{}, fmt.Errorf("%w: %v", ErrInvalidRepository, err)
	}
	if err := validateRepositoryName(r.Name); err != nil {
		return Repository{}, err
	}
	return Repository{Origin: origin, Name: r.Name}, nil
}

func validateRepositoryName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: repository name is empty", ErrInvalidRepository)
	}
	if len(name) > 255 {
		return fmt.Errorf("%w: repository name is too long", ErrInvalidRepository)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" {
			return fmt.Errorf("%w: repository name contains an empty component", ErrInvalidRepository)
		}
		if len(component) > 255 {
			return fmt.Errorf("%w: repository component is too long", ErrInvalidRepository)
		}
		if !validRepositoryComponent(component) {
			return fmt.Errorf("%w: invalid repository component %q", ErrInvalidRepository, component)
		}
	}
	return nil
}

func validRepositoryComponent(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	last := s[len(s)-1]
	if !isLowerAlphaNumeric(first) || !isLowerAlphaNumeric(last) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isLowerAlphaNumeric(c) || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func isLowerAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func canonicalOrigin(raw string, allowIP bool) (string, error) {
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return "", fmt.Errorf("origin contains whitespace or control characters")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin: %v", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return "", fmt.Errorf("origin must use https")
	}
	if u.User != nil {
		return "", fmt.Errorf("origin must not contain userinfo")
	}
	if u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") {
		return "", fmt.Errorf("origin must not contain a fragment")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("origin must not contain a query")
	}
	if u.Opaque != "" {
		return "", fmt.Errorf("opaque origins are not supported")
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("origin must not contain a path")
	}
	if u.Host == "" {
		return "", fmt.Errorf("origin has no host")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", fmt.Errorf("origin has an empty port")
	}

	host, err := normalizeHost(u.Hostname(), allowIP)
	if err != nil {
		return "", err
	}
	port, err := effectivePort(u.Port(), 443)
	if err != nil {
		return "", err
	}
	if u.Port() == "443" {
		port = 443
	}
	if port == 443 {
		if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
			return "https://[" + ip.String() + "]", nil
		}
		return "https://" + host, nil
	}
	return "https://" + net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func normalizeHost(host string, allowIP bool) (string, error) {
	if host == "" {
		return "", fmt.Errorf("host is empty")
	}
	if strings.ContainsAny(host, "\x00\r\n\t /@") {
		return "", fmt.Errorf("invalid host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !allowIP {
			return "", fmt.Errorf("IP literal hosts are not allowed")
		}
		return ip.String(), nil
	}
	lower := strings.ToLower(host)
	lower = strings.TrimSuffix(lower, ".")
	if lower == "" {
		return "", fmt.Errorf("host is empty")
	}
	if looksNumericHost(lower) {
		return "", fmt.Errorf("IP-like host is not allowed")
	}
	labels := strings.Split(lower, ".")
	if len(labels) > 0 && len(lower) > 253 {
		return "", fmt.Errorf("host is too long")
	}
	for _, label := range labels {
		if !validDNSLabel(label) {
			return "", fmt.Errorf("invalid DNS label %q", label)
		}
	}
	return lower, nil
}

func validDNSLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	for i, c := range label {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
			if i == 0 || i == len(label)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func looksNumericHost(host string) bool {
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if strings.HasPrefix(label, "0x") || strings.HasPrefix(label, "0X") {
			if len(label) == 2 {
				return false
			}
			for _, c := range label[2:] {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
					return false
				}
			}
			continue
		}
		for _, c := range label {
			if c < '0' || c > '9' {
				return false
			}
		}
		if label == "" {
			return false
		}
	}
	return true
}

func effectivePort(rawPort string, defaultPort int) (int, error) {
	if rawPort == "" {
		return defaultPort, nil
	}
	for _, c := range rawPort {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid port %q", rawPort)
		}
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", rawPort)
	}
	return port, nil
}
