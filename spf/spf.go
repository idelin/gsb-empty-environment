// Package spf implements SPF (RFC 7208) record evaluation.
package spf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// Result is the outcome of an SPF evaluation.
type Result string

// Status is an alias for Result.
type Status = Result

const (
	None      Result = "none"
	Neutral   Result = "neutral"
	Pass      Result = "pass"
	Fail      Result = "fail"
	SoftFail  Result = "softfail"
	TempError Result = "temperror"
	PermError Result = "permerror"

	ResultNone      = None
	ResultNeutral   = Neutral
	ResultPass      = Pass
	ResultFail      = Fail
	ResultSoftFail  = SoftFail
	ResultTempError = TempError
	ResultPermError = PermError
)

// Resolver is the subset of DNS operations required by SPF. *net.Resolver
// satisfies this interface.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupIP(ctx context.Context, network, name string) ([]net.IP, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// DefaultResolver is used when Options.Resolver is nil.
var DefaultResolver Resolver = net.DefaultResolver

const (
	defaultMaxLookups = 10
	defaultMaxVoids   = 2
)

// Options controls one SPF evaluation.
type Options struct {
	// IP is the connecting client address.
	IP net.IP
	// Domain is the MAIL FROM domain. Sender takes precedence when it contains
	// an '@' and Domain is empty.
	Domain string
	// Sender is the complete MAIL FROM identity, for example user@example.com.
	Sender string
	// Helo is the HELO/EHLO name.
	Helo string
	// Resolver defaults to DefaultResolver.
	Resolver Resolver
	// Now is used for %{t} macro expansion. It defaults to time.Now.
	Now time.Time
	// MaxDNSLookups defaults to 10.
	MaxDNSLookups int
	// MaxVoidLookups defaults to 2.
	MaxVoidLookups int
}

// Evaluation contains the SPF result and diagnostic counters.
type Evaluation struct {
	Result      Result `json:"result"`
	Status      Result `json:"status"`
	Domain      string `json:"domain"`
	Helo        string `json:"helo,omitempty"`
	Sender      string `json:"sender,omitempty"`
	Record      string `json:"record,omitempty"`
	Explanation string `json:"explanation,omitempty"`
	DNSLookups  int    `json:"dns_lookups"`
	VoidLookups int    `json:"void_lookups"`
	Err         error  `json:"-"`
}

// Check evaluates SPF for ip against domain. It returns only the result; use
// Evaluate when the lookup counters or record are needed.
func Check(ctx context.Context, ip net.IP, domain string, resolver Resolver) (Result, error) {
	evaluation, err := Evaluate(ctx, Options{IP: ip, Domain: domain, Resolver: resolver})
	return evaluation.Result, err
}

// CheckHost is an alias for Check with the argument order commonly used by MTA
// integrations.
func CheckHost(ctx context.Context, ip net.IP, domain string, resolver Resolver) (Result, error) {
	return Check(ctx, ip, domain, resolver)
}

// Evaluate performs a complete SPF check.
func Evaluate(ctx context.Context, opts Options) (Evaluation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	domain := strings.TrimSpace(opts.Domain)
	if domain == "" && strings.Contains(opts.Sender, "@") {
		domain = opts.Sender[strings.LastIndex(opts.Sender, "@")+1:]
	}
	domain = normalizeDomain(domain)

	evaluation := Evaluation{
		Domain: domain,
		Helo:   opts.Helo,
		Sender: opts.Sender,
	}
	if domain == "" {
		evaluation.Result = PermError
		evaluation.Status = PermError
		evaluation.Explanation = "empty MAIL FROM domain"
		evaluation.Err = errors.New("spf: empty MAIL FROM domain")
		return evaluation, evaluation.Err
	}
	addr, ok := addrFromIP(opts.IP)
	if !ok {
		evaluation.Result = PermError
		evaluation.Status = PermError
		evaluation.Explanation = "invalid client IP address"
		evaluation.Err = errors.New("spf: invalid client IP address")
		return evaluation, evaluation.Err
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = DefaultResolver
	}
	maxLookups := opts.MaxDNSLookups
	if maxLookups <= 0 {
		maxLookups = defaultMaxLookups
	}
	maxVoids := opts.MaxVoidLookups
	if maxVoids <= 0 {
		maxVoids = defaultMaxVoids
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	state := &evalState{
		ctx:      ctx,
		resolver: resolver,
		ip:       addr,
		helo:     opts.Helo,
		sender:   opts.Sender,
		now:      now,
		maxTerms: maxLookups,
		maxVoids: maxVoids,
		stack:    make(map[string]bool),
	}
	result, record, explanation := state.evaluateDomain(domain, true)
	evaluation.Result = result
	evaluation.Status = result
	evaluation.Record = record
	evaluation.Explanation = explanation
	evaluation.DNSLookups = state.terms
	evaluation.VoidLookups = state.voids
	if result == TempError || result == PermError {
		evaluation.Err = errors.New(explanation)
	}
	return evaluation, nil
}

type evalState struct {
	ctx      context.Context
	resolver Resolver
	ip       netip.Addr
	helo     string
	sender   string
	now      time.Time
	terms    int
	voids    int
	maxTerms int
	maxVoids int
	stack    map[string]bool
}

var (
	errLookupLimit = errors.New("SPF DNS lookup limit exceeded")
	errVoidLimit   = errors.New("SPF void lookup limit exceeded")
)

func (s *evalState) addTerm() error {
	s.terms++
	if s.terms > s.maxTerms {
		return errLookupLimit
	}
	return nil
}

func (s *evalState) addVoid() error {
	s.voids++
	if s.voids > s.maxVoids {
		return errVoidLimit
	}
	return nil
}

// evaluateDomain evaluates the record for domain. required is true for include
// and redirect targets, where the absence of an SPF record is a permerror.
func (s *evalState) evaluateDomain(domain string, required bool) (Result, string, string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return PermError, "", "empty domain in SPF record"
	}
	if s.stack[domain] {
		return PermError, "", "SPF include/redirect loop at " + domain
	}
	s.stack[domain] = true
	defer delete(s.stack, domain)

	texts, void, err := s.lookupTXT(domain)
	if err != nil {
		if errors.Is(err, errVoidLimit) {
			return PermError, "", err.Error()
		}
		return TempError, "", err.Error()
	}
	if void {
		if required {
			return PermError, "", "no SPF record at " + domain
		}
		return None, "", "no SPF record at " + domain
	}

	record, count, err := selectRecord(texts)
	if err != nil {
		return PermError, "", err.Error()
	}
	if count == 0 {
		if required {
			return PermError, "", "no SPF record at " + domain
		}
		return None, "", "no SPF record at " + domain
	}

	parsed, err := parseRecord(record)
	if err != nil {
		return PermError, record, err.Error()
	}
	result, explanation := s.evaluateRecord(domain, parsed)
	return result, record, explanation
}

func (s *evalState) lookupTXT(name string) ([]string, bool, error) {
	records, err := s.resolver.LookupTXT(s.ctx, name)
	if err != nil {
		if isNotFound(err) {
			if voidErr := s.addVoid(); voidErr != nil {
				return nil, true, voidErr
			}
			return nil, true, nil
		}
		if isTemporary(err) {
			return nil, false, err
		}
		return nil, false, err
	}
	if len(records) == 0 {
		if voidErr := s.addVoid(); voidErr != nil {
			return nil, true, voidErr
		}
		return nil, true, nil
	}
	return records, false, nil
}

func (s *evalState) lookupIP(name string, network string) ([]netip.Addr, bool, error) {
	ips, err := s.resolver.LookupIP(s.ctx, network, name)
	if err != nil {
		if isNotFound(err) {
			if voidErr := s.addVoid(); voidErr != nil {
				return nil, true, voidErr
			}
			return nil, true, nil
		}
		return nil, false, err
	}
	result := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if addr, ok := addrFromIP(ip); ok {
			result = append(result, addr)
		}
	}
	if len(result) == 0 {
		if voidErr := s.addVoid(); voidErr != nil {
			return nil, true, voidErr
		}
		return nil, true, nil
	}
	return result, false, nil
}

func (s *evalState) lookupMX(name string) ([]*net.MX, bool, error) {
	mx, err := s.resolver.LookupMX(s.ctx, name)
	if err != nil {
		if isNotFound(err) {
			if voidErr := s.addVoid(); voidErr != nil {
				return nil, true, voidErr
			}
			return nil, true, nil
		}
		return nil, false, err
	}
	filtered := make([]*net.MX, 0, len(mx))
	for _, record := range mx {
		if record != nil && record.Host != "" && record.Host != "." {
			filtered = append(filtered, record)
		}
	}
	if len(filtered) == 0 {
		if voidErr := s.addVoid(); voidErr != nil {
			return nil, true, voidErr
		}
		return nil, true, nil
	}
	return filtered, false, nil
}

func (s *evalState) lookupAddr(addr string) ([]string, bool, error) {
	names, err := s.resolver.LookupAddr(s.ctx, addr)
	if err != nil {
		if isNotFound(err) {
			if voidErr := s.addVoid(); voidErr != nil {
				return nil, true, voidErr
			}
			return nil, true, nil
		}
		return nil, false, err
	}
	if len(names) == 0 {
		if voidErr := s.addVoid(); voidErr != nil {
			return nil, true, voidErr
		}
		return nil, true, nil
	}
	return names, false, nil
}

func selectRecord(texts []string) (string, int, error) {
	var found []string
	for _, text := range texts {
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "v=spf1") {
			found = append(found, strings.TrimSpace(text))
		}
	}
	if len(found) > 1 {
		return "", len(found), fmt.Errorf("multiple SPF records found")
	}
	if len(found) == 0 {
		return "", 0, nil
	}
	return found[0], 1, nil
}

func normalizeDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func addrFromIP(ip net.IP) (netip.Addr, bool) {
	if ip == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "nxdomain") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "no data") ||
		strings.Contains(message, "not found")
}

func isTemporary(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "timeout") || strings.Contains(message, "temporary")
}
