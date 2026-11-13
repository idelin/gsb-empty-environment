package netsec

import (
	"bytes"
	"regexp"
	"testing"
)

func testKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func aliceDevices() []Device {
	return []Device{
		{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 1},
		{DeviceID: "alice-d2", IdentityPublicKey: testKey(0x31), AddedVersion: 1},
	}
}

func bobDevices() []Device {
	return []Device{
		{DeviceID: "bob-d1", IdentityPublicKey: testKey(0x51), AddedVersion: 1},
		{DeviceID: "bob-d2", IdentityPublicKey: testKey(0x71), AddedVersion: 1},
	}
}

func mustSnapshot(t *testing.T, accountID string, version uint64, devices []Device) *DeviceListSnapshot {
	t.Helper()
	snapshot, err := NewDeviceListSnapshot(accountID, version, devices)
	if err != nil {
		t.Fatalf("NewDeviceListSnapshot(%q): %v", accountID, err)
	}
	return snapshot
}

func TestSafetyNumberFormatIsSixtyDigitsInTwelveGroups(t *testing.T) {
	var zero [32]byte
	digits := DecimalCheckValue(zero)
	if len(digits) != SafetyNumberDigits {
		t.Fatalf("zero digest produced %d digits", len(digits))
	}
	if digits != "000000000000000000000000000000000000000000000000000000000000" {
		t.Fatalf("zero digest did not left-pad: %q", digits)
	}

	display := GroupSafetyNumberDigits(digits)
	groups := regexp.MustCompile(`^([0-9]{5} ){11}[0-9]{5}$`)
	if !groups.MatchString(display) {
		t.Fatalf("display is not 12 groups of five: %q", display)
	}
	if len(display) != SafetyNumberDigits+SafetyNumberGroups-1 {
		t.Fatalf("unexpected display length %d", len(display))
	}
}

func TestSafetyNumberBindsEveryDeviceAndIsOrderIndependent(t *testing.T) {
	alice := mustSnapshot(t, "alice", 1, aliceDevices())
	bob := mustSnapshot(t, "bob", 1, bobDevices())
	baseline, err := ComputeSafetyNumber(alice, bob)
	if err != nil {
		t.Fatal(err)
	}

	reorderedAlice := mustSnapshot(t, "alice", 1, []Device{
		{DeviceID: "alice-d2", IdentityPublicKey: testKey(0x31), AddedVersion: 1},
		{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 1},
	})
	reorderedNumber, err := ComputeSafetyNumber(reorderedAlice, bob)
	if err != nil {
		t.Fatal(err)
	}
	if *baseline != *reorderedNumber {
		t.Fatal("device ordering changed the safety number")
	}

	insertedDevice := append(aliceDevices(), Device{
		DeviceID:          "alice-malicious",
		IdentityPublicKey: testKey(0x91),
		AddedVersion:      2,
	})
	inserted := mustSnapshot(t, "alice", 2, insertedDevice)
	insertedNumber, err := ComputeSafetyNumber(inserted, bob)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Checksum == insertedNumber.Checksum {
		t.Fatal("a server-inserted device did not change the safety number")
	}
	if baseline.Display == insertedNumber.Display {
		t.Fatal("a server-inserted device did not change the display")
	}

	changedKeyDevices := aliceDevices()
	changedKeyDevices[0].IdentityPublicKey = testKey(0xA1)
	changedKey := mustSnapshot(t, "alice", 1, changedKeyDevices)
	changedKeyNumber, err := ComputeSafetyNumber(changedKey, bob)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Checksum == changedKeyNumber.Checksum {
		t.Fatal("a public-key change did not change the safety number")
	}
}

func TestSafetyNumberDirectionSwapIsSymmetric(t *testing.T) {
	alice := mustSnapshot(t, "alice", 1, aliceDevices())
	bob := mustSnapshot(t, "bob", 1, bobDevices())
	left, err := ComputeSafetyNumber(alice, bob)
	if err != nil {
		t.Fatal(err)
	}
	right, err := ComputeSafetyNumber(bob, alice)
	if err != nil {
		t.Fatal(err)
	}
	if left.Checksum != right.Checksum || left.Display != right.Display {
		t.Fatal("safety number depends on argument order")
	}
	if !bytes.Equal(left.Checksum[:], right.Checksum[:]) {
		t.Fatal("raw checksum depends on argument order")
	}
}

func TestQRPayloadContainsProtocolBindingsAndRawChecksum(t *testing.T) {
	alice := mustSnapshot(t, "alice", 1, aliceDevices())
	bob := mustSnapshot(t, "bob", 1, bobDevices())
	payload, err := NewQRPayload(alice, bob)
	if err != nil {
		t.Fatal(err)
	}
	if payload.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol version = %d", payload.ProtocolVersion)
	}
	if len(payload.Accounts) != 2 {
		t.Fatalf("payload has %d accounts", len(payload.Accounts))
	}

	encoded, err := payload.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeQRPayload(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Checksum != PairwiseChecksum(alice.AccountDigest, bob.AccountDigest) {
		t.Fatal("decoded payload lost the raw checksum")
	}
	for _, account := range decoded.Accounts {
		var snapshot *DeviceListSnapshot
		switch account.AccountID {
		case "alice":
			snapshot = alice
		case "bob":
			snapshot = bob
		default:
			t.Fatalf("unexpected account %q", account.AccountID)
		}
		if account.ListVersion != snapshot.Version ||
			account.ListDigest != snapshot.ListDigest ||
			account.AccountDigest != snapshot.AccountDigest {
			t.Fatalf("account %q bindings are incomplete", account.AccountID)
		}
	}
}
