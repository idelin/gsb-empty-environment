package netsec_test

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0238"
)

const (
	prodTrustDomain = "prod.example.org"
	testTrustDomain = "test.example.org"

	clientID = "spiffe://prod.example.org/ns/default/sa/client"
	serverID = "spiffe://prod.example.org/ns/default/sa/server"

	clientDNS = "client.prod.example.org"
	serverDNS = "server.prod.example.org"
)

type legitSetup struct {
	mesh       *testMesh
	ca         *testCA
	clientCert *x509.Certificate
	serverCert *x509.Certificate
	policy     netsec.ConnectionPolicy
}

func setupLegit(t *testing.T, configure func(*netsec.Config)) *legitSetup {
	t.Helper()
	mesh := newTestMesh(t, prodTrustDomain, configure)
	ca := newTestCA(t, "prod-root")
	mesh.installBundle(t, 1, ca)

	clientCert := ca.issue(t, leafSpec{
		spiffeID: clientID,
		dns:      []string{clientDNS},
		ips:      []string{"10.0.0.11"},
		lifetime: time.Hour,
	})
	serverCert := ca.issue(t, leafSpec{
		spiffeID: serverID,
		dns:      []string{serverDNS},
		ips:      []string{"10.0.0.12"},
		lifetime: time.Hour,
	})

	policy := netsec.ConnectionPolicy{
		Caller:  identityPolicy(t, prodTrustDomain, clientID, exactRules(clientDNS), []string{"10.0.0.0/24"}, map[string]string{"env": "prod", "node": mesh.nodeID}, netsec.UseClient),
		Target:  identityPolicy(t, prodTrustDomain, serverID, exactRules(serverDNS), []string{"10.0.0.0/24"}, map[string]string{"env": "prod", "node": mesh.nodeID}, netsec.UseServer),
		Actions: []string{"connect"},
	}
	if err := mesh.authority.UpdatePolicies(1, map[string]netsec.ConnectionPolicy{
		"mesh-connect": policy,
	}); err != nil {
		t.Fatal(err)
	}

	return &legitSetup{
		mesh:       mesh,
		ca:         ca,
		clientCert: clientCert,
		serverCert: serverCert,
		policy:     policy,
	}
}

func TestLegitimateSameDomainWorkloadIsAccepted(t *testing.T) {
	setup := setupLegit(t, nil)
	request := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")

	decision, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request)
	if err != nil {
		t.Fatalf("legitimate same-domain connection rejected: %v", err)
	}
	if decision.Caller.SPIFFEID.String() != clientID {
		t.Fatalf("unexpected caller identity: %s", decision.Caller.SPIFFEID)
	}
	if decision.Target.SPIFFEID.String() != serverID {
		t.Fatalf("unexpected target identity: %s", decision.Target.SPIFFEID)
	}
	if decision.Caller.Node.NodeID != setup.mesh.nodeID {
		t.Fatalf("caller node attestation missing: %#v", decision.Caller.Node)
	}
	if len(decision.Caller.SANs) < 3 {
		t.Fatalf("expected typed URI, DNS, and IP SANs, got %v", decision.Caller.SANs)
	}
	if len(decision.Caller.SANs.DNSNames()) != 1 || decision.Caller.SANs.DNSNames()[0] != clientDNS {
		t.Fatalf("unexpected DNS SANs: %v", decision.Caller.SANs.DNSNames())
	}
	if len(decision.Caller.SANs.IPAddresses()) != 1 || decision.Caller.SANs.IPAddresses()[0].String() != "10.0.0.11" {
		t.Fatalf("unexpected IP SANs: %v", decision.Caller.SANs.IPAddresses())
	}
	if len(decision.Caller.SANs.URIs()) != 1 || decision.Caller.SANs.URIs()[0].String() != clientID {
		t.Fatalf("unexpected URI SANs: %v", decision.Caller.SANs.URIs())
	}
	if decision.Caller.Fingerprint() == decision.Target.Fingerprint() {
		t.Fatal("distinct workloads must have distinct identity fingerprints")
	}
	if decision.Generations.Bundles[setup.mesh.trust] != 1 {
		t.Fatalf("unexpected bundle generation: %#v", decision.Generations)
	}
	if decision.Generations.Policies != 1 {
		t.Fatalf("unexpected policy generation: %#v", decision.Generations)
	}

	second, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request)
	if err != nil {
		t.Fatalf("second legitimate connection rejected: %v", err)
	}
	if second.EvaluatedAt != firstEvaluationTime(setup.mesh) {
		t.Fatal("evaluation time should come from the configured clock")
	}
	if got := setup.mesh.authority.Evaluations(); got != 2 {
		t.Fatalf("expected two evaluations, got %d", got)
	}
}

func firstEvaluationTime(mesh *testMesh) time.Time {
	return mesh.now
}

func TestRevocationTakesEffectOnNextConnection(t *testing.T) {
	setup := setupLegit(t, nil)
	request := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); err != nil {
		t.Fatalf("pre-revocation connection rejected: %v", err)
	}

	if err := setup.mesh.authority.RevokeCertificates(setup.mesh.trust, 2, setup.clientCert); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("post-revocation connection: got %v, want ErrRevoked", err)
	}
	if err := setup.mesh.authority.UpdateRevocations(setup.mesh.trust, 1, nil); !errors.Is(err, netsec.ErrStaleGeneration) {
		t.Fatalf("stale revocation update: got %v", err)
	}
	if got := setup.mesh.authority.Generations().Revocations[setup.mesh.trust]; got != 2 {
		t.Fatalf("revocation generation rolled back: %d", got)
	}
}

func TestShortLivedRotationTakesEffectByGeneration(t *testing.T) {
	setup := setupLegit(t, func(cfg *netsec.Config) {
		cfg.RequireRotationRecords = true
	})

	clientRotation := rotationRecord(t, clientID, 1, setup.clientCert)
	serverRotation := rotationRecord(t, serverID, 1, setup.serverCert)
	if err := setup.mesh.authority.UpdateRotations(1, []netsec.RotationRecord{clientRotation, serverRotation}); err != nil {
		t.Fatal(err)
	}

	oldRequest := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), oldRequest); err != nil {
		t.Fatalf("current rotation rejected: %v", err)
	}

	newClient := setup.ca.issue(t, leafSpec{
		spiffeID: clientID,
		dns:      []string{clientDNS},
		ips:      []string{"10.0.0.11"},
		lifetime: time.Hour,
	})
	newServer := setup.ca.issue(t, leafSpec{
		spiffeID: serverID,
		dns:      []string{serverDNS},
		ips:      []string{"10.0.0.12"},
		lifetime: time.Hour,
	})
	if err := setup.mesh.authority.UpdateRotations(2, []netsec.RotationRecord{
		rotationRecord(t, clientID, 2, newClient),
		rotationRecord(t, serverID, 2, newServer),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), oldRequest); !errors.Is(err, netsec.ErrRotated) {
		t.Fatalf("old certificate after rotation: got %v, want ErrRotated", err)
	}
	newRequest := setup.mesh.request("mesh-connect", clientID, newClient, serverID, newServer, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), newRequest); err != nil {
		t.Fatalf("new rotation generation rejected: %v", err)
	}
	if err := setup.mesh.authority.UpdateRotations(1, []netsec.RotationRecord{clientRotation, serverRotation}); !errors.Is(err, netsec.ErrStaleGeneration) {
		t.Fatalf("stale rotation update: got %v", err)
	}
}

func rotationRecord(t *testing.T, workloadID string, generation netsec.Generation, cert *x509.Certificate) netsec.RotationRecord {
	t.Helper()
	id, err := netsec.ParseSPIFFEID(workloadID)
	if err != nil {
		t.Fatal(err)
	}
	certID, err := netsec.CertificateIDForCertificate(cert)
	if err != nil {
		t.Fatal(err)
	}
	return netsec.RotationRecord{
		Workload:   id,
		Generation: generation,
		Active:     []netsec.CertificateID{certID},
		ValidUntil: testTime.Add(24 * time.Hour),
	}
}

func TestBundleUpdateTakesEffectByGeneration(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	oldCA := newTestCA(t, "prod-root-old")
	newCA := newTestCA(t, "prod-root-new")
	mesh.installBundle(t, 1, oldCA)

	workloadID := "spiffe://prod.example.org/ns/default/sa/backend"
	oldCert := oldCA.issue(t, leafSpec{spiffeID: workloadID, lifetime: time.Hour})
	newCert := newCA.issue(t, leafSpec{spiffeID: workloadID, lifetime: time.Hour})

	if _, err := mesh.authority.VerifyPeer(context.Background(), mesh.credential(workloadID, oldCert)); err != nil {
		t.Fatalf("old bundle chain rejected: %v", err)
	}
	if err := mesh.authority.UpdateBundle(mesh.trust, 2, []*x509.Certificate{newCA.cert}); err != nil {
		t.Fatal(err)
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), mesh.credential(workloadID, oldCert)); !errors.Is(err, netsec.ErrInvalidChain) {
		t.Fatalf("chain signed by removed root: got %v", err)
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), mesh.credential(workloadID, newCert)); err != nil {
		t.Fatalf("new bundle chain rejected: %v", err)
	}
	if err := mesh.authority.UpdateBundle(mesh.trust, 1, []*x509.Certificate{oldCA.cert}); !errors.Is(err, netsec.ErrStaleGeneration) {
		t.Fatalf("stale bundle update: got %v", err)
	}
}

func TestIntermediateChainAndChainRevocation(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	root := newTestCA(t, "prod-root")
	intermediate := root.intermediate(t, "prod-intermediate")
	mesh.installBundle(t, 1, root)

	workloadID := "spiffe://prod.example.org/ns/default/sa/backend"
	leaf := intermediate.issue(t, leafSpec{spiffeID: workloadID, lifetime: time.Hour})
	credential := netsec.PeerCredential{
		Leaf:          leaf,
		Intermediates: []*x509.Certificate{intermediate.cert},
		Attestation:   mesh.attest(workloadID, map[string]string{"env": "prod"}),
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), credential); err != nil {
		t.Fatalf("intermediate chain rejected: %v", err)
	}

	revocation, err := netsec.RevocationForCertificate(mesh.trust, intermediate.cert, mesh.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := mesh.authority.UpdateRevocations(mesh.trust, 2, []netsec.Revocation{revocation}); err != nil {
		t.Fatal(err)
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), credential); !errors.Is(err, netsec.ErrRevoked) {
		t.Fatalf("revoked intermediate still accepted: %v", err)
	}
}

func TestCertificateLifetimeIsBounded(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	ca := newTestCA(t, "prod-root")
	mesh.installBundle(t, 1, ca)
	longLived := ca.issue(t, leafSpec{
		spiffeID: "spiffe://prod.example.org/ns/default/sa/backend",
		lifetime: 48 * time.Hour,
	})
	if _, err := mesh.authority.VerifyPeer(context.Background(), mesh.credential("spiffe://prod.example.org/ns/default/sa/backend", longLived)); !errors.Is(err, netsec.ErrCertificateLifetime) {
		t.Fatalf("long-lived certificate: got %v", err)
	}
}

func TestCredentialConstructorsAndAliases(t *testing.T) {
	mesh := newTestMesh(t, prodTrustDomain, nil)
	ca := newTestCA(t, "prod-root")
	mesh.installBundle(t, 1, ca)
	workloadID := "spiffe://prod.example.org/ns/default/sa/backend"
	cert := ca.issue(t, leafSpec{spiffeID: workloadID, dns: []string{"backend.prod.example.org"}, lifetime: time.Hour})
	attestation := mesh.attest(workloadID, map[string]string{"env": "prod"})

	parsed, err := netsec.ParseCredential(cert.Raw, nil, attestation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mesh.authority.Authenticate(context.Background(), parsed); err != nil {
		t.Fatalf("Authenticate rejected a parsed credential: %v", err)
	}
	if _, err := mesh.authority.VerifyPeerWithNonce(context.Background(), parsed, mesh.nonce); err != nil {
		t.Fatalf("VerifyPeerWithNonce rejected a parsed credential: %v", err)
	}

	fromChain, err := netsec.CredentialFromChain([]*x509.Certificate{cert}, attestation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mesh.authority.VerifyPeer(context.Background(), fromChain); err != nil {
		t.Fatalf("VerifyPeer rejected a chain-built credential: %v", err)
	}

	alias, err := netsec.NewVerifier(netsec.Config{
		TrustDomain:     mesh.trust,
		AttestationKeys: nil,
		Clock:           func() time.Time { return mesh.now },
	})
	if err == nil || alias != nil {
		t.Fatal("NewVerifier should reject a configuration without an attestor")
	}
}

func TestPoliciesAreEvaluatedOnEveryConnection(t *testing.T) {
	setup := setupLegit(t, nil)
	request := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); err != nil {
		t.Fatalf("first policy evaluation failed: %v", err)
	}

	updated := setup.policy
	updated.Caller.Paths = []string{"/ns/default/sa/other"}
	if err := setup.mesh.authority.UpdatePolicies(2, map[string]netsec.ConnectionPolicy{"mesh-connect": updated}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("second connection with updated policy: got %v", err)
	}
	if got := setup.mesh.authority.Evaluations(); got != 2 {
		t.Fatalf("authorization was cached; evaluations=%d", got)
	}
	if err := setup.mesh.authority.UpdatePolicies(2, map[string]netsec.ConnectionPolicy{"mesh-connect": setup.policy}); !errors.Is(err, netsec.ErrStaleGeneration) {
		t.Fatalf("stale policy update: got %v", err)
	}
}

func TestTargetPolicyIsEvaluatedOnEveryConnection(t *testing.T) {
	setup := setupLegit(t, nil)
	request := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); err != nil {
		t.Fatalf("first target policy evaluation failed: %v", err)
	}

	updated := setup.policy
	updated.Target.Paths = []string{"/ns/default/sa/other"}
	if err := setup.mesh.authority.UpdatePolicies(2, map[string]netsec.ConnectionPolicy{"mesh-connect": updated}); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("target policy change was not re-evaluated: %v", err)
	}
}

func TestCrossTrustDomainIsNotAccepted(t *testing.T) {
	prodMesh := newTestMesh(t, prodTrustDomain, nil)
	prodCA := newTestCA(t, "prod-root")
	testCA := newTestCA(t, "test-root")
	prodMesh.installBundle(t, 1, prodCA)
	if err := prodMesh.authority.UpdateBundle(netsec.TrustDomain(testTrustDomain), 1, []*x509.Certificate{testCA.cert}); err != nil {
		t.Fatal(err)
	}

	testID := "spiffe://test.example.org/ns/default/sa/client"
	testCert := testCA.issue(t, leafSpec{spiffeID: testID, dns: []string{"evil.prod"}, lifetime: time.Hour})
	credential := netsec.PeerCredential{
		Leaf:        testCert,
		Attestation: prodMesh.attestIn(netsec.TrustDomain(testTrustDomain), testID, map[string]string{"env": "prod"}),
	}
	if _, err := prodMesh.authority.VerifyPeer(context.Background(), credential); !errors.Is(err, netsec.ErrForeignTrustDomain) {
		t.Fatalf("cross-trust-domain peer: got %v", err)
	}

	federated := newTestMesh(t, prodTrustDomain, func(cfg *netsec.Config) {
		cfg.FederatedTrustDomains = []netsec.TrustDomain{netsec.TrustDomain(testTrustDomain)}
	})
	federated.installBundle(t, 1, prodCA)
	if err := federated.authority.UpdateBundle(netsec.TrustDomain(testTrustDomain), 1, []*x509.Certificate{testCA.cert}); err != nil {
		t.Fatal(err)
	}
	federatedCredential := netsec.PeerCredential{
		Leaf:        testCert,
		Attestation: federated.attestIn(netsec.TrustDomain(testTrustDomain), testID, map[string]string{"env": "prod"}),
	}
	if _, err := federated.authority.VerifyPeer(context.Background(), federatedCredential); err != nil {
		t.Fatalf("explicitly federated identity should verify: %v", err)
	}

	prodServer := prodCA.issue(t, leafSpec{spiffeID: serverID, dns: []string{serverDNS}, lifetime: time.Hour})
	policy := netsec.ConnectionPolicy{
		Caller: identityPolicy(t, prodTrustDomain, clientID, exactRules(clientDNS), nil, map[string]string{"env": "prod"}, netsec.UseClient),
		Target: identityPolicy(t, prodTrustDomain, serverID, exactRules(serverDNS), nil, map[string]string{"env": "prod"}, netsec.UseServer),
	}
	if err := federated.authority.UpdatePolicies(1, map[string]netsec.ConnectionPolicy{"mesh-connect": policy}); err != nil {
		t.Fatal(err)
	}
	request := netsec.ConnectionRequest{
		Policy: "mesh-connect",
		Caller: federatedCredential,
		Target: netsec.PeerCredential{
			Leaf:        prodServer,
			Attestation: federated.attest(serverID, map[string]string{"env": "prod"}),
		},
		Action: "connect",
		Nonce:  append([]byte(nil), federated.nonce...),
	}
	if _, err := federated.authority.AuthorizeConnection(context.Background(), request); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("federated identity passed a production policy: %v", err)
	}
}

func TestAttestationBinding(t *testing.T) {
	setup := setupLegit(t, nil)
	baseCredential := setup.mesh.credential(clientID, setup.clientCert)
	request := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")

	missing := setup.mesh.request("mesh-connect", clientID, setup.clientCert, serverID, setup.serverCert, "connect")
	missing.Caller.Attestation = nil
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), missing); !errors.Is(err, netsec.ErrAttestationRequired) {
		t.Fatalf("missing attestation: got %v", err)
	}

	bad := *baseCredential.Attestation
	bad.Signature = append([]byte(nil), baseCredential.Attestation.Signature...)
	bad.Signature[0] ^= 0xff
	badRequest := request
	badRequest.Caller.Attestation = &bad
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), badRequest); !errors.Is(err, netsec.ErrAttestation) {
		t.Fatalf("bad attestation signature: got %v", err)
	}

	otherID := "spiffe://prod.example.org/ns/default/sa/other"
	mismatched := setup.mesh.attest(otherID, map[string]string{"env": "prod"})
	mismatchRequest := request
	mismatchRequest.Caller.Attestation = mismatched
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), mismatchRequest); !errors.Is(err, netsec.ErrAttestation) {
		t.Fatalf("attestation workload mismatch: got %v", err)
	}

	nonceRequest := request
	nonceRequest.Nonce = []byte("different-nonce")
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), nonceRequest); !errors.Is(err, netsec.ErrAttestation) {
		t.Fatalf("nonce mismatch: got %v", err)
	}

	expired := setup.mesh.attest(clientID, map[string]string{"env": "prod"})
	expired.ExpiresAt = setup.mesh.now.Add(-time.Minute)
	if err := expired.Sign(setup.mesh.nodeKey); err != nil {
		t.Fatal(err)
	}
	expiredRequest := request
	expiredRequest.Caller.Attestation = expired
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), expiredRequest); !errors.Is(err, netsec.ErrAttestation) {
		t.Fatalf("expired attestation: got %v", err)
	}

	selectorRequest := request
	selectorRequest.Caller = setup.mesh.credentialWith(clientID, setup.clientCert, map[string]string{"env": "test"})
	if _, err := setup.mesh.authority.AuthorizeConnection(context.Background(), selectorRequest); !errors.Is(err, netsec.ErrPolicyDenied) {
		t.Fatalf("node selector mismatch: got %v", err)
	}
}
