package netsec

import (
	"errors"
	"testing"
)

func newTestPair(t *testing.T, aliceSnapshot, bobSnapshot *DeviceListSnapshot) (*Client, *Client, *Session) {
	t.Helper()
	alice := NewClient("alice")
	bob := NewClient("bob")
	for _, client := range []*Client{alice, bob} {
		if err := client.ImportSnapshots(aliceSnapshot, bobSnapshot); err != nil {
			t.Fatalf("import snapshots: %v", err)
		}
	}
	session, err := NewSession(alice, bob)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return alice, bob, session
}

func TestTwoClientsWithCompleteSetsShowVerifiedInBothDirections(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, session := newTestPair(t, aliceSnapshot, bobSnapshot)

	aliceNumber, err := alice.SafetyNumber(bob)
	if err != nil {
		t.Fatal(err)
	}
	bobNumber, err := bob.SafetyNumber(alice)
	if err != nil {
		t.Fatal(err)
	}
	if aliceNumber.Checksum != bobNumber.Checksum || aliceNumber.Display != bobNumber.Display {
		t.Fatal("clients with identical complete sets displayed different numbers")
	}
	if !session.ViewsConsistent() {
		t.Fatal("complete device sets were not recognized as consistent")
	}
	if status := session.Status(); status != StatusUnverified {
		t.Fatalf("session status before verification = %q", status)
	}

	aliceQR, err := alice.GenerateQRCode("bob")
	if err != nil {
		t.Fatal(err)
	}
	bobQR, err := bob.GenerateQRCode("alice")
	if err != nil {
		t.Fatal(err)
	}
	if string(aliceQR) != string(bobQR) {
		t.Fatal("canonical QR payload depends on which client generated it")
	}

	if result, err := bob.VerifyQR("alice", aliceQR); err != nil || result.Status != StatusVerified {
		t.Fatalf("bob -> alice QR verification = %v, %v", result, err)
	}
	if result, err := alice.VerifyQR("bob", bobQR); err != nil || result.Status != StatusVerified {
		t.Fatalf("alice -> bob QR verification = %v, %v", result, err)
	}
	if result, err := session.VerifyQR(aliceQR); err != nil || result.Status != StatusVerified {
		t.Fatalf("session verification = %v, %v", result, err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("session status = %q, want Verified", status)
	}

	swapped := mustDecodePayload(t, aliceQR).WithSwappedAccounts()
	if result, err := bob.VerifyQRPayload(swapped, "alice"); err != nil || result.Status != StatusVerified {
		t.Fatalf("swapped-direction verification = %v, %v", result, err)
	}
}

func mustDecodePayload(t *testing.T, encoded []byte) *QRPayload {
	t.Helper()
	payload, err := DecodeQRPayload(encoded)
	if err != nil {
		t.Fatalf("DecodeQRPayload: %v", err)
	}
	return payload
}

func TestServerInsertedDeviceCannotShowVerified(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, session := newTestPair(t, aliceSnapshot, bobSnapshot)

	if _, err := session.VerifyQR(mustEncode(t, mustPayload(t, alice, bob))); err != nil {
		t.Fatal(err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("baseline status = %q", status)
	}

	insertedDevices := append(aliceDevices(), Device{
		DeviceID:          "alice-server-added",
		IdentityPublicKey: testKey(0xB1),
		AddedVersion:      2,
	})
	inserted := mustSnapshot(t, "alice", 2, insertedDevices)
	if err := alice.ImportSnapshot(inserted); err != nil {
		t.Fatal(err)
	}

	aliceNumber, err := alice.SafetyNumber(bob)
	if err != nil {
		t.Fatal(err)
	}
	bobNumber, err := bob.SafetyNumber(alice)
	if err != nil {
		t.Fatal(err)
	}
	if aliceNumber.Checksum == bobNumber.Checksum {
		t.Fatal("inserted device did not make the two client views disagree")
	}
	if result, err := bob.VerifyQRFrom(alice); err == nil || result.Status == StatusVerified {
		t.Fatalf("old client verified a newer malicious list: %v, %v", result.Status, err)
	}
	if result, err := alice.VerifyQRFrom(bob); err == nil || result.Status == StatusVerified {
		t.Fatalf("new client silently reused an old verification: %v, %v", result.Status, err)
	}
	if status := session.Status(); status == StatusVerified {
		t.Fatal("session still displayed Verified after a server insertion")
	}
	if status := alice.Status("bob"); status != StatusStale {
		t.Fatalf("alice status after newer list = %q, want Stale", status)
	}
}

func TestHiddenDeviceCannotShowVerified(t *testing.T) {
	fullAlice := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	hiddenAlice := mustSnapshot(t, "alice", 1, []Device{aliceDevices()[0]})

	alice := NewClient("alice")
	bob := NewClient("bob")
	if err := alice.ImportSnapshots(fullAlice, bobSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := bob.ImportSnapshots(hiddenAlice, bobSnapshot); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(alice, bob)
	if err != nil {
		t.Fatal(err)
	}

	left, err := alice.SafetyNumber(bob)
	if err != nil {
		t.Fatal(err)
	}
	right, err := bob.SafetyNumber(alice)
	if err != nil {
		t.Fatal(err)
	}
	if left.Checksum == right.Checksum {
		t.Fatal("hidden device did not change one client's safety number")
	}
	if result, err := bob.VerifyQRFrom(alice); err == nil || result.Status == StatusVerified {
		t.Fatalf("hidden device verified: %v, %v", result.Status, err)
	}
	if status := session.Status(); status == StatusVerified {
		t.Fatal("hidden device still displayed Verified")
	}
}

func TestOnlyDeviceOrderChangeStillVerifies(t *testing.T) {
	alice := mustSnapshot(t, "alice", 1, []Device{
		{DeviceID: "alice-d2", IdentityPublicKey: testKey(0x31), AddedVersion: 1},
		{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 1},
	})
	bob := mustSnapshot(t, "bob", 1, []Device{
		{DeviceID: "bob-d2", IdentityPublicKey: testKey(0x71), AddedVersion: 1},
		{DeviceID: "bob-d1", IdentityPublicKey: testKey(0x51), AddedVersion: 1},
	})
	clientA, clientB, session := newTestPair(t, alice, bob)
	if _, err := session.VerifyQR(mustEncode(t, mustPayload(t, clientA, clientB))); err != nil {
		t.Fatal(err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("reordered-but-identical sets status = %q", status)
	}
}

func TestDeleteAndReaddSameDeviceIDWithNewKeyCannotShowVerified(t *testing.T) {
	oldAlice := mustSnapshot(t, "alice", 2, aliceDevices())
	bob := mustSnapshot(t, "bob", 2, bobDevices())
	alice, bobClient, session := newTestPair(t, oldAlice, bob)
	if _, err := session.VerifyQR(mustEncode(t, mustPayload(t, alice, bobClient))); err != nil {
		t.Fatal(err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("baseline status = %q", status)
	}

	readded := []Device{
		{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 2},
		{DeviceID: "alice-d2", IdentityPublicKey: testKey(0xC1), AddedVersion: 3},
	}
	newAlice := mustSnapshot(t, "alice", 3, readded)
	if err := alice.ImportSnapshot(newAlice); err != nil {
		t.Fatal(err)
	}
	if _, err := bobClient.VerifyQRFrom(alice); err == nil || bobClient.Status("alice") == StatusVerified {
		t.Fatal("re-added device ID with a new key was not rejected")
	}
	if status := session.Status(); status == StatusVerified {
		t.Fatal("session silently retained Verified across delete/re-add")
	}
}

func TestQRAccountSetAndChecksumTamperingIsRejected(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, _ := newTestPair(t, aliceSnapshot, bobSnapshot)
	payload := mustPayload(t, alice, bob)

	badListDigest := *payload
	badListDigest.Accounts = append([]QRParty(nil), payload.Accounts...)
	badListDigest.Accounts[0].ListDigest[0] ^= 0x01
	if result, err := bob.VerifyQRPayload(&badListDigest, "alice"); err == nil || result.Status == StatusVerified {
		t.Fatalf("tampered list digest verified: %v, %v", result.Status, err)
	}

	badAccount := *payload
	badAccount.Accounts = append([]QRParty(nil), payload.Accounts...)
	for i := range badAccount.Accounts {
		if badAccount.Accounts[i].AccountID == "bob" {
			badAccount.Accounts[i].AccountID = "mallory"
		}
	}
	if result, err := bob.VerifyQRPayload(&badAccount, "alice"); err == nil || result.Status == StatusVerified {
		t.Fatalf("altered account set verified: %v, %v", result.Status, err)
	}

	badChecksum := *payload
	badChecksum.Checksum[0] ^= 0x01
	if _, err := badChecksum.Encode(); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("tampered checksum encode error = %v", err)
	}
}

func TestNewerListMakesOldVerificationStale(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, session := newTestPair(t, aliceSnapshot, bobSnapshot)
	oldPayload := mustPayload(t, alice, bob)
	oldNumber, err := alice.SafetyNumber(bob)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := session.VerifyQR(mustEncode(t, oldPayload)); err != nil || result.Status != StatusVerified {
		t.Fatalf("initial verification = %v, %v", result, err)
	}

	newDevices := append(aliceDevices(), Device{
		DeviceID:          "alice-d3",
		IdentityPublicKey: testKey(0xD1),
		AddedVersion:      2,
	})
	newAlice := mustSnapshot(t, "alice", 2, newDevices)
	if err := alice.ImportSnapshot(newAlice); err != nil {
		t.Fatal(err)
	}
	if err := bob.ImportSnapshot(newAlice); err != nil {
		t.Fatal(err)
	}
	newNumber, err := alice.SafetyNumber(bob)
	if err != nil {
		t.Fatal(err)
	}
	if oldNumber.Checksum == newNumber.Checksum || oldNumber.Display == newNumber.Display {
		t.Fatal("inserting a device changed neither the raw checksum nor the display")
	}

	if status := session.Status(); status != StatusStale {
		t.Fatalf("session status after newer list = %q, want Stale", status)
	}
	if status := alice.Status("bob"); status != StatusStale {
		t.Fatalf("alice status after newer list = %q, want Stale", status)
	}
	if status := bob.Status("alice"); status != StatusStale {
		t.Fatalf("bob status after newer list = %q, want Stale", status)
	}
	if result, err := bob.VerifyQRPayload(oldPayload, "alice"); err == nil || result.Status == StatusVerified {
		t.Fatalf("old QR payload remained Verified after newer list: %v, %v", result.Status, err)
	}

	freshPayload := mustPayload(t, alice, bob)
	if result, err := session.VerifyQR(mustEncode(t, freshPayload)); err != nil || result.Status != StatusVerified {
		t.Fatalf("fresh verification = %v, %v", result, err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("fresh session status = %q", status)
	}
}

func mustPayload(t *testing.T, a, b *Client) *QRPayload {
	t.Helper()
	payload, err := a.GenerateQRPayload(b.AccountID())
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustEncode(t *testing.T, payload *QRPayload) []byte {
	t.Helper()
	encoded, err := payload.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
