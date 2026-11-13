package netsec

import (
	"errors"
	"testing"
)

func TestDelayedProofDoesNotAuthorizeDevice(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	keyX, _ := newTestKey(t)

	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	applyCommitted(t, client, signed1, log.publish(t, signed1))

	v2 := addDevice(t, v1, NewDevice("device-x", keyX, 2), 2)
	signed2 := f.sign(t, v2)
	result, err := client.Apply(signed2, nil)
	if err != nil {
		t.Fatalf("staging signed list: %v", err)
	}
	if result.Status != UpdatePendingProof || !result.Pending {
		t.Fatalf("expected pending update, got %+v", result)
	}
	if client.Version() != 1 {
		t.Fatalf("pending update changed committed version to %d", client.Version())
	}
	if client.CanReceiveFrom("device-x") || client.CanSendTo("device-x") {
		t.Fatal("device became authorized before the log proof")
	}
	if _, err := client.OpenSession("device-x"); !errors.Is(err, ErrDeviceNotAuthorized) {
		t.Fatalf("pending device opened a session: %v", err)
	}

	proof2 := log.publish(t, signed2)
	result, err = client.ApplyProof(&proof2)
	if err != nil {
		t.Fatalf("ApplyProof: %v", err)
	}
	if !result.Committed() || client.Version() != 2 {
		t.Fatalf("proof did not commit update: %+v, version=%d", result, client.Version())
	}
	if !client.CanReceiveFrom("device-x") {
		t.Fatal("new device was not authorized after signed list and proof completed")
	}
	if _, err := client.OpenSession("device-x"); err != nil {
		t.Fatalf("authorized device cannot open a session: %v", err)
	}
}

func TestHostProofCallbackCanDelayCommit(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	keyX, _ := newTestKey(t)
	verifier := &delayVerifier{}
	client, err := NewClient(f.account, f.masterPub, verifier)
	if err != nil {
		t.Fatal(err)
	}

	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))

	v2 := addDevice(t, v1, NewDevice("device-x", keyX, 2), 2)
	signed2 := f.sign(t, v2)
	proof2 := log.publish(t, signed2)
	verifier.delay.Store(true)
	result, err := client.Apply(signed2, &proof2)
	if err != nil {
		t.Fatalf("delayed proof should be retryable, got %v", err)
	}
	if result.Status != UpdatePendingProof || !result.Pending {
		t.Fatalf("expected callback-delayed update, got %+v", result)
	}
	if verifier.calls.Load() == 0 {
		t.Fatal("host proof callback was not invoked")
	}
	if client.CanReceiveFrom("device-x") {
		t.Fatal("callback-delayed device was authorized")
	}

	verifier.delay.Store(false)
	result, err = client.ApplyProof(&proof2)
	if err != nil {
		t.Fatalf("retry proof: %v", err)
	}
	if !result.Committed() {
		t.Fatalf("retry did not commit: %+v", result)
	}
	if !client.CanReceiveFrom("device-x") {
		t.Fatal("device not authorized after callback accepted proof")
	}
}

func TestRemovedDeviceSessionStopsNewSends(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))
	session, err := client.OpenSession("device-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Send(); err != nil {
		t.Fatalf("initial send: %v", err)
	}

	v2 := removeDevice(t, v1, "device-a", 2)
	signed2 := f.sign(t, v2)
	proof2 := log.publish(t, signed2)

	result, err := client.Apply(signed2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != UpdatePendingProof {
		t.Fatalf("expected uncommitted removal, got %+v", result)
	}
	if err := session.Send(); err != nil {
		t.Fatalf("unproven removal stopped the session early: %v", err)
	}

	result, err = client.ApplyProof(&proof2)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Committed() {
		t.Fatalf("removal did not commit: %+v", result)
	}
	if session.CanSend() {
		t.Fatal("existing session can still send after committed removal")
	}
	if !errors.Is(session.Send(), ErrDeviceRemoved) {
		t.Fatalf("unexpected send error after removal: %v", session.Send())
	}
	if client.CanSendTo("device-a") || client.CanReceiveFrom("device-a") {
		t.Fatal("removed device still authorized")
	}
	if _, err := client.OpenSession("device-a"); !errors.Is(err, ErrDeviceRemoved) {
		t.Fatalf("removed device opened a session: %v", err)
	}
}

func TestRejectsVersionJumpKeyChangeAndWrongPreviousDigest(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	keyB, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))

	changedKey := AccountState{
		AccountID: f.account,
		Version:   2,
		Devices: []Device{
			NewDevice("device-a", keyB, 1),
		},
		PreviousDigest: mustDigest(t, v1),
	}
	signedChanged := f.sign(t, changedKey)
	if _, err := client.Apply(signedChanged, nil); !errors.Is(err, ErrDeviceKeyChanged) {
		t.Fatalf("active key change: got %v", err)
	}

	jumped := AccountState{
		AccountID:      f.account,
		Version:        3,
		Devices:        []Device{NewDevice("device-a", keyA, 1), NewDevice("device-b", keyB, 3)},
		PreviousDigest: mustDigest(t, v1),
	}
	signedJumped := f.sign(t, jumped)
	if _, err := client.Apply(signedJumped, nil); !errors.Is(err, ErrVersionJump) {
		t.Fatalf("version jump: got %v", err)
	}

	wrongPrevious := AccountState{
		AccountID:      f.account,
		Version:        2,
		Devices:        []Device{NewDevice("device-a", keyA, 1)},
		PreviousDigest: mustDigest(t, changedKey),
	}
	signedWrongPrevious := f.sign(t, wrongPrevious)
	if _, err := client.Apply(signedWrongPrevious, nil); !errors.Is(err, ErrPreviousDigest) {
		t.Fatalf("wrong previous digest: got %v", err)
	}
	if client.Version() != 1 {
		t.Fatalf("invalid updates changed committed version to %d", client.Version())
	}
}

func TestRemoveThenReaddSameDeviceIDIsRejected(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	keyB, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))

	v2 := removeDevice(t, v1, "device-a", 2)
	signed2 := f.sign(t, v2)
	applyCommitted(t, client, signed2, log.publish(t, signed2))

	readd := AccountState{
		AccountID:      f.account,
		Version:        3,
		Devices:        []Device{NewDevice("device-a", keyB, 3)},
		PreviousDigest: mustDigest(t, v2),
	}
	signedReadd := f.sign(t, readd)
	if _, err := client.Apply(signedReadd, nil); !errors.Is(err, ErrDeviceIDReuse) {
		t.Fatalf("re-added device ID: got %v", err)
	}

	missingTombstone := AccountState{
		AccountID:      f.account,
		Version:        3,
		Devices:        nil,
		PreviousDigest: mustDigest(t, v2),
	}
	signedMissing := f.sign(t, missingTombstone)
	if _, err := client.Apply(signedMissing, nil); !errors.Is(err, ErrMissingDevice) {
		t.Fatalf("deleted tombstone: got %v", err)
	}
	if client.Version() != 2 {
		t.Fatalf("invalid re-add changed version to %d", client.Version())
	}
}

func TestLegalRotationRemovesOldDeviceAndAddsNewID(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyOld, _ := newTestKey(t)
	keyStable, _ := newTestKey(t)
	keyNew, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest,
		NewDevice("device-old", keyOld, 1),
		NewDevice("device-stable", keyStable, 1),
	)
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))
	oldSession, err := client.OpenSession("device-old")
	if err != nil {
		t.Fatal(err)
	}

	removedOld := Version(2)
	v2 := AccountState{
		AccountID: f.account,
		Version:   2,
		Devices: []Device{
			{DeviceID: "device-new", IdentityKey: keyNew, AddedVersion: 2},
			{DeviceID: "device-old", IdentityKey: keyOld, AddedVersion: 1, RemovedVersion: &removedOld},
			{DeviceID: "device-stable", IdentityKey: keyStable, AddedVersion: 1},
		},
		PreviousDigest: mustDigest(t, v1),
	}
	signed2 := f.sign(t, v2)
	applyCommitted(t, client, signed2, log.publish(t, signed2))

	if oldSession.CanSend() {
		t.Fatal("rotated-out device can still send")
	}
	if _, err := client.OpenSession("device-new"); err != nil {
		t.Fatalf("rotated-in device cannot open a session: %v", err)
	}
	if _, err := client.OpenSession("device-stable"); err != nil {
		t.Fatalf("stable device cannot open a session: %v", err)
	}
}

func TestCheckpointRollbackIsRejected(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	keyX, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))
	session, _ := client.OpenSession("device-a")

	v2 := addDevice(t, v1, NewDevice("device-b", keyX, 2), 2)
	signed2 := f.sign(t, v2)
	applyCommitted(t, client, signed2, log.publish(t, signed2))
	v3 := addDevice(t, v2, NewDevice("device-c", keyX, 3), 3)
	signed3 := f.sign(t, v3)
	applyCommitted(t, client, signed3, log.publish(t, signed3))

	v4 := addDevice(t, v3, NewDevice("device-d", keyX, 4), 4)
	signed4 := f.sign(t, v4)
	rolledBackLog := newFakeLog(f.logID)
	proof4 := rolledBackLog.publish(t, signed4)
	result, err := client.Apply(signed4, &proof4)
	if !errors.Is(err, ErrCheckpointRollback) {
		t.Fatalf("expected checkpoint rollback, got result=%+v err=%v", result, err)
	}
	if client.Version() != 3 {
		t.Fatalf("rollback changed committed version to %d", client.Version())
	}
	if client.CanReceiveFrom("device-d") {
		t.Fatal("device from rolled-back checkpoint was authorized")
	}
	if !session.CanSend() {
		t.Fatal("valid session was disturbed by a rejected update")
	}
}

func TestStaleReplayIsReportedWithoutChangingState(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	proof1 := log.publish(t, signed1)
	applyCommitted(t, client, signed1, proof1)

	result, err := client.Apply(signed1, &proof1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != UpdateStale {
		t.Fatalf("replay status = %s", result.Status)
	}
}

func TestSignedListRequiresSortedUniqueDevices(t *testing.T) {
	f := newFixture(t)
	keyA, _ := newTestKey(t)
	keyB, _ := newTestKey(t)
	unsorted := AccountState{
		AccountID: f.account,
		Version:   1,
		Devices: []Device{
			NewDevice("device-z", keyB, 1),
			NewDevice("device-a", keyA, 1),
		},
	}
	if _, err := SignDeviceList(f.masterPriv, unsorted); !errors.Is(err, ErrUnsortedDevices) {
		t.Fatalf("unsorted list signed: %v", err)
	}
	duplicate := AccountState{
		AccountID: f.account,
		Version:   1,
		Devices: []Device{
			NewDevice("device-a", keyA, 1),
			NewDevice("device-a", keyB, 1),
		},
	}
	if _, err := SignDeviceList(f.masterPriv, duplicate); !errors.Is(err, ErrDuplicateDeviceID) {
		t.Fatalf("duplicate list signed: %v", err)
	}
}
