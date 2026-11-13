package netsec

import (
	"errors"
	"testing"
	"time"
)

func TestConfirmationBindsListsMethodAndTime(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, session := newTestPair(t, aliceSnapshot, bobSnapshot)
	confirmedAt := time.Date(2026, 11, 14, 3, 4, 5, 0, time.FixedZone("CST", 8*3600))

	record, err := session.Confirm(MethodManual, confirmedAt)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccountA != "alice" || record.AccountB != "bob" {
		t.Fatalf("confirmation accounts = %q, %q", record.AccountA, record.AccountB)
	}
	if record.ListDigestA != aliceSnapshot.ListDigest || record.ListDigestB != bobSnapshot.ListDigest {
		t.Fatal("confirmation did not bind both list digests")
	}
	if record.ListVersionA != 1 || record.ListVersionB != 1 {
		t.Fatal("confirmation did not bind both list versions")
	}
	if record.Method != MethodManual {
		t.Fatalf("confirmation method = %q", record.Method)
	}
	if !record.ConfirmedAt.Equal(confirmedAt.UTC()) || record.ConfirmedAt.Location() != time.UTC {
		t.Fatalf("confirmation time = %v, want %v", record.ConfirmedAt, confirmedAt.UTC())
	}
	if record.Checksum != PairwiseChecksum(aliceSnapshot.AccountDigest, bobSnapshot.AccountDigest) {
		t.Fatal("confirmation did not bind the session checksum")
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("confirmed session status = %q", status)
	}

	storedA, ok := alice.ConfirmationFor("bob")
	if !ok || *storedA != *record {
		t.Fatal("alice did not store the exact confirmation record")
	}
	storedB, ok := bob.ConfirmationFor("alice")
	if !ok || *storedB != *record {
		t.Fatal("bob did not store the exact confirmation record")
	}
}

func TestConfirmationCannotBeCreatedAcrossDifferentViews(t *testing.T) {
	fullAlice := mustSnapshot(t, "alice", 1, aliceDevices())
	hiddenAlice := mustSnapshot(t, "alice", 1, []Device{aliceDevices()[0]})
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())

	alice := NewClient("alice")
	bob := NewClient("bob")
	if err := alice.ImportSnapshots(fullAlice, bobSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := bob.ImportSnapshots(hiddenAlice, bobSnapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.ConfirmPair(bob, MethodManual, time.Now()); !errors.Is(err, ErrViewsMismatch) {
		t.Fatalf("ConfirmPair error = %v, want ErrViewsMismatch", err)
	}
}

func TestDeviceChangesRevokeConfirmation(t *testing.T) {
	cases := []struct {
		name       string
		newAlice   func(t *testing.T) *DeviceListSnapshot
		wantRevoke bool
	}{
		{
			name: "added",
			newAlice: func(t *testing.T) *DeviceListSnapshot {
				return mustSnapshot(t, "alice", 2, append(aliceDevices(), Device{
					DeviceID:          "alice-added",
					IdentityPublicKey: testKey(0xE1),
					AddedVersion:      2,
				}))
			},
			wantRevoke: true,
		},
		{
			name: "removed",
			newAlice: func(t *testing.T) *DeviceListSnapshot {
				return mustSnapshot(t, "alice", 2, []Device{aliceDevices()[0]})
			},
			wantRevoke: true,
		},
		{
			name: "public-key-changed",
			newAlice: func(t *testing.T) *DeviceListSnapshot {
				devices := aliceDevices()
				devices[1].IdentityPublicKey = testKey(0xF1)
				return mustSnapshot(t, "alice", 1, devices)
			},
			wantRevoke: true,
		},
		{
			name: "readded-same-id-new-added-version",
			newAlice: func(t *testing.T) *DeviceListSnapshot {
				devices := aliceDevices()
				devices[1].AddedVersion = 7
				return mustSnapshot(t, "alice", 2, devices)
			},
			wantRevoke: true,
		},
		{
			name: "only-reordered",
			newAlice: func(t *testing.T) *DeviceListSnapshot {
				return mustSnapshot(t, "alice", 1, []Device{
					{DeviceID: "alice-d2", IdentityPublicKey: testKey(0x31), AddedVersion: 1},
					{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 1},
				})
			},
			wantRevoke: false,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
			bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
			alice, _, session := newTestPair(t, aliceSnapshot, bobSnapshot)
			if _, err := session.Confirm(MethodOffline, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := alice.ImportSnapshot(test.newAlice(t)); err != nil {
				t.Fatal(err)
			}

			record, ok := alice.ConfirmationFor("bob")
			if !ok {
				t.Fatal("confirmation record disappeared")
			}
			if record.Revoked != test.wantRevoke {
				t.Fatalf("revoked = %v, want %v (reason %q)", record.Revoked, test.wantRevoke, record.RevocationReason)
			}
			if test.wantRevoke && record.RevokedAt.IsZero() {
				t.Fatal("revocation time was not recorded")
			}
			sessionRecord, ok := session.Confirmation()
			if !ok || sessionRecord.Revoked != test.wantRevoke {
				t.Fatalf("session confirmation revoked = %v, want %v", ok && sessionRecord.Revoked, test.wantRevoke)
			}
			if test.wantRevoke && alice.Status("bob") != StatusStale {
				t.Fatalf("revoked confirmation status = %q, want Stale", alice.Status("bob"))
			}
			if !test.wantRevoke {
				if status := session.Status(); status != StatusVerified {
					t.Fatalf("reorder-only status = %q, want Verified", status)
				}
				if status := alice.Status("bob"); status != StatusVerified {
					t.Fatalf("reorder-only client status = %q, want Verified", status)
				}
			}
		})
	}
}

func TestPresenceChangesDoNotRevokeConfirmation(t *testing.T) {
	aliceSnapshot := mustSnapshot(t, "alice", 1, aliceDevices())
	bobSnapshot := mustSnapshot(t, "bob", 1, bobDevices())
	alice, bob, session := newTestPair(t, aliceSnapshot, bobSnapshot)
	if _, err := session.Confirm(MethodQRCode, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := alice.SetDeviceOnline("alice", "alice-d1", true); err != nil {
		t.Fatal(err)
	}
	if err := bob.SetDeviceOnline("bob", "bob-d2", true); err != nil {
		t.Fatal(err)
	}
	if status := session.Status(); status != StatusVerified {
		t.Fatalf("presence-only changes made status %q, want Verified", status)
	}
	record, ok := alice.ConfirmationFor("bob")
	if !ok || record.Revoked {
		t.Fatal("presence-only changes revoked confirmation")
	}

	// Importing a snapshot whose only difference is the presence bit must
	// also leave both the confirmation and the safety number intact.
	withPresence := mustSnapshot(t, "alice", 1, []Device{
		{DeviceID: "alice-d1", IdentityPublicKey: testKey(0x11), AddedVersion: 1, Online: true},
		{DeviceID: "alice-d2", IdentityPublicKey: testKey(0x31), AddedVersion: 1},
	})
	if err := alice.ImportSnapshot(withPresence); err != nil {
		t.Fatal(err)
	}
	if record, _ := alice.ConfirmationFor("bob"); record.Revoked {
		t.Fatal("presence-only snapshot import revoked confirmation")
	}
	if status := alice.Status("bob"); status != StatusVerified {
		t.Fatalf("presence-only import status = %q", status)
	}
}
