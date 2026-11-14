package netsec

import (
	"errors"
	"testing"
)

func TestParseReferral(t *testing.T) {
	ref, err := ParseReferral("LDAP://DC1.Corp.Example.COM:636/ou%3Dusers,dc%3Dcorp,dc%3Dexample,dc%3Dcom?uid,cn?sub?(uid=alice)?!x-bind")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Scheme != SchemeLDAP {
		t.Fatalf("scheme = %q", ref.Scheme)
	}
	if ref.Host != "dc1.corp.example.com" {
		t.Fatalf("host = %q", ref.Host)
	}
	if ref.Port != 636 {
		t.Fatalf("port = %d", ref.Port)
	}
	if ref.DN != "ou=users,dc=corp,dc=example,dc=com" {
		t.Fatalf("DN = %q", ref.DN)
	}
	if len(ref.Attributes) != 2 || ref.Attributes[0] != "uid" || ref.Attributes[1] != "cn" {
		t.Fatalf("attributes = %#v", ref.Attributes)
	}
	if !ref.HasScope || ref.Scope != ScopeSub {
		t.Fatalf("scope = %v, has=%v", ref.Scope, ref.HasScope)
	}
	if !ref.HasFilter || ref.Filter != "(uid=alice)" {
		t.Fatalf("filter = %q, has=%v", ref.Filter, ref.HasFilter)
	}
	if len(ref.Extensions) != 1 || ref.Extensions[0] != "!x-bind" {
		t.Fatalf("extensions = %#v", ref.Extensions)
	}
}

func TestParseReferralDefaultsAndRejects(t *testing.T) {
	ref, err := ParseReferral("ldaps://ldap.corp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Port != 636 || ref.DN != "" {
		t.Fatalf("unexpected defaults: %#v", ref)
	}

	for _, raw := range []string{
		"",
		"https://ldap.corp.example.com/dn",
		"ldap:///dc=corp,dc=example",
		"ldap://user:password@ldap.corp.example.com/dn",
		"ldap://ldap.corp.example.com:0/dn",
		"ldap://ldap.corp.example.com:70000/dn",
		"ldap://ldap.corp.example.com/dn?uid?invalid",
	} {
		if _, err := ParseReferral(raw); err == nil {
			t.Errorf("ParseReferral(%q) succeeded, want error", raw)
		}
	}
}

func TestNormalizeHostRejectsAliasTricks(t *testing.T) {
	for _, host := range []string{
		"ldap.corp.example.com.evil.test",
		"evil.test/ldap.corp.example.com",
		"ldap_corp.example.com",
		"ldap.corp.example.com..",
	} {
		if _, err := NormalizeHost(host); err == nil && host == "ldap_corp.example.com" {
			t.Errorf("NormalizeHost(%q) accepted an underscore host", host)
		}
	}
	for input, want := range map[string]string{
		"DC1.Corp.Example.COM.": "dc1.corp.example.com",
		"[2001:DB8::1]":         "2001:db8::1",
	} {
		got, err := NormalizeHost(input)
		if err != nil {
			t.Fatalf("NormalizeHost(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("NormalizeHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestPolicyScenarios(t *testing.T) {
	base := Config{
		TrustedDomains:            []string{"corp.example.com"},
		AllowCredentialForwarding: true,
		MaxReferralDepth:          3,
	}
	from := Target{
		Scheme: SchemeLDAPS,
		Host:   "dc1.corp.example.com",
		Port:   636,
		Mode:   TLSModeLDAPS,
	}
	loopKey := (Target{
		Scheme: SchemeLDAPS,
		Host:   "dc1.corp.example.com",
		Port:   636,
		DN:     "ou=users",
	}).Key()

	tests := []struct {
		name      string
		cfg       Config
		raw       string
		state     EvalState
		action    Action
		wantErr   error
		forwarded bool
	}{
		{
			name:      "legitimate same-domain referral",
			raw:       "ldaps://dc2.corp.example.com/ou=users,dc=corp,dc=example,dc=com",
			state:     EvalState{From: &from, Depth: 1},
			action:    FollowWithCredentials,
			forwarded: true,
		},
		{
			name:    "cross-domain referral",
			raw:     "ldaps://attacker.example.net/ou=users,dc=corp",
			state:   EvalState{From: &from, Depth: 1},
			action:  DoNotFollow,
			wantErr: ErrUntrustedHost,
		},
		{
			name:    "host alias suffix attack",
			raw:     "ldaps://dc2.corp.example.com.attacker.example.net/ou=users",
			state:   EvalState{From: &from, Depth: 1},
			action:  DoNotFollow,
			wantErr: ErrUntrustedHost,
		},
		{
			name:    "port replacement",
			raw:     "ldaps://dc2.corp.example.com:1636/ou=users",
			state:   EvalState{From: &from, Depth: 1},
			action:  DoNotFollow,
			wantErr: ErrPortNotAllowed,
		},
		{
			name: "loop",
			raw:  "ldaps://dc1.corp.example.com/ou=users",
			state: EvalState{From: &from, Depth: 1, Visited: map[string]struct{}{
				loopKey: {},
			}},
			action:  DoNotFollow,
			wantErr: ErrReferralLoop,
		},
		{
			name: "cleartext downgrade",
			cfg: Config{
				TrustedDomains:            []string{"corp.example.com"},
				AllowCredentialForwarding: true,
				AllowCleartext:            true,
				MaxReferralDepth:          3,
			},
			raw:     "ldap://dc2.corp.example.com/ou=users",
			state:   EvalState{From: &from, Depth: 1},
			action:  DoNotFollow,
			wantErr: ErrCleartextDowngrade,
		},
		{
			name:      "StartTLS is not a downgrade",
			raw:       "ldap://dc2.corp.example.com/ou=users",
			state:     EvalState{From: &from, Depth: 1},
			action:    FollowWithCredentials,
			forwarded: true,
		},
		{
			name: "anonymous cross-domain follow",
			cfg: Config{
				TrustedDomains:            []string{"corp.example.com"},
				AllowCredentialForwarding: true,
				AllowAnonymousFollow:      true,
				MaxReferralDepth:          3,
			},
			raw:    "ldaps://attacker.example.net/ou=users",
			state:  EvalState{From: &from, Depth: 1},
			action: FollowAnonymous,
		},
		{
			name: "depth limit",
			raw:  "ldaps://dc2.corp.example.com/ou=users",
			state: EvalState{
				From:  &from,
				Depth: 4,
			},
			action:  DoNotFollow,
			wantErr: ErrMaxReferralDepth,
		},
		{
			name:      "trailing dot canonical host",
			raw:       "ldaps://DC2.CORP.EXAMPLE.COM./ou=users",
			state:     EvalState{From: &from, Depth: 1},
			action:    FollowWithCredentials,
			forwarded: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			if tc.cfg.TrustedDomains != nil || tc.cfg.AllowCleartext || tc.cfg.AllowAnonymousFollow {
				cfg = tc.cfg
			}
			f, err := NewFollower(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := ParseReferral(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			got := f.Evaluate(ref, tc.state)
			if got.Action != tc.action {
				t.Fatalf("action = %v (%s), want %v", got.Action, got.Reason, tc.action)
			}
			if tc.wantErr != nil && !errors.Is(got.Err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", got.Err, tc.wantErr)
			}
			if tc.action == FollowWithCredentials && got.CredentialsAllowed != tc.forwarded {
				t.Fatalf("CredentialsAllowed = %v, want %v", got.CredentialsAllowed, tc.forwarded)
			}
		})
	}
}

func TestCrossForestPolicy(t *testing.T) {
	cfg := Config{
		TrustedDomains:            []string{"corp.example.com", "partner.example.com"},
		AllowCredentialForwarding: true,
		AllowAnonymousFollow:      true,
		MaxReferralDepth:          3,
		Forest:                    "corp",
		DomainForests: map[string]string{
			"corp.example.com":    "corp",
			"partner.example.com": "partner",
		},
	}
	f, err := NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	from := Target{Scheme: SchemeLDAPS, Host: "dc1.corp.example.com", Port: 636, Mode: TLSModeLDAPS}
	ref, _ := ParseReferral("ldaps://dc1.partner.example.com/ou=users")
	got := f.Evaluate(ref, EvalState{From: &from, Depth: 1})
	if got.Action != DoNotFollow || !errors.Is(got.Err, ErrCrossForest) {
		t.Fatalf("decision = %v, err = %v", got.Action, got.Err)
	}
	if !got.CrossForest || got.TargetForest != "partner" {
		t.Fatalf("forest fields = %#v", got)
	}

	cfg.AllowCrossForest = true
	f, err = NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got = f.Evaluate(ref, EvalState{From: &from, Depth: 1})
	if got.Action != FollowWithCredentials || got.CrossForest != true {
		t.Fatalf("explicit cross-forest decision = %#v", got)
	}
}

func TestEncodeFilter(t *testing.T) {
	for _, filter := range []string{
		"(uid=alice)",
		"(&(objectClass=person)(uid=a*))",
		"(cn=*)",
		"(|(uid=alice)(uid=bob))",
		"(!(disabled=TRUE))",
		"(cn=foo\\2abar)",
	} {
		if _, err := EncodeFilter(filter); err != nil {
			t.Errorf("EncodeFilter(%q): %v", filter, err)
		}
	}
	for _, filter := range []string{"", "uid=alice", "(uid=alice", "(&)"} {
		if filter == "" {
			continue // empty means the default presence filter
		}
		if _, err := EncodeFilter(filter); err == nil {
			t.Errorf("EncodeFilter(%q) succeeded, want error", filter)
		}
	}
}

func TestHostileReferralFormsAreNotFollowed(t *testing.T) {
	f, err := NewFollower(Config{
		TrustedDomains:            []string{"trusted.example.com"},
		AllowCredentialForwarding: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	from := Target{Scheme: SchemeLDAPS, Host: "dc1.trusted.example.com", Port: 636, Mode: TLSModeLDAPS}

	for _, raw := range []string{
		`ldap://trusted.example.com\@attacker.example.net/dn`,
		"ldap://trusted.example.com%2eattacker.example.net/dn",
		"ldap://trusted.example.com:636@attacker.example.net/dn",
		"ldap://attacker.example.net#@trusted.example.com/dn",
		"ldap://trusted.example.com:1389/dn",
	} {
		decision, err := f.EvaluateReferral(raw, EvalState{From: &from, Depth: 1})
		if err == nil && decision.Action == FollowWithCredentials {
			t.Errorf("hostile referral %q would receive credentials: %#v", raw, decision)
		}
		if decision.Action != DoNotFollow {
			t.Errorf("hostile referral %q action = %v", raw, decision.Action)
		}
	}
}
