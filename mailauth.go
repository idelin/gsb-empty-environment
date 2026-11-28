// Package mailauth provides a combined SPF, DKIM, and DMARC verification
// pipeline for an SMTP ingress gateway.
package mailauth

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"prescreen.local/ps-517b39ba66/dkim"
	"prescreen.local/ps-517b39ba66/dmarc"
	"prescreen.local/ps-517b39ba66/spf"
)

// Resolver is the DNS capability required by the combined verifier.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupIP(ctx context.Context, network, name string) ([]net.IP, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// DefaultResolver is used when Options.Resolver is nil.
var DefaultResolver Resolver = net.DefaultResolver

// Options controls a combined verification.
type Options struct {
	IP       net.IP
	MailFrom string
	Helo     string
	Resolver Resolver
	Now      time.Time
}

// Report is the combined result returned to the MTA.
type Report struct {
	FromDomain string         `json:"from_domain,omitempty"`
	SPF        spf.Evaluation `json:"spf"`
	DKIM       []dkim.Result  `json:"dkim"`
	DMARC      dmarc.Result   `json:"dmarc"`
}

// Verify authenticates raw against SPF, every DKIM signature, and DMARC.
func Verify(ctx context.Context, raw []byte, opts Options) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(raw) == 0 {
		return Report{}, errors.New("mailauth: empty message")
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = DefaultResolver
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	report := Report{}
	fromDomain, fromErr := dmarc.FromDomain(raw)
	if fromErr == nil {
		report.FromDomain = fromDomain
	}

	spfDomain := domainFromIdentity(opts.MailFrom)
	spfResult, _ := spf.Evaluate(ctx, spf.Options{
		IP:       opts.IP,
		Sender:   opts.MailFrom,
		Helo:     opts.Helo,
		Resolver: resolver,
		Now:      now,
	})
	report.SPF = spfResult

	dkimResults, _ := dkim.VerifyWithOptions(ctx, raw, dkim.Options{Resolver: resolver, Now: now})
	report.DKIM = dkimResults

	dkimInputs := make([]dmarc.DKIMResult, 0, len(dkimResults))
	for _, result := range dkimResults {
		dkimInputs = append(dkimInputs, dmarc.DKIMResult{
			Status: string(result.Status),
			Domain: result.Domain,
		})
	}
	dmarcResult, _ := dmarc.Evaluate(ctx, dmarc.Options{
		FromDomain: fromDomain,
		SPF: dmarc.SPFResult{
			Status: string(spfResult.Status),
			Domain: spfDomain,
		},
		DKIM:     dkimInputs,
		Resolver: resolver,
	})
	report.DMARC = dmarcResult
	return report, nil
}

// Authenticate is an alias for Verify.
func Authenticate(ctx context.Context, raw []byte, opts Options) (Report, error) {
	return Verify(ctx, raw, opts)
}

func domainFromIdentity(identity string) string {
	at := strings.LastIndexByte(identity, '@')
	if at < 0 || at == len(identity)-1 {
		return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(identity)), ".")
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(identity[at+1:])), ".")
}
