package navigator

import (
	"net/http"
	"net/url"
)

// Phase identifies the state of the navigation state machine.
type Phase int

const (
	// PhaseInit is the entry state before any request is issued.
	PhaseInit Phase = iota
	// PhaseIssuing means the next hop request is being prepared and sent.
	PhaseIssuing
	// PhaseRedirect means a 3xx response was accepted and a new hop is
	// about to be prepared. The initiator context never changes here.
	PhaseRedirect
	// PhaseDone means the final (non-redirect) response was received.
	PhaseDone
	// PhaseError means the navigation terminated with an error.
	PhaseError
)

func (p Phase) String() string {
	switch p {
	case PhaseInit:
		return "init"
	case PhaseIssuing:
		return "issuing"
	case PhaseRedirect:
		return "redirect"
	case PhaseDone:
		return "done"
	case PhaseError:
		return "error"
	default:
		return "unknown"
	}
}

// State is a pure snapshot of the navigation state machine. It contains no
// network machinery so that transitions can be unit tested directly.
type State struct {
	Phase         Phase
	Context       Context
	Method        string
	URL           *url.URL
	Chain         []Site // sites of every hop already issued
	Hop           int
	CrossSite     bool
	MethodChanged bool
}

// redirectDecision is the pure outcome of processing a 3xx response.
type redirectDecision struct {
	redirect      bool
	method        string
	includeBody   bool
	methodChanged bool
}

// redirectBehavior mirrors RFC 9110 section 15.4: 301/302/303 downgrade
// non-safe methods to GET and drop the request body; 307/308 preserve the
// method and re-send the body.
func redirectBehavior(method string, statusCode int, hasBody bool, canReplayBody bool) redirectDecision {
	d := redirectDecision{method: method}
	switch statusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther:
		d.redirect = true
		if method != http.MethodGet && method != http.MethodHead {
			d.method = http.MethodGet
			d.methodChanged = true
		}
		d.includeBody = false
	case http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		d.redirect = true
		d.includeBody = true
		if hasBody && !canReplayBody {
			d.redirect = false
		}
	}
	return d
}

// advance computes the next state after a redirect to nextURL. The
// initiator context is preserved verbatim; the site chain grows by one and
// the cross-site flag is recomputed from the whole chain rather than from
// the last hop alone.
func (s State) advance(nextURL *url.URL, decision redirectDecision) State {
	next := State{
		Phase:   PhaseRedirect,
		Context: s.Context,
		Method:  decision.method,
		URL:     nextURL,
		Chain:   append(append([]Site(nil), s.Chain...), SiteOf(s.URL)),
		Hop:     s.Hop + 1,
	}
	target := SiteOf(nextURL)
	next.CrossSite = s.Context.IsCrossSite(next.Chain, target)
	next.MethodChanged = s.Method != next.Method
	return next
}

// sensitiveHeaders are stripped automatically when a hop crosses a site
// boundary, following the same rule browsers apply on redirects.
var sensitiveHeaders = []string{
	"Authorization",
	"Proxy-Authorization",
	"WWW-Authenticate",
}

// shouldStripHeader reports whether header must be removed for a request
// going to target given the previous hop's site.
func shouldStripHeader(header string, prev, target Site) bool {
	if prev.SameSite(target) {
		return false
	}
	h := http.CanonicalHeaderKey(header)
	for _, banned := range sensitiveHeaders {
		if h == banned {
			return true
		}
	}
	return false
}
