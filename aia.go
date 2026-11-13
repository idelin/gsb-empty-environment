package netsec

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var (
	// ErrSchemeNotAllowed is returned when a certificate only offers AIA
	// caIssuers URLs whose scheme is not http or https (e.g. file).
	ErrSchemeNotAllowed = errors.New("netsec: AIA caIssuers scheme is not http/https")
	// ErrLoopDetected is returned when AIA chasing revisits a URL.
	ErrLoopDetected = errors.New("netsec: AIA caIssuers loop detected")
	// ErrMaxHops is returned when the caller-provided hop limit is reached.
	ErrMaxHops = errors.New("netsec: AIA hop limit exceeded")
	// ErrIssuerMismatch is returned when a fetched certificate is not the
	// issuer of the certificate that referenced it.
	ErrIssuerMismatch = errors.New("netsec: fetched certificate is not the issuer")
)

// maxIssuerBytes caps a single AIA response body.
const maxIssuerBytes = 1 << 20

// Builder chases AIA caIssuers URLs to complete a certificate path.
// Only http and https URLs are followed, at most MaxHops fetches total,
// and any URL is fetched at most once. The final path decision is made
// by crypto/x509.
type Builder struct {
	// Roots is the trust anchor set used for the final verification.
	Roots *x509.CertPool
	// MaxHops is the caller-provided limit of AIA fetches. Zero disables
	// fetching entirely.
	MaxHops int
	// HTTPClient is used for AIA fetches. If nil, a client with a 10s
	// timeout is used.
	HTTPClient *http.Client
}

func (b *Builder) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// BuildPath completes and verifies the path for leaf, fetching missing
// issuers via AIA within the configured limits. It returns the verified
// chains on success.
func (b *Builder) BuildPath(ctx context.Context, leaf *x509.Certificate) ([][]*x509.Certificate, error) {
	if leaf == nil {
		return nil, errors.New("netsec: nil leaf certificate")
	}
	intermediates := x509.NewCertPool()
	visited := make(map[string]struct{})
	current := leaf
	hops := 0
	for {
		chains, verifyErr := leaf.Verify(x509.VerifyOptions{
			Roots:         b.Roots,
			Intermediates: intermediates,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		})
		if verifyErr == nil {
			return chains, nil
		}
		if hops >= b.MaxHops {
			return nil, fmt.Errorf("%w: %d hops, path still unverified: %v", ErrMaxHops, b.MaxHops, verifyErr)
		}
		next, err := nextIssuerURL(current, visited)
		if err != nil {
			return nil, err
		}
		visited[next] = struct{}{}
		hops++
		issuer, err := b.fetchIssuer(ctx, next)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(current.RawIssuer, issuer.RawSubject) {
			return nil, fmt.Errorf("%w: %q does not issue %q", ErrIssuerMismatch, issuer.Subject, current.Subject)
		}
		intermediates.AddCert(issuer)
		current = issuer
	}
}

// nextIssuerURL picks the first fetchable caIssuers URL of cert. URLs
// with schemes other than http/https are never fetched; a previously
// visited URL means the chase is going in circles.
func nextIssuerURL(cert *x509.Certificate, visited map[string]struct{}) (string, error) {
	sawDisallowed := false
	for _, raw := range cert.IssuingCertificateURL {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			sawDisallowed = true
			continue
		}
		if _, ok := visited[raw]; ok {
			return "", fmt.Errorf("%w: %s", ErrLoopDetected, raw)
		}
		return raw, nil
	}
	if sawDisallowed {
		return "", fmt.Errorf("%w (certificate %q)", ErrSchemeNotAllowed, cert.Subject)
	}
	return "", fmt.Errorf("netsec: no usable AIA caIssuers URL for %q", cert.Subject)
}

func (b *Builder) fetchIssuer(ctx context.Context, rawURL string) (*x509.Certificate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("netsec: AIA request %s: %w", rawURL, err)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("netsec: AIA fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netsec: AIA fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIssuerBytes))
	if err != nil {
		return nil, fmt.Errorf("netsec: AIA read %s: %w", rawURL, err)
	}
	cert, err := x509.ParseCertificate(body)
	if err != nil {
		block, _ := pem.Decode(body)
		if block == nil {
			return nil, fmt.Errorf("netsec: AIA parse %s: %w", rawURL, err)
		}
		cert, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("netsec: AIA parse %s: %w", rawURL, err)
		}
	}
	return cert, nil
}
