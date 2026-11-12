package earlyhints

import (
	"net/http"
	"strings"
)

// DefaultMaxHints is the maximum number of 103 Early Hints responses that
// may be forwarded for a single request.
const DefaultMaxHints = 3

// Processor enforces the per-request early hints policy: protocol gating,
// link allow-listing, header stripping and the maximum hints count.
//
// A Processor is single-use: create one per inbound request and feed it every
// informational (1xx) response received from upstream via
// net/http/httptrace.ClientTrace.Got1xxResponse.
type Processor struct {
	allowedHosts map[string]bool
	maxHints     int
	forwarded    int
}

// NewProcessor creates a Processor for one request. allowedHosts contains the
// lower-cased host names permitted as preload/preconnect targets. If
// maxHints is not positive, DefaultMaxHints is used.
func NewProcessor(allowedHosts map[string]bool, maxHints int) *Processor {
	if maxHints <= 0 {
		maxHints = DefaultMaxHints
	}
	return &Processor{allowedHosts: allowedHosts, maxHints: maxHints}
}

// Forwarded reports how many early hints have actually been sent downstream.
func (p *Processor) Forwarded() int { return p.forwarded }

// HandleInformational applies the policy to one upstream 1xx response.
//
// A non-nil header is returned only when the response must be forwarded as an
// early hint. The returned header contains exactly one (possibly multi-valued)
// Link header; every other header, including Set-Cookie and Content-Length, is
// stripped. Nil means the response must be silently dropped.
//
// Only HTTP status 103 is eligible. All other informational responses are
// ignored. The clientMajor/minor arguments are the inbound connection's
// HTTP protocol version (r.ProtoMajor/ProtoMinor on the client request).
func (p *Processor) HandleInformational(statusCode int, upstreamHeader http.Header, clientMajor, clientMinor int) http.Header {
	if statusCode != http.StatusEarlyHints {
		return nil
	}
	if !clientSupportsEarlyHints(clientMajor, clientMinor) {
		return nil
	}
	if p.forwarded >= p.maxHints {
		return nil
	}

	links := ParseLinkHeader(upstreamHeader.Values("Link"))
	safe := FilterLinkValues(links, p.allowedHosts)
	if len(safe) == 0 {
		return nil
	}

	// Build a fresh header so no upstream field (Set-Cookie, Content-Length,
	// Link parameters on rejected values, ...) can leak into the hint.
	hint := http.Header{}
	hint.Set("Link", strings.Join(safe, ", "))
	p.forwarded++
	return hint
}

// clientSupportsEarlyHints reports whether the downstream connection can
// receive 1xx interim responses. HTTP/1.1 and every newer protocol can;
// HTTP/1.0 and HTTP/0.9 cannot.
func clientSupportsEarlyHints(major, minor int) bool {
	switch {
	case major >= 2:
		return true
	case major == 1 && minor >= 1:
		return true
	default:
		return false
	}
}
