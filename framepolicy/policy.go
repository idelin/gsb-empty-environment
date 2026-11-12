// Package framepolicy decides whether an HTTP response may be embedded in a
// frame by the requesting browsing context.
//
// The decision combines the framing controls that the response carries in the
// Content-Security-Policy and X-Frame-Options headers. The intended caller is
// the response egress of an HTTP gateway:
//
//	decision := framepolicy.Decide(resp.Header, framepolicy.Options{
//	    SelfURL:   selfURL,
//	    Ancestors: ancestorURLs,
//	})
//	if decision == framepolicy.Deny {
//	    // block, strip framing, or reject the response
//	}
//
// Rules implemented here:
//
//   - When an enforced Content-Security-Policy header contains a
//     frame-ancestors directive, X-Frame-Options is ignored entirely.
//   - Content-Security-Policy-Report-Only never participates in the decision.
//   - Multiple enforced Content-Security-Policy headers are intersected: the
//     embedding is allowed only when every policy permits it.
//   - A frame-ancestors source list supports 'none', 'self', scheme sources,
//     and host sources with an optional *. wildcard prefix and port. When
//     'none' shares the list with other sources, 'none' is ignored and the
//     other sources remain effective.
//   - A malformed source expression makes the whole frame-ancestors directive
//     fail closed (the policy denies every embedding).
//   - X-Frame-Options recognizes only DENY and SAMEORIGIN. Multiple values,
//     comma-separated values, repeated headers, or unknown tokens are treated
//     as invalid and, per this package's contract, deny embedding.
//   - Every URL in the nested ancestor chain must match; checking only the
//     immediate parent is not sufficient.
package framepolicy

import (
	"net/http"
	"net/url"
)

// Decision is the embedding verdict for a response.
type Decision int

const (
	// Allow means the response may be framed by the requesting context.
	Allow Decision = iota
	// Deny means at least one framing policy forbids the embedding.
	Deny
)

// Options carries the request-side context required for policy evaluation.
type Options struct {
	// SelfURL is the URL of the protected response. It is required for
	// 'self' and X-Frame-Options: SAMEORIGIN matching. When it is nil and
	// such a comparison is needed, the decision fails closed.
	SelfURL *url.URL

	// Ancestors holds the full nested ancestor chain, ordered from the
	// top-level browsing context down to the immediate parent of the
	// framed document. Every entry must match the effective policy. An
	// empty slice means the document is being loaded as the top-level
	// document, which is always allowed by framing controls.
	Ancestors []*url.URL
}

// Decide evaluates the framing policies carried by hdr.
//
// opt may be nil, which is equivalent to a zero Options (no known self URL and
// no ancestors).
func Decide(hdr http.Header, opt *Options) Decision {
	var o Options
	if opt != nil {
		o = *opt
	}
	if len(o.Ancestors) == 0 {
		// Framing controls only govern embedding. A document loaded as
		// the top-level browsing context is never blocked by either
		// frame-ancestors or X-Frame-Options.
		return Allow
	}

	directives := parseFrameAncestors(hdr)
	if len(directives) > 0 {
		// frame-ancestors takes precedence; X-Frame-Options is ignored.
		return decideFrameAncestors(directives, o)
	}
	return decideXFrameOptions(hdr, o)
}

// decideFrameAncestors intersects every enforced frame-ancestors directive.
func decideFrameAncestors(directives []*faDirective, o Options) Decision {
	for _, directive := range directives {
		if !directive.allows(o) {
			return Deny
		}
	}
	return Allow
}
