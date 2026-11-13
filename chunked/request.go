package chunked

import (
	"net/http"
	"strings"
)

// ValidateRequest rejects ambiguous request framing. A message carrying both
// Content-Length and Transfer-Encoding is refused unconditionally, regardless
// of their values, because different HTTP implementations may choose
// different framing fields when forwarding it.
func ValidateRequest(req *http.Request) error {
	if req == nil {
		return ErrMalformed
	}
	if err := ValidateHeaders(req.Header); err != nil {
		return err
	}

	if len(req.TransferEncoding) > 0 {
		if hasHeader(req.Header, "Content-Length") {
			return ErrConflictingFraming
		}
		// A parsed chunked request has ContentLength == -1. A positive
		// ContentLength field alongside chunked TransferEncoding therefore
		// describes the same ambiguous framing even if a caller removed the
		// corresponding header entries.
		if req.ContentLength > 0 {
			return ErrConflictingFraming
		}
		if !isChunkedEncoding(req.TransferEncoding) {
			return ErrUnsupportedTransferEncoding
		}
	}
	return nil
}

// ValidateHeaders is the header-only form of ValidateRequest.
func ValidateHeaders(header http.Header) error {
	hasCL := hasHeader(header, "Content-Length")
	hasTE, transferEncoding := headerValues(header, "Transfer-Encoding")

	if hasCL && hasTE {
		return ErrConflictingFraming
	}
	if hasTE && !isChunkedEncoding(transferEncoding) {
		return ErrUnsupportedTransferEncoding
	}
	return nil
}

// HasConflictingFraming reports whether header contains both Content-Length
// and Transfer-Encoding. Presence is checked case-insensitively.
func HasConflictingFraming(header http.Header) bool {
	return hasHeader(header, "Content-Length") && hasHeader(header, "Transfer-Encoding")
}

func hasHeader(header http.Header, name string) bool {
	found, _ := headerValues(header, name)
	return found
}

func headerValues(header http.Header, name string) (bool, []string) {
	var values []string
	found := false
	for key, headerValues := range header {
		if !strings.EqualFold(key, name) {
			continue
		}
		found = true
		values = append(values, headerValues...)
	}
	return found, values
}

func isChunkedEncoding(values []string) bool {
	var codings []string
	for _, value := range values {
		for _, coding := range strings.Split(value, ",") {
			coding = strings.Trim(coding, " \t")
			if coding == "" {
				return false
			}
			codings = append(codings, coding)
		}
	}
	if len(codings) == 0 || !strings.EqualFold(codings[len(codings)-1], "chunked") {
		return false
	}
	for _, coding := range codings[:len(codings)-1] {
		if strings.EqualFold(coding, "chunked") {
			return false
		}
	}
	return true
}
