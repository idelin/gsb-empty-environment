package netsec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ensureDiscovery performs OIDC discovery once and pins the JWKS endpoint to
// the issuer's own origin so the verifier cannot be steered to fetch keys from
// another party.
func (v *Verifier) ensureDiscovery(ctx context.Context) error {
	v.discoveryMu.Lock()
	defer v.discoveryMu.Unlock()

	v.mu.Lock()
	done := v.discovered
	v.mu.Unlock()
	if done {
		return nil
	}

	endpoint := strings.TrimRight(v.issuer, "/") + discoveryPath
	body, err := v.httpGet(ctx, endpoint)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("netsec: invalid discovery document: %w", err)
	}
	jwks, _ := doc["jwks_uri"].(string)
	if jwks == "" {
		return fmt.Errorf("netsec: discovery document for %q has no jwks_uri", v.issuer)
	}
	if !sameOrigin(v.issuer, jwks) {
		return fmt.Errorf("netsec: jwks_uri %q is not served by issuer %q", jwks, v.issuer)
	}

	v.mu.Lock()
	v.jwksURI = jwks
	v.discovered = true
	v.mu.Unlock()
	return nil
}

func (v *Verifier) fetchJWKS(ctx context.Context) (*KeySet, error) {
	v.mu.Lock()
	endpoint := v.jwksURI
	v.mu.Unlock()
	if endpoint == "" {
		return nil, fmt.Errorf("netsec: JWKS endpoint unavailable for issuer %q", v.issuer)
	}
	body, err := v.httpGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return ParseKeySet(body)
}

func (v *Verifier) httpGet(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("netsec: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("netsec: fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netsec: fetch %s: status %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("netsec: read %s: %w", endpoint, err)
	}
	return body, nil
}

// sameOrigin reports whether target shares the scheme, host and port of the
// issuer URL. Empty/opaque references are rejected.
func sameOrigin(issuer, target string) bool {
	base, err := url.Parse(issuer)
	if err != nil || base.Host == "" {
		return false
	}
	ref, err := url.Parse(target)
	if err != nil || ref.Host == "" {
		return false
	}
	return strings.EqualFold(base.Scheme, ref.Scheme) &&
		strings.EqualFold(base.Host, ref.Host)
}
