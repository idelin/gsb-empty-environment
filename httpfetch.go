package netsec

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// DefaultMaxStatusListBytes is the default upper bound for a fetched status
// list body.
const DefaultMaxStatusListBytes int64 = 16 << 20

// HTTPFetcher retrieves status lists over HTTP. It refuses redirects that
// leave the host of the originally requested status list, so a same-host URI
// cannot be redirected to a different host behind the verifier's back.
type HTTPFetcher struct {
	// Client is optional. Its Transport, Jar, Timeout, and CheckRedirect
	// behavior are preserved; the redirect chain is additionally constrained
	// to the original status list host.
	Client *http.Client

	// MaxBytes limits the response body size. Zero means
	// DefaultMaxStatusListBytes.
	MaxBytes int64
}

// FetchStatusList implements StatusListFetcher.
func (f *HTTPFetcher) FetchStatusList(ctx context.Context, uri string) ([]byte, error) {
	statusHost, err := StatusListHost(uri)
	if err != nil {
		return nil, err
	}

	base := f.Client
	if base == nil {
		base = http.DefaultClient
	}
	maxBytes := f.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxStatusListBytes
	}

	client := &http.Client{
		Transport: base.Transport,
		Jar:       base.Jar,
		Timeout:   base.Timeout,
	}
	originalRedirect := base.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		redirectHost, err := StatusListHost(req.URL.String())
		if err != nil {
			return err
		}
		if redirectHost != statusHost {
			return fmt.Errorf("%w: redirect host %q does not match status list host %q", ErrStatusListHostMismatch, redirectHost, statusHost)
		}
		if originalRedirect != nil {
			return originalRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("netsec: stopped after 10 redirects")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidStatusListURI, err)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("netsec: fetch status list: %w", err)
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil {
		return nil, fmt.Errorf("netsec: status list response has no request URL")
	}

	if finalHost, err := StatusListHost(resp.Request.URL.String()); err != nil || finalHost != statusHost {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: response host %q does not match status list host %q", ErrStatusListHostMismatch, finalHost, statusHost)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("netsec: status list returned HTTP %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("netsec: read status list: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("netsec: status list body exceeds %d bytes", maxBytes)
	}
	return body, nil
}
