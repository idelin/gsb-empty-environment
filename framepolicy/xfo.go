package framepolicy

import (
	"net/http"
	"strings"
)

const xfoHeader = "X-Frame-Options"

// decideXFrameOptions evaluates X-Frame-Options. It runs only when no
// enforced frame-ancestors directive is present.
//
// Only a single header carrying exactly one recognized token (DENY or
// SAMEORIGIN) is valid. Repeated headers, comma-separated values, additional
// whitespace-separated tokens, or unknown tokens are invalid and, per this
// package's contract, deny embedding.
func decideXFrameOptions(hdr http.Header, o Options) Decision {
	values := hdr.Values(xfoHeader)
	if len(values) == 0 {
		// No framing control present: nothing forbids the embedding.
		return Allow
	}
	if len(values) > 1 {
		return Deny
	}

	fields := strings.Fields(values[0])
	if len(fields) != 1 || strings.Contains(values[0], ",") {
		return Deny
	}

	switch strings.ToUpper(fields[0]) {
	case "DENY":
		return Deny
	case "SAMEORIGIN":
		return decideSameOrigin(o)
	default:
		return Deny
	}
}

// decideSameOrigin requires every ancestor to share the protected document's
// origin. When the self URL is unknown, the comparison cannot be satisfied and
// the decision fails closed.
func decideSameOrigin(o Options) Decision {
	if o.SelfURL == nil {
		return Deny
	}
	for _, ancestor := range o.Ancestors {
		if ancestor == nil || !sameOrigin(ancestor, o.SelfURL) {
			return Deny
		}
	}
	return Allow
}
