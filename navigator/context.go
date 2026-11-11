package navigator

import "net/url"

// Kind classifies who initiated a navigation.
type Kind int

const (
	// TopLevelNavigation is a request made by the top-level page itself.
	// The site-for-cookies is the top-level document's site.
	TopLevelNavigation Kind = iota
	// EmbeddedResource is a subresource request issued on behalf of an
	// embedded component (iframe, script, image, fetch from the embed).
	// Its site-for-cookies is the embedding document's site, never the
	// top-level login state when the embed is cross-site.
	EmbeddedResource
)

func (k Kind) String() string {
	switch k {
	case TopLevelNavigation:
		return "top-level"
	case EmbeddedResource:
		return "embedded"
	default:
		return "unknown"
	}
}

// Context describes the immutable initiator information of a navigation.
// The same Context is carried through every hop of a redirect chain: a
// redirect never promotes an embedded request into a top-level one and never
// replaces the initiator with the previous hop's origin.
type Context struct {
	kind           Kind
	siteForCookies Site
	initiator      Site
}

// NewTopLevelContext creates the context of a top-level document located at
// topLevelURL.
func NewTopLevelContext(topLevelURL *url.URL) Context {
	site := SiteOf(topLevelURL)
	return Context{kind: TopLevelNavigation, siteForCookies: site, initiator: site}
}

// NewEmbeddedContext creates the context of a component embedded in the
// document at embedderURL. The embedder's site becomes the site-for-cookies;
// the top-level page's login state is deliberately unavailable.
func NewEmbeddedContext(embedderURL *url.URL) Context {
	site := SiteOf(embedderURL)
	return Context{kind: EmbeddedResource, siteForCookies: site, initiator: site}
}

// NewOpaqueEmbeddedContext models an embed whose origin is opaque (sandboxed
// iframe, data: document). It has no site-for-cookies, so every request it
// makes is cross-site and SameSite=Lax/Strict cookies are never attached.
func NewOpaqueEmbeddedContext() Context {
	return Context{kind: EmbeddedResource}
}

// Kind returns the navigation kind.
func (c Context) Kind() Kind { return c.kind }

// SiteForCookies returns the site used for SameSite evaluation. It is zero
// for opaque embedded contexts.
func (c Context) SiteForCookies() Site { return c.siteForCookies }

// Initiator returns the site that originally issued the navigation.
func (c Context) Initiator() Site { return c.initiator }

// IsCrossSite reports whether a request for target is cross-site in this
// context, given the sites already visited in the redirect chain.
func (c Context) IsCrossSite(chain []Site, target Site) bool {
	return crossSite(c.siteForCookies, chain, target)
}

// isSafeMethod reports whether method qualifies as a "top-level navigation
// using a safe HTTP method" for SameSite=Lax (RFC 6265bis section 5.2).
func isSafeMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}
