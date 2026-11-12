package framepolicy

import (
	"net/http"
	"net/url"
	"strings"
)

// cspHeader is the enforced Content-Security-Policy response header.
// Content-Security-Policy-Report-Only is intentionally absent: report-only
// policies never participate in the embedding decision.
const cspHeader = "Content-Security-Policy"

const frameAncestors = "frame-ancestors"

// faDirective is a parsed frame-ancestors directive from one enforced
// Content-Security-Policy header.
type faDirective struct {
	// denyAll is true when the directive cannot be parsed safely. The
	// directive then fails closed and forbids every embedding.
	denyAll bool
	// sources contains the effective source expressions. When the parsed
	// list contains only 'none', this is empty and denyAll is set instead.
	sources []source
}

// parseFrameAncestors extracts the frame-ancestors directive from every
// enforced Content-Security-Policy header. Report-only headers are not read.
// Repeated Content-Security-Policy headers are kept separate so that their
// policies can be intersected.
func parseFrameAncestors(hdr http.Header) []*faDirective {
	values := hdr.Values(cspHeader)
	directives := make([]*faDirective, 0, len(values))
	for _, policy := range values {
		if directive, ok := parsePolicy(policy); ok {
			directives = append(directives, directive)
		}
	}
	return directives
}

// parsePolicy parses one Content-Security-Policy header value. The second
// return value reports whether the policy contains a frame-ancestors
// directive. When the same directive appears more than once in a policy, the
// first occurrence wins, matching how CSP consumes duplicate directives.
func parsePolicy(policy string) (*faDirective, bool) {
	var directive *faDirective
	for _, raw := range strings.Split(policy, ";") {
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}
		if !strings.EqualFold(fields[0], frameAncestors) {
			continue
		}
		if directive != nil {
			continue
		}
		directive = parseFrameAncestorsDirective(fields[1:])
	}
	return directive, directive != nil
}

// parseFrameAncestorsDirective parses the source list of a frame-ancestors
// directive. 'none' mixed with other sources is ignored. A directive whose
// effective source list is empty ('none' alone, or no token at all) denies
// every embedding, as does a directive containing an unparseable source.
func parseFrameAncestorsDirective(tokens []string) *faDirective {
	directive := &faDirective{}
	for _, token := range tokens {
		source, kind := parseSource(token)
		switch kind {
		case sourceNone:
			// 'none' only matters when it is the sole effective source.
		case sourceInvalid:
			directive.denyAll = true
		default:
			directive.sources = append(directive.sources, source)
		}
	}
	if directive.denyAll {
		directive.sources = nil
		return directive
	}
	// No effective sources ('none' alone, empty directive, or only
	// ignored tokens): the directive matches nothing and fails closed.
	if len(directive.sources) == 0 {
		directive.denyAll = true
	}
	return directive
}

// allows reports whether every ancestor URL in the chain matches the
// directive. A document loaded at the top level (no ancestors) is not subject
// to frame-ancestors, so it is allowed.
func (d *faDirective) allows(o Options) bool {
	if d.denyAll {
		return false
	}
	for _, ancestor := range o.Ancestors {
		if !d.allowsAncestor(ancestor, o.SelfURL) {
			return false
		}
	}
	return true
}

func (d *faDirective) allowsAncestor(ancestor, self *url.URL) bool {
	for _, source := range d.sources {
		if source.matches(ancestor, self) {
			return true
		}
	}
	return false
}
