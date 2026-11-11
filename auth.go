package sigv4chunk

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

var (
	errMalformedAuth     = errors.New("sigv4: malformed Authorization header")
	errUnsupportedAlg    = errors.New("sigv4: unsupported signing algorithm")
	errBadCredential     = errors.New("sigv4: malformed Credential scope")
	errDuplicateHeader   = errors.New("sigv4: signed header listed more than once")
	errMissingHeader     = errors.New("sigv4: signed header missing from request")
	errBadSignature      = errors.New("sigv4: signature is not valid hex")
	errSignatureMismatch = errors.New("sigv4: signature does not match")
	errRequestExpired    = errors.New("sigv4: request outside permitted time window")
)

// authorizationParts holds the fields parsed from the Authorization header:
//
//	Authorization: AWS4-HMAC-SHA256
//	    Credential=<access-key>/<date>/<region>/<service>/aws4_request,
//	    SignedHeaders=<semicolon-separated header names>,
//	    Signature=<hex seed signature>
type authorizationParts struct {
	accessKey     string
	date          string
	region        string
	service       string
	signedHeaders []string
	signature     []byte
}

func parseAuthorization(header string) (*authorizationParts, error) {
	if header == "" {
		return nil, errMalformedAuth
	}
	space := strings.IndexByte(header, ' ')
	if space <= 0 {
		return nil, errMalformedAuth
	}
	algorithm := strings.TrimSpace(header[:space])
	if algorithm != algorithmName {
		return nil, errUnsupportedAlg
	}

	fields := map[string]string{}
	for _, piece := range strings.Split(header[space+1:], ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		eq := strings.IndexByte(piece, '=')
		if eq <= 0 {
			return nil, errMalformedAuth
		}
		fields[strings.TrimSpace(piece[:eq])] = strings.TrimSpace(piece[eq+1:])
	}

	credential, ok := fields["Credential"]
	if !ok {
		return nil, errMalformedAuth
	}
	signed, ok := fields["SignedHeaders"]
	if !ok || signed == "" {
		return nil, errMalformedAuth
	}
	signature, ok := fields["Signature"]
	if !ok || len(signature) != sha256.Size*2 {
		return nil, errMalformedAuth
	}
	rawSignature, err := hex.DecodeString(signature)
	if err != nil {
		return nil, errBadSignature
	}

	scope := strings.Split(credential, "/")
	if len(scope) != 5 || scope[4] != terminatorString {
		return nil, errBadCredential
	}

	headers := strings.Split(signed, ";")
	seen := make(map[string]bool, len(headers))
	for _, name := range headers {
		if name == "" || seen[name] {
			return nil, errDuplicateHeader
		}
		seen[name] = true
	}

	return &authorizationParts{
		accessKey:     scope[0],
		date:          scope[1],
		region:        scope[2],
		service:       scope[3],
		signedHeaders: headers,
		signature:     rawSignature,
	}, nil
}

func (a *authorizationParts) credentialScope() string {
	return strings.Join([]string{a.date, a.region, a.service, terminatorString}, "/")
}

// signingKey derives the signature key:
// kDate    = HMAC("AWS4"+secret, date)
// kRegion  = HMAC(kDate, region)
// kService = HMAC(kRegion, service)
// kSigning = HMAC(kService, "aws4_request")
func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte(terminatorString))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// stringToSign renders the SigV4 string to sign from a canonical request.
func stringToSign(amzDate, scope, canonicalRequest string) string {
	return strings.Join([]string{
		algorithmName,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")
}

// verifySeedSignature validates the seed signature carried in the
// Authorization header against the canonical request built with the supplied
// payload hash (the streaming marker for aws-chunked uploads).
func verifySeedSignature(r *http.Request, auth *authorizationParts, secret, payloadHash string) error {
	canonicalRequest := buildCanonicalRequest(r, auth.signedHeaders, payloadHash)
	signed := stringToSign(r.Header.Get(amzDateHeader), auth.credentialScope(), canonicalRequest)
	expected := hmacSHA256(signingKey(secret, auth.date, auth.region, auth.service), []byte(signed))
	if !hmac.Equal(expected, auth.signature) {
		return errSignatureMismatch
	}
	return nil
}

// validateAmzDate checks the X-Amz-Date header format and that the request is
// within the permitted clock-skew window. The date must also match the
// credential scope date.
func validateAmzDate(r *http.Request, scopeDate string, now time.Time, maxSkew time.Duration) error {
	amzDate := r.Header.Get(amzDateHeader)
	if amzDate == "" {
		return errMissingHeader
	}
	requestTime, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return errMalformedAuth
	}
	if amzDate[:8] != scopeDate {
		return errBadCredential
	}
	diff := now.UTC().Sub(requestTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > maxSkew {
		return errRequestExpired
	}
	return nil
}

// ensureSignedHeaders rejects requests that omit mandatory headers from the
// signed header set or reference headers that are absent on the request.
func ensureSignedHeaders(r *http.Request, signedHeaders []string, mandatory map[string]bool) error {
	present := make(map[string]bool, len(signedHeaders))
	for _, name := range signedHeaders {
		if present[name] {
			return errDuplicateHeader
		}
		present[name] = true
		if name != "host" && r.Header.Get(http.CanonicalHeaderKey(name)) == "" {
			return errMissingHeader
		}
	}
	if r.Host == "" {
		return errMissingHeader
	}
	for required := range mandatory {
		if !present[required] {
			return errMissingHeader
		}
	}
	return nil
}
