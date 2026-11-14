package pinning

import (
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"
)

func managerWith(t *testing.T, now time.Time, policies ...*Policy) *Manager {
	t.Helper()
	m := NewManager(func() time.Time { return now })
	for _, p := range policies {
		if err := m.AddPolicy(p); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestPrimaryChainAccepted(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	err := m.Verify(purposeGateway, testServerName, rawChain(f.primaryLeaf, f.primaryInt))
	if err != nil {
		t.Fatalf("primary chain rejected: %v", err)
	}
}

func TestLegitimateBackupChainAccepted(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	err := m.Verify(purposeGateway, testServerName, rawChain(f.backupLeaf, f.backupInt))
	if err != nil {
		t.Fatalf("legitimate backup chain rejected: %v", err)
	}
}

// An attacker leaf issued under the genuine backup intermediate (a
// mis-issued or otherwise obtained certificate inside the backup chain)
// must fail because the attacker leaf key is not pinned.
func TestMITMLeafUnderBackupIssuerRejected(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	attackerLeaf := issueLeaf(t, f.backupInt, testServerName)
	err := m.Verify(purposeGateway, testServerName, rawChain(attackerLeaf, f.backupInt))
	if !errors.Is(err, ErrNoPinnedChain) {
		t.Fatalf("expected ErrNoPinnedChain, got %v", err)
	}
}

// An attacker intermediate under the backup root must fail because the
// intermediate SPKI is not pinned, even though the chain anchors at a
// declared root.
func TestMITMIntermediateUnderBackupRootRejected(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	attackerInt := newCA(t, "attacker-intermediate", f.backupRoot)
	attackerLeaf := issueLeaf(t, attackerInt, testServerName)
	err := m.Verify(purposeGateway, testServerName, rawChain(attackerLeaf, attackerInt))
	if !errors.Is(err, ErrNoPinnedChain) {
		t.Fatalf("expected ErrNoPinnedChain, got %v", err)
	}
}

// A chain anchoring at a root that no policy declares must fail; the
// verifier never falls back to the system trust store.
func TestUnknownRootRejected(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	rogueRoot := newCA(t, "rogue-root", nil)
	rogueInt := newCA(t, "rogue-intermediate", rogueRoot)
	rogueLeaf := issueLeaf(t, rogueInt, testServerName)
	if err := m.Verify(purposeGateway, testServerName, rawChain(rogueLeaf, rogueInt)); err == nil {
		t.Fatal("chain under undeclared root was accepted")
	}
}

// Multiple pins per purpose: the set declares two leaves and four
// issuers, and both declared chains verify.
func TestMultiplePinsAccepted(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))

	for name, chain := range map[string][][]byte{
		"primary": rawChain(f.primaryLeaf, f.primaryInt),
		"backup":  rawChain(f.backupLeaf, f.backupInt),
	} {
		if err := m.Verify(purposeGateway, testServerName, chain); err != nil {
			t.Fatalf("%s chain rejected: %v", name, err)
		}
	}
}

// rotationFixture models a leaf-key rotation: v1 pins the old leaf, v2
// pins the new leaf, both under the same primary chain issuers.
type rotationFixture struct {
	f       *fixture
	oldLeaf *testLeaf
	newLeaf *testLeaf

	t0       time.Time
	rotation time.Time
	grace    time.Duration
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	f := newFixture(t)
	r := &rotationFixture{
		f:        f,
		oldLeaf:  f.primaryLeaf,
		newLeaf:  issueLeaf(t, f.primaryInt, testServerName),
		t0:       testStart,
		rotation: testStart.Add(10 * 24 * time.Hour),
		grace:    5 * 24 * time.Hour,
	}
	return r
}

func (r *rotationFixture) policyFor(version string, leaf *testLeaf, notBefore, notAfter time.Time) *Policy {
	pins := NewPinSet()
	pins.PinLeaf(leaf.cert)
	pins.PinIssuer(r.f.primaryInt.cert)
	pins.PinIssuer(r.f.primaryRoot.cert)
	return &Policy{
		Version:   version,
		NotBefore: notBefore,
		NotAfter:  notAfter,
		Roots:     rootPool(r.f.primaryRoot),
		Pins:      map[Purpose]*PinSet{purposeGateway: pins},
	}
}

func (r *rotationFixture) v1() *Policy {
	return r.policyFor("v1", r.oldLeaf, r.t0, r.rotation.Add(r.grace))
}

func (r *rotationFixture) v2() *Policy {
	return r.policyFor("v2", r.newLeaf, r.rotation, r.t0.Add(30*24*time.Hour))
}

// Before the rotation time only the old policy is effective; the old
// chain verifies and the successor policy is not yet consulted.
func TestOldPolicyBeforeRotation(t *testing.T) {
	r := newRotationFixture(t)
	before := r.rotation.Add(-time.Hour)
	m := managerWith(t, before, r.v1(), r.v2())

	if err := m.Verify(purposeGateway, testServerName, rawChain(r.oldLeaf, r.f.primaryInt)); err != nil {
		t.Fatalf("old chain rejected before rotation: %v", err)
	}
	if err := m.Verify(purposeGateway, testServerName, rawChain(r.newLeaf, r.f.primaryInt)); !errors.Is(err, ErrNoPinnedChain) {
		t.Fatalf("new chain should not verify before rotation, got %v", err)
	}
}

// During the grace period both policies are active in parallel, so the
// old and the new chain both verify.
func TestGracePeriodBothPoliciesActive(t *testing.T) {
	r := newRotationFixture(t)
	during := r.rotation.Add(time.Hour)
	m := managerWith(t, during, r.v1(), r.v2())

	if err := m.Verify(purposeGateway, testServerName, rawChain(r.oldLeaf, r.f.primaryInt)); err != nil {
		t.Fatalf("old chain rejected during grace period: %v", err)
	}
	if err := m.Verify(purposeGateway, testServerName, rawChain(r.newLeaf, r.f.primaryInt)); err != nil {
		t.Fatalf("new chain rejected during grace period: %v", err)
	}
}

// After the grace period the old policy is expired: the old chain fails
// and only the new chain verifies.
func TestExpiredPolicyRejected(t *testing.T) {
	r := newRotationFixture(t)
	after := r.rotation.Add(r.grace).Add(time.Hour)
	m := managerWith(t, after, r.v1(), r.v2())

	if err := m.Verify(purposeGateway, testServerName, rawChain(r.oldLeaf, r.f.primaryInt)); !errors.Is(err, ErrNoPinnedChain) {
		t.Fatalf("old chain should fail after expiry, got %v", err)
	}
	if err := m.Verify(purposeGateway, testServerName, rawChain(r.newLeaf, r.f.primaryInt)); err != nil {
		t.Fatalf("new chain rejected after rotation: %v", err)
	}
}

// With only an expired policy published, no policy is in force.
func TestOnlyExpiredPolicyPublished(t *testing.T) {
	r := newRotationFixture(t)
	after := r.rotation.Add(r.grace).Add(time.Hour)
	m := managerWith(t, after, r.v1())

	if err := m.Verify(purposeGateway, testServerName, rawChain(r.oldLeaf, r.f.primaryInt)); !errors.Is(err, ErrNoActivePolicy) {
		t.Fatalf("expected ErrNoActivePolicy, got %v", err)
	}
}

func handshake(serverCert tls.Certificate, clientCfg *tls.Config) error {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverErr := make(chan error, 1)
	go func() {
		server := tls.Server(serverConn, &tls.Config{
			Certificates:           []tls.Certificate{serverCert},
			MinVersion:             tls.VersionTLS12,
			SessionTicketsDisabled: true,
		})
		serverErr <- server.Handshake()
	}()

	client := tls.Client(clientConn, clientCfg)
	err := client.Handshake()
	clientConn.Close()
	<-serverErr
	return err
}

// End to end: a TLS client using the manager TLSConfig completes the
// handshake with the legitimate backup chain and rejects a MITM leaf
// issued inside the backup chain.
func TestTLSHandshake(t *testing.T) {
	f := newFixture(t)
	m := managerWith(t, testStart, f.policy("v1", testStart, testStart.Add(24*time.Hour)))
	clientCfg := m.TLSConfig(purposeGateway, testServerName)

	if err := handshake(tlsCert(f.backupLeaf, f.backupInt), clientCfg); err != nil {
		t.Fatalf("handshake with legitimate backup chain failed: %v", err)
	}

	attackerLeaf := issueLeaf(t, f.backupInt, testServerName)
	if err := handshake(tlsCert(attackerLeaf, f.backupInt), clientCfg); err == nil {
		t.Fatal("handshake with MITM leaf under backup chain succeeded")
	}
}
