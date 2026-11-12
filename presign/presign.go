// Package presign validates SigV4 presigned URLs for an object storage
// gateway. It enforces the structural rules that prevent common bypasses:
// duplicated X-Amz-* query parameters, out-of-range X-Amz-Expires values,
// unsigned x-amz-* request headers, a missing host entry in
// X-Amz-SignedHeaders, and expired signatures.
package presign

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// MinExpiresSeconds is the smallest accepted X-Amz-Expires value.
	MinExpiresSeconds = 1
	// MaxExpiresSeconds is the largest accepted X-Amz-Expires value
	// (one week, per the SigV4 specification).
	MaxExpiresSeconds = 604800

	amzAlgorithm  = "X-Amz-Algorithm"
	amzDate       = "X-Amz-Date"
	amzExpires    = "X-Amz-Expires"
	amzSignature  = "X-Amz-Signature"
	amzSignedHdrs = "X-Amz-SignedHeaders"

	signingAlgorithm = "AWS4-HMAC-SHA256"
	amzDateFormat    = "20060102T150405Z"
)

var (
	ErrDuplicateQueryParam   = errors.New("presign: duplicate X-Amz-* query parameter")
	ErrMissingQueryParam     = errors.New("presign: missing required X-Amz-* query parameter")
	ErrExpiresOutOfRange     = errors.New("presign: X-Amz-Expires must be between 1 and 604800 seconds")
	ErrHostNotSigned         = errors.New("presign: X-Amz-SignedHeaders must include host")
	ErrUnsignedAmzHeader     = errors.New("presign: request carries an x-amz-* header that is not signed")
	ErrExpired               = errors.New("presign: presigned URL has expired")
	ErrMalformedDate         = errors.New("presign: malformed X-Amz-Date")
	ErrUnsupportedAlgorithm  = errors.New("presign: X-Amz-Algorithm is not AWS4-HMAC-SHA256")
	ErrMalformedSignedHeader = errors.New("presign: malformed entry in X-Amz-SignedHeaders")
)

// queryParam is one raw key=value pair from the query string. The encoded
// forms are kept verbatim so the canonical query string can be rebuilt
// without re-encoding; the decoded key is used for rule checks.
type queryParam struct {
	encKey string
	encVal string
	decKey string
}

// parseRawQuery splits rawQuery on '&' without re-encoding anything. Keys
// are decoded only for case-insensitive rule checks.
func parseRawQuery(rawQuery string) ([]queryParam, error) {
	if rawQuery == "" {
		return nil, nil
	}
	parts := strings.Split(rawQuery, "&")
	params := make([]queryParam, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		encKey, encVal, _ := strings.Cut(part, "=")
		decKey, err := url.QueryUnescape(encKey)
		if err != nil {
			return nil, fmt.Errorf("presign: undecodable query key %q: %w", encKey, err)
		}
		params = append(params, queryParam{encKey: encKey, encVal: encVal, decKey: decKey})
	}
	return params, nil
}

func isAmzParam(decodedKey string) bool {
	return strings.HasPrefix(strings.ToLower(decodedKey), "x-amz-")
}

// rejectDuplicateAmzParams rejects the request when any X-Amz-* parameter
// name (compared case-insensitively after decoding) appears more than once.
func rejectDuplicateAmzParams(params []queryParam) error {
	seen := make(map[string]struct{}, len(params))
	for _, p := range params {
		if !isAmzParam(p.decKey) {
			continue
		}
		name := strings.ToLower(p.decKey)
		if _, dup := seen[name]; dup {
			return fmt.Errorf("%w: %s", ErrDuplicateQueryParam, p.decKey)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// CanonicalQueryString builds the SigV4 canonical query string: pairs are
// sorted by their encoded key (ties broken by encoded value) and the
// X-Amz-Signature pair is excluded. Duplicate X-Amz-* parameters are
// rejected.
func CanonicalQueryString(rawQuery string) (string, error) {
	params, err := parseRawQuery(rawQuery)
	if err != nil {
		return "", err
	}
	if err := rejectDuplicateAmzParams(params); err != nil {
		return "", err
	}
	kept := params[:0]
	for _, p := range params {
		if p.decKey == amzSignature {
			continue
		}
		kept = append(kept, p)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].encKey != kept[j].encKey {
			return kept[i].encKey < kept[j].encKey
		}
		return kept[i].encVal < kept[j].encVal
	})
	var b strings.Builder
	for i, p := range kept {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.encKey)
		b.WriteByte('=')
		b.WriteString(p.encVal)
	}
	return b.String(), nil
}

// CanonicalPath returns the canonical URI for the request. SigV4 for S3
// signs the already-encoded path verbatim, so an encoded slash (%2F) is
// preserved rather than decoded or double-encoded.
func CanonicalPath(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(escapedPath, "/") {
		return "/" + escapedPath
	}
	return escapedPath
}

// amzParamValues returns the decoded value of a single X-Amz-* parameter.
// Callers must have already rejected duplicates via rejectDuplicateAmzParams,
// so at most one value exists.
func amzParamValues(params []queryParam, name string) (string, bool) {
	for _, p := range params {
		if p.decKey == name {
			v, err := url.QueryUnescape(p.encVal)
			if err != nil {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

func parseSignedHeaders(raw string) (map[string]struct{}, error) {
	set := make(map[string]struct{})
	for _, h := range strings.Split(raw, ";") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, " \t\r\n:") {
			return nil, fmt.Errorf("%w: %q", ErrMalformedSignedHeader, h)
		}
		set[h] = struct{}{}
	}
	return set, nil
}

// ValidateRequest checks the structural SigV4 presigning rules for r as of
// the time now. It does not verify the HMAC signature itself; callers are
// expected to rebuild the canonical request (see CanonicalQueryString and
// CanonicalPath) and compare signatures with the caller's credentials after
// ValidateRequest succeeds.
func ValidateRequest(r *http.Request, now time.Time) error {
	params, err := parseRawQuery(r.URL.RawQuery)
	if err != nil {
		return err
	}
	if err := rejectDuplicateAmzParams(params); err != nil {
		return err
	}

	if _, ok := amzParamValues(params, amzSignature); !ok {
		return fmt.Errorf("%w: %s", ErrMissingQueryParam, amzSignature)
	}

	if alg, ok := amzParamValues(params, amzAlgorithm); !ok {
		return fmt.Errorf("%w: %s", ErrMissingQueryParam, amzAlgorithm)
	} else if alg != signingAlgorithm {
		return fmt.Errorf("%w: got %q", ErrUnsupportedAlgorithm, alg)
	}

	expiresRaw, ok := amzParamValues(params, amzExpires)
	if !ok {
		return fmt.Errorf("%w: %s", ErrMissingQueryParam, amzExpires)
	}
	expires, err := strconv.Atoi(expiresRaw)
	if err != nil || expires < MinExpiresSeconds || expires > MaxExpiresSeconds {
		return fmt.Errorf("%w: got %q", ErrExpiresOutOfRange, expiresRaw)
	}

	signedRaw, ok := amzParamValues(params, amzSignedHdrs)
	if !ok {
		return fmt.Errorf("%w: %s", ErrMissingQueryParam, amzSignedHdrs)
	}
	signedHeaders, err := parseSignedHeaders(signedRaw)
	if err != nil {
		return err
	}
	if _, ok := signedHeaders["host"]; !ok {
		return ErrHostNotSigned
	}

	// Every x-amz-* header actually present on the request must be covered
	// by X-Amz-SignedHeaders; otherwise an attacker can inject headers such
	// as x-amz-acl after the URL was signed.
	for name := range r.Header {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "x-amz-") {
			continue
		}
		if _, ok := signedHeaders[lower]; !ok {
			return fmt.Errorf("%w: %s", ErrUnsignedAmzHeader, lower)
		}
	}

	dateRaw, ok := amzParamValues(params, amzDate)
	if !ok {
		return fmt.Errorf("%w: %s", ErrMissingQueryParam, amzDate)
	}
	signedAt, err := time.Parse(amzDateFormat, dateRaw)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrMalformedDate, dateRaw)
	}
	if now.After(signedAt.Add(time.Duration(expires) * time.Second)) {
		return fmt.Errorf("%w: signed at %s with %ds validity, now %s",
			ErrExpired, signedAt.UTC().Format(time.RFC3339), expires, now.UTC().Format(time.RFC3339))
	}
	return nil
}
