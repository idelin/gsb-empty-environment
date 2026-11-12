package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Parser struct {
	issuer                      string
	clients                     ClientLookup
	decryptionKeys              []any
	jweAlgorithms               map[string]bool
	contentEncryptionAlgorithms map[string]bool
	requestURIFetcher           RequestURIFetcher
	clock                       func() time.Time
	leeway                      time.Duration
}

// NewParser validates the configuration and constructs a request object
// parser suitable for an authorization endpoint.
func NewParser(cfg Config) (*Parser, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	jweAlgorithms, err := normalizeAlgorithmSet(cfg.DecryptionAlgorithms, supportedJWEKeyAlgorithms, "JWE alg")
	if err != nil {
		return nil, err
	}
	contentEncryptionAlgorithms, err := normalizeAlgorithmSet(cfg.ContentEncryptionAlgorithms, supportedContentEncryptionAlgorithms, "JWE enc")
	if err != nil {
		return nil, err
	}
	return &Parser{
		issuer:                      cfg.Issuer,
		clients:                     cfg.Clients,
		decryptionKeys:              append([]any(nil), cfg.DecryptionKeys...),
		jweAlgorithms:               jweAlgorithms,
		contentEncryptionAlgorithms: contentEncryptionAlgorithms,
		requestURIFetcher:           cfg.RequestURIFetcher,
		clock:                       cfg.Clock,
		leeway:                      cfg.Leeway,
	}, nil
}

// RequestObject is the verified contents of an RFC 9101 request object. Only
// claims from the signed (and, when applicable, encrypted) JOSE object are
// exposed. Query parameters other than client_id, request, and request_uri
// never take part in this result.
type RequestObject struct {
	Claims     map[string]any
	ClientID   string
	Issuer     string
	Audience   []string
	RawToken   string
	RequestURI string
	Encrypted  bool
}

// Claim returns a top-level claim from the verified request object.
func (r *RequestObject) Claim(name string) (any, bool) {
	value, ok := r.Claims[name]
	return value, ok
}

// StringClaim returns a top-level string claim.
func (r *RequestObject) StringClaim(name string) (string, bool) {
	value, ok := r.Claims[name]
	if !ok {
		return "", false
	}
	return claimString(value)
}

// EffectiveParameters returns authorization parameters derived exclusively
// from the verified request object. It is convenient for handlers that want a
// url.Values-shaped view without accidentally merging the outer query.
func (r *RequestObject) EffectiveParameters() url.Values {
	values := make(url.Values, len(r.Claims))
	for name, value := range r.Claims {
		addClaimValues(values, name, value)
	}
	return values
}

// Resolve is an alias for ParseAuthorizationRequest.
func (p *Parser) Resolve(ctx context.Context, request *http.Request) (*RequestObject, error) {
	return p.ParseAuthorizationRequest(ctx, request)
}

// Parse is an alias for ParseAuthorizationRequest.
func (p *Parser) Parse(ctx context.Context, request *http.Request) (*RequestObject, error) {
	return p.ParseAuthorizationRequest(ctx, request)
}

// ParseRequestObject is an alias for ParseAuthorizationRequest.
func (p *Parser) ParseRequestObject(ctx context.Context, request *http.Request) (*RequestObject, error) {
	return p.ParseAuthorizationRequest(ctx, request)
}

// ParseRequestObject constructs a parser from cfg and parses one authorization
// request. It is a convenience function for stateless integrations.
func ParseRequestObject(ctx context.Context, cfg Config, request *http.Request) (*RequestObject, error) {
	parser, err := NewParser(cfg)
	if err != nil {
		return nil, err
	}
	return parser.ParseAuthorizationRequest(ctx, request)
}

// ParseAuthorizationRequest resolves the request or request_uri parameter of
// an authorization endpoint request.
func (p *Parser) ParseAuthorizationRequest(ctx context.Context, request *http.Request) (*RequestObject, error) {
	if request == nil || request.URL == nil {
		return nil, errorf(ErrMissingRequest, "nil HTTP request")
	}
	return p.ParseQuery(ctx, request.URL.Query())
}

// ParseQuery is the lower-level entry point for callers that already have the
// authorization endpoint parameters.
func (p *Parser) ParseQuery(ctx context.Context, query url.Values) (*RequestObject, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	requestValues := query["request"]
	requestURIValues := query["request_uri"]
	if len(requestValues) == 0 && len(requestURIValues) == 0 {
		return nil, ErrMissingRequest
	}
	if len(requestValues) > 0 && len(requestURIValues) > 0 {
		return nil, ErrAmbiguousRequest
	}
	if len(requestValues) > 1 || len(requestURIValues) > 1 {
		return nil, errorf(ErrAmbiguousRequest, "duplicate request parameter")
	}

	outerClientIDs := query["client_id"]
	if len(outerClientIDs) == 0 {
		return nil, ErrMissingClientID
	}
	if len(outerClientIDs) > 1 {
		return nil, errorf(ErrMissingClientID, "duplicate client_id parameter")
	}
	outerClientID := strings.TrimSpace(outerClientIDs[0])
	if outerClientID == "" {
		return nil, ErrMissingClientID
	}

	client, err := p.clients.LookupClient(ctx, outerClientID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errorf(ErrUnknownClient, outerClientID)
	}
	if client.ClientID != "" && client.ClientID != outerClientID {
		return nil, errorf(ErrUnknownClient, "client registration ID mismatch")
	}

	var (
		token      string
		requestURI string
	)
	if len(requestValues) == 1 {
		token = strings.TrimSpace(requestValues[0])
		if token == "" {
			return nil, errorf(ErrInvalidRequestObject, "request parameter is empty")
		}
	} else {
		requestURI = strings.TrimSpace(requestURIValues[0])
		if requestURI == "" {
			return nil, errorf(ErrRequestURIUnavailable, "request_uri is empty")
		}
		if p.requestURIFetcher == nil {
			return nil, errorf(ErrRequestURIUnavailable, "no request_uri fetcher is configured")
		}
		body, err := p.requestURIFetcher.FetchRequestURI(ctx, requestURI)
		if err != nil {
			if errors.Is(err, ErrRequestURIFetch) {
				return nil, err
			}
			return nil, errorf(ErrRequestURIFetch, err.Error())
		}
		token = strings.TrimSpace(string(body))
		if token == "" {
			return nil, errorf(ErrInvalidRequestObject, "request_uri returned an empty document")
		}
	}

	claims, encrypted, err := p.parseToken(token, client)
	if err != nil {
		return nil, err
	}

	object, err := p.validateClaims(claims, outerClientID)
	if err != nil {
		return nil, err
	}
	object.RawToken = token
	object.RequestURI = requestURI
	object.Encrypted = encrypted
	return object, nil
}

func (p *Parser) parseToken(token string, client *Client) (map[string]any, bool, error) {
	if len(token) > maxJOSESegmentSize*5 {
		return nil, false, errorf(ErrInvalidRequestObject, "request value is too large")
	}
	parts := strings.Split(token, ".")
	switch len(parts) {
	case 3:
		payload, _, err := p.verifyJWS(token, client)
		if err != nil {
			return nil, false, err
		}
		claims, err := p.decodeClaims(payload)
		return claims, false, err
	case 5:
		if len(p.decryptionKeys) == 0 {
			return nil, false, ErrInvalidDecryptionKey
		}
		plaintext, _, err := p.decryptJWE(token)
		if err != nil {
			return nil, false, err
		}
		innerToken := strings.TrimSpace(string(plaintext))
		innerParts := strings.Split(innerToken, ".")
		if len(innerParts) == 5 {
			return nil, false, ErrNestedJWE
		}
		if len(innerParts) != 3 {
			return nil, false, errorf(ErrMalformedJOSE, "JWE plaintext must contain a compact JWS")
		}
		payload, _, err := p.verifyJWS(innerToken, client)
		if err != nil {
			return nil, false, err
		}
		claims, err := p.decodeClaims(payload)
		return claims, true, err
	default:
		return nil, false, errorf(ErrMalformedJOSE, "request value must be a compact JWS or a JWE containing a JWS")
	}
}

func (p *Parser) decodeClaims(payload []byte) (map[string]any, error) {
	claims, err := decodeJSONObject(payload)
	if err != nil {
		return nil, err
	}
	if _, exists := claims["request"]; exists {
		return nil, errorf(ErrNestedRequestParameter, "request")
	}
	if _, exists := claims["request_uri"]; exists {
		return nil, errorf(ErrNestedRequestParameter, "request_uri")
	}
	return claims, nil
}

func (p *Parser) validateClaims(claims map[string]any, outerClientID string) (*RequestObject, error) {
	clientIDValue, exists := claims["client_id"]
	if !exists {
		return nil, errorf(ErrInvalidRequestObject, "request object has no client_id")
	}
	clientID, ok := clientIDValue.(string)
	if !ok || clientID == "" {
		return nil, errorf(ErrInvalidRequestObject, "client_id is not a non-empty string")
	}
	if clientID != outerClientID {
		return nil, errorf(ErrClientIDMismatch, fmt.Sprintf("query=%q object=%q", outerClientID, clientID))
	}

	issuerValue, exists := claims["iss"]
	if !exists {
		return nil, errorf(ErrIssuerMismatch, "iss is missing")
	}
	issuer, ok := issuerValue.(string)
	if !ok || issuer != clientID {
		return nil, errorf(ErrIssuerMismatch, "iss must equal client_id")
	}

	audience, ok := claimStrings(claims["aud"])
	if !ok {
		return nil, errorf(ErrAudienceMismatch, "aud is missing or malformed")
	}
	audienceContainsIssuer := false
	for _, value := range audience {
		if value == p.issuer {
			audienceContainsIssuer = true
			break
		}
	}
	if !audienceContainsIssuer {
		return nil, ErrAudienceMismatch
	}

	now := p.clock()
	if exp, ok, err := numericClaim(claims, "exp"); err != nil {
		return nil, err
	} else if ok && now.After(time.Unix(exp, 0).Add(p.leeway)) {
		return nil, ErrExpiredRequestObject
	}
	if nbf, ok, err := numericClaim(claims, "nbf"); err != nil {
		return nil, err
	} else if ok && now.Add(p.leeway).Before(time.Unix(nbf, 0)) {
		return nil, ErrExpiredRequestObject
	}

	return &RequestObject{
		Claims:   claims,
		ClientID: clientID,
		Issuer:   issuer,
		Audience: audience,
	}, nil
}

func claimString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case float64:
		return fmt.Sprintf("%v", typed), true
	}
	return "", false
}

func claimStrings(value any) ([]string, bool) {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil, false
		}
		return []string{typed}, true
	case []any:
		if len(typed) == 0 {
			return nil, false
		}
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	case []string:
		if len(typed) == 0 {
			return nil, false
		}
		return typed, true
	}
	return nil, false
}

func numericClaim(claims map[string]any, name string) (int64, bool, error) {
	value, exists := claims[name]
	if !exists {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Int64()
		if err != nil {
			return 0, false, errorf(ErrInvalidRequestObject, name+" is not an integer")
		}
		return number, true, nil
	case float64:
		return int64(typed), true, nil
	default:
		return 0, false, errorf(ErrInvalidRequestObject, name+" is not a number")
	}
}

func addClaimValues(values url.Values, name string, value any) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		values.Add(name, typed)
	case json.Number:
		values.Add(name, typed.String())
	case bool:
		if typed {
			values.Add(name, "true")
		} else {
			values.Add(name, "false")
		}
	case float64:
		values.Add(name, fmt.Sprintf("%v", typed))
	case []any:
		for _, item := range typed {
			if text, ok := claimString(item); ok {
				values.Add(name, text)
			}
		}
	case []string:
		for _, item := range typed {
			values.Add(name, item)
		}
	}
}
