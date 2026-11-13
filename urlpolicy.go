package netsec

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	DefaultMaxRedirects = 5
	defaultHTTPSPort    = 443
)

// ExternalURLPolicy is the host-owned policy applied to every external URL
// and every redirect hop. HTTPS on port 443 is allowed by default. Any other
// port must be registered for the exact host first.
type ExternalURLPolicy struct {
	mu    sync.RWMutex
	ports map[string]map[int]struct{}
}

// NewExternalURLPolicy returns an empty policy.
func NewExternalURLPolicy() *ExternalURLPolicy {
	return &ExternalURLPolicy{ports: make(map[string]map[int]struct{})}
}

// RegisterOrigin registers the non-default port used by an external origin.
// Origins using port 443 do not need registration.
func (p *ExternalURLPolicy) RegisterOrigin(raw string) error {
	if p == nil {
		return fmt.Errorf("%w: nil policy", ErrURLPolicy)
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return fmt.Errorf("%w: origin contains whitespace or control characters", ErrURLPolicy)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid origin: %v", ErrURLPolicy, err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: external origin must use https", ErrURLPolicy)
	}
	if u.User != nil || u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: invalid external origin", ErrURLPolicy)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("%w: external origin must not contain a query", ErrURLPolicy)
	}
	if u.Opaque != "" {
		return fmt.Errorf("%w: opaque origins are not supported", ErrURLPolicy)
	}
	if u.Path != "" && u.Path != "/" {
		return fmt.Errorf("%w: external origin must not contain a path", ErrURLPolicy)
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%w: external origin has an empty port", ErrURLPolicy)
	}
	host, err := normalizeHost(u.Hostname(), false)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLPolicy, err)
	}
	port, err := effectivePort(u.Port(), defaultHTTPSPort)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLPolicy, err)
	}
	if port == defaultHTTPSPort {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ports == nil {
		p.ports = make(map[string]map[int]struct{})
	}
	byPort := p.ports[host]
	if byPort == nil {
		byPort = make(map[int]struct{})
		p.ports[host] = byPort
	}
	byPort[port] = struct{}{}
	return nil
}

// RegisterHostPort registers a non-default port for an external host.
func (p *ExternalURLPolicy) RegisterHostPort(host string, port int) error {
	if p == nil {
		return fmt.Errorf("%w: nil policy", ErrURLPolicy)
	}
	h, err := normalizeHost(host, false)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrURLPolicy, err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: invalid port %d", ErrURLPolicy, port)
	}
	if port == defaultHTTPSPort {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ports == nil {
		p.ports = make(map[string]map[int]struct{})
	}
	byPort := p.ports[h]
	if byPort == nil {
		byPort = make(map[int]struct{})
		p.ports[h] = byPort
	}
	byPort[port] = struct{}{}
	return nil
}

// ValidateURL validates an external HTTPS URL and returns its canonical form.
func (p *ExternalURLPolicy) ValidateURL(raw string) (*url.URL, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: nil policy", ErrURLPolicy)
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") {
		return nil, fmt.Errorf("%w: URL contains whitespace or control characters", ErrURLPolicy)
	}
	if strings.Contains(raw, "#") {
		return nil, fmt.Errorf("%w: URL must not contain a fragment", ErrURLPolicy)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL: %v", ErrURLPolicy, err)
	}
	if err := validateCommonHTTPSURL(u); err != nil {
		return nil, err
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, fmt.Errorf("%w: external URL must use https", ErrURLPolicy)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: URL has no host", ErrURLPolicy)
	}
	if u.Opaque != "" {
		return nil, fmt.Errorf("%w: opaque URLs are not supported", ErrURLPolicy)
	}
	host, err := normalizeHost(u.Hostname(), false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLPolicy, err)
	}
	port, err := effectivePort(u.Port(), defaultHTTPSPort)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrURLPolicy, err)
	}
	if port != defaultHTTPSPort {
		p.mu.RLock()
		byPort, ok := p.ports[host]
		_, registered := byPort[port]
		p.mu.RUnlock()
		if !ok || !registered {
			return nil, fmt.Errorf("%w: port %d is not registered for host %s", ErrURLPolicy, port, host)
		}
	}

	canonical := *u
	canonical.Scheme = "https"
	canonical.User = nil
	canonical.Fragment = ""
	canonical.RawFragment = ""
	if port == defaultHTTPSPort && u.Port() == "443" {
		canonical.Host = host
	} else if port == defaultHTTPSPort {
		canonical.Host = host
	} else {
		canonical.Host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return &canonical, nil
}

// ValidateRedirect applies the policy to a redirect target and requires the
// target to remain on the same registrable domain as the previous hop.
func (p *ExternalURLPolicy) ValidateRedirect(from, to *url.URL) error {
	if from == nil || to == nil {
		return fmt.Errorf("%w: nil redirect URL", ErrURLPolicy)
	}
	next, err := p.ValidateURL(to.String())
	if err != nil {
		return err
	}
	if RegistrableDomain(from.Hostname()) != RegistrableDomain(next.Hostname()) {
		return fmt.Errorf("%w: redirect crosses registrable domains (%s -> %s)", ErrURLPolicy, RegistrableDomain(from.Hostname()), RegistrableDomain(next.Hostname()))
	}
	return nil
}

// RegistrableDomain returns a conservative eTLD+1 approximation for a
// hostname. It is intentionally based on a small, explicit list of common
// multi-label public suffixes rather than on attacker-controlled labels.
func RegistrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return host
	}
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	if len(labels) >= 4 {
		lastThree := labels[len(labels)-3] + "." + labels[len(labels)-2] + "." + labels[len(labels)-1]
		if _, ok := multiLabelPublicSuffixes[lastThree]; ok {
			return strings.Join(labels[len(labels)-4:], ".")
		}
	}
	lastTwo := labels[len(labels)-2] + "." + labels[len(labels)-1]
	if _, ok := multiLabelPublicSuffixes[lastTwo]; ok {
		if len(labels) >= 3 {
			return strings.Join(labels[len(labels)-3:], ".")
		}
		return lastTwo
	}
	return lastTwo
}

var multiLabelPublicSuffixes = map[string]struct{}{
	"ac.uk": {}, "co.uk": {}, "gov.uk": {}, "ltd.uk": {}, "me.uk": {}, "net.uk": {}, "org.uk": {}, "plc.uk": {},
	"com.au": {}, "edu.au": {}, "gov.au": {}, "net.au": {}, "org.au": {},
	"co.jp": {}, "ne.jp": {}, "or.jp": {}, "com.jp": {}, "ac.jp": {},
	"co.kr": {}, "ne.kr": {}, "or.kr": {}, "com.kr": {},
	"co.nz": {}, "net.nz": {}, "org.nz": {},
	"co.in": {}, "net.in": {}, "org.in": {},
	"com.br": {}, "net.br": {}, "org.br": {},
	"com.cn": {}, "net.cn": {}, "org.cn": {},
	"com.sg": {}, "net.sg": {}, "org.sg": {},
	"com.hk": {}, "net.hk": {}, "org.hk": {},
	"com.tw": {}, "net.tw": {}, "org.tw": {},
	"co.za": {}, "net.za": {}, "org.za": {},
	"com.mx": {}, "net.mx": {}, "org.mx": {},
	"com.tr": {}, "net.tr": {}, "org.tr": {},
	"co.il": {}, "net.il": {}, "org.il": {},
	"com.ar": {}, "net.ar": {}, "org.ar": {},
	"com.es": {}, "net.es": {}, "org.es": {},
	"co.at": {}, "or.at": {}, "com.at": {},
	"co.id": {}, "net.id": {}, "org.id": {},
	"com.my": {}, "net.my": {}, "org.my": {},
	"com.ph": {}, "net.ph": {}, "org.ph": {},
	"com.pk": {}, "net.pk": {}, "org.pk": {},
	"com.ua": {}, "net.ua": {}, "org.ua": {},
	"co.ke": {}, "net.ke": {}, "org.ke": {},
	"com.ng": {}, "net.ng": {}, "org.ng": {},
	"github.io": {}, "gitlab.io": {}, "pages.dev": {}, "workers.dev": {},
	"netlify.app": {}, "vercel.app": {}, "herokuapp.com": {}, "appspot.com": {},
	"azurewebsites.net": {}, "cloudfront.net": {}, "s3.amazonaws.com": {},
	"blogspot.com": {}, "web.app": {}, "firebaseapp.com": {}, "glitch.me": {},
	"onrender.com": {}, "fly.dev": {},
}

func validateCommonHTTPSURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: nil URL", ErrURLPolicy)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%w: URL must use https", ErrURLPolicy)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URL must not contain userinfo", ErrURLPolicy)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return fmt.Errorf("%w: URL must not contain a fragment", ErrURLPolicy)
	}
	if u.Host == "" || strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("%w: URL has no usable host", ErrURLPolicy)
	}
	return nil
}
