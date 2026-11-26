package krb5policy

import (
	"fmt"
	"strings"
	"time"
)

// ServiceAccount is the directory view of a service principal.
type ServiceAccount struct {
	Principal Principal
	// AllowedToDelegateTo is an exact SPN allow-list for constrained
	// delegation.  Wildcards are not interpreted.
	AllowedToDelegateTo []string
	Flags               AccountFlags
	// TrustedToAuthForDelegation is the AD protocol-transition bit.  It is
	// optional in this verifier because some hosts model S4U2Self separately.
	TrustedToAuthForDelegation bool
}

// UserAccount is the directory view of a user principal.
type UserAccount struct {
	Principal Principal
	Flags     AccountFlags
}

// Directory is a static directory snapshot used for delegation decisions.
type Directory struct {
	services     map[string]ServiceAccount
	users        map[string]UserAccount
	allowed      map[string]map[string]struct{}
	serviceNames []string
}

// NewDirectory validates and copies service/user entries.
func NewDirectory(services []ServiceAccount, users []UserAccount) (*Directory, error) {
	d := &Directory{
		services: make(map[string]ServiceAccount),
		users:    make(map[string]UserAccount),
		allowed:  make(map[string]map[string]struct{}),
	}
	for i, svc := range services {
		if svc.Principal.IsZero() {
			return nil, fmt.Errorf("%w: service %d has no principal", ErrInvalidConfig, i)
		}
		if err := validatePrincipalForDirectory(svc.Principal); err != nil {
			return nil, fmt.Errorf("service %d: %w", i, err)
		}
		key := svc.Principal.Canonical()
		if _, exists := d.services[key]; exists {
			return nil, fmt.Errorf("%w: duplicate service %q", ErrInvalidConfig, svc.Principal.String())
		}
		copySvc := svc
		copySvc.AllowedToDelegateTo = append([]string(nil), svc.AllowedToDelegateTo...)
		d.services[key] = copySvc
		d.serviceNames = append(d.serviceNames, svc.Principal.String())
		d.allowed[key] = make(map[string]struct{}, len(svc.AllowedToDelegateTo))
		for j, target := range svc.AllowedToDelegateTo {
			target = strings.TrimSpace(target)
			if target == "" {
				return nil, fmt.Errorf("%w: service %q has empty target %d", ErrInvalidConfig, svc.Principal.String(), j)
			}
			if strings.Contains(target, "*") {
				return nil, fmt.Errorf("%w: wildcard delegation target %q", ErrInvalidConfig, target)
			}
			p, err := ParsePrincipal(target)
			if err != nil {
				return nil, fmt.Errorf("service %q target %d: %w", svc.Principal.String(), j, err)
			}
			if p.Realm == "" {
				// A realm-less SPN is only usable within the service's own
				// realm.  This avoids silently widening a local SPN entry to
				// every realm that happens to use the same service name.
				p.Realm = svc.Principal.Realm
			}
			canonical := p.Canonical()
			d.allowed[key][canonical] = struct{}{}
		}
	}
	for i, user := range users {
		if user.Principal.IsZero() {
			return nil, fmt.Errorf("%w: user %d has no principal", ErrInvalidConfig, i)
		}
		if err := validatePrincipalForDirectory(user.Principal); err != nil {
			return nil, fmt.Errorf("user %d: %w", i, err)
		}
		key := user.Principal.Canonical()
		if _, exists := d.users[key]; exists {
			return nil, fmt.Errorf("%w: duplicate user %q", ErrInvalidConfig, user.Principal.String())
		}
		d.users[key] = user
	}
	return d, nil
}

func validatePrincipalForDirectory(p Principal) error {
	if p.Realm == "" {
		return fmt.Errorf("%w: principal %q has no realm", ErrInvalidRealm, p.String())
	}
	if err := validateRealm(p.Realm); err != nil {
		return err
	}
	for _, part := range p.Name {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("%w: empty principal component", ErrInvalidRealm)
		}
	}
	return nil
}

// Service returns a copy of the service entry.
func (d *Directory) Service(p Principal) (ServiceAccount, bool) {
	if d == nil {
		return ServiceAccount{}, false
	}
	svc, ok := d.services[p.Canonical()]
	if !ok {
		return ServiceAccount{}, false
	}
	svc.AllowedToDelegateTo = append([]string(nil), svc.AllowedToDelegateTo...)
	return svc, true
}

// User returns a user entry.
func (d *Directory) User(p Principal) (UserAccount, bool) {
	if d == nil {
		return UserAccount{}, false
	}
	u, ok := d.users[p.Canonical()]
	return u, ok
}

// AllowedToDelegate reports whether service may delegate to target.
func (d *Directory) AllowedToDelegate(service, target Principal) bool {
	if d == nil {
		return false
	}
	svc, ok := d.services[service.Canonical()]
	if !ok {
		return false
	}
	allowed := d.allowed[svc.Principal.Canonical()]
	if target.Realm == "" {
		target.Realm = svc.Principal.Realm
	}
	canonicalTarget := target.Canonical()
	if _, ok := allowed[canonicalTarget]; ok {
		return true
	}
	return false
}

// Services returns service entries in stable principal order.
func (d *Directory) Services() []ServiceAccount {
	if d == nil {
		return nil
	}
	out := make([]ServiceAccount, 0, len(d.services))
	names := append([]string(nil), d.serviceNames...)
	// serviceNames order is deterministic from input; preserve it.
	for _, name := range names {
		p, _ := ParsePrincipal(name)
		svc := d.services[p.Canonical()]
		svc.AllowedToDelegateTo = append([]string(nil), svc.AllowedToDelegateTo...)
		out = append(out, svc)
	}
	return out
}

// VerifierConfig is the host policy used by S4U2Self/S4U2Proxy checks.
type VerifierConfig struct {
	Keytab    *Keytab
	Directory *Directory
	Trust     *TrustGraph

	// Now is injectable for tests.  A nil clock means time.Now.
	Now func() time.Time
	// MaxClockSkew is required to be non-negative.  Zero selects five minutes.
	MaxClockSkew time.Duration
	Transited    TransitedOptions

	TrustedPACSigners []string

	// DisablePACVerification is available for low-level ticket checks only;
	// delegation calls leave it false and require a PAC.
	DisablePACVerification bool
	// LenientTicketKeyBinding permits a ticket MAC keyed by any keytab key
	// instead of the ticket server key.  Off by default.
	LenientTicketKeyBinding bool
	// AllowNonForwardableEvidence permits S4U2Proxy without the AD
	// forwardable evidence requirement.
	AllowNonForwardableEvidence bool
	// AllowDerivedTimeExtension permits a renewal to extend endtime up to the
	// source renew-till.  It still never permits renew-till expansion.
	AllowDerivedTimeExtension bool
	// RequireTrustedToAuthForDelegation enforces the directory protocol
	// transition bit for S4U2Self.
	RequireTrustedToAuthForDelegation bool
}

// Verifier applies host policy to ticket and delegation requests.
type Verifier struct {
	keytab               *Keytab
	dir                  *Directory
	trust                *TrustGraph
	now                  func() time.Time
	skew                 time.Duration
	transited            TransitedOptions
	trustedPAC           []string
	disablePAC           bool
	lenientBinding       bool
	allowNonForwardable  bool
	allowTimeExtension   bool
	requireTrustedToAuth bool
}

// NewVerifier validates configuration and returns an immutable verifier.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.Keytab == nil || cfg.Keytab.Len() == 0 {
		return nil, fmt.Errorf("%w: empty keytab", ErrInvalidConfig)
	}
	if cfg.Directory == nil {
		return nil, fmt.Errorf("%w: nil directory", ErrInvalidConfig)
	}
	if cfg.MaxClockSkew < 0 {
		return nil, fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}
	skew := cfg.MaxClockSkew
	if skew == 0 {
		skew = 5 * time.Minute
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		keytab:               cfg.Keytab,
		dir:                  cfg.Directory,
		trust:                cfg.Trust,
		now:                  now,
		skew:                 skew,
		transited:            cfg.Transited,
		trustedPAC:           append([]string(nil), cfg.TrustedPACSigners...),
		disablePAC:           cfg.DisablePACVerification,
		lenientBinding:       cfg.LenientTicketKeyBinding,
		allowNonForwardable:  cfg.AllowNonForwardableEvidence,
		allowTimeExtension:   cfg.AllowDerivedTimeExtension,
		requireTrustedToAuth: cfg.RequireTrustedToAuthForDelegation,
	}, nil
}

// Len returns the number of distinct key principals in a keytab.
func (kt *Keytab) Len() int {
	if kt == nil {
		return 0
	}
	return len(kt.keys)
}

// VerifyTicket verifies integrity, PAC, transited encoding, and the clock
// window.  It does not decide whether a delegation chain is authorized.
func (v *Verifier) VerifyTicket(t *Ticket, requirePAC bool) error {
	if v == nil {
		return fmt.Errorf("%w: nil verifier", ErrInvalidConfig)
	}
	if err := v.verifyTicketCore(t, requirePAC, v.now()); err != nil {
		return err
	}
	return nil
}

func (v *Verifier) verifyTicketCore(t *Ticket, requirePAC bool, now time.Time) error {
	if t == nil {
		return fmt.Errorf("%w: nil ticket", ErrIntegrity)
	}
	if t.Flags.Has(FlagInvalid) {
		return fmt.Errorf("%w: ticket has invalid flag", ErrInvalidTicketWindow)
	}
	if err := VerifyTicketIntegrity(t, v.keytab, !v.lenientBinding); err != nil {
		return err
	}
	if err := ValidateTicketWindow(t, now, v.skew); err != nil {
		return err
	}
	if t.PAC == nil {
		if requirePAC && !v.disablePAC {
			return fmt.Errorf("%w: ticket has no PAC", ErrPACIntegrity)
		}
	} else if !v.disablePAC {
		if err := VerifyPAC(t.PAC, v.keytab, v.trustedPAC); err != nil {
			return err
		}
	}
	if t.Transited.Type != 0 || len(t.Transited.Contents) != 0 {
		if err := ValidateTransitedEncoding(t.Transited, v.transited.Limits); err != nil {
			return err
		}
	}
	return nil
}

func (v *Verifier) grantTime(override time.Time) time.Time {
	if override.IsZero() {
		return v.now()
	}
	return override
}

func (v *Verifier) validatePath(t *Ticket, clientRealm, serverRealm string) (*ValidatedPath, error) {
	te := t.Transited
	if te.Type == 0 && len(te.Contents) == 0 {
		te.Type = TransitedDomainX500Compress
	}
	opts := v.transited
	exp, err := te.Expand(clientRealm, serverRealm, opts)
	if err != nil {
		return nil, err
	}
	if exp.TrailingComma && !exp.Complete {
		return nil, fmt.Errorf("%w: trailing empty item forbids implicit completion", ErrIncompleteTransit)
	}
	if v.trust == nil {
		if sameRealm(clientRealm, serverRealm) && len(exp.Realms) == 0 {
			return &ValidatedPath{
				Client:   clientRealm,
				Server:   serverRealm,
				FullPath: []string{clientRealm},
			}, nil
		}
		return nil, fmt.Errorf("%w: cross-realm ticket without a trust graph", ErrNoTrust)
	}
	return v.trust.ValidateExpanded(clientRealm, serverRealm, exp)
}

// DerivationKind identifies the monotonicity rule being applied.
type DerivationKind int

const (
	DerivationRenewal DerivationKind = iota
	DerivationS4U2Self
	DerivationS4U2Proxy
)

// CheckFlagMonotonicity rejects derived tickets that invent forwardable,
// proxiable, forwarded/proxy, renewable, or post-date bits, and rejects
// attempts to strip NOT_DELEGATED/SENSITIVE from a source.
func CheckFlagMonotonicity(derived TicketFlags, sources ...TicketFlags) error {
	required := []struct {
		bit  TicketFlags
		name string
	}{
		{FlagForwardable, "forwardable"},
		{FlagForwarded, "forwarded"},
		{FlagProxiable, "proxiable"},
		{FlagProxy, "proxy"},
		{FlagMayPostdate, "may-postdate"},
		{FlagPostdated, "postdated"},
		{FlagRenewable, "renewable"},
	}
	var union TicketFlags
	for _, src := range sources {
		union |= src
	}
	for _, f := range required {
		if derived.Has(f.bit) && !union.Has(f.bit) {
			return fmt.Errorf("%w: %s", ErrFlagEscalation, f.name)
		}
	}
	for _, src := range sources {
		if src.Has(FlagNotDelegated) && !derived.Has(FlagNotDelegated) {
			return fmt.Errorf("%w: NOT_DELEGATED was stripped", ErrFlagEscalation)
		}
		if src.Has(FlagSensitive) && !derived.Has(FlagSensitive) {
			return fmt.Errorf("%w: SENSITIVE was stripped", ErrFlagEscalation)
		}
	}
	if derived.Has(FlagInvalid) && !union.Has(FlagInvalid) {
		return fmt.Errorf("%w: invalid", ErrFlagEscalation)
	}
	return nil
}

// CheckDerivedTimeBounds enforces non-expansion of endtime and renew-till.
// With allowRenewalExtension, a renewal may extend endtime only up to the
// source renew-till; renew-till itself can never grow.
func CheckDerivedTimeBounds(derived, source Ticket, kind DerivationKind, allowRenewalExtension bool, skew time.Duration) error {
	if skew < 0 {
		return fmt.Errorf("%w: negative clock skew", ErrInvalidConfig)
	}
	if derived.EndTime.IsZero() || source.EndTime.IsZero() {
		return fmt.Errorf("%w: missing endtime", ErrInvalidTicketWindow)
	}
	if derived.Flags.Has(FlagRenewable) && derived.RenewTill.IsZero() {
		return fmt.Errorf("%w: renewable derived ticket has no renew-till", ErrInvalidTicketWindow)
	}
	if derived.EffectiveStart().IsZero() {
		return fmt.Errorf("%w: missing derived start time", ErrInvalidTicketWindow)
	}
	if !source.EffectiveStart().IsZero() && derived.EffectiveStart().Before(source.EffectiveStart()) {
		return fmt.Errorf("%w: derived start time predates source", ErrTimeExpansion)
	}
	if !derived.AuthTime.IsZero() && !source.AuthTime.IsZero() && !derived.AuthTime.Equal(source.AuthTime) {
		return fmt.Errorf("%w: derived auth time differs from source", ErrTimeExpansion)
	}

	limitEnd := source.EndTime.UTC()
	if kind == DerivationRenewal {
		if !source.Flags.Has(FlagRenewable) || source.RenewTill.IsZero() {
			return fmt.Errorf("%w: source is not renewable", ErrRenewalWindow)
		}
		if allowRenewalExtension {
			limitEnd = source.RenewTill.UTC()
		}
	} else if kind != DerivationS4U2Self && kind != DerivationS4U2Proxy {
		return fmt.Errorf("%w: unknown derivation kind", ErrInvalidConfig)
	}
	if derived.EndTime.UTC().After(limitEnd) {
		return fmt.Errorf("%w: endtime %s exceeds limit %s", ErrTimeExpansion, derived.EndTime.UTC(), limitEnd)
	}
	if !derived.RenewTill.IsZero() {
		if source.RenewTill.IsZero() {
			return fmt.Errorf("%w: derived renew-till invented", ErrTimeExpansion)
		}
		if derived.RenewTill.UTC().After(source.RenewTill.UTC()) {
			return fmt.Errorf("%w: renew-till %s exceeds source %s", ErrTimeExpansion, derived.RenewTill.UTC(), source.RenewTill.UTC())
		}
	}
	return nil
}

// S4U2SelfRequest describes a protocol-transition ticket request.  UserTGT is
// the user's TGT for the service realm; Output is the proposed U -> S ticket.
type S4U2SelfRequest struct {
	Service Principal
	User    Principal
	UserTGT Ticket
	Output  Ticket
	Now     time.Time
}

// DelegationResult is a successful policy decision.
type DelegationResult struct {
	Kind      DerivationKind
	Client    Principal
	Service   Principal
	Target    Principal
	Flags     TicketFlags
	EndTime   time.Time
	RenewTill time.Time
	Path      *ValidatedPath
}

// VerifyS4U2Self authorizes (or rejects) a proposed S4U2Self ticket.
func (v *Verifier) VerifyS4U2Self(req S4U2SelfRequest) (*DelegationResult, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidConfig)
	}
	if req.Service.IsZero() || req.User.IsZero() {
		return nil, fmt.Errorf("%w: service and user principals are required", ErrDelegationDenied)
	}
	if req.Service.Realm == "" || req.User.Realm == "" {
		return nil, fmt.Errorf("%w: service and user realms are required", ErrDelegationDenied)
	}
	svc, ok := v.dir.Service(req.Service)
	if !ok {
		return nil, fmt.Errorf("%w: service %q is not in the directory", ErrDelegationDenied, req.Service.String())
	}
	if svc.Flags.Protected() {
		return nil, fmt.Errorf("%w: service account is marked %s", ErrProtectedPrincipal, svc.Flags)
	}
	if v.requireTrustedToAuth && !svc.TrustedToAuthForDelegation {
		return nil, fmt.Errorf("%w: service is not trusted to authenticate for delegation", ErrDelegationDenied)
	}
	if !sameRealm(req.User.Realm, req.UserTGT.Client.Realm) || !req.UserTGT.Client.Equal(req.User) {
		return nil, fmt.Errorf("%w: TGT client does not match user", ErrDelegationDenied)
	}
	if !req.UserTGT.Server.IsTGT() {
		return nil, fmt.Errorf("%w: user evidence is not a TGT", ErrDelegationDenied)
	}
	if !sameRealm(req.UserTGT.Server.TargetRealm(), req.Service.Realm) {
		return nil, fmt.Errorf("%w: user TGT is for realm %q, service is in %q", ErrDelegationDenied, req.UserTGT.Server.TargetRealm(), req.Service.Realm)
	}
	if !req.Output.Client.Equal(req.User) || !sameRealm(req.Output.Client.Realm, req.User.Realm) {
		return nil, fmt.Errorf("%w: output ticket client does not match user", ErrDelegationDenied)
	}
	if !req.Output.Server.Equal(req.Service) {
		return nil, fmt.Errorf("%w: output ticket server does not match service", ErrDelegationDenied)
	}

	now := v.grantTime(req.Now)
	if err := v.verifyTicketCore(&req.UserTGT, true, now); err != nil {
		return nil, err
	}
	if err := v.verifyTicketCore(&req.Output, true, now); err != nil {
		return nil, err
	}
	if err := v.checkPACUser(&req.UserTGT, req.User); err != nil {
		return nil, err
	}
	if err := v.checkPACUser(&req.Output, req.User); err != nil {
		return nil, err
	}
	if err := v.checkProtected(req.User, &req.UserTGT, &req.Output); err != nil {
		return nil, err
	}
	if err := CheckFlagMonotonicity(req.Output.Flags, req.UserTGT.Flags); err != nil {
		return nil, err
	}
	if err := CheckDerivedTimeBounds(req.Output, req.UserTGT, DerivationS4U2Self, false, v.skew); err != nil {
		return nil, err
	}

	tgtPath, err := v.validatePath(&req.UserTGT, req.User.Realm, req.Service.Realm)
	if err != nil {
		return nil, err
	}
	outPath, err := v.validatePath(&req.Output, req.User.Realm, req.Service.Realm)
	if err != nil {
		return nil, err
	}
	_ = tgtPath
	return &DelegationResult{
		Kind:      DerivationS4U2Self,
		Client:    req.User,
		Service:   req.Service,
		Flags:     req.Output.Flags,
		EndTime:   req.Output.EndTime,
		RenewTill: req.Output.RenewTill,
		Path:      outPath,
	}, nil
}

// S4U2ProxyRequest describes a constrained-delegation request.  Evidence is
// the U -> Service S4U2Self ticket.  UserTGT is optional, but when present it
// is included in protection, flag, and lifetime monotonicity checks.
type S4U2ProxyRequest struct {
	Service  Principal
	Target   Principal
	Evidence Ticket
	UserTGT  *Ticket
	Output   Ticket
	Now      time.Time
}

// VerifyS4U2Proxy authorizes (or rejects) a proposed S4U2Proxy ticket.
func (v *Verifier) VerifyS4U2Proxy(req S4U2ProxyRequest) (*DelegationResult, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidConfig)
	}
	if req.Service.IsZero() || req.Target.IsZero() {
		return nil, fmt.Errorf("%w: service and target principals are required", ErrDelegationDenied)
	}
	if req.Service.Realm == "" || req.Target.Realm == "" {
		return nil, fmt.Errorf("%w: service and target realms are required", ErrDelegationDenied)
	}
	svc, ok := v.dir.Service(req.Service)
	if !ok {
		return nil, fmt.Errorf("%w: service %q is not in the directory", ErrDelegationDenied, req.Service.String())
	}
	if svc.Flags.Protected() {
		return nil, fmt.Errorf("%w: service account is marked %s", ErrProtectedPrincipal, svc.Flags)
	}
	if !v.dir.AllowedToDelegate(req.Service, req.Target) {
		return nil, fmt.Errorf("%w: service %q -> target %q", ErrTargetNotAllowed, req.Service.String(), req.Target.String())
	}
	if !req.Evidence.Server.Equal(req.Service) {
		return nil, fmt.Errorf("%w: evidence server does not match service", ErrDelegationDenied)
	}
	if !req.Output.Server.Equal(req.Target) {
		return nil, fmt.Errorf("%w: output server does not match target", ErrDelegationDenied)
	}
	if !req.Evidence.Client.Equal(req.Output.Client) {
		return nil, fmt.Errorf("%w: evidence/output clients differ", ErrDelegationDenied)
	}
	user := req.Evidence.Client
	if user.IsZero() || user.Realm == "" {
		return nil, fmt.Errorf("%w: evidence has no client principal", ErrDelegationDenied)
	}
	if req.UserTGT != nil {
		if !req.UserTGT.Client.Equal(user) {
			return nil, fmt.Errorf("%w: user TGT client does not match evidence", ErrDelegationDenied)
		}
		if !req.UserTGT.Server.IsTGT() || !sameRealm(req.UserTGT.Server.TargetRealm(), req.Service.Realm) {
			return nil, fmt.Errorf("%w: user TGT is not for the service realm", ErrDelegationDenied)
		}
	}
	if !req.Evidence.Flags.Has(FlagForwardable) && !v.allowNonForwardable {
		return nil, ErrEvidenceNotForwardable
	}

	now := v.grantTime(req.Now)
	if err := v.verifyTicketCore(&req.Evidence, true, now); err != nil {
		return nil, err
	}
	if err := v.verifyTicketCore(&req.Output, true, now); err != nil {
		return nil, err
	}
	if req.UserTGT != nil {
		if err := v.verifyTicketCore(req.UserTGT, true, now); err != nil {
			return nil, err
		}
	}
	if err := v.checkPACUser(&req.Evidence, user); err != nil {
		return nil, err
	}
	if err := v.checkPACUser(&req.Output, user); err != nil {
		return nil, err
	}
	if req.UserTGT != nil {
		if err := v.checkPACUser(req.UserTGT, user); err != nil {
			return nil, err
		}
	}
	if err := v.checkProtected(user, &req.Evidence, &req.Output, req.UserTGT); err != nil {
		return nil, err
	}

	sources := []TicketFlags{req.Evidence.Flags}
	if req.UserTGT != nil {
		sources = append(sources, req.UserTGT.Flags)
	}
	if err := CheckFlagMonotonicity(req.Output.Flags, sources...); err != nil {
		return nil, err
	}
	if req.Output.Flags.Has(FlagForwardable) && !req.Evidence.Flags.Has(FlagForwardable) {
		return nil, fmt.Errorf("%w: output forwardable without forwardable evidence", ErrFlagEscalation)
	}
	if req.UserTGT != nil && req.Output.Flags.Has(FlagForwardable) && !req.UserTGT.Flags.Has(FlagForwardable) {
		return nil, fmt.Errorf("%w: output forwardable without forwardable TGT", ErrFlagEscalation)
	}

	if err := CheckDerivedTimeBounds(req.Output, req.Evidence, DerivationS4U2Proxy, false, v.skew); err != nil {
		return nil, err
	}
	if req.UserTGT != nil {
		if err := CheckDerivedTimeBounds(req.Output, *req.UserTGT, DerivationS4U2Proxy, false, v.skew); err != nil {
			return nil, err
		}
	}

	evidencePath, err := v.validatePath(&req.Evidence, user.Realm, req.Service.Realm)
	if err != nil {
		return nil, err
	}
	outputPath, err := v.validatePath(&req.Output, user.Realm, req.Target.Realm)
	if err != nil {
		return nil, err
	}
	if req.UserTGT != nil {
		if _, err := v.validatePath(req.UserTGT, user.Realm, req.Service.Realm); err != nil {
			return nil, err
		}
	}
	_ = evidencePath
	return &DelegationResult{
		Kind:      DerivationS4U2Proxy,
		Client:    user,
		Service:   req.Service,
		Target:    req.Target,
		Flags:     req.Output.Flags,
		EndTime:   req.Output.EndTime,
		RenewTill: req.Output.RenewTill,
		Path:      outputPath,
	}, nil
}

func (v *Verifier) checkPACUser(t *Ticket, user Principal) error {
	if t.PAC != nil && !t.PAC.User.Equal(user) {
		return fmt.Errorf("%w: PAC user %q != expected %q", ErrDelegationDenied, t.PAC.User.String(), user.String())
	}
	return nil
}

func (v *Verifier) checkProtected(user Principal, tickets ...*Ticket) error {
	if acct, ok := v.dir.User(user); ok && acct.Flags.Protected() {
		return fmt.Errorf("%w: directory flags %s for %q", ErrProtectedPrincipal, acct.Flags, user.String())
	}
	for _, t := range tickets {
		if t == nil {
			continue
		}
		if t.Flags.Protected() {
			return fmt.Errorf("%w: ticket flags %s for %q", ErrProtectedPrincipal, t.Flags, t.Client.String())
		}
		if t.PAC != nil && t.PAC.UserFlags.Protected() {
			return fmt.Errorf("%w: PAC flags %s for %q", ErrProtectedPrincipal, t.PAC.UserFlags, t.PAC.User.String())
		}
	}
	return nil
}

// CheckRenewalPolicy is a convenience wrapper for callers that apply a
// renewal policy without the full S4U request machinery.
func CheckRenewalPolicy(source, renewed Ticket, allowExtension bool, skew time.Duration) error {
	if !source.Flags.Has(FlagRenewable) || source.RenewTill.IsZero() {
		return fmt.Errorf("%w: source is not renewable", ErrRenewalWindow)
	}
	if err := ValidateTicketWindow(&renewed, renewed.EffectiveStart(), skew); err != nil {
		return err
	}
	if err := ValidateRenewalWindow(&source, renewed.EffectiveStart(), skew); err != nil {
		return err
	}
	if err := CheckFlagMonotonicity(renewed.Flags, source.Flags); err != nil {
		return err
	}
	return CheckDerivedTimeBounds(renewed, source, DerivationRenewal, allowExtension, skew)
}
