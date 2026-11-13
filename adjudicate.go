package netsec

import (
	"net/http"
	"strconv"
	"strings"
)

// Decision is the gateway verdict for a single request.
type Decision int

const (
	// DecisionReject drops the request; it must not reach the backend.
	DecisionReject Decision = iota
	// DecisionForward allows the request to be proxied upstream.
	DecisionForward
)

func (d Decision) String() string {
	switch d {
	case DecisionForward:
		return "forward"
	default:
		return "reject"
	}
}

// Verdict pairs a Decision with a human-readable reason.
type Verdict struct {
	Decision Decision
	Reason   string
}

// Adjudicate decides whether a request may be forwarded when the front-end
// truncates bodies by Content-Length while the backend reads them as
// chunked. The rule is deliberately one-sided: a request carrying both
// Content-Length and Transfer-Encoding is rejected outright instead of
// preferring either header. Only a request with Content-Length alone whose
// value matches the actual body length is forwarded; every other shape is
// rejected.
func Adjudicate(header http.Header, bodyLen int) Verdict {
	hasTE := false
	for _, v := range header.Values("Transfer-Encoding") {
		if strings.TrimSpace(v) != "" {
			hasTE = true
			break
		}
	}
	cls := header.Values("Content-Length")

	if hasTE && len(cls) > 0 {
		return Verdict{DecisionReject, "both Content-Length and Transfer-Encoding present"}
	}
	if hasTE {
		return Verdict{DecisionReject, "Transfer-Encoding without Content-Length is not forwarded"}
	}
	if len(cls) == 0 {
		if bodyLen == 0 {
			return Verdict{DecisionForward, "no body headers and empty body"}
		}
		return Verdict{DecisionReject, "body present without Content-Length"}
	}
	if len(cls) > 1 {
		return Verdict{DecisionReject, "multiple Content-Length headers"}
	}
	cl, err := strconv.ParseInt(strings.TrimSpace(cls[0]), 10, 64)
	if err != nil || cl < 0 {
		return Verdict{DecisionReject, "invalid Content-Length"}
	}
	if int64(bodyLen) != cl {
		return Verdict{DecisionReject, "Content-Length does not match body length"}
	}
	return Verdict{DecisionForward, "Content-Length matches body length"}
}
