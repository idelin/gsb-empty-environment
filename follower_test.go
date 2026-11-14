package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type bindCall struct {
	Target   Target
	BindDN   string
	Password string
}

type fakeRecorder struct {
	binds []bindCall
	dials []Target
}

type fakeDialer struct {
	recorder *fakeRecorder
	refs     map[string][]string
	entries  map[string][]Entry
}

func (d *fakeDialer) Dial(_ context.Context, target Target) (Conn, error) {
	d.recorder.dials = append(d.recorder.dials, target)
	return &fakeConn{
		target:   target,
		recorder: d.recorder,
		refs:     d.refs[fakeKey(target)],
		entries:  d.entries[fakeKey(target)],
	}, nil
}

type fakeConn struct {
	target   Target
	recorder *fakeRecorder
	refs     []string
	entries  []Entry
}

func (c *fakeConn) Bind(_ context.Context, dn, password string) (BindResult, error) {
	c.recorder.binds = append(c.recorder.binds, bindCall{Target: c.target, BindDN: dn, Password: password})
	return BindResult{}, nil
}

func (c *fakeConn) Search(_ context.Context, _ SearchRequest) (SearchResult, error) {
	return SearchResult{Entries: c.entries, References: c.refs}, nil
}

func (c *fakeConn) Close() error { return nil }

func fakeKey(t Target) string { return t.Host + "|" + canonicalDN(t.DN) }

func TestFollowerRoutesPasswordOnlyToTrustedHosts(t *testing.T) {
	const (
		source   = "10.1.2.3:5555"
		password = "correct-horse-battery-staple"
		bindDN   = "cn=gateway,ou=services,dc=corp,dc=example,dc=com"
		baseDN   = "ou=users,dc=corp,dc=example,dc=com"
	)
	initial := "ldaps://dc1.corp.example.com/" + baseDN
	recorder := &fakeRecorder{}
	audit := &MemoryAudit{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|" + canonicalDN(baseDN): {
				"ldaps://dc2.corp.example.com/" + baseDN,
				"ldaps://attacker.example.net/" + baseDN,
				"ldaps://dc2.corp.example.com.attacker.example.net/" + baseDN,
				"ldaps://dc3.corp.example.com:1636/" + baseDN,
				"ldaps://dc1.corp.example.com/" + baseDN,
				"ldap://dc4.corp.example.com/" + baseDN,
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		AllowAnonymousFollow:      true,
		AllowCleartext:            true,
		MaxReferralDepth:          10,
		Dialer:                    dialer,
		Audit:                     audit,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := f.Follow(context.Background(), Request{
		Server:   initial,
		BindDN:   bindDN,
		Password: password,
		Source:   source,
		Filter:   "(uid=alice)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hops) != 5 {
		t.Fatalf("hops = %d, want 5: %#v", len(result.Hops), result.Hops)
	}

	forwardedHosts := map[string]bool{}
	anonymousHosts := map[string]bool{}
	for _, bind := range recorder.binds {
		switch bind.Target.Host {
		case "dc1.corp.example.com", "dc2.corp.example.com":
			if bind.Password != password || bind.BindDN != bindDN {
				t.Errorf("trusted host %s got dn=%q password=%q", bind.Target.Host, bind.BindDN, bind.Password)
			}
			forwardedHosts[bind.Target.Host] = true
		default:
			if bind.Password != "" || bind.BindDN != "" {
				t.Errorf("untrusted host %s received credentials: dn=%q password=%q", bind.Target.Host, bind.BindDN, bind.Password)
			}
			anonymousHosts[bind.Target.Host] = true
		}
	}
	if len(forwardedHosts) != 2 {
		t.Fatalf("forwarded hosts = %#v", forwardedHosts)
	}
	for _, host := range []string{
		"attacker.example.net",
		"dc2.corp.example.com.attacker.example.net",
		"dc3.corp.example.com",
	} {
		if !anonymousHosts[host] {
			t.Errorf("expected anonymous bind to %s", host)
		}
	}
	for _, target := range recorder.dials {
		if target.Host == "dc4.corp.example.com" {
			t.Errorf("cleartext downgrade target was dialed")
		}
	}

	if len(result.Unfollowed) != 2 {
		t.Fatalf("unfollowed = %#v, want loop and downgrade", result.Unfollowed)
	}
	var sawLoop, sawDowngrade bool
	for _, u := range result.Unfollowed {
		sawLoop = sawLoop || errors.Is(u.Err, ErrReferralLoop)
		sawDowngrade = sawDowngrade || errors.Is(u.Err, ErrCleartextDowngrade)
	}
	if !sawLoop || !sawDowngrade {
		t.Fatalf("unfollowed errors = %#v", result.Unfollowed)
	}

	events := audit.Events()
	var bindEvents int
	for _, event := range events {
		if event.Source != source {
			t.Errorf("event source = %q, want %q", event.Source, source)
		}
		if event.Kind == "bind" {
			bindEvents++
			if event.CredentialForwarded && event.BoundDN != bindDN {
				t.Errorf("forwarded bind identity = %q", event.BoundDN)
			}
			if !event.CredentialForwarded && (event.BoundDN != "" || event.AuthMechanism != AuthAnonymousSimple) {
				t.Errorf("anonymous bind audit = %#v", event)
			}
		}
	}
	if bindEvents != len(recorder.binds) {
		t.Fatalf("bind audit events = %d, binds = %d", bindEvents, len(recorder.binds))
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), password) {
		t.Fatalf("audit log contains the bind password")
	}
}

func TestFollowerDepthLimit(t *testing.T) {
	recorder := &fakeRecorder{}
	audit := &MemoryAudit{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"ldaps://dc2.corp.example.com/ou=one",
			},
			"dc2.corp.example.com|ou=one": {
				"ldaps://dc3.corp.example.com/ou=two",
			},
			"dc3.corp.example.com|ou=two": {
				"ldaps://dc4.corp.example.com/ou=three",
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		AllowAnonymousFollow:      true,
		MaxReferralDepth:          2,
		Dialer:                    dialer,
		Audit:                     audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "secret",
		Source:   "client",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hops) != 3 {
		t.Fatalf("hops = %d, want 3", len(result.Hops))
	}
	if len(result.Unfollowed) != 1 || !errors.Is(result.Unfollowed[0].Err, ErrMaxReferralDepth) {
		t.Fatalf("unfollowed = %#v", result.Unfollowed)
	}
	for _, target := range recorder.dials {
		if target.Host == "dc4.corp.example.com" {
			t.Fatal("referral beyond MaxReferralDepth was dialed")
		}
	}
}

func TestFollowerCycle(t *testing.T) {
	recorder := &fakeRecorder{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"ldaps://dc1.corp.example.com/ou=base",
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.binds) != 1 {
		t.Fatalf("binds = %d, want 1", len(recorder.binds))
	}
	if len(result.Unfollowed) != 1 || !errors.Is(result.Unfollowed[0].Err, ErrReferralLoop) {
		t.Fatalf("unfollowed = %#v", result.Unfollowed)
	}
}

func TestFollowerCrossForestBoundary(t *testing.T) {
	recorder := &fakeRecorder{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"ldaps://dc1.partner.example.com/ou=partner",
			},
		},
	}
	cfg := Config{
		TrustedDomains:            []string{"corp.example.com", "partner.example.com"},
		AllowCredentialForwarding: true,
		AllowAnonymousFollow:      true,
		MaxReferralDepth:          5,
		Forest:                    "corp",
		DomainForests: map[string]string{
			"corp.example.com":    "corp",
			"partner.example.com": "partner",
		},
		Dialer: dialer,
	}
	f, err := NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.binds) != 1 {
		t.Fatalf("cross-forest target was contacted: %#v", recorder.binds)
	}
	if len(result.Unfollowed) != 1 || !errors.Is(result.Unfollowed[0].Err, ErrCrossForest) {
		t.Fatalf("unfollowed = %#v", result.Unfollowed)
	}

	cfg.AllowCrossForest = true
	f, err = NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	recorder.binds = nil
	if _, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "secret",
	}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.binds) != 2 {
		t.Fatalf("explicit cross-forest follow binds = %d, want 2", len(recorder.binds))
	}
}

func TestAnonymousOnlyPolicyNeverSendsPassword(t *testing.T) {
	recorder := &fakeRecorder{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"ldaps://dc2.corp.example.com/ou=base",
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: false,
		AllowAnonymousFollow:      true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "must-not-leak",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bind := range recorder.binds {
		if bind.Password != "" || bind.BindDN != "" {
			t.Fatalf("anonymous policy sent credentials: %#v", bind)
		}
	}
}

func TestMalformedReferralIsNotFollowed(t *testing.T) {
	recorder := &fakeRecorder{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"https://dc2.corp.example.com/ou=base",
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Unfollowed) != 1 || !errors.Is(result.Unfollowed[0].Err, ErrUnsupportedScheme) {
		t.Fatalf("unfollowed = %#v", result.Unfollowed)
	}
	if len(recorder.binds) != 1 {
		t.Fatalf("binds = %d, want 1", len(recorder.binds))
	}
}

func TestAuditAndErrorsDoNotEchoURLUserinfo(t *testing.T) {
	recorder := &fakeRecorder{}
	audit := &MemoryAudit{}
	dialer := &fakeDialer{
		recorder: recorder,
		refs: map[string][]string{
			"dc1.corp.example.com|ou=base": {
				"ldap://user:url-secret@dc2.corp.example.com/ou=base",
			},
		},
	}
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		Dialer:                    dialer,
		Audit:                     audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Follow(context.Background(), Request{
		Server:   "ldaps://dc1.corp.example.com/ou=base",
		BindDN:   "cn=gateway",
		Password: "bind-secret",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(audit.Events())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "url-secret") {
		t.Fatalf("audit echoed URL userinfo: %s", raw)
	}

	_, err = f.Follow(context.Background(), Request{
		Server: "ldap://user:url-secret@dc1.corp.example.com/ou=base",
	})
	if err == nil {
		t.Fatal("userinfo URL was accepted")
	}
	if strings.Contains(err.Error(), "url-secret") {
		t.Fatalf("error echoed URL userinfo: %v", err)
	}
}
