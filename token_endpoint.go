package netsec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// cibaGrantType is the OAuth 2.0 grant type URI defined for CIBA.
const cibaGrantType = "urn:openid:params:grant-type:ciba"

// HTTPTokenFetcher is the default TokenFetcher: it polls the authorization
// server's token endpoint with the CIBA grant after a valid ping.
type HTTPTokenFetcher struct {
	// TokenEndpoint is the authorization server's token endpoint URL.
	TokenEndpoint string
	// ClientID identifies this client.
	ClientID string
	// ClientSecret, when non-empty, is sent via HTTP Basic authentication
	// (client_secret_basic). Public clients leave it empty; then
	// client_id is sent in the request body.
	ClientSecret string
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient *http.Client
}

type tokenEndpointResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// FetchToken implements TokenFetcher. Only the auth_req_id is used: nothing
// from a notification body is forwarded or trusted.
func (f *HTTPTokenFetcher) FetchToken(ctx context.Context, authReqID string) (*TokenResponse, error) {
	if f.TokenEndpoint == "" {
		return nil, fmt.Errorf("netsec: token endpoint URL is not configured")
	}

	form := url.Values{}
	form.Set("grant_type", cibaGrantType)
	form.Set("auth_req_id", authReqID)
	if f.ClientSecret == "" {
		form.Set("client_id", f.ClientID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if f.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(f.ClientID), url.QueryEscape(f.ClientSecret))
	}

	client := f.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPingBodyBytes))
	if err != nil {
		return nil, err
	}

	var parsed tokenEndpointResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("netsec: token endpoint returned an unparseable response (status %d)", resp.StatusCode)
	}
	if parsed.Error != "" || resp.StatusCode != http.StatusOK {
		if parsed.Error != "" {
			return nil, fmt.Errorf("netsec: token endpoint error %q: %s", parsed.Error, parsed.ErrorDesc)
		}
		return nil, fmt.Errorf("netsec: token endpoint returned status %d", resp.StatusCode)
	}

	return &TokenResponse{
		AccessToken:  parsed.AccessToken,
		TokenType:    parsed.TokenType,
		ExpiresIn:    time.Duration(parsed.ExpiresIn) * time.Second,
		RefreshToken: parsed.RefreshToken,
		IDToken:      parsed.IDToken,
		Scope:        parsed.Scope,
	}, nil
}
