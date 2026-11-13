package referral

import "testing"

func testOrigin(transport Security) Origin {
	return Origin{
		Transport:  transport,
		Bind:       Identity{BindDN: "cn=alice,dc=example,dc=org", Password: "s3cret"},
		SearchBase: "dc=example,dc=org",
	}
}

func testPolicy() Policy {
	return Policy{AllowedHosts: []string{"dir1.example.org", "dir2.example.org"}}
}

func TestTransportSecurity(t *testing.T) {
	pol := testPolicy()

	tests := []struct {
		name      string
		transport Security
		url       string
		want      Reason
	}{
		{
			name:      "ldap cleartext rejected from StartTLS origin",
			transport: SecurityStartTLS,
			url:       "ldap://dir1.example.org/dc=example,dc=org",
			want:      ReasonDowngrade,
		},
		{
			name:      "ldap cleartext rejected from ldaps origin",
			transport: SecurityLDAPS,
			url:       "ldap://dir1.example.org/dc=example,dc=org",
			want:      ReasonDowngrade,
		},
		{
			name:      "ldaps always allowed",
			transport: SecurityCleartext,
			url:       "ldaps://dir1.example.org/dc=example,dc=org",
			want:      ReasonFollow,
		},
		{
			name:      "StartTLS to StartTLS allowed",
			transport: SecurityStartTLS,
			url:       "ldap://dir1.example.org/dc=example,dc=org??sub?(objectClass=*)?1.3.6.1.4.1.1466.20037",
			want:      ReasonFollow,
		},
		{
			name:      "StartTLS origin but referral lacks StartTLS",
			transport: SecurityStartTLS,
			url:       "ldap://dir1.example.org/dc=example,dc=org",
			want:      ReasonDowngrade,
		},
		{
			name:      "StartTLS referral from ldaps origin rejected",
			transport: SecurityLDAPS,
			url:       "ldap://dir1.example.org/dc=example,dc=org??sub??1.3.6.1.4.1.1466.20037",
			want:      ReasonDowngrade,
		},
		{
			name:      "unsupported scheme rejected",
			transport: SecurityLDAPS,
			url:       "http://dir1.example.org/dc=example,dc=org",
			want:      ReasonMalformedReferral,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := pol.Decide(testOrigin(tt.transport), NewChain(), tt.url)
			if d.Reason != tt.want {
				t.Fatalf("reason = %q, want %q (follow=%v)", d.Reason, tt.want, d.Follow)
			}
		})
	}
}

func TestWhitelist(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)

	d := pol.Decide(origin, NewChain(), "ldaps://evil.attacker.com/dc=example,dc=org")
	if d.Reason != ReasonHostNotWhitelisted || d.Follow {
		t.Fatalf("attacker host must be rejected, got %q", d.Reason)
	}

	// Case-insensitive host comparison.
	d = pol.Decide(origin, NewChain(), "ldaps://DIR1.Example.ORG/dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("whitelist comparison must be case-insensitive, got %q", d.Reason)
	}

	// host:port whitelist entry enforces the port.
	portPol := Policy{AllowedHosts: []string{"dir1.example.org:636"}}
	d = portPol.Decide(origin, NewChain(), "ldaps://dir1.example.org/dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("default ldaps port 636 must match whitelist, got %q", d.Reason)
	}
	d = portPol.Decide(origin, NewChain(), "ldaps://dir1.example.org:1636/dc=example,dc=org")
	if d.Reason != ReasonHostNotWhitelisted {
		t.Fatalf("non-whitelisted port must be rejected, got %q", d.Reason)
	}
}

func TestDNScope(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)

	tests := []struct {
		name string
		url  string
		want Reason
	}{
		{
			name: "subordinate DN allowed",
			url:  "ldaps://dir1.example.org/ou=people,dc=example,dc=org",
			want: ReasonFollow,
		},
		{
			name: "equal DN allowed",
			url:  "ldaps://dir1.example.org/dc=example,dc=org",
			want: ReasonFollow,
		},
		{
			name: "foreign DN rejected",
			url:  "ldaps://dir1.example.org/dc=attacker,dc=net",
			want: ReasonDNOutOfScope,
		},
		{
			name: "prefix lookalike rejected",
			url:  "ldaps://dir1.example.org/dc=example,dc=org.evil,dc=net",
			want: ReasonDNOutOfScope,
		},
		{
			name: "percent-encoded DN decoded then compared",
			url:  "ldaps://dir1.example.org/ou=people%2cdc=example%2cdc=org",
			want: ReasonFollow,
		},
		{
			name: "percent-encoded foreign DN cannot smuggle scope",
			url:  "ldaps://dir1.example.org/dc=attacker%2cdc=net",
			want: ReasonDNOutOfScope,
		},
		{
			name: "dot-dot traversal rejected",
			url:  "ldaps://dir1.example.org/ou=..%2cdc=attacker%2cdc=net",
			want: ReasonDNOutOfScope,
		},
		{
			name: "escaped dot-dot traversal rejected",
			url:  "ldaps://dir1.example.org/cn=%5c..%2cdc=attacker%2cdc=net",
			want: ReasonDNOutOfScope,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := pol.Decide(origin, NewChain(), tt.url)
			if d.Reason != tt.want {
				t.Fatalf("reason = %q, want %q", d.Reason, tt.want)
			}
		})
	}
}

func TestReferralLoopAndDepth(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)
	chain := NewChain()

	first := "ldaps://dir1.example.org/ou=a,dc=example,dc=org"
	second := "ldaps://dir2.example.org/ou=b,dc=example,dc=org"

	d1 := pol.Decide(origin, chain, first)
	if !d1.Follow {
		t.Fatalf("first referral: %q", d1.Reason)
	}
	chain.Advance(d1)

	d2 := pol.Decide(origin, chain, second)
	if !d2.Follow {
		t.Fatalf("second referral: %q", d2.Reason)
	}
	chain.Advance(d2)

	// Repeating host+port+DN is a loop even on a short chain.
	dLoop := pol.Decide(origin, chain, first)
	if dLoop.Reason != ReasonReferralLoop {
		t.Fatalf("loop reason = %q, want %q", dLoop.Reason, ReasonReferralLoop)
	}

	// Fill the chain to the depth limit (two more distinct referrals).
	d3 := pol.Decide(origin, chain, "ldaps://dir1.example.org/ou=c,dc=example,dc=org")
	if !d3.Follow {
		t.Fatalf("third referral: %q", d3.Reason)
	}
	chain.Advance(d3)

	d4 := pol.Decide(origin, chain, "ldaps://dir2.example.org/ou=d,dc=example,dc=org")
	if d4.Reason != ReasonDepthExceeded {
		t.Fatalf("depth reason = %q, want %q", d4.Reason, ReasonDepthExceeded)
	}
}

func TestReferralBackToOriginIsLoop(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)
	origin.Addr = "ldaps://dir1.example.org/dc=example,dc=org"

	// Same host, port and DN as the original request is an immediate loop.
	d := pol.Decide(origin, NewChain(), "ldaps://dir1.example.org/dc=example,dc=org")
	if d.Reason != ReasonReferralLoop {
		t.Fatalf("referral back to origin = %q, want %q", d.Reason, ReasonReferralLoop)
	}

	// A different DN under the same host is not a loop.
	d = pol.Decide(origin, NewChain(), "ldaps://dir1.example.org/ou=people,dc=example,dc=org")
	if !d.Follow {
		t.Fatalf("same host with a new DN should be followable, got %q", d.Reason)
	}

	// host:port form of the origin address works as well.
	origin.Addr = "dir2.example.org:636"
	d = pol.Decide(origin, NewChain(), "ldaps://dir2.example.org/dc=example,dc=org")
	if d.Reason != ReasonReferralLoop {
		t.Fatalf("host:port origin loop = %q, want %q", d.Reason, ReasonReferralLoop)
	}
}

func TestCredentialForwarding(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)
	chain := NewChain()

	// First followed hop gets the bound credentials for one rebind.
	d1 := pol.Decide(origin, chain, "ldaps://dir1.example.org/ou=a,dc=example,dc=org")
	if !d1.Follow || d1.Rebind.Password != "s3cret" || d1.Rebind.BindDN != origin.Bind.BindDN {
		t.Fatalf("first hop must carry the bind credentials, got %+v", d1.Rebind)
	}
	chain.Advance(d1)

	// Second hop may be followed but must rebind anonymously; the password is
	// never forwarded down the chain.
	d2 := pol.Decide(origin, chain, "ldaps://dir2.example.org/ou=b,dc=example,dc=org")
	if !d2.Follow {
		t.Fatalf("second hop should still be followable, got %q", d2.Reason)
	}
	if d2.Rebind.Password != "" || d2.Rebind.BindDN != "" {
		t.Fatalf("second hop must rebind anonymously, got %+v", d2.Rebind)
	}

	// A third hop likewise never receives the credentials.
	chain.Advance(d2)
	d3 := pol.Decide(origin, chain, "ldaps://dir1.example.org/ou=c,dc=example,dc=org")
	if !d3.Follow || d3.Rebind.Password != "" {
		t.Fatalf("later hops must stay anonymous, got follow=%v rebind=%+v", d3.Follow, d3.Rebind)
	}
}

func TestAnonymousStaysAnonymous(t *testing.T) {
	pol := testPolicy()
	origin := Origin{
		Transport:  SecurityLDAPS,
		Bind:       Identity{},
		SearchBase: "dc=example,dc=org",
	}
	chain := NewChain()

	d1 := pol.Decide(origin, chain, "ldaps://dir1.example.org/ou=a,dc=example,dc=org")
	if !d1.Follow || !d1.Rebind.Anonymous() {
		t.Fatalf("anonymous origin must produce an anonymous referral bind")
	}
	chain.Advance(d1)

	// Anonymous hops are not limited by the single-rebind rule.
	d2 := pol.Decide(origin, chain, "ldaps://dir2.example.org/ou=b,dc=example,dc=org")
	if !d2.Follow || !d2.Rebind.Anonymous() {
		t.Fatalf("anonymous chain must remain anonymous and followable, got %q", d2.Reason)
	}
}

func TestDecideAllStopsAtFirstFollowable(t *testing.T) {
	pol := testPolicy()
	origin := testOrigin(SecurityLDAPS)
	referrals := []string{
		"ldap://dir1.example.org/dc=example,dc=org",            // downgrade
		"ldaps://evil.attacker.com/dc=example,dc=org",          // not whitelisted
		"ldaps://dir2.example.org/ou=people,dc=example,dc=org", // ok
		"ldaps://dir1.example.org/dc=example,dc=org",           // never evaluated
	}

	out := pol.DecideAll(origin, NewChain(), referrals)
	if len(out) != 3 || !out[2].Follow {
		t.Fatalf("expected two rejections then a follow, got %+v", out)
	}
}

func TestParseNormalization(t *testing.T) {
	ref, err := Parse("ldaps://Dir1.Example.ORG/ou=people,dc=example,dc=org")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Host != "dir1.example.org" || ref.Port != 636 {
		t.Fatalf("normalization = %s:%d", ref.Host, ref.Port)
	}

	ref, err = Parse("ldap://dir1.example.org:1389/dc=example,dc=org")
	if err != nil || ref.Port != 1389 {
		t.Fatalf("explicit port parsing failed: %+v err=%v", ref, err)
	}

	if _, err := Parse("ldaps:///dc=example,dc=org"); err == nil {
		t.Fatal("missing host must fail parsing")
	}
}
