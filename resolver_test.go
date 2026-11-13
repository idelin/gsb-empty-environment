package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUniqueResolutionAndCompletion(t *testing.T) {
	device := newTestDevice("device-a", 7)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	version, err := resolver.AddDevice(device.record())
	if err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0x21, 0x31})
	resolution, err := resolver.Resolve(address)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolution.IsUnique() {
		t.Fatalf("status = %s, want unique", resolution.Status)
	}
	ticket := resolution.Ticket
	if ticket.DeviceID() != device.id {
		t.Fatalf("ticket device = %s, want %s", ticket.DeviceID(), device.id)
	}
	if ticket.RecordGeneration() != device.generation {
		t.Fatalf("ticket generation = %d, want %d", ticket.RecordGeneration(), device.generation)
	}
	if ticket.Address() != address {
		t.Fatalf("ticket address = %s, want %s", ticket.Address(), address)
	}
	if ticket.TableVersion() != version {
		t.Fatalf("ticket table version = %d, want %d", ticket.TableVersion(), version)
	}
	if ticket.RequiresReauthentication() {
		t.Fatal("current-IRK ticket unexpectedly requires reauthentication")
	}

	nonce := bytes.Repeat([]byte{0x5a}, 32)
	const connectionID = uint64(0x0102030405060708)
	signature := signChallenge(t, device.privateKey, address, connectionID, nonce)
	identity, err := resolver.Complete(ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: connectionID,
	}, IdentityProof{Signature: signature})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if identity.DeviceID != device.id ||
		identity.Address != address ||
		identity.ConnectionID != connectionID ||
		identity.RecordGeneration != device.generation ||
		identity.TableVersion != version {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.Privilege != PrivilegeFull || !identity.HasFullPrivilege() {
		t.Fatalf("privilege = %s, reauth = %t", identity.Privilege, identity.ReauthenticationRequired)
	}
	if publisher.count() != 1 {
		t.Fatalf("published identities = %d, want 1", publisher.count())
	}
}

func TestCollisionRejectsResolutionAndReportsAllDevices(t *testing.T) {
	deviceA := newTestDevice("device-a", 1)
	deviceB := newTestDevice("device-b", 1)
	resolver, err := NewResolver(Options{AES: collisionAES})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	for _, device := range []testDevice{deviceA, deviceB} {
		if _, err := resolver.AddDevice(device.record()); err != nil {
			t.Fatalf("AddDevice(%s): %v", device.id, err)
		}
	}

	address := addressFor(t, collisionAES, deviceA.irk, [3]byte{0x40, 0x71, 0x81})
	resolution, err := resolver.Resolve(address)
	if !errors.Is(err, ErrAddressConflict) {
		t.Fatalf("Resolve error = %v, want ErrAddressConflict", err)
	}
	if resolution.Status != ResolutionConflict {
		t.Fatalf("status = %s, want conflict", resolution.Status)
	}
	if !resolution.Ticket.IsZero() {
		t.Fatalf("conflict issued a ticket: %s", resolution.Ticket)
	}
	if len(resolution.Conflicts) != 2 ||
		resolution.Conflicts[0] != deviceA.id ||
		resolution.Conflicts[1] != deviceB.id {
		t.Fatalf("conflicts = %v, want [%s %s]", resolution.Conflicts, deviceA.id, deviceB.id)
	}

	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if conflict.Address != address || len(conflict.DeviceIDs) != 2 {
		t.Fatalf("conflict detail = %+v", conflict)
	}
}

func TestResolutionPinsOneTableSnapshotAcrossUpdate(t *testing.T) {
	deviceA := newTestDevice("device-a", 1)
	deviceB := newTestDevice("device-b", 1)
	address := addressFor(t, testAES, deviceA.irk, [3]byte{0x40, 0x91, 0xa1})

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	releaseGate := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseGate()

	gatedAES := func(key IRK, block [16]byte) ([16]byte, error) {
		startOnce.Do(func() {
			close(started)
			<-release
		})
		return testAES(key, block)
	}

	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             gatedAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(deviceA.record()); err != nil {
		t.Fatalf("AddDevice(A): %v", err)
	}

	type result struct {
		resolution Resolution
		err        error
	}
	resolved := make(chan result, 1)
	go func() {
		resolution, err := resolver.Resolve(address)
		resolved <- result{resolution: resolution, err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("resolution did not enter the AES callback")
	}

	versionAfterUpdate, err := resolver.AddDevice(deviceB.record())
	if err != nil {
		t.Fatalf("AddDevice(B): %v", err)
	}
	if versionAfterUpdate != 2 {
		t.Fatalf("table version = %d, want 2", versionAfterUpdate)
	}
	releaseGate()

	var first result
	select {
	case first = <-resolved:
	case <-time.After(5 * time.Second):
		t.Fatal("resolution did not finish")
	}
	if first.err != nil {
		t.Fatalf("Resolve: %v", first.err)
	}
	if !first.resolution.IsUnique() {
		t.Fatalf("status = %s, want unique", first.resolution.Status)
	}
	if first.resolution.Ticket.TableVersion() != 1 {
		t.Fatalf("ticket table version = %d, want pinned version 1", first.resolution.Ticket.TableVersion())
	}

	nonce := bytes.Repeat([]byte{0x42}, 32)
	signature := signChallenge(t, deviceA.privateKey, address, 9, nonce)
	if _, err := resolver.Complete(first.resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 9,
	}, IdentityProof{Signature: signature}); !errors.Is(err, ErrStaleTable) {
		t.Fatalf("Complete old snapshot error = %v, want ErrStaleTable", err)
	}

	fresh, err := resolver.Resolve(address)
	if err != nil {
		t.Fatalf("Resolve after update: %v", err)
	}
	if !fresh.IsUnique() || fresh.Ticket.TableVersion() != versionAfterUpdate {
		t.Fatalf("fresh resolution = %+v", fresh)
	}
	identity, err := resolver.Complete(fresh.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 10,
	}, IdentityProof{Signature: signChallenge(t, deviceA.privateKey, address, 10, nonce)})
	if err != nil {
		t.Fatalf("Complete fresh ticket: %v", err)
	}
	if identity.TableVersion != versionAfterUpdate {
		t.Fatalf("identity table version = %d, want %d", identity.TableVersion, versionAfterUpdate)
	}
	if publisher.count() != 1 {
		t.Fatalf("published identities = %d, want 1", publisher.count())
	}
}

func TestRevocationInvalidatesTicketAndStopsResolution(t *testing.T) {
	device := newTestDevice("device-a", 3)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0xb1, 0xc1})
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	version, err := resolver.RevokeDevice(device.id)
	if err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if version != 2 {
		t.Fatalf("table version = %d, want 2", version)
	}
	record, ok := resolver.Record(device.id)
	if !ok || record.State != RecordRevoked || record.Generation != device.generation+1 {
		t.Fatalf("revoked record = %+v, ok = %t", record, ok)
	}

	nonce := bytes.Repeat([]byte{0x33}, 32)
	signature := signChallenge(t, device.privateKey, address, 1, nonce)
	if _, err := resolver.Complete(resolution.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 1,
	}, IdentityProof{Signature: signature}); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("Complete error = %v, want ErrDeviceRevoked", err)
	}

	anonymous, err := resolver.Resolve(address)
	if err != nil {
		t.Fatalf("Resolve after revocation: %v", err)
	}
	if !anonymous.IsAnonymous() || !anonymous.Ticket.IsZero() {
		t.Fatalf("post-revocation resolution = %+v, want anonymous", anonymous)
	}
	if publisher.count() != 0 {
		t.Fatalf("published identities = %d, want 0", publisher.count())
	}
}

func TestRotationLegacyTicketIsRestricted(t *testing.T) {
	clock := newFakeClock()
	device := newTestDevice("device-a", 5)
	publisher := &identityCapture{}
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
		PublishIdentity: publisher.publish,
		OldIRKRetention: time.Hour,
		Now:             clock.Now,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	prand := [3]byte{0x40, 0xd1, 0xe1}
	oldAddress := addressFor(t, testAES, device.irk, prand)
	beforeRotation, err := resolver.Resolve(oldAddress)
	if err != nil || !beforeRotation.IsUnique() {
		t.Fatalf("Resolve before rotation = %+v, %v", beforeRotation, err)
	}

	var newIRK IRK
	copy(newIRK[:], bytes.Repeat([]byte{0x7e}, len(newIRK)))
	version, generation, err := resolver.RotateIRK(device.id, newIRK)
	if err != nil {
		t.Fatalf("RotateIRK: %v", err)
	}
	if version != 2 || generation != device.generation+1 {
		t.Fatalf("rotation = version %d generation %d", version, generation)
	}
	record, _ := resolver.Record(device.id)
	if record.IRK != newIRK || record.Generation != generation || record.State != RecordActive {
		t.Fatalf("rotated record = %+v", record)
	}

	nonce := bytes.Repeat([]byte{0x6c}, 32)
	if _, err := resolver.Complete(beforeRotation.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 1,
	}, IdentityProof{Signature: signChallenge(t, device.privateKey, oldAddress, 1, nonce)}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Complete pre-rotation ticket error = %v, want ErrStaleGeneration", err)
	}

	newAddress := addressFor(t, testAES, newIRK, prand)
	current, err := resolver.Resolve(newAddress)
	if err != nil || !current.IsUnique() || current.Ticket.RequiresReauthentication() {
		t.Fatalf("current resolution = %+v, %v", current, err)
	}
	legacy, err := resolver.Resolve(oldAddress)
	if err != nil {
		t.Fatalf("Resolve old address: %v", err)
	}
	if !legacy.IsUnique() {
		t.Fatalf("legacy status = %s, want unique", legacy.Status)
	}
	if !legacy.Ticket.RequiresReauthentication() {
		t.Fatal("old-IRK ticket did not require reauthentication")
	}
	if legacy.Ticket.RecordGeneration() != generation || legacy.Ticket.TableVersion() != version {
		t.Fatalf("legacy ticket = %s", legacy.Ticket)
	}

	legacySignature := signChallenge(t, device.privateKey, oldAddress, 2, nonce)
	legacyIdentity, err := resolver.Complete(legacy.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 2,
	}, IdentityProof{Signature: legacySignature})
	if err != nil {
		t.Fatalf("Complete legacy ticket: %v", err)
	}
	if legacyIdentity.Privilege != PrivilegeDiscovery ||
		!legacyIdentity.ReauthenticationRequired ||
		legacyIdentity.HasFullPrivilege() {
		t.Fatalf("legacy identity = %+v", legacyIdentity)
	}

	currentSignature := signChallenge(t, device.privateKey, newAddress, 3, nonce)
	currentIdentity, err := resolver.Complete(current.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 3,
	}, IdentityProof{Signature: currentSignature})
	if err != nil {
		t.Fatalf("Complete current ticket: %v", err)
	}
	if currentIdentity.Privilege != PrivilegeFull || !currentIdentity.HasFullPrivilege() {
		t.Fatalf("current identity = %+v", currentIdentity)
	}

	inFlightLegacy, err := resolver.Resolve(oldAddress)
	if err != nil || !inFlightLegacy.IsUnique() {
		t.Fatalf("second legacy resolution = %+v, %v", inFlightLegacy, err)
	}
	clock.Advance(2 * time.Hour)
	if _, err := resolver.Complete(inFlightLegacy.Ticket, Challenge{
		Nonce:        nonce,
		ConnectionID: 4,
	}, IdentityProof{Signature: signChallenge(t, device.privateKey, oldAddress, 4, nonce)}); !errors.Is(err, ErrLegacyIRKExpired) {
		t.Fatalf("Complete expired legacy ticket error = %v, want ErrLegacyIRKExpired", err)
	}
	expired, err := resolver.Resolve(oldAddress)
	if err != nil {
		t.Fatalf("Resolve expired old address: %v", err)
	}
	if !expired.IsAnonymous() {
		t.Fatalf("expired old address status = %s, want anonymous", expired.Status)
	}

	if publisher.count() != 2 {
		t.Fatalf("published identities = %d, want 2", publisher.count())
	}
}

func TestOldIRKIsNotRetainedWithoutPolicy(t *testing.T) {
	device := newTestDevice("device-a", 1)
	resolver, err := NewResolver(Options{AES: testAES})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	prand := [3]byte{0x40, 0x12, 0x34}
	oldAddress := addressFor(t, testAES, device.irk, prand)

	var newIRK IRK
	copy(newIRK[:], bytes.Repeat([]byte{0x31}, len(newIRK)))
	if _, _, err := resolver.RotateIRK(device.id, newIRK); err != nil {
		t.Fatalf("RotateIRK: %v", err)
	}
	resolution, err := resolver.Resolve(oldAddress)
	if err != nil {
		t.Fatalf("Resolve old address: %v", err)
	}
	if !resolution.IsAnonymous() {
		t.Fatalf("old address status = %s, want anonymous", resolution.Status)
	}
}

func TestTicketSealRejectsTampering(t *testing.T) {
	device := newTestDevice("device-a", 1)
	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: testSignatureVerifier,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := resolver.AddDevice(device.record()); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	address := addressFor(t, testAES, device.irk, [3]byte{0x40, 0x56, 0x67})
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	tampered := resolution.Ticket
	tampered.tableVersion++
	nonce := bytes.Repeat([]byte{0x11}, 32)
	if _, err := resolver.Complete(tampered, Challenge{Nonce: nonce}, IdentityProof{Signature: []byte{1}}); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("tampered ticket error = %v, want ErrTicketInvalid", err)
	}
	if _, err := resolver.Complete(Ticket{}, Challenge{Nonce: nonce}, IdentityProof{Signature: []byte{1}}); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("zero ticket error = %v, want ErrTicketInvalid", err)
	}
}

func TestTableMutationCannotRaceCompletion(t *testing.T) {
	deviceA := newTestDevice("device-a", 1)
	deviceB := newTestDevice("device-b", 1)
	address := addressFor(t, testAES, deviceA.irk, [3]byte{0x40, 0x13, 0x24})

	enteredVerifier := make(chan struct{})
	releaseVerifier := make(chan struct{})
	var once sync.Once
	blockingVerifier := func(publicKey, message, signature []byte) error {
		once.Do(func() {
			close(enteredVerifier)
			<-releaseVerifier
		})
		return testSignatureVerifier(publicKey, message, signature)
	}

	resolver, err := NewResolver(Options{
		AES:             testAES,
		VerifySignature: blockingVerifier,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	version, err := resolver.AddDevice(deviceA.record())
	if err != nil {
		t.Fatalf("AddDevice(A): %v", err)
	}
	resolution, err := resolver.Resolve(address)
	if err != nil || !resolution.IsUnique() {
		t.Fatalf("Resolve = %+v, %v", resolution, err)
	}

	nonce := bytes.Repeat([]byte{0x8a}, 32)
	signature := signChallenge(t, deviceA.privateKey, address, 21, nonce)
	type completion struct {
		identity *Identity
		err      error
	}
	completed := make(chan completion, 1)
	go func() {
		identity, err := resolver.Complete(resolution.Ticket, Challenge{
			Nonce:        nonce,
			ConnectionID: 21,
		}, IdentityProof{Signature: signature})
		completed <- completion{identity: identity, err: err}
	}()

	select {
	case <-enteredVerifier:
	case <-time.After(5 * time.Second):
		t.Fatal("Complete did not reach the signature verifier")
	}

	mutationDone := make(chan error, 1)
	go func() {
		_, err := resolver.AddDevice(deviceB.record())
		mutationDone <- err
	}()
	select {
	case err := <-mutationDone:
		t.Fatalf("table mutation completed during Complete critical section: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := resolver.TableVersion(); got != version {
		t.Fatalf("table version changed during Complete: %d, want %d", got, version)
	}

	close(releaseVerifier)
	select {
	case result := <-completed:
		if result.err != nil {
			t.Fatalf("Complete: %v", result.err)
		}
		if result.identity == nil || result.identity.TableVersion != version {
			t.Fatalf("identity = %+v", result.identity)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Complete did not finish")
	}
	select {
	case err := <-mutationDone:
		if err != nil {
			t.Fatalf("AddDevice after Complete: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("table mutation did not proceed after Complete")
	}
	if got := resolver.TableVersion(); got != version+1 {
		t.Fatalf("table version after mutation = %d, want %d", got, version+1)
	}
}
