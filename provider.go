package netsec

import "context"

// Exchanger performs the authorization-code-to-token exchange against a
// single, transaction-pinned token endpoint. Implementations must not infer
// the endpoint from callback data: the endpoint is fixed when the transaction
// begins so an IdP mix-up callback cannot redirect the exchange to an
// attacker-controlled provider.
type Exchanger interface {
	Exchange(ctx context.Context, req ExchangeRequest) (TokenResponse, error)
}

// Validator verifies an ID token against a single, transaction-pinned issuer.
type Validator interface {
	VerifyIDToken(ctx context.Context, rawIDToken, nonce string) (Claims, error)
}

// ExchangeRequest is the fixed set of inputs for a token exchange.
type ExchangeRequest struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
}

// TokenResponse is the parsed token endpoint response.
type TokenResponse struct {
	AccessToken string
	TokenType   string
	IDToken     string
	ExpiresIn   int64
	Raw         map[string]any
}

// Claims is the verified identity payload carried by an ID token.
type Claims map[string]any

// Issuer returns the issuer claim ("iss").
func (c Claims) Issuer() string {
	v, _ := c["iss"].(string)
	return v
}

// Subject returns the subject claim ("sub").
func (c Claims) Subject() string {
	v, _ := c["sub"].(string)
	return v
}

// Nonce returns the nonce claim ("nonce").
func (c Claims) Nonce() string {
	v, _ := c["nonce"].(string)
	return v
}
