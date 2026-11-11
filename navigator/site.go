// Package navigator implements an embeddable navigation state machine that
// models a browser-like request engine for a web-embeddable component.
//
// Unlike net/http.Client, it keeps the navigation initiator (the top-level
// page or an embedded resource) separate from every individual hop of a
// redirect chain. Cookie attachment is therefore computed from the original
// request's context instead of borrowing the login state of a previous
// redirect hop.
package navigator

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// Site is the "schemeful site" of an origin: the scheme plus the registrable
// domain. Ports never participate in site identity, matching the
// site-for-cookies definition used by SameSite cookies.
type Site struct {
	Scheme string
	Domain string
}

// String renders the site as "scheme://domain".
func (s Site) String() string {
	return s.Scheme + "://" + s.Domain
}

// IsZero reports whether the site carries no origin information. A request
// whose site-for-cookies is the zero site is always treated as cross-site.
func (s Site) IsZero() bool {
	return s.Scheme == "" || s.Domain == ""
}

// SameSite reports whether two sites are identical. A zero site is never
// same-site with anything.
func (s Site) SameSite(other Site) bool {
	if s.IsZero() || other.IsZero() {
		return false
	}
	return s.Scheme == other.Scheme && strings.EqualFold(s.Domain, other.Domain)
}

// publicSuffixes is the minimal built-in list of multi-label public
// suffixes. The full rule set lives in golang.org/x/net/publicsuffix; keeping
// the core engine free of external dependencies means tests run fully
// offline. A Navigator accepts extra suffixes via WithPublicSuffixes.
var publicSuffixes = map[string]bool{
	"co.uk":          true,
	"org.uk":         true,
	"gov.uk":         true,
	"ac.uk":          true,
	"co.jp":          true,
	"co.kr":          true,
	"com.au":         true,
	"com.br":         true,
	"com.cn":         true,
	"co.nz":          true,
	"github.io":      true,
	"cloudfront.net": true,
}

// SiteOf computes the schemeful site of a URL. Loopback literals and plain
// IP addresses are treated as their own registrable domain so that test
// servers bound to localhost and 127.0.0.1 form distinct sites.
func SiteOf(u *url.URL) Site {
	return siteOf(u, nil)
}

// siteOf computes the schemeful site of a URL using an optional extra suffix
// list supplied through Navigator configuration.
func siteOf(u *url.URL, extraSuffixes map[string]bool) Site {
	if u == nil {
		return Site{}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return Site{}
	}
	host := u.Hostname()
	if host == "" {
		return Site{}
	}
	host = strings.ToLower(host)
	if net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return Site{Scheme: scheme, Domain: host}
	}
	return Site{Scheme: scheme, Domain: registrableDomain(host, extraSuffixes)}
}

// registrableDomain returns the eTLD+1 of a host using a small static suffix
// list. Unknown suffixes are assumed to be a single label.
func registrableDomain(host string, extra map[string]bool) string {
	labels := strings.Split(host, ".")
	for n := len(labels); n > 1; n-- {
		candidate := strings.Join(labels[len(labels)-n:], ".")
		if publicSuffixes[candidate] || extra[candidate] {
			if n == len(labels) {
				return host
			}
			return strings.Join(labels[len(labels)-n-1:], ".")
		}
	}
	if len(labels) >= 2 {
		return strings.Join(labels[len(labels)-2:], ".")
	}
	return host
}

// crossSite reports whether target is cross-site relative to siteForCookies,
// applying the redirect-chain rule from RFC 6265bis section 5.2: a request
// created because of a 3xx response is cross-site if any URL in the chain is
// cross-site with the site-for-cookies. Once a cross-site URL enters the
// chain, every later hop stays cross-site even if it returns to the
// original site.
func crossSite(siteForCookies Site, chain []Site, target Site) bool {
	if siteForCookies.IsZero() {
		return true
	}
	if !siteForCookies.SameSite(target) {
		return true
	}
	for _, hop := range chain {
		if !siteForCookies.SameSite(hop) {
			return true
		}
	}
	return false
}

// ErrNoOrigin is returned when an embedded navigation context cannot derive
// any site-for-cookies (for example an iframe with an opaque origin).
var ErrNoOrigin = errors.New("navigator: context has no usable origin")
