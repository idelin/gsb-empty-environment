package netsec

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

// maliciousDirectory models a split-view directory. It signs both branches
// with the real account master key, but publishes each branch into a separate
// transparency-log view. Exactly one view receives the attacker device.
type maliciousDirectory struct {
	fixture *testFixture
	base    *fakeLog
}

func newMaliciousDirectory(t *testing.T) *maliciousDirectory {
	return &maliciousDirectory{
		fixture: newFixture(t),
		base:    newFakeLog("transparency-log-test"),
	}
}

func (d *maliciousDirectory) genesis(t *testing.T, devices ...Device) (*SignedDeviceList, LogInclusionProof) {
	t.Helper()
	list := d.fixture.list(t, 1, ZeroDigest, devices...)
	signed := d.fixture.sign(t, list)
	proof := d.base.publish(t, signed)
	return signed, proof
}

func (d *maliciousDirectory) branch() *fakeLog {
	return d.base.clone()
}

func (d *maliciousDirectory) publish(t *testing.T, view *fakeLog, list AccountState) (*SignedDeviceList, LogInclusionProof) {
	t.Helper()
	signed := d.fixture.sign(t, list)
	proof := view.publish(t, signed)
	return signed, proof
}

func cloneDevices(devices []Device) []Device {
	out := make([]Device, len(devices))
	for i := range devices {
		out[i] = devices[i].Clone()
	}
	return out
}

func TestMaliciousDirectoryHidesDeviceOnlyFromOneContact(t *testing.T) {
	directory := newMaliciousDirectory(t)
	f := directory.fixture
	keyTrusted, _ := newTestKey(t)
	keyAttacker, _ := newTestKey(t)
	contactAPub, contactAPriv := newTestKey(t)
	contactBPub, contactBPriv := newTestKey(t)

	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-trusted", keyTrusted, 1))
	signed1 := f.sign(t, v1)
	proof1 := directory.base.publish(t, signed1)

	clientA, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	applyCommitted(t, clientA, signed1, proof1)
	applyCommitted(t, clientB, signed1, proof1)

	viewA := directory.branch()
	viewB := directory.branch()
	v2A := addDevice(t, v1, NewDevice("device-attacker", keyAttacker, 2), 2)
	v2B := f.list(t, 2, mustDigest(t, v1), cloneDevices(v1.Devices)...)
	signed2A, proof2A := directory.publish(t, viewA, v2A)
	signed2B, proof2B := directory.publish(t, viewB, v2B)
	applyCommitted(t, clientA, signed2A, proof2A)
	applyCommitted(t, clientB, signed2B, proof2B)

	if _, ok := clientB.List(); !ok {
		t.Fatal("client B has no list")
	}
	listB, _ := clientB.List()
	if _, ok := listB.Device("device-attacker"); ok {
		t.Fatal("client B was not supposed to see the attacker device")
	}
	if !clientA.CanReceiveFrom("device-attacker") {
		t.Fatal("split-view client A did not receive attacker device")
	}

	receiptA, err := clientA.Observe("contact-a", contactAPriv)
	if err != nil {
		t.Fatal(err)
	}
	receiptB, err := clientB.Observe("contact-b", contactBPriv)
	if err != nil {
		t.Fatal(err)
	}

	result, err := clientA.CheckObservation(receiptB, contactBPub)
	if !errors.Is(err, ErrForkDetected) {
		t.Fatalf("client A did not detect receipt fork: result=%+v err=%v", result, err)
	}
	if result.Evidence == nil || result.Evidence.Reason != ForkSameVersionDifferentDigest {
		t.Fatalf("unexpected evidence: %+v", result.Evidence)
	}
	if _, err := clientA.OpenSession("device-attacker"); !errors.Is(err, ErrForkFrozen) {
		t.Fatalf("new session was not frozen after fork: %v", err)
	}

	result, err = clientB.CheckObservation(receiptA, contactAPub)
	if !errors.Is(err, ErrForkDetected) {
		t.Fatalf("client B did not detect receipt fork: result=%+v err=%v", result, err)
	}

	exported, err := result.Evidence.Export()
	if err != nil {
		t.Fatalf("export evidence: %v", err)
	}
	parsed, err := ParseForkEvidence(exported)
	if err != nil {
		t.Fatalf("parse evidence: %v", err)
	}
	if parsed.Version != 2 || parsed.Reason != ForkSameVersionDifferentDigest {
		t.Fatalf("parsed evidence = %+v", parsed)
	}
	if parsed.First.Observation == nil || parsed.Second.Observation == nil {
		t.Fatalf("exported evidence is missing receipts: %+v", parsed)
	}
	contactKeys := map[ContactID]ed25519.PublicKey{
		"contact-a": contactAPub,
		"contact-b": contactBPub,
	}
	if err := parsed.First.Observation.Verify(f.account, contactKeys[parsed.First.Observation.ObserverID]); err != nil {
		t.Fatalf("first receipt signature: %v", err)
	}
	if err := parsed.Second.Observation.Verify(f.account, contactKeys[parsed.Second.Observation.ObserverID]); err != nil {
		t.Fatalf("second receipt signature: %v", err)
	}
	if err := parsed.Verify(f.masterPub, contactKeys); err != nil {
		t.Fatalf("verify exported evidence: %v", err)
	}
}

func TestMaliciousSignedListConflictFreezesWithoutProof(t *testing.T) {
	directory := newMaliciousDirectory(t)
	f := directory.fixture
	keyA, _ := newTestKey(t)
	keyX, _ := newTestKey(t)
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	proof1 := directory.base.publish(t, signed1)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	applyCommitted(t, client, signed1, proof1)

	viewA := directory.branch()
	v2A := addDevice(t, v1, NewDevice("device-x", keyX, 2), 2)
	signed2A, proof2A := directory.publish(t, viewA, v2A)
	applyCommitted(t, client, signed2A, proof2A)

	viewB := directory.branch()
	v2B := f.list(t, 2, mustDigest(t, v1), cloneDevices(v1.Devices)...)
	signed2B, _ := directory.publish(t, viewB, v2B)
	result, err := client.Apply(signed2B, nil)
	if !errors.Is(err, ErrForkDetected) {
		t.Fatalf("same-version signed-list fork: result=%+v err=%v", result, err)
	}
	if !result.Frozen || result.Evidence == nil {
		t.Fatalf("signed-list fork was not frozen: %+v", result)
	}
	if result.Evidence.Reason != ForkSameVersionDifferentDigest {
		t.Fatalf("evidence reason = %s", result.Evidence.Reason)
	}
}

func TestConflictingReceiptBeforeListAlsoFreezes(t *testing.T) {
	directory := newMaliciousDirectory(t)
	f := directory.fixture
	keyA, _ := newTestKey(t)
	keyX, _ := newTestKey(t)
	contactPub, contactPriv := newTestKey(t)
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	proof1 := directory.base.publish(t, signed1)
	client, err := NewClient(f.account, f.masterPub)
	if err != nil {
		t.Fatal(err)
	}
	applyCommitted(t, client, signed1, proof1)

	viewA := directory.branch()
	viewB := directory.branch()
	v2A := addDevice(t, v1, NewDevice("device-x", keyX, 2), 2)
	v2B := f.list(t, 2, mustDigest(t, v1), cloneDevices(v1.Devices)...)
	_, proof2A := directory.publish(t, viewA, v2A)
	signed2B, _ := directory.publish(t, viewB, v2B)

	receiptA := signedObservation(t, "contact", contactPriv,
		observationFor(t, f.account, 2, v2A, proof2A.Checkpoint))
	if result, err := client.CheckObservation(receiptA, contactPub); err != nil || result.Stale {
		t.Fatalf("receipt should be current: result=%+v err=%v", result, err)
	}

	result, err := client.Apply(signed2B)
	if !errors.Is(err, ErrForkDetected) {
		t.Fatalf("receipt/list fork: result=%+v err=%v", result, err)
	}
	if !result.Frozen || result.Evidence == nil {
		t.Fatalf("receipt/list conflict did not freeze: %+v", result)
	}
	if err := result.Evidence.Verify(f.masterPub, map[ContactID]ed25519.PublicKey{"contact": contactPub}); err != nil {
		t.Fatalf("receipt/list evidence: %v", err)
	}
	if client.Version() != 1 {
		t.Fatalf("conflicting list was committed at version %d", client.Version())
	}
	if _, err := client.OpenSession("device-x"); !errors.Is(err, ErrForkFrozen) {
		t.Fatalf("session was not frozen: %v", err)
	}
}
