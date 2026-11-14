package netsec_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	netsec "example.org/netsec/q0497"
)

func testDevice(t *testing.T, id netsec.DeviceID) *netsec.DeviceSecrets {
	t.Helper()
	dev, err := netsec.GenerateDevice(id)
	if err != nil {
		t.Fatalf("GenerateDevice(%q): %v", id, err)
	}
	return dev
}

func testAccount(t *testing.T, svc *netsec.Service, owner netsec.NewDevice) netsec.AccountID {
	t.Helper()
	accountID, err := svc.CreateAccount(owner)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return accountID
}

func testApprove(t *testing.T, svc *netsec.Service, accountID netsec.AccountID, approver netsec.DeviceID, enrollmentID netsec.EnrollmentID, device netsec.NewDevice, accountKey []byte, scopes ...netsec.EntryID) {
	t.Helper()
	binding := netsec.DeviceKeyBinding(accountID, device.ID, enrollmentID)
	wrapped, err := netsec.WrapAccountKey(accountKey, device.KeyAgreementPublicKey, binding)
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}
	if err := svc.ApproveEnrollment(accountID, approver, enrollmentID, wrapped, scopes); err != nil {
		t.Fatalf("ApproveEnrollment: %v", err)
	}
}

func TestLegitimateJoinScopedSyncAndKeyUnwrap(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner-phone")
	accountID := testAccount(t, svc, owner.Public())

	if err := svc.PutEntry(accountID, owner.ID, "entry-mail", []byte("sealed-mail"), []byte("v1")); err != nil {
		t.Fatalf("PutEntry mail: %v", err)
	}
	if err := svc.PutEntry(accountID, owner.ID, "entry-bank", []byte("sealed-bank"), nil); err != nil {
		t.Fatalf("PutEntry bank: %v", err)
	}

	joining := testDevice(t, "joining-laptop")
	enrollment, err := svc.BeginEnrollment(accountID, joining.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	if enrollment.Status != netsec.EnrollmentPending {
		t.Fatalf("new enrollment status = %s, want pending", enrollment.Status)
	}
	device, err := svc.Device(accountID, joining.ID)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if device.Status != netsec.DevicePending {
		t.Fatalf("device status = %s, want pending", device.Status)
	}

	if entries, err := svc.Sync(accountID, joining.ID, []netsec.EntryID{"entry-mail"}); !errors.Is(err, netsec.ErrDeviceNotApproved) || entries != nil {
		t.Fatalf("pending Sync = (%v, %v), want (nil, ErrDeviceNotApproved)", entries, err)
	}
	if key, err := svc.WrappedAccountKey(accountID, joining.ID); !errors.Is(err, netsec.ErrDeviceNotApproved) || key != nil {
		t.Fatalf("pending WrappedAccountKey = (%x, %v), want (nil, ErrDeviceNotApproved)", key, err)
	}

	accountKey := randomKey(t)
	testApprove(t, svc, accountID, owner.ID, enrollment.ID, joining.Public(), accountKey, "entry-mail")

	device, err = svc.Device(accountID, joining.ID)
	if err != nil {
		t.Fatalf("Device after approval: %v", err)
	}
	if device.Status != netsec.DeviceApproved {
		t.Fatalf("approved device status = %s", device.Status)
	}
	if len(device.Scopes) != 1 || device.Scopes[0] != "entry-mail" {
		t.Fatalf("device scopes = %v, want [entry-mail]", device.Scopes)
	}

	envelope, err := svc.WrappedAccountKey(accountID, joining.ID)
	if err != nil {
		t.Fatalf("WrappedAccountKey: %v", err)
	}
	unwrapped, err := netsec.UnwrapAccountKey(envelope, joining.KeyAgreementPrivateKey.Bytes(), netsec.DeviceKeyBinding(accountID, joining.ID, enrollment.ID))
	if err != nil {
		t.Fatalf("UnwrapAccountKey: %v", err)
	}
	if !bytes.Equal(unwrapped, accountKey) {
		t.Fatalf("unwrapped account key does not match")
	}

	entries, err := svc.Sync(accountID, joining.ID, []netsec.EntryID{"entry-mail"})
	if err != nil {
		t.Fatalf("approved Sync: %v", err)
	}
	if len(entries) != 1 || !bytes.Equal(entries[0].Ciphertext, []byte("sealed-mail")) {
		t.Fatalf("Sync returned %+v", entries)
	}

	entries, err = svc.Sync(accountID, joining.ID, []netsec.EntryID{"entry-bank"})
	if !errors.Is(err, netsec.ErrScopeDenied) || entries != nil {
		t.Fatalf("out-of-scope Sync = (%v, %v), want (nil, ErrScopeDenied)", entries, err)
	}
	entries, err = svc.Sync(accountID, joining.ID, []netsec.EntryID{"entry-mail", "entry-bank"})
	if !errors.Is(err, netsec.ErrScopeDenied) || entries != nil {
		t.Fatalf("mixed-scope Sync = (%v, %v), want (nil, ErrScopeDenied)", entries, err)
	}

	entries, err = svc.SyncAll(accountID, joining.ID)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "entry-mail" {
		t.Fatalf("scoped SyncAll returned %+v, want only entry-mail", entries)
	}

	if err := svc.PutEntry(accountID, owner.ID, "entry-new", []byte("sealed-new"), nil); err != nil {
		t.Fatalf("PutEntry new: %v", err)
	}
	if entries, err = svc.SyncAll(accountID, joining.ID); err != nil || len(entries) != 1 {
		t.Fatalf("scoped SyncAll after new entry = (%+v, %v), want only original scope", entries, err)
	}
	if entries, err = svc.SyncAll(accountID, owner.ID); err != nil {
		t.Fatalf("owner SyncAll: %v", err)
	} else if len(entries) != 3 {
		t.Fatalf("owner SyncAll returned %d entries, want 3", len(entries))
	}

	if err := svc.PutEntry(accountID, owner.ID, "entry-mail", []byte("sealed-mail-v2"), []byte("v2")); err != nil {
		t.Fatalf("update entry-mail: %v", err)
	}
	entries, err = svc.Sync(accountID, joining.ID, []netsec.EntryID{"entry-mail"})
	if err != nil {
		t.Fatalf("Sync after update: %v", err)
	}
	if entries[0].Version != 2 || !bytes.Equal(entries[0].Ciphertext, []byte("sealed-mail-v2")) {
		t.Fatalf("updated entry = %+v", entries[0])
	}
}

func TestZeroValueServiceIsUsable(t *testing.T) {
	var svc netsec.Service
	owner := testDevice(t, "owner")
	accountID := testAccount(t, &svc, owner.Public())
	if err := svc.PutEntry(accountID, owner.ID, "entry", []byte("ciphertext"), nil); err != nil {
		t.Fatalf("PutEntry on zero-value Service: %v", err)
	}
	if entries, err := svc.SyncAll(accountID, owner.ID); err != nil || len(entries) != 1 {
		t.Fatalf("SyncAll on zero-value Service = (%v, %v)", entries, err)
	}
}

func TestRecoveryCodeSingleUseAlertAndApproval(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	if err := svc.PutEntry(accountID, owner.ID, "entry-secret", []byte("ciphertext"), nil); err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	code, err := svc.IssueRecoveryCode(accountID, owner.ID)
	if err != nil {
		t.Fatalf("IssueRecoveryCode: %v", err)
	}
	if code == "" {
		t.Fatal("empty recovery code")
	}

	var alertsMu sync.Mutex
	var alerts []netsec.Alert
	svc.SetAlertHandler(func(alert netsec.Alert) {
		alertsMu.Lock()
		alerts = append(alerts, alert)
		alertsMu.Unlock()
	})

	first := testDevice(t, "recovered-device-1")
	enrollment, err := svc.BeginRecoveryEnrollment(accountID, code, first.Public())
	if err != nil {
		t.Fatalf("BeginRecoveryEnrollment first use: %v", err)
	}
	if !enrollment.RecoveryUsed || enrollment.Status != netsec.EnrollmentPending {
		t.Fatalf("recovery enrollment = %+v", enrollment)
	}

	alertsMu.Lock()
	usedAlerts := countAlerts(alerts, netsec.AlertRecoveryCodeUsed)
	alertsMu.Unlock()
	if usedAlerts != 1 {
		t.Fatalf("recovery-code-used alerts = %d, want 1", usedAlerts)
	}
	if entries, err := svc.Sync(accountID, first.ID, []netsec.EntryID{"entry-secret"}); !errors.Is(err, netsec.ErrDeviceNotApproved) || entries != nil {
		t.Fatalf("recovery-only device Sync = (%v, %v), want denied", entries, err)
	}

	second := testDevice(t, "recovered-device-2")
	if _, err := svc.BeginRecoveryEnrollment(accountID, code, second.Public()); !errors.Is(err, netsec.ErrRecoveryCodeUsed) {
		t.Fatalf("recovery code reuse error = %v, want ErrRecoveryCodeUsed", err)
	}
	if _, err := svc.Device(accountID, second.ID); !errors.Is(err, netsec.ErrDeviceNotFound) {
		t.Fatalf("reused code created a device: %v", err)
	}

	alertsMu.Lock()
	reuseAlerts := countAlerts(alerts, netsec.AlertRecoveryCodeReuse)
	alertsMu.Unlock()
	if reuseAlerts != 1 {
		t.Fatalf("recovery-code-reuse alerts = %d, want 1", reuseAlerts)
	}

	accountKey := randomKey(t)
	testApprove(t, svc, accountID, owner.ID, enrollment.ID, first.Public(), accountKey, "entry-secret")
	if entries, err := svc.Sync(accountID, first.ID, []netsec.EntryID{"entry-secret"}); err != nil || len(entries) != 1 {
		t.Fatalf("approved recovery device Sync = (%v, %v)", entries, err)
	}

	otherOwner := testDevice(t, "other-owner")
	otherAccount := testAccount(t, svc, otherOwner.Public())
	otherDevice := testDevice(t, "other-recovery")
	if _, err := svc.BeginRecoveryEnrollment(otherAccount, code, otherDevice.Public()); !errors.Is(err, netsec.ErrRecoveryCodeInvalid) {
		t.Fatalf("cross-account recovery code error = %v, want ErrRecoveryCodeInvalid", err)
	}
}

func TestUnapprovedDeviceCannotGainAccess(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	if err := svc.PutEntry(accountID, owner.ID, "entry-secret", []byte("ciphertext"), nil); err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	pending := testDevice(t, "pending")
	enrollment, err := svc.BeginEnrollment(accountID, pending.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}

	if entries, err := svc.SyncAll(accountID, pending.ID); !errors.Is(err, netsec.ErrDeviceNotApproved) || entries != nil {
		t.Fatalf("pending SyncAll = (%v, %v)", entries, err)
	}
	if key, err := svc.WrappedAccountKey(accountID, pending.ID); !errors.Is(err, netsec.ErrDeviceNotApproved) || key != nil {
		t.Fatalf("pending WrappedAccountKey = (%x, %v)", key, err)
	}

	accountKey := randomKey(t)
	binding := netsec.DeviceKeyBinding(accountID, pending.ID, enrollment.ID)
	wrapped, err := netsec.WrapAccountKey(accountKey, pending.KeyAgreementPublicKey, binding)
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}
	if err := svc.ApproveEnrollment(accountID, pending.ID, enrollment.ID, wrapped, []netsec.EntryID{"entry-secret"}); !errors.Is(err, netsec.ErrDeviceNotApproved) {
		t.Fatalf("self-approval error = %v, want ErrDeviceNotApproved", err)
	}
	if err := svc.ApproveEnrollment(accountID, "missing-approver", enrollment.ID, wrapped, []netsec.EntryID{"entry-secret"}); !errors.Is(err, netsec.ErrDeviceNotFound) {
		t.Fatalf("unknown approver error = %v, want ErrDeviceNotFound", err)
	}

	if err := svc.RejectEnrollment(accountID, owner.ID, enrollment.ID); err != nil {
		t.Fatalf("RejectEnrollment: %v", err)
	}
	if entries, err := svc.Sync(accountID, pending.ID, []netsec.EntryID{"entry-secret"}); !errors.Is(err, netsec.ErrDeviceNotApproved) || entries != nil {
		t.Fatalf("rejected device Sync = (%v, %v)", entries, err)
	}
}

func TestApprovalRequiresValidWrappedKey(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	joining := testDevice(t, "joining")
	enrollment, err := svc.BeginEnrollment(accountID, joining.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}

	if err := svc.ApproveEnrollment(accountID, owner.ID, enrollment.ID, nil, []netsec.EntryID{"entry"}); !errors.Is(err, netsec.ErrInvalidWrappedKey) {
		t.Fatalf("nil wrapped key error = %v", err)
	}
	if err := svc.ApproveEnrollment(accountID, owner.ID, enrollment.ID, make([]byte, 96), []netsec.EntryID{"entry"}); !errors.Is(err, netsec.ErrInvalidWrappedKey) {
		t.Fatalf("malformed wrapped key error = %v", err)
	}
	device, err := svc.Device(accountID, joining.ID)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if device.Status != netsec.DevicePending {
		t.Fatalf("device status after failed approvals = %s", device.Status)
	}
}

func TestScopedDeviceCannotEscalateThroughApprovalOrSelfGrant(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	for _, id := range []netsec.EntryID{"entry-1", "entry-2"} {
		if err := svc.PutEntry(accountID, owner.ID, id, []byte("ciphertext-"+string(id)), nil); err != nil {
			t.Fatalf("PutEntry %s: %v", id, err)
		}
	}

	limited := testDevice(t, "limited")
	limitedEnrollment, err := svc.BeginEnrollment(accountID, limited.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment limited: %v", err)
	}
	testApprove(t, svc, accountID, owner.ID, limitedEnrollment.ID, limited.Public(), randomKey(t), "entry-1")

	if err := svc.GrantEntries(accountID, limited.ID, limited.ID, []netsec.EntryID{"entry-2"}); !errors.Is(err, netsec.ErrApprovalRequired) {
		t.Fatalf("self-grant error = %v, want ErrApprovalRequired", err)
	}
	if entries, err := svc.Sync(accountID, limited.ID, []netsec.EntryID{"entry-2"}); !errors.Is(err, netsec.ErrScopeDenied) || entries != nil {
		t.Fatalf("self-granted Sync = (%v, %v)", entries, err)
	}

	replacement := testDevice(t, "replacement")
	replacementEnrollment, err := svc.BeginEnrollment(accountID, replacement.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment replacement: %v", err)
	}
	binding := netsec.DeviceKeyBinding(accountID, replacement.ID, replacementEnrollment.ID)
	wrapped, err := netsec.WrapAccountKey(randomKey(t), replacement.Public().KeyAgreementPublicKey, binding)
	if err != nil {
		t.Fatalf("WrapAccountKey: %v", err)
	}
	if err := svc.ApproveEnrollment(accountID, limited.ID, replacementEnrollment.ID, wrapped, []netsec.EntryID{"entry-2"}); !errors.Is(err, netsec.ErrScopeDenied) {
		t.Fatalf("scope escalation error = %v, want ErrScopeDenied", err)
	}
	if err := svc.ApproveEnrollmentAll(accountID, limited.ID, replacementEnrollment.ID, wrapped); !errors.Is(err, netsec.ErrScopeDenied) {
		t.Fatalf("all-entry escalation error = %v, want ErrScopeDenied", err)
	}
	if err := svc.ApproveEnrollment(accountID, limited.ID, replacementEnrollment.ID, wrapped, []netsec.EntryID{"entry-1"}); err != nil {
		t.Fatalf("in-scope approval: %v", err)
	}
	if entries, err := svc.Sync(accountID, replacement.ID, []netsec.EntryID{"entry-1"}); err != nil || len(entries) != 1 {
		t.Fatalf("replacement Sync = (%v, %v)", entries, err)
	}
	if entries, err := svc.Sync(accountID, replacement.ID, []netsec.EntryID{"entry-2"}); !errors.Is(err, netsec.ErrScopeDenied) || entries != nil {
		t.Fatalf("replacement escalated Sync = (%v, %v)", entries, err)
	}

	if err := svc.GrantEntries(accountID, owner.ID, limited.ID, []netsec.EntryID{"entry-2"}); err != nil {
		t.Fatalf("owner GrantEntries: %v", err)
	}
	if entries, err := svc.Sync(accountID, limited.ID, []netsec.EntryID{"entry-2"}); err != nil || len(entries) != 1 {
		t.Fatalf("owner-granted Sync = (%v, %v)", entries, err)
	}
	if err := svc.RemoveEntries(accountID, owner.ID, limited.ID, []netsec.EntryID{"entry-2"}); err != nil {
		t.Fatalf("owner RemoveEntries: %v", err)
	}
	if entries, err := svc.Sync(accountID, limited.ID, []netsec.EntryID{"entry-2"}); !errors.Is(err, netsec.ErrScopeDenied) || entries != nil {
		t.Fatalf("reduced-scope Sync = (%v, %v)", entries, err)
	}
}

func TestCrossAccountSyncAndDeviceReuseDenied(t *testing.T) {
	svc := netsec.NewService()
	ownerA := testDevice(t, "owner-a")
	ownerB := testDevice(t, "owner-b")
	accountA := testAccount(t, svc, ownerA.Public())
	accountB := testAccount(t, svc, ownerB.Public())

	if err := svc.PutEntry(accountA, ownerA.ID, "shared-entry", []byte("secret-a"), nil); err != nil {
		t.Fatalf("PutEntry A: %v", err)
	}
	if err := svc.PutEntry(accountB, ownerB.ID, "shared-entry", []byte("secret-b"), nil); err != nil {
		t.Fatalf("PutEntry B: %v", err)
	}

	deviceA := testDevice(t, "device-a")
	enrollment, err := svc.BeginEnrollment(accountA, deviceA.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment A: %v", err)
	}
	testApprove(t, svc, accountA, ownerA.ID, enrollment.ID, deviceA.Public(), randomKey(t), "shared-entry")

	entries, err := svc.Sync(accountB, deviceA.ID, []netsec.EntryID{"shared-entry"})
	if !errors.Is(err, netsec.ErrCrossAccount) || entries != nil {
		t.Fatalf("cross-account Sync = (%v, %v), want (nil, ErrCrossAccount)", entries, err)
	}
	if entries, err := svc.SyncAll(accountB, deviceA.ID); !errors.Is(err, netsec.ErrCrossAccount) || entries != nil {
		t.Fatalf("cross-account SyncAll = (%v, %v)", entries, err)
	}
	if key, err := svc.WrappedAccountKey(accountB, deviceA.ID); !errors.Is(err, netsec.ErrCrossAccount) || key != nil {
		t.Fatalf("cross-account WrappedAccountKey = (%x, %v)", key, err)
	}
	if err := svc.PutEntry(accountB, deviceA.ID, "poison", []byte("x"), nil); !errors.Is(err, netsec.ErrCrossAccount) {
		t.Fatalf("cross-account PutEntry error = %v", err)
	}

	ownEntries, err := svc.Sync(accountA, deviceA.ID, []netsec.EntryID{"shared-entry"})
	if err != nil || len(ownEntries) != 1 || !bytes.Equal(ownEntries[0].Ciphertext, []byte("secret-a")) {
		t.Fatalf("same-account Sync = (%v, %v)", ownEntries, err)
	}

	if _, err := svc.BeginEnrollment(accountB, deviceA.Public()); !errors.Is(err, netsec.ErrDeviceAlreadyExists) {
		t.Fatalf("cross-account device ID reuse error = %v, want ErrDeviceAlreadyExists", err)
	}
}

func TestRevocationImmediatelyStopsSync(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	if err := svc.PutEntry(accountID, owner.ID, "entry-secret", []byte("ciphertext"), nil); err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	device := testDevice(t, "device")
	enrollment, err := svc.BeginEnrollment(accountID, device.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	testApprove(t, svc, accountID, owner.ID, enrollment.ID, device.Public(), randomKey(t), "entry-secret")

	if entries, err := svc.Sync(accountID, device.ID, []netsec.EntryID{"entry-secret"}); err != nil || len(entries) != 1 {
		t.Fatalf("pre-revocation Sync = (%v, %v)", entries, err)
	}
	if err := svc.RevokeDevice(accountID, owner.ID, device.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	if entries, err := svc.Sync(accountID, device.ID, []netsec.EntryID{"entry-secret"}); !errors.Is(err, netsec.ErrDeviceRevoked) || entries != nil {
		t.Fatalf("post-revocation Sync = (%v, %v), want (nil, ErrDeviceRevoked)", entries, err)
	}
	if entries, err := svc.SyncAll(accountID, device.ID); !errors.Is(err, netsec.ErrDeviceRevoked) || entries != nil {
		t.Fatalf("post-revocation SyncAll = (%v, %v)", entries, err)
	}
	if key, err := svc.WrappedAccountKey(accountID, device.ID); !errors.Is(err, netsec.ErrDeviceRevoked) || key != nil {
		t.Fatalf("post-revocation WrappedAccountKey = (%x, %v)", key, err)
	}
	if err := svc.PutEntry(accountID, device.ID, "entry-new", []byte("x"), nil); !errors.Is(err, netsec.ErrDeviceRevoked) {
		t.Fatalf("post-revocation PutEntry error = %v", err)
	}
	if err := svc.RevokeDevice(accountID, owner.ID, device.ID); err != nil {
		t.Fatalf("idempotent RevokeDevice: %v", err)
	}

	info, err := svc.Device(accountID, device.ID)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if info.Status != netsec.DeviceRevoked || len(info.Scopes) != 0 {
		t.Fatalf("revoked device info = %+v", info)
	}

	var revokedAlerts int
	for _, alert := range svc.Alerts() {
		if alert.Type == netsec.AlertDeviceRevoked && alert.DeviceID == device.ID {
			revokedAlerts++
		}
	}
	if revokedAlerts != 1 {
		t.Fatalf("revocation alerts = %d, want 1", revokedAlerts)
	}
}

func TestEnrollmentInfoAndAccountIDReuse(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	joining := testDevice(t, "joining")
	enrollment, err := svc.BeginEnrollment(accountID, joining.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	info, err := svc.EnrollmentInfo(accountID, enrollment.ID)
	if err != nil {
		t.Fatalf("EnrollmentInfo: %v", err)
	}
	if info.DeviceID != joining.ID || info.Status != netsec.EnrollmentPending {
		t.Fatalf("EnrollmentInfo = %+v", info)
	}
	if _, err := svc.EnrollmentInfo("other-account", enrollment.ID); !errors.Is(err, netsec.ErrEnrollmentNotFound) {
		t.Fatalf("cross-account EnrollmentInfo error = %v", err)
	}

	otherOwner := testDevice(t, "other-owner")
	if err := svc.CreateAccountWithID(accountID, otherOwner.Public()); !errors.Is(err, netsec.ErrAccountAlreadyExists) {
		t.Fatalf("duplicate account error = %v", err)
	}
}

func TestConcurrentRecoveryCodeUseConsumesExactlyOnce(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	code, err := svc.IssueRecoveryCode(accountID, owner.ID)
	if err != nil {
		t.Fatalf("IssueRecoveryCode: %v", err)
	}

	const attempts = 64
	var successes atomic.Int32
	var reuseErrors atomic.Int32
	var otherErrors atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			device, err := netsec.GenerateDevice(netsec.DeviceID(fmt.Sprintf("recovery-race-%02d", i)))
			if err != nil {
				otherErrors.Add(1)
				return
			}
			<-start
			_, err = svc.BeginRecoveryEnrollment(accountID, code, device.Public())
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, netsec.ErrRecoveryCodeUsed):
				reuseErrors.Add(1)
			default:
				otherErrors.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("successful recovery uses = %d, want exactly 1", successes.Load())
	}
	if reuseErrors.Load() != attempts-1 {
		t.Fatalf("reuse errors = %d, want %d", reuseErrors.Load(), attempts-1)
	}
	if otherErrors.Load() != 0 {
		t.Fatalf("unexpected errors = %d", otherErrors.Load())
	}

	var used, reused int
	for _, alert := range svc.Alerts() {
		switch alert.Type {
		case netsec.AlertRecoveryCodeUsed:
			used++
		case netsec.AlertRecoveryCodeReuse:
			reused++
		}
	}
	if used != 1 || reused != attempts-1 {
		t.Fatalf("alerts used=%d reused=%d, want 1/%d", used, reused, attempts-1)
	}
}

func TestRevocationConcurrentlyBlocksFurtherSync(t *testing.T) {
	svc := netsec.NewService()
	owner := testDevice(t, "owner")
	accountID := testAccount(t, svc, owner.Public())
	if err := svc.PutEntry(accountID, owner.ID, "entry", []byte("ciphertext"), nil); err != nil {
		t.Fatalf("PutEntry: %v", err)
	}
	device := testDevice(t, "device")
	enrollment, err := svc.BeginEnrollment(accountID, device.Public())
	if err != nil {
		t.Fatalf("BeginEnrollment: %v", err)
	}
	testApprove(t, svc, accountID, owner.ID, enrollment.ID, device.Public(), randomKey(t), "entry")

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Sync(accountID, device.ID, []netsec.EntryID{"entry"}); err != nil {
				t.Errorf("pre-revocation concurrent Sync: %v", err)
			}
		}()
	}
	wg.Wait()

	if err := svc.RevokeDevice(accountID, owner.ID, device.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if entries, err := svc.Sync(accountID, device.ID, []netsec.EntryID{"entry"}); !errors.Is(err, netsec.ErrDeviceRevoked) || entries != nil {
				t.Errorf("post-revocation concurrent Sync = (%v, %v)", entries, err)
			}
		}()
	}
	wg.Wait()
}

func randomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}

func countAlerts(alerts []netsec.Alert, kind netsec.AlertType) int {
	var count int
	for _, alert := range alerts {
		if alert.Type == kind {
			count++
		}
	}
	return count
}
