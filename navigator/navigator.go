package navigator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultMaxHops mirrors net/http.Client's redirect limit.
const defaultMaxHops = 10

// Option customizes a Navigator.
type Option func(*Navigator)

// WithTransport overrides the RoundTripper used for individual hops.
func WithTransport(rt http.RoundTripper) Option {
	return func(n *Navigator) { n.transport = rt }
}

// WithMaxHops limits the number of requests in one navigation including the
// initial request.
func WithMaxHops(max int) Option {
	return func(n *Navigator) {
		if max > 0 {
			n.maxHops = max
		}
	}
}

// WithLaxPostWindow configures the Lax+POST mitigation window. The browser
// default is two minutes; pass zero to disable the intervention.
func WithLaxPostWindow(d time.Duration) Option {
	return func(n *Navigator) { n.laxPostWindow = d }
}

// WithPublicSuffixes registers additional multi-label public suffixes used
// when computing registrable domains.
func WithPublicSuffixes(suffixes ...string) Option {
	return func(n *Navigator) {
		for _, s := range suffixes {
			n.extraSuffixes[s] = true
		}
	}
}

// Navigator executes navigations as an explicit state machine. Each hop is
// issued through a plain RoundTripper with CheckRedirect disabled, so the
// Navigator owns every redirect decision itself.
type Navigator struct {
	transport     http.RoundTripper
	jar           *Jar
	maxHops       int
	laxPostWindow time.Duration
	extraSuffixes map[string]bool
}

// New creates a Navigator backed by jar. A nil jar is replaced with an
// empty SameSite-aware store.
func New(jar *Jar, opts ...Option) *Navigator {
	n := &Navigator{
		jar:           jar,
		maxHops:       defaultMaxHops,
		laxPostWindow: 2 * time.Minute,
		extraSuffixes: map[string]bool{},
	}
	if n.jar == nil {
		n.jar = NewJar()
	}
	for _, opt := range opts {
		opt(n)
	}
	if n.transport == nil {
		n.transport = http.DefaultTransport
	}
	return n
}

// Jar exposes the cookie store so tests can seed login cookies.
func (n *Navigator) Jar() *Jar { return n.jar }

// HopRecord captures what was actually put on the wire for one request.
type HopRecord struct {
	Index        int
	Phase        Phase
	Method       string
	URL          *url.URL
	Site         Site
	CrossSite    bool
	TopLevel     bool
	CookieHeader string
	Headers      http.Header
	Status       int
}

// Trace is the full execution record of a navigation.
type Trace struct {
	Context Context
	Hops    []HopRecord
	Final   *http.Response
	State   State
}

// Navigate runs a navigation for req under ctx. The request body must be
// replayable for 307/308 redirects; use NewRequest (which sets GetBody) or
// provide Request.GetBody explicitly.
func (n *Navigator) Navigate(ctx context.Context, navCtx Context, req *http.Request) (*Trace, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("navigator: nil request")
	}

	state := State{
		Phase:   PhaseInit,
		Context: navCtx,
		Method:  req.Method,
		URL:     req.URL,
	}
	if state.Method == "" {
		state.Method = http.MethodGet
	}
	state.CrossSite = navCtx.IsCrossSite(nil, SiteOf(req.URL))

	trace := &Trace{Context: navCtx}
	body, err := snapshotBody(req)
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}

	currentReq := req
	for {
		if state.Hop >= n.maxHops {
			state.Phase = PhaseError
			trace.State = state
			return trace, fmt.Errorf("navigator: stopped after %d hops", state.Hop)
		}
		state.Phase = PhaseIssuing
		resp, record, err := n.issue(ctx, state, currentReq, body)
		trace.Hops = append(trace.Hops, record)
		if err != nil {
			state.Phase = PhaseError
			trace.State = state
			return trace, err
		}
		n.jar.SetCookies(currentReq.URL, resp.Cookies())

		nextURL := redirectLocation(resp, currentReq.URL)
		// The initial body is snapshotted in Navigate, so every hop can
		// replay it; 307/308 therefore always include the body when one
		// exists. Requests streamed without a GetBody are consumed once at
		// snapshot time with the same semantics.
		decision := redirectBehavior(state.Method, resp.StatusCode,
			len(body) > 0, true)
		if nextURL == nil || !decision.redirect {
			state.Phase = PhaseDone
			trace.Final = resp
			trace.State = state
			return trace, nil
		}
		closeDrain(resp)

		state = state.advance(nextURL, decision)
		currentReq = n.buildRedirectRequest(currentReq, state, body, decision)
	}
}

// issue prepares and sends one hop, attaching cookies according to the
// current state rather than the previous response.
func (n *Navigator) issue(ctx context.Context, state State, req *http.Request, body []byte) (*http.Response, HopRecord, error) {
	out := req.Clone(ctx)
	out.Header = req.Header.Clone()

	// Cookies are recomputed from scratch for every hop. Nothing is copied
	// blindly from the previous request's Cookie header.
	out.Header.Del("Cookie")
	cookies := n.jar.SelectCookies(CookieRequest{
		URL:           out.URL,
		Method:        state.Method,
		CrossSite:     state.CrossSite,
		TopLevel:      state.Context.Kind() == TopLevelNavigation,
		LaxPostWindow: n.laxPostWindow,
	})
	cookieHeader := serializeCookies(cookies)
	if cookieHeader != "" {
		out.Header.Set("Cookie", cookieHeader)
	}

	if state.Hop > 0 {
		prevSite := state.Chain[len(state.Chain)-1]
		targetSite := SiteOf(out.URL)
		for key := range out.Header {
			if shouldStripHeader(key, prevSite, targetSite) {
				out.Header.Del(key)
			}
		}
	}

	resp, err := n.transport.RoundTrip(out)
	record := HopRecord{
		Index:        state.Hop,
		Phase:        state.Phase,
		Method:       out.Method,
		URL:          out.URL,
		Site:         SiteOf(out.URL),
		CrossSite:    state.CrossSite,
		TopLevel:     state.Context.Kind() == TopLevelNavigation,
		CookieHeader: out.Header.Get("Cookie"),
		Headers:      out.Header.Clone(),
	}
	if resp != nil {
		record.Status = resp.StatusCode
	}
	return resp, record, err
}

// buildRedirectRequest constructs the next hop request, preserving the
// initiator's context and applying method/body downgrade rules.
func (n *Navigator) buildRedirectRequest(prev *http.Request, state State, body []byte, decision redirectDecision) *http.Request {
	out := prev.Clone(prev.Context())
	out.URL = state.URL
	out.RequestURI = ""
	out.Method = state.Method
	out.Header = prev.Header.Clone()
	if decision.includeBody && len(body) > 0 {
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		out.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	} else {
		out.Body = nil
		out.ContentLength = 0
		out.GetBody = nil
	}
	return out
}

// snapshotBody reads the initial request body once so every hop can replay
// it deterministically.
func snapshotBody(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}
	if req.GetBody != nil {
		b, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer b.Close()
		return io.ReadAll(b)
	}
	data, err := io.ReadAll(req.Body)
	req.Body.Close()
	return data, err
}

// redirectLocation resolves a relative Location header against the request
// URL, or returns nil when the response is not a redirect.
func redirectLocation(resp *http.Response, base *url.URL) *url.URL {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return nil
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return nil
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return nil
	}
	return base.ResolveReference(ref)
}

func closeDrain(resp *http.Response) {
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func serializeCookies(cookies []*http.Cookie) string {
	var b bytes.Buffer
	for i, c := range cookies {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Name)
		b.WriteByte('=')
		b.WriteString(c.Value)
	}
	return b.String()
}
