package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPExchanger exchanges authorization codes at one fixed token endpoint.
// One instance belongs to one issuer and is pinned on a transaction so a
// mixed-up callback cannot steer the exchange elsewhere.
type HTTPExchanger struct {
	// TokenEndpoint is the issuer's token endpoint, fixed at construction.
	TokenEndpoint string

	// ClientID/ClientSecret identify the relying party at this issuer.
	ClientID     string
	ClientSecret string

	// HTTPClient defaults to an http.Client with a 10s timeout.
	HTTPClient *http.Client

	// ExtraForm is merged into every token request (e.g. custom parameters).
	ExtraForm url.Values
}

// NewHTTPExchanger constructs an exchanger bound to one token endpoint.
func NewHTTPExchanger(endpoint, clientID, clientSecret string) *HTTPExchanger {
	return &HTTPExchanger{
		TokenEndpoint: endpoint,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		HTTPClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Exchange implements Exchanger.
func (e *HTTPExchanger) Exchange(ctx context.Context, req ExchangeRequest) (TokenResponse, error) {
	if e.TokenEndpoint == "" {
		return TokenResponse{}, errors.New("netsec: exchanger has no token endpoint")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("redirect_uri", req.RedirectURI)
	form.Set("client_id", e.ClientID)
	if e.ClientSecret != "" {
		form.Set("client_secret", e.ClientSecret)
	}
	if req.CodeVerifier != "" {
		form.Set("code_verifier", req.CodeVerifier)
	}
	for key, values := range e.ExtraForm {
		for _, value := range values {
			form.Add(key, value)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.TokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	client := e.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("netsec: token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return TokenResponse{}, fmt.Errorf("netsec: token endpoint status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw := make(map[string]any)
	if err := json.Unmarshal(body, &raw); err != nil {
		return TokenResponse{}, fmt.Errorf("netsec: malformed token response: %w", err)
	}

	var expiresIn int64
	switch v := raw["expires_in"].(type) {
	case float64:
		expiresIn = int64(v)
	case int64:
		expiresIn = v
	}

	return TokenResponse{
		AccessToken: stringField(raw, "access_token"),
		TokenType:   stringField(raw, "token_type"),
		IDToken:     stringField(raw, "id_token"),
		ExpiresIn:   expiresIn,
		Raw:         raw,
	}, nil
}

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}
