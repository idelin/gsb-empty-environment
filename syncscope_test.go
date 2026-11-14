package netsec

import (
	"errors"
	"testing"
)

func approvedDevice(t *testing.T, scope *SyncScope, accountID, deviceID string) {
	t.Helper()
	if err := scope.RegisterDevice(accountID, deviceID); err != nil {
		t.Fatalf("register device: %v", err)
	}
	if err := scope.ApproveDevice(accountID, deviceID); err != nil {
		t.Fatalf("approve device: %v", err)
	}
}

func mustIssue(t *testing.T, scope *SyncScope, accountID, deviceID string) Token {
	t.Helper()
	token, err := scope.IssueToken(accountID, deviceID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return token
}

func mustSync(t *testing.T, scope *SyncScope, req SyncRequest) SyncResult {
	t.Helper()
	result, err := scope.Sync(req)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return result
}

func TestDeviceForgeryAndApproval(t *testing.T) {
	scope := NewSyncScope()
	approvedDevice(t, scope, "account-victim", "device-victim")

	// A device identifier already registered to one account cannot be claimed
	// by another account.
	if err := scope.RegisterDevice("account-attacker", "device-victim"); !errors.Is(err, ErrDeviceScopeMismatch) {
		t.Fatalf("cross-account device registration: got %v", err)
	}

	// An unregistered device cannot get a token.
	if _, err := scope.IssueToken("account-attacker", "device-invented"); !errors.Is(err, ErrDeviceNotRegistered) {
		t.Fatalf("unregistered device token: got %v", err)
	}

	// Registration alone is not enough; a pending device must be approved.
	if err := scope.RegisterDevice("account-attacker", "device-pending"); err != nil {
		t.Fatalf("register pending device: %v", err)
	}
	if _, err := scope.IssueToken("account-attacker", "device-pending"); !errors.Is(err, ErrDeviceNotApproved) {
		t.Fatalf("pending device token: got %v", err)
	}

	// An attacker's approved device cannot use a victim's token, even if the
	// attacker supplies the victim account identifier.
	if err := scope.ApproveDevice("account-attacker", "device-pending"); err != nil {
		t.Fatalf("approve attacker device: %v", err)
	}
	victimToken := mustIssue(t, scope, "account-victim", "device-victim")
	_, err := scope.Sync(SyncRequest{
		AccountID: "account-victim",
		DeviceID:  "device-pending",
		Token:     victimToken,
		Cursor:    0,
	})
	if !errors.Is(err, ErrDeviceScopeMismatch) {
		t.Fatalf("forged device pull: got %v", err)
	}

	// Conversely, an attacker token cannot be widened to the victim's approved
	// device by forging both the account and device identifiers.
	attackerToken := mustIssue(t, scope, "account-attacker", "device-pending")
	_, err = scope.Sync(SyncRequest{
		AccountID: "account-victim",
		DeviceID:  "device-victim",
		Token:     attackerToken,
		Cursor:    0,
	})
	if !errors.Is(err, ErrTokenScopeMismatch) {
		t.Fatalf("token widened to victim device: got %v", err)
	}
}

func TestTokenReplayAndCursorBinding(t *testing.T) {
	scope := NewSyncScope()
	approvedDevice(t, scope, "account-a", "device-a")
	if _, err := scope.UpsertContact("account-a", Contact{ID: "contact-1", Name: "Alice"}); err != nil {
		t.Fatal(err)
	}

	first := mustIssue(t, scope, "account-a", "device-a")
	result := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     first,
		Cursor:    0,
	})
	if result.NextToken == "" {
		t.Fatal("expected a rotated token")
	}

	// Replaying the consumed token is rejected even with its original cursor.
	_, err := scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     first,
		Cursor:    0,
	})
	if !errors.Is(err, ErrTokenUsed) {
		t.Fatalf("token replay: got %v", err)
	}

	// A fresh token is valid only with the cursor bound to it. Supplying an old
	// cursor must not rewind the account.
	_, err = scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     result.NextToken,
		Cursor:    0,
	})
	if !errors.Is(err, ErrCursorStale) {
		t.Fatalf("old cursor: got %v", err)
	}

	// An invented cursor is rejected as well.
	_, err = scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     result.NextToken,
		Cursor:    999,
	})
	if !errors.Is(err, ErrCursorStale) {
		t.Fatalf("invented cursor: got %v", err)
	}

	// The rejected attempts did not consume the rotated token.
	if _, err := scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     result.NextToken,
		Cursor:    result.NextCursor,
	}); err != nil {
		t.Fatalf("valid cursor after rejected attempts: %v", err)
	}
}

func TestOnlyOwnAccountContactsAreSynchronized(t *testing.T) {
	scope := NewSyncScope()
	approvedDevice(t, scope, "account-a", "device-a")
	approvedDevice(t, scope, "account-b", "device-b")

	// Deliberately use the same contact ID in both accounts.
	if _, err := scope.UpsertContact("account-a", Contact{ID: "contact-1", Name: "Alice A", Phone: "111"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.UpsertContact("account-b", Contact{ID: "contact-1", Name: "Bob B", Phone: "222"}); err != nil {
		t.Fatal(err)
	}

	tokenA := mustIssue(t, scope, "account-a", "device-a")
	resultA := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     tokenA,
		Cursor:    0,
	})
	if len(resultA.Changes) != 1 {
		t.Fatalf("account A got %d changes", len(resultA.Changes))
	}
	if got := resultA.Changes[0].Contact.Name; got != "Alice A" {
		t.Fatalf("account A leaked or reordered contacts: %q", got)
	}

	tokenB := mustIssue(t, scope, "account-b", "device-b")
	resultB := mustSync(t, scope, SyncRequest{
		AccountID: "account-b",
		DeviceID:  "device-b",
		Token:     tokenB,
		Cursor:    0,
	})
	if len(resultB.Changes) != 1 {
		t.Fatalf("account B got %d changes", len(resultB.Changes))
	}
	if got := resultB.Changes[0].Contact.Name; got != "Bob B" {
		t.Fatalf("account B got wrong contact: %q", got)
	}

	// Cross-account token use is refused rather than merged. Use a fresh token
	// so the rejection is specifically about scope, not prior consumption.
	freshTokenA := mustIssue(t, scope, "account-a", "device-a")
	_, err := scope.Sync(SyncRequest{
		AccountID: "account-b",
		DeviceID:  "device-b",
		Token:     freshTokenA,
		Cursor:    0,
	})
	if !errors.Is(err, ErrTokenScopeMismatch) {
		t.Fatalf("cross-account token: got %v", err)
	}
}

func TestIncrementalAndDeleteSynchronization(t *testing.T) {
	scope := NewSyncScope()
	approvedDevice(t, scope, "account-a", "device-a")
	approvedDevice(t, scope, "account-b", "device-b")

	if _, err := scope.UpsertContact("account-a", Contact{ID: "contact-1", Name: "Alice", Phone: "111"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.UpsertContact("account-a", Contact{ID: "contact-2", Name: "Carol"}); err != nil {
		t.Fatal(err)
	}

	token := mustIssue(t, scope, "account-a", "device-a")
	initial := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     token,
		Cursor:    0,
	})
	if len(initial.Changes) != 2 {
		t.Fatalf("initial sync returned %d changes, want 2", len(initial.Changes))
	}

	// An unrelated account's mutation must not advance account A's cursor or
	// appear in account A's incremental page.
	if _, err := scope.UpsertContact("account-b", Contact{ID: "contact-9", Name: "Mallory"}); err != nil {
		t.Fatal(err)
	}

	if _, err := scope.UpsertContact("account-a", Contact{ID: "contact-3", Name: "Dave"}); err != nil {
		t.Fatal(err)
	}
	incremental := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     initial.NextToken,
		Cursor:    initial.NextCursor,
	})
	if len(incremental.Changes) != 1 {
		t.Fatalf("incremental sync returned %d changes, want 1", len(incremental.Changes))
	}
	if got := incremental.Changes[0].Contact.ID; got != "contact-3" {
		t.Fatalf("incremental change = %q, want contact-3", got)
	}

	// Delete a contact and verify the tombstone is delivered, without leaking
	// the old contact body.
	if _, err := scope.DeleteContact("account-a", "contact-1"); err != nil {
		t.Fatal(err)
	}
	deleted := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     incremental.NextToken,
		Cursor:    incremental.NextCursor,
	})
	if len(deleted.Changes) != 1 {
		t.Fatalf("delete sync returned %d changes, want 1", len(deleted.Changes))
	}
	change := deleted.Changes[0]
	if !change.Deleted {
		t.Fatal("expected a deletion tombstone")
	}
	if change.Contact.ID != "contact-1" || change.Contact.Name != "" || change.Contact.Phone != "" {
		t.Fatalf("tombstone leaked contact data: %+v", change.Contact)
	}

	// The deletion in account A is invisible to account B.
	tokenB := mustIssue(t, scope, "account-b", "device-b")
	resultB := mustSync(t, scope, SyncRequest{
		AccountID: "account-b",
		DeviceID:  "device-b",
		Token:     tokenB,
		Cursor:    0,
	})
	if len(resultB.Changes) != 1 || resultB.Changes[0].Contact.Name != "Mallory" {
		t.Fatalf("account B delete isolation failed: %+v", resultB.Changes)
	}

	// A deletion record remains the source of truth for the original
	// account: no later pull resurrects contact-1.
	afterDelete := mustSync(t, scope, SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     deleted.NextToken,
		Cursor:    deleted.NextCursor,
	})
	if len(afterDelete.Changes) != 0 {
		t.Fatalf("deleted contact reappeared: %+v", afterDelete.Changes)
	}
}

func TestRevocationInvalidatesTokens(t *testing.T) {
	scope := NewSyncScope()
	approvedDevice(t, scope, "account-a", "device-a")
	token := mustIssue(t, scope, "account-a", "device-a")

	if err := scope.RevokeDevice("account-a", "device-a"); err != nil {
		t.Fatal(err)
	}
	_, err := scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     token,
		Cursor:    0,
	})
	if !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device sync: got %v", err)
	}
	if _, err := scope.IssueToken("account-a", "device-a"); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device token: got %v", err)
	}
}

func TestContactIDsAreScopedPerAccount(t *testing.T) {
	scope := NewSyncScope()
	if _, err := scope.UpsertContact("account-a", Contact{ID: "same-id", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.UpsertContact("account-b", Contact{ID: "same-id", Name: "B"}); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.DeleteContact("account-a", "same-id"); err != nil {
		t.Fatal(err)
	}

	// Deleting the same contact ID in account A must not delete account B's
	// independent record.
	if version, err := scope.UpsertContact("account-b", Contact{ID: "same-id", Name: "B2"}); err != nil || version == 0 {
		t.Fatalf("account B contact was lost: version=%d err=%v", version, err)
	}
}

func TestZeroValueScopeIsUsable(t *testing.T) {
	var scope SyncScope
	approvedDevice(t, &scope, "account-a", "device-a")
	token := mustIssue(t, &scope, "account-a", "device-a")
	if _, err := scope.Sync(SyncRequest{
		AccountID: "account-a",
		DeviceID:  "device-a",
		Token:     token,
		Cursor:    0,
	}); err != nil {
		t.Fatalf("zero-value scope sync: %v", err)
	}
}
