package netsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// Record is a DNS Certification Authority Authorization (CAA) record.
type Record struct {
	Flags uint8
	Tag   string
	Value string
}

// CAARecord is an alias for Record.
type CAARecord = Record

// Critical reports whether the issuer-critical flag is set.
func (r Record) Critical() bool {
	return r.Flags&IssuerCriticalFlag != 0
}

// IssuerCriticalFlag is bit 0 of the CAA flags octet (value 128).
const IssuerCriticalFlag uint8 = 0x80

// DNSSECStatus is the validation state reported for a DNS lookup.
type DNSSECStatus uint8

const (
	// DNSSECUnknown means the resolver did not report a validation state.
	DNSSECUnknown DNSSECStatus = iota
	// DNSSECInsecure means the name is provably outside a signed zone, or
	// the resolver does not validate DNSSEC.
	DNSSECInsecure
	// DNSSECSecure means the response was validated by a DNSSEC-validating
	// resolver.
	DNSSECSecure
	// DNSSECBogus means the response failed DNSSEC validation.
	DNSSECBogus
)

func (s DNSSECStatus) String() string {
	switch s {
	case DNSSECSecure:
		return "secure"
	case DNSSECInsecure:
		return "insecure"
	case DNSSECBogus:
		return "bogus"
	default:
		return "unknown"
	}
}

// DNSSECMode controls which DNSSEC validation states are acceptable.
// A bogus response is never acceptable.
type DNSSECMode uint8

const (
	// DNSSECPermissive accepts secure, insecure, and unknown responses. This
	// is the zero value and the default. It is suitable for a CA that uses a
	// non-validating recursive resolver.
	DNSSECPermissive DNSSECMode = iota
	// DNSSECAcceptInsecure accepts secure and insecure responses, but rejects
	// responses whose validation state is unknown.
	DNSSECAcceptInsecure
	// DNSSECRequireSecure accepts only DNSSEC-validated responses.
	DNSSECRequireSecure
)

// Delegation describes a DNS referral encountered while resolving CAA.
// A ReferralResolver can use it to continue the lookup at the delegated zone.
type Delegation struct {
	// Zone is the owner name of the delegation NS RRset.
	Zone string
	// NameServers are the names or addresses of the delegated name servers.
	NameServers []string
}

// DNAME describes a DNAME alias. DNS responses normally also include the
// synthesized CNAME, but supporting the DNAME directly avoids depending on
// that behavior in custom resolvers.
type DNAME struct {
	Owner  string
	Target string
}

// LookupResult is the answer to a single CAA query.
//
// A resolver must report CNAME/DNAME aliases rather than silently hiding
// them. The checker follows aliases itself so that it can enforce loop limits
// and re-evaluate authorization after the lookup chain changes. If Records is
// non-empty it takes precedence over an alias.
type LookupResult struct {
	Records    []Record
	CNAME      string
	DNAME      *DNAME
	Delegation *Delegation
	DNSSEC     DNSSECStatus
	// AnswerName is the owner name of Records when it differs from the
	// queried name, for example when a recursive resolver returned the CAA
	// RRset at a CNAME target. It is used for audit reporting.
	AnswerName string
}

// Resolver performs a CAA query for a fully qualified domain name. The name
// is normalized (lowercase, no trailing dot). Implementations should return
// DNS resolution failures as errors, including timeouts. Returning an error
// always causes the checker to deny issuance.
type Resolver interface {
	LookupCAA(ctx context.Context, name string) (LookupResult, error)
}

// ResolverFunc adapts a function to a Resolver.
type ResolverFunc func(ctx context.Context, name string) (LookupResult, error)

// LookupCAA calls f.
func (f ResolverFunc) LookupCAA(ctx context.Context, name string) (LookupResult, error) {
	return f(ctx, name)
}

// ReferralResolver is an optional interface for resolvers that can continue
// a lookup after receiving a delegation referral. It is required when a
// Resolver reports LookupResult.Delegation. Without it the checker fails
// closed rather than treating an unanswered referral as permission to issue.
type ReferralResolver interface {
	LookupCAAVia(ctx context.Context, name string, referral Delegation) (LookupResult, error)
}

// ParameterPolicy decides whether an issuer-parameter is understood and
// acceptable. Parameter names are lowercase. A nil validator accepts any
// value for the named parameter. A parameter that is absent from the map is
// not understood, so the containing issue/issuewild record does not grant
// authorization. This is deliberately fail-closed.
type ParameterPolicy map[string]func(value string) bool

func (p ParameterPolicy) accepts(name, value string) bool {
	validator, ok := p[strings.ToLower(name)]
	if !ok {
		return false
	}
	return validator == nil || validator(value)
}

// Request identifies the certificate name whose CAA policy is checked.
type Request struct {
	// Name is a DNS name. A leading "*." is accepted and implies Wildcard.
	Name string
	// Wildcard requests apply the issuewild property instead of issue when
	// any issuewild property is present.
	Wildcard bool
}

// Policy configures a Checker.
type Policy struct {
	// IssuerDomainNames are the CA's CAA issuer-domain-name identities. At
	// least one is needed whenever a relevant CAA RRset contains issue or
	// issuewild properties. Comparisons are case-insensitive.
	IssuerDomainNames []string

	// DNSSECMode controls acceptable DNSSEC validation states. DNSSECBogus is
	// always denied.
	DNSSECMode DNSSECMode

	// Parameters lists issuer-parameters understood by this CA.
	Parameters ParameterPolicy

	// Logger receives unknown-tag, iodef, alias, and delegation records.
	// The zero value uses slog.Default.
	Logger *slog.Logger

	// QueryTimeout bounds each DNS query. Zero uses five seconds. A resolver
	// timeout or any other lookup error denies issuance.
	QueryTimeout time.Duration

	// MaxAliasDepth bounds CNAME/DNAME chains at each name. Zero uses 8.
	MaxAliasDepth int

	// MaxDelegations bounds referral chains at each name. Zero uses 8.
	MaxDelegations int

	// MaxLookups bounds the total number of DNS queries for one decision.
	// Zero uses 128.
	MaxLookups int

	// AllowUnknownCritical, when true, ignores unknown tags even if their
	// issuer-critical flag is set. RFC 8659 requires such a policy to block
	// issuance, so the default is false.
	AllowUnknownCritical bool
}

// Decision is the result of a CAA check.
type Decision struct {
	Allowed       bool
	Reason        string
	RequestName   string
	Wildcard      bool
	RelevantName  string
	Records       []Record
	MatchedTag    string
	MatchedIssuer string
	UnknownTags   []Record
	IODef         []string
	AliasChain    []string
	Delegations   []Delegation
	DNSSEC        DNSSECStatus

	dnssecSet bool
}

// Checker evaluates CAA authorization. It is safe for concurrent use if the
// supplied Resolver is safe for concurrent use.
type Checker struct {
	resolver Resolver
	policy   Policy
	logger   *slog.Logger
}

const (
	defaultQueryTimeout   = 5 * time.Second
	defaultMaxAliasDepth  = 8
	defaultMaxDelegations = 8
	defaultMaxLookups     = 128
)

var (
	// ErrInvalidRequest indicates that the requested DNS name or wildcard
	// form is invalid.
	ErrInvalidRequest = errors.New("netsec: invalid CAA request")
	// ErrLookupFailed indicates that CAA could not be determined because a
	// DNS lookup failed. Issuance must be denied.
	ErrLookupFailed = errors.New("netsec: CAA lookup failed")
	// ErrAliasLoop indicates a CNAME or DNAME loop.
	ErrAliasLoop = errors.New("netsec: CAA alias loop")
	// ErrDelegationLoop indicates a delegation loop.
	ErrDelegationLoop = errors.New("netsec: CAA delegation loop")
	// ErrDelegationUnsupported indicates that a referral could not be
	// followed because the resolver lacks ReferralResolver.
	ErrDelegationUnsupported = errors.New("netsec: resolver cannot follow CAA delegation")
	// ErrNoIssuerIdentity indicates that a CAA RRset restricts issuance but
	// the checker was not configured with any issuer-domain-name.
	ErrNoIssuerIdentity = errors.New("netsec: no CAA issuer identity configured")
	// ErrInvalidPolicy indicates an unsupported policy configuration.
	ErrInvalidPolicy = errors.New("netsec: invalid CAA policy")
	// ErrNotAuthorized indicates that CAA policy did not authorize issuance.
	ErrNotAuthorized = errors.New("netsec: CAA does not authorize issuance")
)

// NewChecker returns a Checker. A nil resolver is a configuration error
// reported by Check.
func NewChecker(resolver Resolver, policy Policy) *Checker {
	policy.IssuerDomainNames = append([]string(nil), policy.IssuerDomainNames...)
	if policy.Parameters != nil {
		parameters := make(ParameterPolicy, len(policy.Parameters))
		for name, validator := range policy.Parameters {
			parameters[name] = validator
		}
		policy.Parameters = parameters
	}
	logger := policy.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Checker{
		resolver: resolver,
		policy:   policy,
		logger:   logger,
	}
}

// Check performs a CAA authorization check. A denied result has a nil error
// when the DNS answer was obtained and a policy decision was possible. A
// lookup, configuration, or parsing failure returns an error and a Decision
// with Allowed false.
func (c *Checker) Check(ctx context.Context, request Request) (Decision, error) {
	return c.check(ctx, request)
}

// Authorize is an error-only convenience wrapper for certificate issuance
// paths. It returns nil only when Check permits issuance. Lookup and
// configuration errors are returned unchanged; a policy denial is wrapped
// with ErrNotAuthorized and the decision's reason.
func (c *Checker) Authorize(ctx context.Context, request Request) error {
	decision, err := c.Check(ctx, request)
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf("%w: %s", ErrNotAuthorized, decision.Reason)
	}
	return nil
}

// CheckName is a convenience wrapper around Check.
func (c *Checker) CheckName(ctx context.Context, name string, wildcard bool) (Decision, error) {
	return c.Check(ctx, Request{Name: name, Wildcard: wildcard})
}

// Check performs a one-shot CAA authorization check.
func Check(ctx context.Context, resolver Resolver, request Request, policy Policy) (Decision, error) {
	return NewChecker(resolver, policy).Check(ctx, request)
}

func (c *Checker) check(ctx context.Context, request Request) (Decision, error) {
	decision := Decision{}
	name, wildcard, err := normalizeRequest(request)
	if err != nil {
		decision.Reason = err.Error()
		return decision, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	decision.RequestName = name
	decision.Wildcard = wildcard

	if ctx == nil {
		ctx = context.Background()
	}
	if c.resolver == nil {
		decision.Reason = "no CAA resolver configured"
		return decision, fmt.Errorf("%w: nil resolver", ErrLookupFailed)
	}
	if err := validateIdentities(c.policy.IssuerDomainNames); err != nil {
		decision.Reason = err.Error()
		return decision, err
	}
	if c.policy.DNSSECMode > DNSSECRequireSecure {
		decision.Reason = fmt.Sprintf("unsupported DNSSEC mode %d", c.policy.DNSSECMode)
		return decision, fmt.Errorf("%w: DNSSEC mode %d", ErrInvalidPolicy, c.policy.DNSSECMode)
	}

	maxLookups := c.policy.MaxLookups
	if maxLookups <= 0 {
		maxLookups = defaultMaxLookups
	}
	lookups := 0

	// RFC 8659 Section 3: climb the DNS tree from the requested name. Aliases
	// are chased at each step, but failure to find CAA at an alias target
	// causes the climb to continue with the parent of the *original* name,
	// not the parent of the alias target.
	for base := name; base != "."; base = parentName(base) {
		current := base
		var result LookupResult
		var err error
		resultName := current
		aliasVisited := map[string]struct{}{current: {}}
		aliasDepth := c.limit(c.policy.MaxAliasDepth, defaultMaxAliasDepth)

		for depth := 0; ; depth++ {
			if lookups >= maxLookups {
				decision.Reason = "CAA lookup limit exceeded"
				return decision, fmt.Errorf("%w: lookup limit exceeded", ErrLookupFailed)
			}
			lookups++

			result, err = c.lookupChain(ctx, current, &decision, &lookups, maxLookups)
			if err != nil {
				decision.Reason = err.Error()
				return decision, err
			}

			if len(result.Records) > 0 {
				resultName = current
				if result.AnswerName != "" {
					normalized, err := NormalizeName(result.AnswerName)
					if err != nil {
						decision.Reason = fmt.Sprintf("invalid CAA answer owner %q", result.AnswerName)
						return decision, fmt.Errorf("%w: invalid CAA answer owner %q: %v", ErrLookupFailed, result.AnswerName, err)
					}
					resultName = normalized
				}
				break
			}
			if result.CNAME != "" {
				if depth >= aliasDepth {
					decision.Reason = "CAA CNAME chain too deep"
					return decision, fmt.Errorf("%w: %w: CNAME depth exceeded at %s", ErrLookupFailed, ErrAliasLoop, current)
				}
				target, err := NormalizeName(result.CNAME)
				if err != nil {
					decision.Reason = err.Error()
					return decision, fmt.Errorf("%w: invalid CNAME target %q: %v", ErrLookupFailed, result.CNAME, err)
				}
				if _, ok := aliasVisited[target]; ok {
					decision.Reason = "CAA CNAME loop"
					return decision, fmt.Errorf("%w: %w: %s -> %s", ErrLookupFailed, ErrAliasLoop, current, target)
				}
				aliasVisited[target] = struct{}{}
				decision.AliasChain = append(decision.AliasChain, target)
				c.logger.DebugContext(ctx, "following CAA CNAME", "from", current, "to", target)
				current = target
				continue
			}
			if result.DNAME != nil {
				if depth >= aliasDepth {
					decision.Reason = "CAA DNAME chain too deep"
					return decision, fmt.Errorf("%w: %w: DNAME depth exceeded at %s", ErrLookupFailed, ErrAliasLoop, current)
				}
				owner, ownerErr := NormalizeName(result.DNAME.Owner)
				if ownerErr == nil && owner == current {
					// A DNAME at its owner name does not alias the owner
					// itself (RFC 6672). Treat the CAA answer as empty.
					c.logger.DebugContext(ctx, "ignoring DNAME at its owner name", "name", current)
					break
				}
				target, err := applyDNAME(current, *result.DNAME)
				if err != nil {
					decision.Reason = err.Error()
					return decision, fmt.Errorf("%w: invalid DNAME at %s: %v", ErrLookupFailed, current, err)
				}
				if _, ok := aliasVisited[target]; ok {
					decision.Reason = "CAA DNAME loop"
					return decision, fmt.Errorf("%w: %w: %s -> %s", ErrLookupFailed, ErrAliasLoop, current, target)
				}
				aliasVisited[target] = struct{}{}
				decision.AliasChain = append(decision.AliasChain, target)
				c.logger.DebugContext(ctx, "following CAA DNAME", "from", current, "to", target,
					"owner", result.DNAME.Owner, "target", result.DNAME.Target)
				current = target
				continue
			}
			break
		}

		// An empty RRset does not stop the climb. The first non-empty set is
		// relevant, even if it contains only iodef or unknown properties.
		if len(result.Records) == 0 {
			continue
		}

		decision.RelevantName = resultName
		decision.Records = append([]Record(nil), result.Records...)
		allowed, reason, err := c.evaluate(ctx, &decision, wildcard)
		if err != nil {
			decision.Reason = reason
			return decision, err
		}
		decision.Allowed = allowed
		decision.Reason = reason
		return decision, nil
	}

	decision.Allowed = true
	decision.Reason = "no CAA records found"
	return decision, nil
}

func (c *Checker) lookupChain(ctx context.Context, name string, decision *Decision, lookups *int, maxLookups int) (LookupResult, error) {
	var referral *Delegation
	visitedZones := map[string]struct{}{}
	maxDelegations := c.limit(c.policy.MaxDelegations, defaultMaxDelegations)

	for depth := 0; ; depth++ {
		var (
			result LookupResult
			err    error
		)
		if referral == nil {
			result, err = c.query(ctx, name, nil)
		} else {
			result, err = c.query(ctx, name, referral)
		}
		if err != nil {
			return result, fmt.Errorf("%w: %s: %w", ErrLookupFailed, name, err)
		}
		if err := c.recordDNSSEC(ctx, result.DNSSEC, name, decision); err != nil {
			return result, err
		}

		if len(result.Records) > 0 || result.CNAME != "" || result.DNAME != nil || result.Delegation == nil {
			return result, nil
		}

		zone := strings.ToLower(strings.TrimSuffix(result.Delegation.Zone, "."))
		if _, ok := visitedZones[zone]; ok {
			return result, fmt.Errorf("%w: %w: %s", ErrLookupFailed, ErrDelegationLoop, zone)
		}
		visitedZones[zone] = struct{}{}
		if depth >= maxDelegations {
			return result, fmt.Errorf("%w: %w: depth exceeded at %s", ErrLookupFailed, ErrDelegationLoop, zone)
		}
		if *lookups >= maxLookups {
			return result, fmt.Errorf("%w: lookup limit exceeded", ErrLookupFailed)
		}
		*lookups = *lookups + 1

		c.logger.DebugContext(ctx, "following CAA delegation", "name", name, "zone", zone,
			"name_servers", result.Delegation.NameServers)
		decision.Delegations = append(decision.Delegations, *result.Delegation)
		referral = result.Delegation
	}
}

func (c *Checker) query(ctx context.Context, name string, referral *Delegation) (LookupResult, error) {
	timeout := c.policy.QueryTimeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if referral != nil {
		resolver, ok := c.resolver.(ReferralResolver)
		if !ok {
			return LookupResult{}, ErrDelegationUnsupported
		}
		result, err := resolver.LookupCAAVia(queryCtx, name, *referral)
		if err != nil {
			if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
				return LookupResult{}, fmt.Errorf("CAA query timed out: %w", queryCtx.Err())
			}
			return LookupResult{}, err
		}
		if queryCtx.Err() != nil {
			return LookupResult{}, fmt.Errorf("CAA query did not complete before deadline: %w", queryCtx.Err())
		}
		return normalizeLookupResult(result)
	}

	result, err := c.resolver.LookupCAA(queryCtx, name)
	if err != nil {
		if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			return LookupResult{}, fmt.Errorf("CAA query timed out: %w", queryCtx.Err())
		}
		return LookupResult{}, err
	}
	if queryCtx.Err() != nil {
		return LookupResult{}, fmt.Errorf("CAA query did not complete before deadline: %w", queryCtx.Err())
	}
	return normalizeLookupResult(result)
}

func normalizeLookupResult(result LookupResult) (LookupResult, error) {
	result.Records = append([]Record(nil), result.Records...)
	for i := range result.Records {
		tag := strings.ToLower(result.Records[i].Tag)
		if tag == "" || !validCAATag(tag) {
			// Keep the record so it can be logged and so a critical unknown
			// tag can still block issuance. An invalid tag is unknown.
			result.Records[i].Tag = tag
			continue
		}
		result.Records[i].Tag = tag
	}
	if result.DNSSEC > DNSSECBogus {
		return LookupResult{}, fmt.Errorf("invalid DNSSEC status %d", result.DNSSEC)
	}
	return result, nil
}

func (c *Checker) recordDNSSEC(ctx context.Context, status DNSSECStatus, name string, decision *Decision) error {
	if !decision.dnssecSet {
		decision.DNSSEC = status
		decision.dnssecSet = true
	} else if dnssecWorse(decision.DNSSEC, status) == status {
		decision.DNSSEC = status
	}

	if status == DNSSECBogus {
		decision.Reason = fmt.Sprintf("DNSSEC validation failed for %s", name)
		return fmt.Errorf("%w: DNSSEC bogus at %s", ErrLookupFailed, name)
	}

	switch c.policy.DNSSECMode {
	case DNSSECRequireSecure:
		if status != DNSSECSecure {
			decision.Reason = fmt.Sprintf("DNSSEC status %s is not acceptable for %s (secure required)", status, name)
			return fmt.Errorf("%w: DNSSEC status %s at %s", ErrLookupFailed, status, name)
		}
	case DNSSECAcceptInsecure:
		if status == DNSSECUnknown {
			decision.Reason = fmt.Sprintf("DNSSEC status unknown for %s", name)
			return fmt.Errorf("%w: DNSSEC status unknown at %s", ErrLookupFailed, name)
		}
	default:
		// Bogus is rejected above; secure, insecure, and unknown are
		// acceptable for a non-validating resolver.
	}
	return nil
}

func dnssecWorse(a, b DNSSECStatus) DNSSECStatus {
	rank := func(status DNSSECStatus) int {
		switch status {
		case DNSSECSecure:
			return 0
		case DNSSECInsecure:
			return 1
		case DNSSECUnknown:
			return 2
		case DNSSECBogus:
			return 3
		default:
			return 4
		}
	}
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func (c *Checker) evaluate(ctx context.Context, decision *Decision, wildcard bool) (bool, string, error) {
	records := decision.Records
	var (
		hasIssue     bool
		hasIssueWild bool
	)

	for _, record := range records {
		switch record.Tag {
		case "issue":
			hasIssue = true
		case "issuewild":
			hasIssueWild = true
		case "iodef":
			value, err := parseIOdef(record.Value)
			if err != nil {
				c.logger.WarnContext(ctx, "ignoring malformed CAA iodef property",
					"name", decision.RelevantName, "value", record.Value, "error", err)
				continue
			}
			decision.IODef = append(decision.IODef, value)
			c.logger.DebugContext(ctx, "CAA iodef property", "name", decision.RelevantName, "value", value)
		default:
			decision.UnknownTags = append(decision.UnknownTags, record)
			c.logger.WarnContext(ctx, "ignoring unrecognized CAA property",
				"name", decision.RelevantName, "tag", record.Tag,
				"value", record.Value, "flags", record.Flags, "critical", record.Critical())
			if record.Critical() && !c.policy.AllowUnknownCritical {
				return false, fmt.Sprintf("unknown critical CAA property %q at %s", record.Tag, decision.RelevantName), nil
			}
		}
	}

	if len(c.policy.IssuerDomainNames) == 0 && (hasIssue || hasIssueWild) {
		return false, "CAA policy restricts issuance but no issuer identity is configured", ErrNoIssuerIdentity
	}

	// issuewild takes precedence for wildcard requests if it is present at
	// all. For non-wildcard requests, issuewild never grants authorization.
	// If no issuewild property is present, wildcard requests use issue.
	candidateTag := "issue"
	hasCandidates := hasIssue
	if wildcard && hasIssueWild {
		candidateTag = "issuewild"
		hasCandidates = true
	}

	if !hasCandidates {
		return true, fmt.Sprintf("relevant CAA RRset at %s has no %s authorization properties", decision.RelevantName, candidateTag), nil
	}

	for _, record := range records {
		if record.Tag != candidateTag {
			continue
		}
		parsed, malformed := parseIssueValue(record.Value)
		if malformed {
			c.logger.WarnContext(ctx, "malformed CAA issue value denies that record",
				"name", decision.RelevantName, "tag", record.Tag, "value", record.Value)
		}
		if !parsed.hasIssuer {
			continue
		}
		if !c.parametersAccept(parsed.parameters) {
			c.logger.InfoContext(ctx, "CAA record has unsupported issuer-parameters",
				"name", decision.RelevantName, "tag", record.Tag,
				"issuer", parsed.issuer, "parameters", parameterNames(parsed.parameters))
			continue
		}
		if c.identityMatches(parsed.issuer) {
			decision.MatchedTag = candidateTag
			decision.MatchedIssuer = parsed.issuer
			return true, fmt.Sprintf("authorized by %s property at %s", candidateTag, decision.RelevantName), nil
		}
	}

	if candidateTag == "issuewild" {
		return false, fmt.Sprintf("no matching issuewild property at %s", decision.RelevantName), nil
	}
	return false, fmt.Sprintf("no matching issue property at %s", decision.RelevantName), nil
}

func (c *Checker) parametersAccept(parameters []parameter) bool {
	for _, parameter := range parameters {
		if !c.policy.Parameters.accepts(parameter.name, parameter.value) {
			return false
		}
	}
	return true
}

func (c *Checker) identityMatches(issuer string) bool {
	for _, identity := range c.policy.IssuerDomainNames {
		if strings.EqualFold(issuer, strings.TrimSuffix(identity, ".")) {
			return true
		}
	}
	return false
}

func (c *Checker) limit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

type parameter struct {
	name  string
	value string
}

type issueValue struct {
	issuer     string
	hasIssuer  bool
	parameters []parameter
}

func parseIssueValue(value string) (issueValue, bool) {
	value = trimWSP(value)
	issuerPart := value
	parameterPart := ""
	if index := strings.IndexByte(value, ';'); index >= 0 {
		issuerPart = trimWSP(value[:index])
		parameterPart = value[index+1:]
	}

	parsed := issueValue{}
	malformed := false
	if issuerPart != "" {
		issuer, err := NormalizeName(issuerPart)
		if err != nil {
			malformed = true
		} else {
			parsed.issuer = issuer
			parsed.hasIssuer = true
		}
	}

	if parameterPart != "" {
		for _, item := range strings.Split(parameterPart, ";") {
			item = trimWSP(item)
			if item == "" {
				// A trailing semicolon with no parameters is allowed. An
				// empty parameter between semicolons is malformed.
				if strings.TrimSpace(parameterPart) == "" {
					continue
				}
				malformed = true
				continue
			}
			index := strings.IndexByte(item, '=')
			if index < 0 {
				malformed = true
				continue
			}
			name := trimWSP(item[:index])
			paramValue := trimWSP(item[index+1:])
			if !validParameterTag(name) || !validParameterValue(paramValue) {
				malformed = true
				continue
			}
			parsed.parameters = append(parsed.parameters, parameter{
				name:  strings.ToLower(name),
				value: paramValue,
			})
		}
	}
	if malformed {
		// RFC 8659: a malformed issue-value is treated as one specifying an
		// empty issuer-domain-name. It cannot authorize this CA, but it does
		// still count as an issue/issuewild property.
		parsed.hasIssuer = false
		parsed.issuer = ""
	}
	return parsed, malformed
}

func parseIOdef(value string) (string, error) {
	value = trimWSP(value)
	if value == "" {
		return "", errors.New("empty iodef value")
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", errors.New("iodef value contains whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "mailto":
		if parsed.Opaque == "" {
			return "", errors.New("empty mailto iodef value")
		}
	case "http", "https":
		if parsed.Host == "" {
			return "", errors.New("iodef URL has no host")
		}
	default:
		return "", fmt.Errorf("unsupported iodef scheme %q", parsed.Scheme)
	}
	return value, nil
}

func parameterNames(parameters []parameter) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		names = append(names, parameter.name)
	}
	return names
}

// NormalizeName validates a DNS name and returns its canonical lowercase
// form without a trailing dot. Only ASCII LDH labels are accepted; callers
// that accept internationalized names must convert them with IDNA first.
func NormalizeName(name string) (string, error) {
	if strings.TrimSpace(name) != name {
		return "", errors.New("DNS name contains surrounding whitespace")
	}
	if name == "" {
		return "", errors.New("empty DNS name")
	}
	if strings.Contains(name, "..") {
		return "", errors.New("DNS name contains an empty label")
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return "", errors.New("root DNS name cannot be a certificate name")
	}
	if len(name) > 253 {
		return "", errors.New("DNS name exceeds 253 bytes")
	}
	if strings.ContainsAny(name, " \t\r\n") {
		return "", errors.New("DNS name contains whitespace")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 0x80 {
			return "", errors.New("non-ASCII DNS name; convert it with IDNA first")
		}
	}

	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" {
			return "", errors.New("DNS name contains an empty label")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("DNS label %q exceeds 63 bytes", label)
		}
		if !validHostLabel(label) {
			return "", fmt.Errorf("invalid DNS label %q", label)
		}
	}
	return strings.ToLower(name), nil
}

func normalizeRequest(request Request) (string, bool, error) {
	wildcard := request.Wildcard
	name := request.Name
	if strings.HasPrefix(name, "*.") {
		wildcard = true
		name = name[2:]
	}
	if strings.Contains(name, "*") {
		return "", false, errors.New("wildcard is only permitted as the complete leftmost label")
	}
	name, err := NormalizeName(name)
	if err != nil {
		return "", false, err
	}
	return name, wildcard, nil
}

func validateIdentities(identities []string) error {
	for _, identity := range identities {
		if _, err := NormalizeName(identity); err != nil {
			return fmt.Errorf("%w: invalid CAA issuer identity %q: %v", ErrInvalidPolicy, identity, err)
		}
	}
	return nil
}

func parentName(name string) string {
	index := strings.IndexByte(name, '.')
	if index < 0 {
		return "."
	}
	return name[index+1:]
}

func applyDNAME(name string, dname DNAME) (string, error) {
	owner, err := NormalizeName(dname.Owner)
	if err != nil {
		return "", fmt.Errorf("invalid DNAME owner: %w", err)
	}
	target, err := NormalizeName(dname.Target)
	if err != nil {
		return "", fmt.Errorf("invalid DNAME target: %w", err)
	}
	suffix := "." + owner
	if !strings.HasSuffix(name, suffix) {
		return "", fmt.Errorf("name %q is not below DNAME owner %q", name, owner)
	}
	prefix := strings.TrimSuffix(name, suffix)
	if prefix == "" {
		return "", fmt.Errorf("DNAME owner %q does not alias itself", owner)
	}
	result := prefix + "." + target
	if _, err := NormalizeName(result); err != nil {
		return "", err
	}
	return result, nil
}

func validCAATag(tag string) bool {
	if len(tag) == 0 || len(tag) > 15 {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func validHostLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if i == 0 || i == len(label)-1 {
			if !alnum {
				return false
			}
			continue
		}
		if !alnum && c != '-' {
			return false
		}
	}
	return true
}

func validParameterTag(tag string) bool {
	if len(tag) == 0 || len(tag) > 63 {
		return false
	}
	return validHostLabel(tag)
}

func validParameterValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x21 || c > 0x7e || c == ';' {
			return false
		}
	}
	return true
}

func trimWSP(value string) string {
	return strings.Trim(value, " \t")
}
