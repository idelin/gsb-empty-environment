package krb5policy

import (
	"errors"
	"testing"
	"time"
)

type delegationFixture struct {
	verifier *Verifier
	self     S4U2SelfRequest
	proxy    S4U2ProxyRequest
	now      time.Time

	user       Principal
	service    Principal
	target     Principal
	tgtKey     Key
	serviceKey Key
	targetKey  Key
	pacKey     Key
}

func buildDelegationFixture(t *testing.T) *delegationFixture {
	t.Helper()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	user := NewPrincipal("CLIENT.ORG", "user")
	service := NewPrincipal("CORE.ORG", "http", "app")
	target := NewPrincipal("CORE.ORG", "http", "db")
	tgtServer := NewPrincipal("EDGE.ORG", "krbtgt", "CORE.ORG")
	tgtKey := keyFor(tgtServer.String(), 1)
	serviceKey := keyFor(service.String(), 1)
	targetKey := keyFor(target.String(), 1)
	pacKey := keyFor("krbtgt/CORE.ORG@CORE.ORG", 1)

	tgt := Ticket{
		Client:      user,
		Server:      tgtServer,
		IssuerRealm: "EDGE.ORG",
		Flags:       FlagForwardable | FlagRenewable,
		AuthTime:    now.Add(-time.Hour),
		StartTime:   now.Add(-time.Hour),
		EndTime:     now.Add(2 * time.Hour),
		RenewTill:   now.Add(10 * time.Hour),
		Transited:   NewTransitedEncoding([]byte("EDGE.ORG")),
		PAC: &PAC{
			User:      user,
			LogonTime: now.Add(-2 * time.Hour),
			Groups:    []string{"users"},
		},
	}
	self := Ticket{
		Client:      user,
		Server:      service,
		IssuerRealm: "CORE.ORG",
		Flags:       FlagForwardable | FlagRenewable,
		AuthTime:    tgt.AuthTime,
		StartTime:   now,
		EndTime:     now.Add(time.Hour),
		RenewTill:   now.Add(5 * time.Hour),
		Transited:   NewTransitedEncoding([]byte("EDGE.ORG")),
		PAC: &PAC{
			User:      user,
			LogonTime: tgt.PAC.LogonTime,
			Groups:    []string{"users"},
		},
	}
	proxyOut := Ticket{
		Client:      user,
		Server:      target,
		IssuerRealm: "CORE.ORG",
		Flags:       FlagForwardable | FlagRenewable,
		AuthTime:    tgt.AuthTime,
		StartTime:   now,
		EndTime:     now.Add(time.Hour),
		RenewTill:   now.Add(4 * time.Hour),
		Transited:   NewTransitedEncoding([]byte("EDGE.ORG")),
		PAC: &PAC{
			User:      user,
			LogonTime: tgt.PAC.LogonTime,
			Groups:    []string{"users"},
		},
	}
	for _, ticket := range []*Ticket{&tgt, &self, &proxyOut} {
		ticket.PAC.Signatures = nil
		if err := SealPAC(ticket.PAC, pacKey); err != nil {
			t.Fatal(err)
		}
	}
	if err := SealTicket(&tgt, tgtKey); err != nil {
		t.Fatal(err)
	}
	if err := SealTicket(&self, serviceKey); err != nil {
		t.Fatal(err)
	}
	if err := SealTicket(&proxyOut, targetKey); err != nil {
		t.Fatal(err)
	}

	dir, err := NewDirectory([]ServiceAccount{{
		Principal:           service,
		AllowedToDelegateTo: []string{target.String()},
	}}, []UserAccount{{Principal: user}})
	if err != nil {
		t.Fatal(err)
	}
	trust, err := NewTrustGraph(TrustGraphConfig{
		Edges: []TrustEdge{
			{From: "CLIENT.ORG", To: "EDGE.ORG"},
			{From: "EDGE.ORG", To: "CORE.ORG"},
		},
		Capaths: []CapathRule{{
			ClientRealm: "CLIENT.ORG", ServerRealm: "CORE.ORG", Intermediates: []string{"EDGE.ORG"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	kt, err := NewKeytab(tgtKey, serviceKey, targetKey, pacKey)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierConfig{
		Keytab:            kt,
		Directory:         dir,
		Trust:             trust,
		Now:               func() time.Time { return now },
		MaxClockSkew:      5 * time.Minute,
		TrustedPACSigners: []string{pacKey.Principal},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &delegationFixture{
		verifier:   v,
		now:        now,
		user:       user,
		service:    service,
		target:     target,
		tgtKey:     tgtKey,
		serviceKey: serviceKey,
		targetKey:  targetKey,
		pacKey:     pacKey,
		self: S4U2SelfRequest{
			Service: service,
			User:    user,
			UserTGT: tgt,
			Output:  self,
			Now:     now,
		},
		proxy: S4U2ProxyRequest{
			Service:  service,
			Target:   target,
			Evidence: self,
			UserTGT:  &tgt,
			Output:   proxyOut,
			Now:      now,
		},
	}
}

func (f *delegationFixture) resealSelf(t *testing.T) {
	t.Helper()
	if err := SealTicket(&f.self.Output, f.serviceKey); err != nil {
		t.Fatal(err)
	}
	f.proxy.Evidence = f.self.Output
}

func (f *delegationFixture) resealProxy(t *testing.T) {
	t.Helper()
	if err := SealTicket(&f.proxy.Output, f.targetKey); err != nil {
		t.Fatal(err)
	}
}

func TestS4U2SelfAndProxySuccess(t *testing.T) {
	f := buildDelegationFixture(t)
	self, err := f.verifier.VerifyS4U2Self(f.self)
	if err != nil {
		t.Fatalf("S4U2Self: %v", err)
	}
	if self.Kind != DerivationS4U2Self || !self.Flags.Has(FlagForwardable) {
		t.Fatalf("result = %+v", self)
	}
	proxy, err := f.verifier.VerifyS4U2Proxy(f.proxy)
	if err != nil {
		t.Fatalf("S4U2Proxy: %v", err)
	}
	if proxy.Kind != DerivationS4U2Proxy || proxy.Target.String() != f.target.String() {
		t.Fatalf("result = %+v", proxy)
	}
}

func TestS4U2SelfRejectsTimeAndFlagExpansion(t *testing.T) {
	f := buildDelegationFixture(t)
	f.self.Output.EndTime = f.self.UserTGT.EndTime.Add(time.Hour)
	f.resealSelf(t)
	if _, err := f.verifier.VerifyS4U2Self(f.self); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("endtime expansion: %v", err)
	}

	f = buildDelegationFixture(t)
	f.self.Output.RenewTill = f.self.UserTGT.RenewTill.Add(time.Hour)
	f.resealSelf(t)
	if _, err := f.verifier.VerifyS4U2Self(f.self); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("renew-till expansion: %v", err)
	}

	f = buildDelegationFixture(t)
	f.self.UserTGT.Flags = FlagRenewable
	f.self.Output.Flags = FlagForwardable | FlagRenewable
	if err := SealTicket(&f.self.UserTGT, f.tgtKey); err != nil {
		t.Fatal(err)
	}
	f.resealSelf(t)
	if _, err := f.verifier.VerifyS4U2Self(f.self); !errors.Is(err, ErrFlagEscalation) {
		t.Fatalf("forwardable escalation: %v", err)
	}
}

func TestS4U2ProxyRejectsTimeExpansion(t *testing.T) {
	f := buildDelegationFixture(t)
	f.proxy.Output.EndTime = f.proxy.Evidence.EndTime.Add(time.Hour)
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("endtime: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Output.RenewTill = f.proxy.Evidence.RenewTill.Add(time.Hour)
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("renew-till: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Output.RenewTill = f.now.Add(6 * time.Hour)
	f.proxy.Evidence.RenewTill = f.now.Add(5 * time.Hour)
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("cross-source renew-till: %v", err)
	}
}

func TestS4U2ProxyRejectsForwardableEscalation(t *testing.T) {
	f := buildDelegationFixture(t)
	yes := true
	v, err := NewVerifier(VerifierConfig{
		Keytab:                            f.verifier.keytab,
		Directory:                         f.verifier.dir,
		Trust:                             f.verifier.trust,
		Now:                               func() time.Time { return f.now },
		MaxClockSkew:                      5 * time.Minute,
		TrustedPACSigners:                 []string{f.pacKey.Principal},
		AllowNonForwardableEvidence:       true,
		LenientTicketKeyBinding:           false,
		RequireTrustedToAuthForDelegation: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = yes
	f.proxy.Evidence.Flags = FlagRenewable
	f.proxy.UserTGT.Flags = FlagRenewable
	f.proxy.Output.Flags = FlagForwardable | FlagRenewable
	if err := SealTicket(f.proxy.UserTGT, f.tgtKey); err != nil {
		t.Fatal(err)
	}
	if err := SealTicket(&f.proxy.Evidence, f.serviceKey); err != nil {
		t.Fatal(err)
	}
	f.resealProxy(t)
	if _, err := v.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrFlagEscalation) {
		t.Fatalf("forwardable escalation: %v", err)
	}
}

func TestProtectedPrincipalsRejected(t *testing.T) {
	f := buildDelegationFixture(t)
	dir, err := NewDirectory([]ServiceAccount{{
		Principal:           f.service,
		AllowedToDelegateTo: []string{f.target.String()},
	}}, []UserAccount{{Principal: f.user, Flags: AccountNotDelegated}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierConfig{
		Keytab:            f.verifier.keytab,
		Directory:         dir,
		Trust:             f.verifier.trust,
		Now:               func() time.Time { return f.now },
		MaxClockSkew:      5 * time.Minute,
		TrustedPACSigners: []string{f.pacKey.Principal},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrProtectedPrincipal) {
		t.Fatalf("directory NOT_DELEGATED: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Evidence.PAC.UserFlags = AccountSensitive
	f.proxy.Evidence.PAC.Signatures = nil
	if err := SealPAC(f.proxy.Evidence.PAC, f.pacKey); err != nil {
		t.Fatal(err)
	}
	if err := SealTicket(&f.proxy.Evidence, f.serviceKey); err != nil {
		t.Fatal(err)
	}
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrProtectedPrincipal) {
		t.Fatalf("PAC SENSITIVE: %v", err)
	}
}

func TestDelegationDenials(t *testing.T) {
	f := buildDelegationFixture(t)
	f.proxy.Target = NewPrincipal("CORE.ORG", "http", "other")
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrTargetNotAllowed) {
		t.Fatalf("target allow-list: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Evidence.Client = NewPrincipal("CLIENT.ORG", "other")
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrDelegationDenied) {
		t.Fatalf("client mismatch: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Evidence.Flags = FlagRenewable
	if err := SealTicket(&f.proxy.Evidence, f.serviceKey); err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrEvidenceNotForwardable) {
		t.Fatalf("non-forwardable evidence: %v", err)
	}
}

func TestIntegrityFailures(t *testing.T) {
	f := buildDelegationFixture(t)
	f.proxy.Evidence.EndTime = f.proxy.Evidence.EndTime.Add(time.Second)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ticket tamper: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Evidence.PAC.Groups[0] = "administrators"
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ticket must bind PAC body: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Evidence.PAC.Groups[0] = "administrators"
	f.proxy.Evidence.PAC.Signatures = nil
	if err := SealPAC(f.proxy.Evidence.PAC, f.pacKey); err != nil {
		t.Fatal(err)
	}
	if err := SealTicket(&f.proxy.Evidence, f.serviceKey); err != nil {
		t.Fatal(err)
	}
	f.proxy.Evidence.PAC.Signatures[0].Value[0] ^= 0xff
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrPACIntegrity) {
		t.Fatalf("PAC tamper: %v", err)
	}
}

func TestOutputPACUserMismatch(t *testing.T) {
	f := buildDelegationFixture(t)
	f.proxy.Output.PAC.User = NewPrincipal("CLIENT.ORG", "admin")
	f.proxy.Output.PAC.Signatures = nil
	if err := SealPAC(f.proxy.Output.PAC, f.pacKey); err != nil {
		t.Fatal(err)
	}
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrDelegationDenied) {
		t.Fatalf("PAC user mismatch: %v", err)
	}
}

func TestTransitedFailures(t *testing.T) {
	f := buildDelegationFixture(t)
	f.proxy.Output.Transited = NewTransitedEncoding(nil)
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrNoTrust) {
		t.Fatalf("missing transit: %v", err)
	}

	f = buildDelegationFixture(t)
	f.proxy.Output.Transited = NewTransitedEncoding([]byte("EDGE.ORG,"))
	f.resealProxy(t)
	if _, err := f.verifier.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrIncompleteTransit) {
		t.Fatalf("trailing comma: %v", err)
	}
}

func TestClockSkewBoundaries(t *testing.T) {
	start := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	skew := 5 * time.Minute
	ticket := &Ticket{
		Client:    NewPrincipal("R", "u"),
		Server:    NewPrincipal("R", "s"),
		Flags:     FlagForwardable,
		AuthTime:  start,
		StartTime: start,
		EndTime:   start.Add(time.Hour),
	}
	if err := ValidateTicketWindow(ticket, start.Add(-skew), skew); err != nil {
		t.Fatalf("start boundary should be inclusive: %v", err)
	}
	if err := ValidateTicketWindow(ticket, start.Add(-skew-time.Nanosecond), skew); !errors.Is(err, ErrTicketNotYetValid) {
		t.Fatalf("one ns early: %v", err)
	}
	if err := ValidateTicketWindow(ticket, ticket.EndTime.Add(skew), skew); err != nil {
		t.Fatalf("end boundary should be inclusive: %v", err)
	}
	if err := ValidateTicketWindow(ticket, ticket.EndTime.Add(skew+time.Nanosecond), skew); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("one ns late: %v", err)
	}
	if err := ValidateTicketWindow(ticket, start, -time.Minute); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative skew: %v", err)
	}
}

func TestRenewalPolicy(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	source := Ticket{
		Client:    NewPrincipal("R", "u"),
		Server:    NewPrincipal("R", "s"),
		Flags:     FlagForwardable | FlagRenewable,
		AuthTime:  now.Add(-time.Hour),
		StartTime: now.Add(-time.Hour),
		EndTime:   now.Add(time.Hour),
		RenewTill: now.Add(5 * time.Hour),
	}
	renewed := source
	renewed.StartTime = now
	renewed.EndTime = now.Add(2 * time.Hour)
	renewed.RenewTill = source.RenewTill
	if err := CheckRenewalPolicy(source, renewed, false, 5*time.Minute); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("strict renewal: %v", err)
	}
	if err := CheckRenewalPolicy(source, renewed, true, 5*time.Minute); err != nil {
		t.Fatalf("bounded extension: %v", err)
	}
	renewed.RenewTill = now.Add(6 * time.Hour)
	if err := CheckRenewalPolicy(source, renewed, true, 5*time.Minute); !errors.Is(err, ErrTimeExpansion) {
		t.Fatalf("renew-till growth: %v", err)
	}
	renewed = source
	renewed.Flags = FlagRenewable // drops forwardable, which is allowed for a renewal
	renewed.StartTime = now
	renewed.EndTime = source.EndTime
	if err := CheckRenewalPolicy(source, renewed, false, 5*time.Minute); err != nil {
		t.Fatalf("flag reduction: %v", err)
	}
	source.Flags = 0
	if err := CheckRenewalPolicy(source, renewed, false, 5*time.Minute); !errors.Is(err, ErrRenewalWindow) {
		t.Fatalf("non-renewable source: %v", err)
	}
}

func TestProtectedS4U2SelfRejected(t *testing.T) {
	f := buildDelegationFixture(t)
	f.self.UserTGT.Flags |= FlagNotDelegated
	if err := SealTicket(&f.self.UserTGT, f.tgtKey); err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.VerifyS4U2Self(f.self); !errors.Is(err, ErrProtectedPrincipal) {
		t.Fatalf("protected S4U2Self: %v", err)
	}
}

func TestAllowListCaseAndRealmMatching(t *testing.T) {
	f := buildDelegationFixture(t)
	dir, err := NewDirectory([]ServiceAccount{{
		Principal:           NewPrincipal("core.org", "HTTP", "APP"),
		AllowedToDelegateTo: []string{"HTTP/DB@core.org"},
	}}, []UserAccount{{Principal: f.user}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierConfig{
		Keytab:            f.verifier.keytab,
		Directory:         dir,
		Trust:             f.verifier.trust,
		Now:               func() time.Time { return f.now },
		MaxClockSkew:      5 * time.Minute,
		TrustedPACSigners: []string{f.pacKey.Principal},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyS4U2Proxy(f.proxy); err != nil {
		t.Fatalf("case-insensitive SPN: %v", err)
	}

	d2, err := NewDirectory([]ServiceAccount{{
		Principal:           f.service,
		AllowedToDelegateTo: []string{"http/db"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !d2.AllowedToDelegate(f.service, f.target) {
		t.Fatal("realm-less SPN should match the service realm")
	}
	if d2.AllowedToDelegate(f.service, NewPrincipal("OTHER.ORG", "http", "db")) {
		t.Fatal("realm-less SPN must not widen to another realm")
	}
}

func TestProtectedServiceRejected(t *testing.T) {
	f := buildDelegationFixture(t)
	dir, err := NewDirectory([]ServiceAccount{{
		Principal:           f.service,
		AllowedToDelegateTo: []string{f.target.String()},
		Flags:               AccountSensitive,
	}}, []UserAccount{{Principal: f.user}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierConfig{
		Keytab:            f.verifier.keytab,
		Directory:         dir,
		Trust:             f.verifier.trust,
		Now:               func() time.Time { return f.now },
		MaxClockSkew:      5 * time.Minute,
		TrustedPACSigners: []string{f.pacKey.Principal},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.VerifyS4U2Proxy(f.proxy); !errors.Is(err, ErrProtectedPrincipal) {
		t.Fatalf("protected service: %v", err)
	}
}

func TestProxiableEscalationRejected(t *testing.T) {
	f := buildDelegationFixture(t)
	f.self.UserTGT.Flags &^= FlagProxiable
	f.self.Output.Flags |= FlagProxiable
	if err := SealTicket(&f.self.UserTGT, f.tgtKey); err != nil {
		t.Fatal(err)
	}
	f.resealSelf(t)
	if _, err := f.verifier.VerifyS4U2Self(f.self); !errors.Is(err, ErrFlagEscalation) {
		t.Fatalf("proxiable escalation: %v", err)
	}
}
