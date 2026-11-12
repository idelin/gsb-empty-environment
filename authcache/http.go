package authcache

import (
	"net/http"
	"net/url"
)

// HTTPAuthorizer performs authorization subrequests against an authorization
// endpoint using an http.Client.
type HTTPAuthorizer struct {
	Endpoint *url.URL
	Client   *http.Client
}

// NewHTTPAuthorizer parses endpoint and returns an HTTPAuthorizer that uses
// client for subrequests. A nil client means http.DefaultClient.
func NewHTTPAuthorizer(endpoint string, client *http.Client) (*HTTPAuthorizer, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPAuthorizer{Endpoint: parsed, Client: client}, nil
}

// Authorize sends a subrequest to the authorization endpoint. Like the Nginx
// auth_request module it forwards the original method and request headers but
// no body and no query string, and returns the service response verbatim.
func (a *HTTPAuthorizer) Authorize(r *http.Request) (*http.Response, error) {
	sub, err := http.NewRequestWithContext(r.Context(), r.Method, a.Endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	sub.Header = r.Header.Clone()

	resp, err := a.Client.Do(sub)
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	return resp, nil
}
