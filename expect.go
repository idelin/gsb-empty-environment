package netsec

import (
	"net/http"
	"strings"
)

// HasExpectContinue reports whether header contains an Expect: 100-continue
// token. Tokens are matched case-insensitively and may be comma-separated.
func HasExpectContinue(header http.Header) bool {
	for key, values := range header {
		if !strings.EqualFold(key, "Expect") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if strings.EqualFold(strings.TrimSpace(token), "100-continue") {
					return true
				}
			}
		}
	}
	return false
}

// IsExpectContinue is an alias for HasExpectContinue.
func IsExpectContinue(header http.Header) bool {
	return HasExpectContinue(header)
}

// Screen applies the early-body check only when the request advertises
// Expect: 100-continue. Requests without that expectation are accepted
// unchanged.
func Screen(header http.Header, body []byte, opts ...any) error {
	if !HasExpectContinue(header) {
		return nil
	}
	return ValidateEarlyBody(body, opts...)
}

// CheckRequest applies the early-body check to req.
func CheckRequest(req *http.Request, body []byte, opts ...any) error {
	if req == nil {
		return ErrInvalidInput
	}
	return Screen(req.Header, body, opts...)
}
