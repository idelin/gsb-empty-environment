package netsec

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Purpose identifies what a request is for. Credentials are bound to both an
// exact normalized source origin and a purpose, so a token issued for one kind
// of request is not reusable for another.
type Purpose string

const (
	// PurposeDownload is the conventional purpose for artifact downloads.
	PurposeDownload Purpose = "download"
	// PurposeMetadata is the conventional purpose for index or metadata reads.
	PurposeMetadata Purpose = "metadata"
)

// Scope is the exact key under which a credential may be used.
type Scope struct {
	Source  Origin
	Purpose Purpose
}

// Credential is caller-authored authorization data for one exact Scope.
// Headers are applied only after the destination has been normalized and
// matched against Source and Purpose.
type Credential struct {
	// Identity is an optional audit label. It never widens the scope.
	Identity string
	Source   Origin
	Purpose  Purpose
	Headers  http.Header
}

// Scope returns the exact source/purpose key for c.
func (c Credential) Scope() Scope {
	return Scope{Source: c.Source, Purpose: c.Purpose}
}

// Rule and Policy are aliases for Credential.
type Rule = Credential
type Policy = Credential

// RedirectEvent describes a credential decision for one redirect hop.
type RedirectEvent struct {
	From       Origin
	To         Origin
	Purpose    Purpose
	Authorized bool
	Identity   string
}

// Config is caller-owned redirect policy. Only credentials present here can
// authorize a request or a redirect hop.
type Config struct {
	// Credentials are exact (source, purpose) rules. Duplicate scopes are
	// rejected so credential selection is never ambiguous.
	Credentials []Credential

	// DefaultPurpose is used for requests that do not carry an explicit
	// purpose. An empty default means such requests receive no credentials
	// but can still follow redirects.
	DefaultPurpose Purpose

	// MaxRedirects is the maximum number of redirects to follow. Zero
	// defaults to 10.
	MaxRedirects int

	// OnRedirect, when set, observes each successful hop. It is called after
	// the destination credentials have been selected.
	OnRedirect func(RedirectEvent)
}

type ruleKey struct {
	source  Origin
	purpose Purpose
}

// Planner selects credentials for an initial request and for every redirect
// hop. It implements http.Client's CheckRedirect signature.
type Planner struct {
	rules          map[ruleKey]Credential
	managedHeaders map[string]struct{}
	defaultPurpose Purpose
	maxRedirects   int
	onRedirect     func(RedirectEvent)
}

// NewPlanner validates cfg and constructs a Planner.
func NewPlanner(cfg Config) (*Planner, error) {
	p := &Planner{
		rules:          make(map[ruleKey]Credential, len(cfg.Credentials)),
		managedHeaders: sensitiveHeaders(),
		defaultPurpose: cfg.DefaultPurpose,
		maxRedirects:   cfg.MaxRedirects,
		onRedirect:     cfg.OnRedirect,
	}
	if p.maxRedirects == 0 {
		p.maxRedirects = 10
	}
	if p.maxRedirects < 1 {
		return nil, fmt.Errorf("netsec: MaxRedirects must be positive")
	}

	for i, credential := range cfg.Credentials {
		source, err := credential.Source.Canonical()
		if err != nil {
			return nil, fmt.Errorf("netsec: credential %d source: %w", i, err)
		}
		if credential.Purpose == "" {
			return nil, fmt.Errorf("netsec: credential %d has empty purpose", i)
		}
		if len(credential.Headers) == 0 {
			return nil, fmt.Errorf("netsec: credential %d has no headers", i)
		}

		headers := make(http.Header, len(credential.Headers))
		for name, values := range credential.Headers {
			canonical, err := validateHeader(name, values)
			if err != nil {
				return nil, fmt.Errorf("netsec: credential %d: %w", i, err)
			}
			if _, duplicate := headers[canonical]; duplicate {
				return nil, fmt.Errorf("netsec: credential %d has duplicate header %q", i, canonical)
			}
			headers[canonical] = append([]string(nil), values...)
			p.managedHeaders[strings.ToLower(canonical)] = struct{}{}
		}

		credential.Source = source
		credential.Headers = headers
		key := ruleKey{source: source, purpose: credential.Purpose}
		if previous, exists := p.rules[key]; exists {
			return nil, fmt.Errorf(
				"netsec: duplicate credential scope %s (%q and %q)",
				key.source, previous.Identity, credential.Identity,
			)
		}
		p.rules[key] = credential
	}
	return p, nil
}

// New is an alias for NewPlanner.
func New(cfg Config) (*Planner, error) {
	return NewPlanner(cfg)
}

// NewRedirectPlanner is an alias for NewPlanner.
func NewRedirectPlanner(cfg Config) (*Planner, error) {
	return NewPlanner(cfg)
}

// CredentialFor returns the credential configured for the exact normalized
// origin and purpose. It never synthesizes a credential from request data.
func (p *Planner) CredentialFor(source Origin, purpose Purpose) (Credential, bool) {
	canonical, err := source.Canonical()
	if err != nil {
		return Credential{}, false
	}
	credential, ok := p.rules[ruleKey{source: canonical, purpose: purpose}]
	if !ok {
		return Credential{}, false
	}
	credential.Headers = credential.Headers.Clone()
	return credential, true
}

func validateHeader(name string, values []string) (string, error) {
	if !validHeaderName(name) {
		return "", fmt.Errorf("invalid header name %q", name)
	}
	canonical := http.CanonicalHeaderKey(name)
	switch strings.ToLower(canonical) {
	case "host", "content-length", "transfer-encoding", "connection",
		"trailer", "upgrade", "proxy-connection", "keep-alive", "te":
		return "", fmt.Errorf("header %q is not allowed in a credential", name)
	}
	if len(values) == 0 {
		return "", fmt.Errorf("header %q has no values", name)
	}
	for _, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return "", fmt.Errorf("header %q has a newline", name)
		}
	}
	return canonical, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)):
		default:
			return false
		}
	}
	return true
}

func sensitiveHeaders() map[string]struct{} {
	names := []string{
		"Host",
		"Authorization",
		"Proxy-Authorization",
		"Cookie",
		"Cookie2",
		"Www-Authenticate",
		"Proxy-Authenticate",
		"X-Api-Key",
		"X-Auth-Token",
		"X-Access-Token",
		"Private-Token",
		"X-Gitlab-Token",
		"X-Jfrog-Artifactory-Api-Key",
		"X-Nexus-Api-Key",
	}
	managed := make(map[string]struct{}, len(names))
	for _, name := range names {
		managed[strings.ToLower(name)] = struct{}{}
	}
	return managed
}

func (p *Planner) stripManagedHeaders(header http.Header) {
	if header == nil {
		return
	}
	names := make([]string, 0, len(header))
	for name := range header {
		if _, managed := p.managedHeaders[strings.ToLower(name)]; managed {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		delete(header, name)
	}
}

func applyHeaders(dst, src http.Header) {
	for name, values := range src {
		dst[name] = append([]string(nil), values...)
	}
}
