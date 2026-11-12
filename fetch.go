package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
)

// MaxBodySize is the largest accepted assetlinks.json response body (1 MiB).
const MaxBodySize = 1 << 20

var (
	// ErrInsecureScheme is returned when the URL is not https.
	ErrInsecureScheme = errors.New("assetlinks: URL must use https")
	// ErrUnexpectedStatus is returned for any non-200 response, including
	// redirects, which are never followed.
	ErrUnexpectedStatus = errors.New("assetlinks: unexpected HTTP status")
	// ErrUnexpectedContentType is returned when Content-Type is not
	// application/json.
	ErrUnexpectedContentType = errors.New("assetlinks: Content-Type must be application/json")
	// ErrBodyTooLarge is returned when the body exceeds MaxBodySize.
	ErrBodyTooLarge = errors.New("assetlinks: response body exceeds 1 MiB")
)

// NewClient returns an http.Client that refuses to follow redirects.
func NewClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// FetchStatements retrieves url and parses it as an assetlinks.json
// document. The URL must be https, the response must be 200 with
// Content-Type application/json and a body of at most 1 MiB, and redirects
// are rejected. If client is nil, NewClient() is used; a custom client must
// not follow redirects either (its CheckRedirect is overridden).
func FetchStatements(ctx context.Context, client *http.Client, rawurl string) ([]Statement, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, fmt.Errorf("assetlinks: bad URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("%w, got %q", ErrInsecureScheme, u.Scheme)
	}
	if client == nil {
		client = NewClient()
	} else {
		c := *client
		c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		client = &c
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, fmt.Errorf("assetlinks: bad URL: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("assetlinks: fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return nil, fmt.Errorf("%w, got %q", ErrUnexpectedContentType, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("assetlinks: read body: %w", err)
	}
	if len(body) > MaxBodySize {
		return nil, ErrBodyTooLarge
	}
	return ParseStatements(body)
}

// VerifyURL fetches the assetlinks.json document at url and reports whether
// it authorizes any of the app's signing certificate fingerprints for
// packageName.
func VerifyURL(ctx context.Context, client *http.Client, rawurl, packageName string, appCertFingerprints []string) (bool, error) {
	stmts, err := FetchStatements(ctx, client, rawurl)
	if err != nil {
		return false, err
	}
	return VerifyPackage(stmts, packageName, appCertFingerprints), nil
}
