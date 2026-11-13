// Package netsec implements the JWT Secured Authorization Response Mode
// (JARM) used by an OAuth 2.0 authorization service and its relying parties.
//
// When response_mode is "jwt" (or the "jwt" variant of another response mode)
// the authorization result is carried inside a signed JWT in the response
// parameter. The outer query parameters are transport metadata only: any
// outer parameter that shares a name with a JWT claim, including code and
// state, is ignored and never treated as part of the authorization result.
// The token signature, issuer, audience, expiry and state claims must all
// match the relying party configuration before the payload is accepted.
//
// For any non-jwt response mode the callback is read as plain query
// parameters in the conventional way.
package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// responseJWTParam is the callback parameter that carries the JARM response
// token, as defined by the JARM specification.
const responseJWTParam = "response"

var (
	// ErrMissingResponse is returned when a jwt response_mode callback does
	// not contain a response parameter.
	ErrMissingResponse = errors.New("netsec: missing response parameter")
	// ErrInvalidConfiguration is returned when a Verifier or Signer is used
	// without the required key, issuer or audience configuration.
	ErrInvalidConfiguration = errors.New("netsec: invalid configuration")
	// ErrIssuerMismatch is returned when the token iss claim does not equal
	// the configured issuer.
	ErrIssuerMismatch = errors.New("netsec: issuer mismatch")
	// ErrAudienceMismatch is returned when the token aud claim does not
	// contain the configured client audience.
	ErrAudienceMismatch = errors.New("netsec: audience mismatch")
	// ErrTokenExpired is returned when the token exp claim is not in the
	// future after applying the configured clock leeway.
	ErrTokenExpired = errors.New("netsec: token expired")
	// ErrStateMismatch is returned when the token state claim does not equal
	// the state expected by the relying party.
	ErrStateMismatch = errors.New("netsec: state mismatch")
	// ErrMissingClaim is returned when a required claim such as exp is absent
	// or has an unexpected type.
	ErrMissingClaim = errors.New("netsec: missing or invalid claim")
	// ErrUnsupportedResponseMode is returned for an unrecognised response
	// mode value.
	ErrUnsupportedResponseMode = errors.New("netsec: unsupported response mode")
)

// AuthorizationResponse is the validated result of an authorization callback.
//
// In jwt mode Parameters contains only the claims found inside the verified
// JWT payload. Registered JWT claims such as iss, aud and exp are exposed
// through the dedicated fields and never mixed into Parameters. In query
// mode Parameters holds the ordinary query parameters.
type AuthorizationResponse struct {
	// Mode is the response mode used to deliver the response.
	Mode string
	// Issuer is the verified iss claim, populated in jwt mode only.
	Issuer string
	// Audience is the verified aud claim, populated in jwt mode only.
	Audience string
	// Expiry is the verified exp claim, populated in jwt mode only.
	Expiry time.Time
	// Parameters holds the authorization result parameters (code, state and
	// any service specific parameters).
	Parameters map[string]string
}

// Get returns the named result parameter and whether it was present.
func (r *AuthorizationResponse) Get(name string) (string, bool) {
	value, ok := r.Parameters[name]
	return value, ok
}

// Code returns the authorization code. In jwt mode this is always the code
// claim from inside the JWT, never an outer query parameter.
func (r *AuthorizationResponse) Code() string {
	return r.Parameters["code"]
}

// State returns the state echoed by the authorization service.
func (r *AuthorizationResponse) State() string {
	return r.Parameters["state"]
}

// Verifier validates JARM callbacks for a single relying party.
type Verifier struct {
	// Key is the shared HMAC secret used to verify HS256 response tokens.
	Key []byte
	// Issuer is the expected value of the JWT iss claim.
	Issuer string
	// Audience is the client identifier that must appear in the JWT aud claim.
	Audience string
	// ExpectedState, when non-empty, must equal the JWT state claim.
	ExpectedState string
	// Leeway is subtracted from exp when checking expiry to tolerate clock
	// skew between the authorization service and the relying party.
	Leeway time.Duration
	// now is overridable in tests; zero means time.Now is used.
	now func() time.Time
}

// ParseRequest validates an authorization callback request. responseMode is
// the mode that the relying party requested when starting the flow.
func (v *Verifier) ParseRequest(r *http.Request, responseMode string) (*AuthorizationResponse, error) {
	return v.ParseCallback(r.URL, responseMode)
}

// ParseCallback validates an authorization callback URL. It is the URL based
// counterpart of ParseRequest and is useful outside an HTTP handler.
func (v *Verifier) ParseCallback(callbackURL *url.URL, responseMode string) (*AuthorizationResponse, error) {
	if responseMode == "" {
		return nil, fmt.Errorf("%w: empty response mode", ErrUnsupportedResponseMode)
	}
	query := callbackURL.Query()
	if !isJWTMode(responseMode) {
		return parseQueryResponse(responseMode, query), nil
	}
	return v.parseJWTResponse(responseMode, query)
}

// isJWTMode reports whether responseMode delivers the result inside a signed
// JWT. Both the bare "jwt" mode and composite values such as
// "form_post.jwt" or "query.jwt" use JARM.
func isJWTMode(responseMode string) bool {
	return responseMode == "jwt" || strings.HasSuffix(responseMode, ".jwt")
}

// parseQueryResponse reads a conventional non-jwt callback whose result
// parameters live directly in the query string.
func parseQueryResponse(mode string, query url.Values) *AuthorizationResponse {
	parameters := make(map[string]string, len(query))
	for name, values := range query {
		if len(values) > 0 {
			parameters[name] = values[len(values)-1]
		}
	}
	return &AuthorizationResponse{Mode: mode, Parameters: parameters}
}

// parseJWTResponse verifies the JARM token and builds the response solely
// from its payload. Outer query parameters are intentionally discarded.
func (v *Verifier) parseJWTResponse(mode string, query url.Values) (*AuthorizationResponse, error) {
	if len(v.Key) == 0 || v.Issuer == "" || v.Audience == "" {
		return nil, ErrInvalidConfiguration
	}
	token := query.Get(responseJWTParam)
	if token == "" {
		return nil, ErrMissingResponse
	}

	claims, err := verifyJWT(token, v.Key)
	if err != nil {
		return nil, err
	}
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}

	parameters := make(map[string]string)
	for name, value := range claims {
		if registeredClaims[name] {
			continue
		}
		parameters[name] = claimString(value)
	}

	return &AuthorizationResponse{
		Mode:       mode,
		Issuer:     claimString(claims["iss"]),
		Audience:   claimString(claims["aud"]),
		Expiry:     claimTime(claims["exp"]),
		Parameters: parameters,
	}, nil
}

// registeredClaims are the standard JWT/JARM claims that describe the token
// rather than the authorization result and therefore never appear in
// AuthorizationResponse.Parameters.
var registeredClaims = map[string]bool{
	"iss": true,
	"aud": true,
	"exp": true,
	"nbf": true,
	"iat": true,
	"jti": true,
	"sub": true,
}

// validateClaims enforces the iss, aud, exp and state requirements.
func (v *Verifier) validateClaims(claims map[string]any) error {
	issuer, ok := claims["iss"].(string)
	if !ok || issuer == "" {
		return fmt.Errorf("%w: iss", ErrMissingClaim)
	}
	if issuer != v.Issuer {
		return fmt.Errorf("%w: got %q want %q", ErrIssuerMismatch, issuer, v.Issuer)
	}

	if !audienceContains(claims["aud"], v.Audience) {
		return fmt.Errorf("%w: %q", ErrAudienceMismatch, v.Audience)
	}

	expiry, err := numericClaim(claims, "exp")
	if err != nil {
		return err
	}
	now := v.currentTime()
	if now.After(time.Unix(expiry, 0).Add(v.Leeway)) {
		return fmt.Errorf("%w: exp %s", ErrTokenExpired, time.Unix(expiry, 0).Format(time.RFC3339))
	}

	if v.ExpectedState != "" {
		state, _ := claims["state"].(string)
		if state != v.ExpectedState {
			return fmt.Errorf("%w: got %q want %q", ErrStateMismatch, state, v.ExpectedState)
		}
	}
	return nil
}

// audienceContains accepts both a single aud string and an aud array.
func audienceContains(audienceClaim any, expected string) bool {
	switch value := audienceClaim.(type) {
	case string:
		return value == expected
	case []any:
		for _, entry := range value {
			if entry, ok := entry.(string); ok && entry == expected {
				return true
			}
		}
	}
	return false
}

// numericClaim reads a NumericDate claim such as exp.
func numericClaim(claims map[string]any, name string) (int64, error) {
	value, ok := claims[name]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingClaim, name)
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrMissingClaim, name)
	}
	return int64(number), nil
}

func claimTime(value any) time.Time {
	number, ok := value.(float64)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(number), 0)
}

// claimString renders non-string claims deterministically while preserving
// ordinary string claims verbatim.
func claimString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func (v *Verifier) currentTime() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

// Signer produces JARM response tokens on the authorization service side.
type Signer struct {
	// Key is the shared HMAC secret used to sign HS256 response tokens.
	Key []byte
	// Issuer is placed in the JWT iss claim.
	Issuer string
	// Audience is placed in the JWT aud claim, normally the client id.
	Audience string
	// DefaultTTL is the token lifetime used when SignResponse receives a zero
	// expiry; zero means one minute.
	DefaultTTL time.Duration
	// now is overridable in tests; zero means time.Now is used.
	now func() time.Time
}

// SignResponse builds a signed JARM token containing the result parameters
// plus the iss, aud, exp and state claims. state is read from parameters and
// also retained there so the relying party can echo it back.
func (s *Signer) SignResponse(parameters map[string]string, expiry time.Time) (string, error) {
	if len(s.Key) == 0 || s.Issuer == "" || s.Audience == "" {
		return "", ErrInvalidConfiguration
	}
	now := s.currentTime()
	if expiry.IsZero() {
		ttl := s.DefaultTTL
		if ttl <= 0 {
			ttl = time.Minute
		}
		expiry = now.Add(ttl)
	}

	claims := make(map[string]any, len(parameters)+4)
	for name, value := range parameters {
		claims[name] = value
	}
	claims["iss"] = s.Issuer
	claims["aud"] = s.Audience
	claims["exp"] = expiry.Unix()
	claims["iat"] = now.Unix()

	return signJWT(claims, s.Key)
}

// SignCallbackURL returns the full redirect URL for a jwt response_mode
// callback. The signed token is placed in response; outer transport
// parameters such as state may be included but the relying party ignores any
// that duplicate JWT claims.
func (s *Signer) SignCallbackURL(redirectURL *url.URL, parameters map[string]string, expiry time.Time) (*url.URL, error) {
	token, err := s.SignResponse(parameters, expiry)
	if err != nil {
		return nil, err
	}
	result := *redirectURL
	query := result.Query()
	query.Set(responseJWTParam, token)
	result.RawQuery = query.Encode()
	return &result, nil
}

func (s *Signer) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
