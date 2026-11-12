package netsec

import (
	"testing"
)

func TestAuthLevelString(t *testing.T) {
	cases := map[AuthLevel]string{
		AuthAnonymous:   "anonymous",
		AuthCertificate: "certificate",
		AuthMutual:      "mutual",
		AuthLevel(99):   "unknown",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Fatalf("level %d string = %q, want %q", level, got, want)
		}
	}
}

func TestDefaultSessionFactory(t *testing.T) {
	clock := newFakeClock(baseTime)
	store := newPolicyStore(policyFor(testTenantA, AuthCertificate, 600, "h2"))
	keyring, err := NewKeyring(testKey(t), clock.get(), 60)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	manager, err := NewManager(keyring, store, nil, WithClock(clock.get))
	if err != nil {
		t.Fatalf("new manager with nil factory: %v", err)
	}

	data := []byte("default-factory")
	ticket, err := manager.Issue(testTenantA, AuthCertificate, tls13Options("h2"), data)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	session, err := manager.Resume(testTenantA, ticket)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if session.Tenant != testTenantA || string(session.SessionData) != string(data) {
		t.Fatalf("default factory session mismatch: %+v", session)
	}
}

func TestNewManagerValidation(t *testing.T) {
	store := PolicyProviderFunc(func(string) (Policy, bool) { return Policy{}, false })
	if _, err := NewManager(nil, store, nil); err == nil {
		t.Fatal("nil keyring must be rejected")
	}
	keyring, err := NewKeyring(testKey(t), baseTime, 60)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	if _, err := NewManager(keyring, nil, nil); err == nil {
		t.Fatal("nil policy provider must be rejected")
	}
}

func TestUnmarshalClaimsRejectsGarbage(t *testing.T) {
	valid := claims{
		tenant:       testTenantA,
		authLevel:    AuthCertificate,
		minVersion:   0x0304,
		maxVersion:   0x0304,
		alpn:         []string{"h2"},
		policyDigest: [32]byte{1, 2, 3},
		issuedAt:     baseTime,
		expiresAt:    baseTime + 60,
		sessionData:  []byte("data"),
	}
	raw, err := valid.marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if _, err := unmarshalClaims(raw[:len(raw)-1]); err == nil {
		t.Fatal("truncated claims must be rejected")
	}
	if _, err := unmarshalClaims(append(raw, 0x01)); err == nil {
		t.Fatal("trailing bytes must be rejected")
	}

	badLevel := valid
	badLevel.authLevel = AuthLevel(42)
	badRaw, err := badLevel.marshal()
	if err != nil {
		t.Fatalf("marshal bad level: %v", err)
	}
	if _, err := unmarshalClaims(badRaw); err == nil {
		t.Fatal("unknown auth level must be rejected")
	}

	emptyTenant := valid
	emptyTenant.tenant = ""
	emptyRaw, err := emptyTenant.marshal()
	if err != nil {
		t.Fatalf("marshal empty tenant: %v", err)
	}
	if _, err := unmarshalClaims(emptyRaw); err == nil {
		t.Fatal("empty tenant must be rejected")
	}
}

func TestAllowsBranches(t *testing.T) {
	policy := Policy{
		MinAuthLevel: AuthCertificate,
		Options:      ProtocolOptions{MinVersion: 0x0303, MaxVersion: 0x0304, ALPN: []string{"h2"}},
	}
	if policy.Allows(AuthAnonymous, tls13Options("h2")) {
		t.Fatal("level below minimum must be rejected")
	}
	if policy.Allows(AuthMutual, ProtocolOptions{MinVersion: 0x0302, MaxVersion: 0x0304, ALPN: []string{"h2"}}) {
		t.Fatal("negotiated minimum version below policy must be rejected")
	}
	if policy.Allows(AuthMutual, ProtocolOptions{MinVersion: 0x0303, MaxVersion: 0x0305, ALPN: []string{"h2"}}) {
		t.Fatal("negotiated maximum version above policy must be rejected")
	}
	if policy.Allows(AuthMutual, ProtocolOptions{MinVersion: 0x0304, MaxVersion: 0x0304, ALPN: []string{"ftp"}}) {
		t.Fatal("ALPN outside allowlist must be rejected")
	}
	if !policy.Allows(AuthMutual, ProtocolOptions{MinVersion: 0x0304, MaxVersion: 0x0304, ALPN: []string{"h2"}}) {
		t.Fatal("compliant options must be allowed")
	}
}
