// Package redirect plans safe HTTP redirect follow-ups for a Go transport
// host. A Planner decides, from the response status code and the original
// request method, whether the next hop keeps the method and replays the
// body or rewrites to GET and drops every body-related field. Sensitive
// headers are re-authorized against the target origin, and both redirect
// loops and the total hop count are bounded.
package redirect

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	// ErrTooManyHops is returned when a redirect chain exceeds the hop bound.
	ErrTooManyHops = errors.New("redirect: too many hops")
	// ErrLoop is returned when a planned hop revisits a method+URL pair
	// already seen in the chain.
	ErrLoop = errors.New("redirect: loop detected")
	// ErrBodyReplay is returned when a 307/308 redirect must replay a body
	// that has no GetBody factory.
	ErrBodyReplay = errors.New("redirect: cannot replay request body")
)

// DefaultMaxHops bounds the total number of requests in a chain, including
// the initial one.
const DefaultMaxHops = 10

var defaultSensitiveHeaders = []string{
	"Authorization",
	"Www-Authenticate",
	"Cookie",
	"Cookie2",
}

// Planner derives the next request in a redirect chain.
type Planner struct {
	// MaxHops bounds the total number of requests in a chain, including the
	// initial request. Values <= 0 use DefaultMaxHops.
	MaxHops int
	// SensitiveHeaders lists headers that are only forwarded to the initial
	// origin or its subdomains. Nil uses a default list covering
	// Authorization, Www-Authenticate, Cookie and Cookie2.
	SensitiveHeaders []string
}

func (p *Planner) maxHops() int {
	if p.MaxHops > 0 {
		return p.MaxHops
	}
	return DefaultMaxHops
}

func (p *Planner) sensitiveHeaders() []string {
	if p.SensitiveHeaders != nil {
		return p.SensitiveHeaders
	}
	return defaultSensitiveHeaders
}

// Next plans the follow-up request for resp, which must be the response to
// req. via holds the requests already sent in the chain, oldest first, and
// is used for hop bounding, loop detection and origin trust decisions.
//
// Next returns (nil, nil) when resp is not a redirect or carries no
// Location header. The returned request inherits req's context.
func (p *Planner) Next(req *http.Request, resp *http.Response, via []*http.Request) (*http.Request, error) {
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		return nil, nil
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil, nil
	}
	if len(via)+2 > p.maxHops() {
		return nil, ErrTooManyHops
	}
	dest, err := req.URL.Parse(loc)
	if err != nil {
		return nil, fmt.Errorf("redirect: bad Location %q: %w", loc, err)
	}

	// Method rewrite and body replay are decided together: a 307/308 keeps
	// the method and must replay the body, while 301/302/303 may rewrite to
	// GET, in which case the body and its header fields are dropped.
	method := req.Method
	var body io.ReadCloser
	var getBody func() (io.ReadCloser, error)
	dropBody := false
	switch resp.StatusCode {
	case http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		if req.Body != nil {
			if req.GetBody == nil {
				return nil, ErrBodyReplay
			}
			body, err = req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("redirect: replay body: %w", err)
			}
			getBody = req.GetBody
		}
	case http.StatusSeeOther:
		if req.Method != http.MethodHead {
			method = http.MethodGet
			dropBody = true
		}
	default: // 301, 302
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			method = http.MethodGet
			dropBody = true
		}
	}

	key := method + " " + dest.String()
	for _, prior := range via {
		if prior.Method+" "+prior.URL.String() == key {
			return nil, fmt.Errorf("%w: %s", ErrLoop, key)
		}
	}
	if req.Method+" "+req.URL.String() == key {
		return nil, fmt.Errorf("%w: %s", ErrLoop, key)
	}

	header := req.Header.Clone()
	contentLength := req.ContentLength
	if dropBody {
		header.Del("Content-Length")
		header.Del("Content-Type")
		header.Del("Transfer-Encoding")
		contentLength = 0
	}

	initial := req.URL
	if len(via) > 0 {
		initial = via[0].URL
	}
	if !trustedOrigin(initial, dest) {
		for _, h := range p.sensitiveHeaders() {
			header.Del(h)
		}
	}

	next := &http.Request{
		Method:        method,
		URL:           dest,
		Header:        header,
		Body:          body,
		GetBody:       getBody,
		ContentLength: contentLength,
	}
	return next.WithContext(req.Context()), nil
}

// trustedOrigin reports whether dest belongs to the same host as initial or
// to one of its subdomains, mirroring net/http's redirect header policy.
func trustedOrigin(initial, dest *url.URL) bool {
	initHost := strings.ToLower(initial.Hostname())
	destHost := strings.ToLower(dest.Hostname())
	return destHost == initHost || strings.HasSuffix(destHost, "."+initHost)
}
