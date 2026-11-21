package cryptoplan

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sort"
	"testing"
	"time"
)

const (
	algMLKEM = "ML-KEM-1024"
	algXWing = "X-Wing"
	algRSA   = "RSA-KEM-2048"
)

type testHost struct {
	issuer *Issuer
	pub    ed25519.PublicKey
}

func newTestHost(t *testing.T) *testHost {
	t.Helper()
	issuer, pub, err := NewIssuer()
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return &testHost{issuer: issuer, pub: pub}
}

func deviceKey(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	return pub
}

func (h *testHost) cert(t *testing.T, certID, principal, device string, gen uint64,
	pub ed25519.PublicKey, algs []string, notBefore, notAfter time.Time) DeviceCert {
	t.Helper()
	cert := DeviceCert{
		CertID:      certID,
		PrincipalID: principal,
		DeviceID:    device,
		Generation:  gen,
		PublicKey:   pub,
		Algorithms:  append([]string(nil), algs...),
		NotBefore:   notBefore,
		NotAfter:    notAfter,
	}
	if err := h.issuer.SignDeviceCert(&cert); err != nil {
		t.Fatalf("SignDeviceCert: %v", err)
	}
	return cert
}

func (h *testHost) edge(t *testing.T, edgeID, address, principal, displayName string,
	notBefore, notAfter time.Time) AliasEdge {
	t.Helper()
	edge := AliasEdge{
		EdgeID:      edgeID,
		Address:     address,
		PrincipalID: principal,
		DisplayName: displayName,
		NotBefore:   notBefore,
		NotAfter:    notAfter,
	}
	if err := h.issuer.SignAliasEdge(&edge); err != nil {
		t.Fatalf("SignAliasEdge: %v", err)
	}
	return edge
}

func snapshot(host *testHost, certs []DeviceCert, edges []AliasEdge) *Snapshot {
	return &Snapshot{
		HostPublicKey: host.pub,
		DeviceCerts:   certs,
		AliasEdges:    edges,
	}
}

func minuteTime(offset int) (time.Time, time.Time) {
	base := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(offset-30) * time.Minute),
		base.Add(time.Duration(offset+30) * time.Minute)
}

func window(startOffset, endOffset int) (time.Time, time.Time) {
	base := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(startOffset) * time.Minute),
		base.Add(time.Duration(endOffset) * time.Minute)
}

func assertPlanVerifies(t *testing.T, plan *Plan, snap *Snapshot,
	req *DeliveryRequest, revoked RevocationChecker) {
	t.Helper()
	if err := VerifyDelivery(plan, snap, req, revoked); err != nil {
		t.Fatalf("VerifyDelivery: %v", err)
	}
	if err := Recheck(plan, snap, req, revoked); err != nil {
		t.Fatalf("Recheck: %v", err)
	}
}

func envelopeGroups(plan *Plan) [][]string {
	out := make([][]string, len(plan.Envelopes))
	for i, envelope := range plan.Envelopes {
		out[i] = append([]string(nil), envelope.GroupStableIDs...)
	}
	return out
}

func partitionKey(groups [][]string) string {
	cp := append([][]string(nil), groups...)
	sort.Slice(cp, func(i, j int) bool { return cp[i][0] < cp[j][0] })
	return fmt.Sprintf("%v", cp)
}

func TestForbiddenPairForcesExtraEnvelope(t *testing.T) {
	host := newTestHost(t)
	release := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	notBefore, notAfter := minuteTime(0)
	keyA, keyB := deviceKey(t), deviceKey(t)
	certs := []DeviceCert{
		host.cert(t, "c-a", "id-alice", "dev-a", 1, keyA,
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
		host.cert(t, "c-b", "id-bob", "dev-b", 1, keyB,
			[]string{algMLKEM, algXWing}, notBefore, notAfter),
	}
	edges := []AliasEdge{
		host.edge(t, "e-a", "alice@example.test", "id-alice", "Alice", notBefore, notAfter),
		host.edge(t, "e-b", "bcc-bob@example.test", "id-bob", "Bob", notBefore, notAfter),
	}
	req := &DeliveryRequest{
		Groups: []DeviceGroup{
			{StableID: "g1", Recipient: "alice@example.test", DeviceIDs: []string{"dev-a"}},
			{StableID: "g2", Recipient: "bcc-bob@example.test", DeviceIDs: []string{"dev-b"}},
		},
		ForbiddenPairs:      [][2]string{{"id-alice", "id-bob"}},
		AlgorithmPreference: []string{algMLKEM, algXWing},
		ReleaseTime:         release,
	}
	plan, err := PlanDelivery(snapshot(host, certs, edges), req, nil)
	if err != nil {
		t.Fatalf("PlanDelivery: %v", err)
	}
	if len(plan.Envelopes) != 2 {
		t.Fatalf("保密身份对必须拆成 2 个信封，实际 %d", len(plan.Envelopes))
	}
	for _, envelope := range plan.Envelopes {
		if len(envelope.Principals) != 1 {
			t.Fatalf("每个信封只能披露一个保密身份: %v", envelope.Principals)
		}
		if envelope.Algorithm != algMLKEM {
			t.Fatalf("应选最高等级算法，实际 %s", envelope.Algorithm)
		}
	}
	assertPlanVerifies(t, plan, snapshot(host, certs, edges), req, nil)
}
