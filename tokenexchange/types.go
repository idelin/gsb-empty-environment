// Package tokenexchange implements the authorization decision logic for the
// OAuth 2.0 Token Exchange grant (RFC 8693).
//
// It does not mint tokens itself: callers parse the token endpoint request
// into a Request, pass it to Engine.Decide together with an Inspector and an
// AudiencePolicy, and use the returned Decision claims to issue the new token.
package tokenexchange

import (
	"context"
	"time"
)

// MaxDelegationDepth is the maximum number of nested actors allowed in the
// act claim of an issued token (RFC 8693 section 4.4).
const MaxDelegationDepth = 3

// TokenType identifies the concrete representation of a token, as carried in
// the subject_token_type and actor_token_type request parameters.
type TokenType string

const (
	// TypeAccessToken is the URN for an OAuth 2.0 access token.
	TypeAccessToken TokenType = "urn:ietf:params:oauth:token-type:access_token"
	// TypeRefreshToken is the URN for an OAuth 2.0 refresh token.
	TypeRefreshToken TokenType = "urn:ietf:params:oauth:token-type:refresh_token"
	// TypeIDToken is the URN for an OpenID Connect ID token.
	TypeIDToken TokenType = "urn:ietf:params:oauth:token-type:id_token"
	// TypeJWT is the URN for a generic JSON Web Token.
	TypeJWT TokenType = "urn:ietf:params:oauth:token-type:jwt"
)

// TokenInfo is the introspection result for a presented token.
type TokenInfo struct {
	// Active reports whether the token is currently valid (RFC 7662).
	Active bool
	// Revoked marks a structurally valid token that has been revoked.
	Revoked bool
	// Type is the actual token type URN as known to the authorization server.
	Type TokenType
	// Subject is the subject (sub) of the token.
	Subject string
	// ClientID is the client the token was issued to (RFC 7662 client_id).
	ClientID string
	// Scopes are the scope tokens granted to the token.
	Scopes []string
	// Audience lists the audiences the token is currently valid for.
	Audience []string
	// Expiry is the token expiration time; zero means it does not expire.
	Expiry time.Time
	// MayAct is the may_act claim authorizing an actor to act for the subject.
	MayAct *Actor
	// Act is the existing delegation chain (act claim), outermost actor first.
	Act *Actor
}

// Actor represents one entry in an act or may_act claim.
type Actor struct {
	// Subject is the issuer-unique identifier of the actor (sub).
	Subject string
	// ClientID is the OAuth client_id when the actor is identified that way.
	ClientID string
	// Act is the inner, previously established actor in a delegation chain.
	Act *Actor
}

// Requester identifies the authenticated party making the exchange request.
type Requester struct {
	// ClientID is the OAuth client_id of the requesting party.
	ClientID string
	// Subject is the authenticated subject identifier of the requesting party.
	Subject string
}

// Request is a parsed RFC 8693 token exchange request.
type Request struct {
	// Requester is the authenticated caller.
	Requester Requester
	// SubjectToken is the token being exchanged.
	SubjectToken string
	// SubjectTokenType is the declared type URN of the subject token.
	SubjectTokenType TokenType
	// ActorToken is the optional token of the acting party for delegation.
	ActorToken string
	// ActorTokenType is the declared type URN of the actor token.
	ActorTokenType TokenType
	// Audience is the requested target audience (audience parameter).
	Audience string
	// Scopes are the requested scope tokens; empty means keeping the subject
	// token's scopes.
	Scopes []string
}

// Decision is the approved outcome of an exchange request. The authorization
// server uses these claims when minting the exchanged token.
type Decision struct {
	// Subject is the subject carried over from the subject token.
	Subject string
	// Audience is the approved target audience.
	Audience string
	// Scopes are the approved scope tokens, never exceeding the subject token.
	Scopes []string
	// Act is the new delegation chain for delegation exchanges; nil otherwise.
	Act *Actor
	// Delegated reports whether the exchange is a delegation (act claim present).
	Delegated bool
}

// Inspector resolves and validates a presented token. Implementations perform
// token introspection (RFC 7662), including revocation checks, and return
// Active=false for any token that cannot be used.
type Inspector interface {
	InspectToken(ctx context.Context, token string) (TokenInfo, error)
}
