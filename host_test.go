package roomdelivery_test

import (
	"errors"
	"reflect"
	"testing"

	roomdelivery "prescreen.local/ddd-10531-v3"
)

func join(t *testing.T, host *roomdelivery.Host, instance roomdelivery.MembershipInstanceID, principal roomdelivery.PrincipalID, name roomdelivery.DisplayName, jid roomdelivery.RealJID) roomdelivery.MembershipRef {
	t.Helper()
	member, err := host.Join(roomdelivery.JoinRequest{
		Principal:   principal,
		DisplayName: name,
		RealJID:     jid,
		InstanceID:  instance,
	})
	if err != nil {
		t.Fatalf("join %s: %v", principal, err)
	}
	return member
}

func reveal(t *testing.T, host *roomdelivery.Host, subject, recipient roomdelivery.MembershipRef, expires roomdelivery.RoomVersion) roomdelivery.RevealID {
	t.Helper()
	id, err := host.Reveal(roomdelivery.RevealRequest{
		Subject:        subject,
		Recipient:      recipient,
		ExpiresVersion: expires,
	})
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	return id
}

func send(t *testing.T, host *roomdelivery.Host, author roomdelivery.MembershipRef, targets []roomdelivery.MembershipRef, id roomdelivery.MessageID, body roomdelivery.Body, quote roomdelivery.MessageID) roomdelivery.Dispatch {
	t.Helper()
	dispatch, err := host.Send(roomdelivery.SendRequest{
		Author:       author,
		Targets:      targets,
		MessageID:    id,
		MessageType:  roomdelivery.MessageTypeBody,
		Body:         body,
		QuoteMessage: quote,
	})
	if err != nil {
		t.Fatalf("send %s: %v", id, err)
	}
	return dispatch
}

func publish(t *testing.T, host *roomdelivery.Host, receipt roomdelivery.Receipt) {
	t.Helper()
	if err := host.Publish(receipt.ID); err != nil {
		t.Fatalf("publish %s: %v", receipt.ID, err)
	}
}

func viewFor(t *testing.T, host *roomdelivery.Host, viewer roomdelivery.MembershipRef) roomdelivery.RecipientView {
	t.Helper()
	view, err := roomdelivery.RebuildRecipientView(host.History(), viewer)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	return view
}

func TestPartialRevealDoesNotUnlockHistory(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")
	carol := join(t, host, "carol-1", "carol", "carol", "carol@private")

	first := send(t, host, alice, []roomdelivery.MembershipRef{bob, carol}, "m1",
		roomdelivery.TextBody("from alice").WithIdentity("sender", roomdelivery.IdentityRef{
			Principal:  alice.Principal,
			Generation: alice.Generation,
			Instance:   alice.Instance,
		}), "")
	for _, receipt := range first.Receipts {
		publish(t, host, receipt)
	}
	before, err := roomdelivery.RebuildRecipientView(host.History(), bob)
	if err != nil {
		t.Fatal(err)
	}
	if before.Entries[0].Author.JID != "" || before.Entries[0].Body.Attributes[0].Value == "alice@private" {
		t.Fatalf("real JID leaked before reveal: %#v", before.Entries[0])
	}

	reveal(t, host, alice, bob, 0)

	afterReveal, err := roomdelivery.RebuildRecipientView(host.History(), bob)
	if err != nil {
		t.Fatal(err)
	}
	if afterReveal.Entries[0].Author.JID != "" || afterReveal.Entries[0].Body.Attributes[0].Value == "alice@private" {
		t.Fatalf("new reveal retroactively unlocked old receipt: %#v", afterReveal.Entries[0])
	}
	if afterReveal.Entries[0].Body.Attributes[0].Value != before.Entries[0].Body.Attributes[0].Value {
		t.Fatal("old entry changed after a new reveal")
	}

	second := send(t, host, alice, []roomdelivery.MembershipRef{bob, carol}, "m2",
		roomdelivery.TextBody("still alice").WithIdentity("sender", roomdelivery.IdentityRef{
			Principal:  alice.Principal,
			Generation: alice.Generation,
			Instance:   alice.Instance,
		}), "")
	for _, receipt := range second.Receipts {
		publish(t, host, receipt)
	}

	bobView, err := roomdelivery.RebuildRecipientView(host.History(), bob)
	if err != nil {
		t.Fatal(err)
	}
	carolView, err := roomdelivery.RebuildRecipientView(host.History(), carol)
	if err != nil {
		t.Fatal(err)
	}
	bobSecond := bobView.Entries[1]
	carolSecond := carolView.Entries[1]
	if bobSecond.Author.JID != "alice@private" || bobSecond.Body.Attributes[0].Value != "alice@private" {
		t.Fatalf("explicitly revealed recipient did not see JID: %#v", bobSecond)
	}
	if carolSecond.Author.JID != "" || carolSecond.Body.Attributes[0].Value == "alice@private" {
		t.Fatalf("unrevealed recipient saw JID: %#v", carolSecond)
	}
	if bobSecond.Body.Attributes[0].Value == carolSecond.Body.Attributes[0].Value {
		t.Fatal("partial reveal should produce different recipient output")
	}
}

func TestNicknameReuseAndJoinInstanceSeparation(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "same-name", "alice@one")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	first := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1", roomdelivery.TextBody("first"), "")
	publish(t, host, first.Receipts[0])
	if _, err := host.Kick(roomdelivery.KickRequest{Member: alice}); err != nil {
		t.Fatal(err)
	}
	alice2, err := host.Rejoin(roomdelivery.RejoinRequest{
		Principal:        "alice",
		PreviousInstance: alice.Instance,
		DisplayName:      "same-name",
		RealJID:          "alice@two",
		InstanceID:       "alice-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if alice2.Instance == alice.Instance || alice2.Generation == alice.Generation {
		t.Fatal("rejoin reused an instance or generation")
	}
	if _, err := host.Rejoin(roomdelivery.RejoinRequest{
		Principal:        "alice",
		PreviousInstance: alice.Instance,
		DisplayName:      "same-name",
		RealJID:          "alice@three",
		InstanceID:       alice.Instance,
	}); !errors.Is(err, roomdelivery.ErrInstanceReused) {
		t.Fatalf("expected instance reuse error, got %v", err)
	}

	second := send(t, host, alice2, []roomdelivery.MembershipRef{bob}, "m2", roomdelivery.TextBody("second"), "")
	publish(t, host, second.Receipts[0])
	view := viewFor(t, host, bob)
	if len(view.Entries) != 2 {
		t.Fatalf("want two entries, got %d", len(view.Entries))
	}
	left, right := view.Entries[0].Author, view.Entries[1].Author
	if left.DisplayName != right.DisplayName {
		t.Fatal("test requires reused display name")
	}
	if left.Instance == right.Instance || left.Generation == right.Generation || left.Alias == right.Alias {
		t.Fatalf("reused nickname collapsed two join instances: %#v %#v", left, right)
	}
}

func TestQuoteKeepsPreRenameIdentity(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "old-name", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	first := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1", roomdelivery.TextBody("before rename"), "")
	publish(t, host, first.Receipts[0])
	if _, err := host.Rename(roomdelivery.RenameRequest{Member: alice, DisplayName: "new-name"}); err != nil {
		t.Fatal(err)
	}
	second := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m2", roomdelivery.TextBody("after rename"), "m1")
	publish(t, host, second.Receipts[0])

	view := viewFor(t, host, bob)
	if len(view.Entries) != 2 || view.Entries[1].Quote == nil || !view.Entries[1].Quote.Available {
		t.Fatalf("quote missing: %#v", view)
	}
	if view.Entries[1].Author.DisplayName != "new-name" {
		t.Fatalf("current entry should use new name, got %q", view.Entries[1].Author.DisplayName)
	}
	if view.Entries[1].Quote.Author.DisplayName != "old-name" {
		t.Fatalf("quote should retain pre-rename name, got %q", view.Entries[1].Quote.Author.DisplayName)
	}
}

func TestQuoteDoesNotInheritNewerReveal(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	first := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1", roomdelivery.TextBody("old"), "")
	publish(t, host, first.Receipts[0])
	reveal(t, host, alice, bob, 0)
	second := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m2", roomdelivery.TextBody("new"), "m1")
	publish(t, host, second.Receipts[0])

	view := viewFor(t, host, bob)
	if view.Entries[0].Author.JID != "" {
		t.Fatalf("old receipt was retroactively revealed: %#v", view.Entries[0].Author)
	}
	if view.Entries[1].Author.JID != "alice@private" {
		t.Fatalf("new receipt should see reveal: %#v", view.Entries[1].Author)
	}
	if view.Entries[1].Quote == nil || view.Entries[1].Quote.Author.JID != "" {
		t.Fatalf("quote inherited newer reveal: %#v", view.Entries[1].Quote)
	}
}

func TestRetractionAndOfflineDeliveryInterleave(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	early := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "early", roomdelivery.TextBody("secret early"), "")
	late := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "late", roomdelivery.TextBody("late body"), "")
	publish(t, host, late.Receipts[0])
	if _, err := host.Retract(roomdelivery.RetractRequest{
		Author:     alice,
		ReceiptIDs: []roomdelivery.ReceiptID{early.Receipts[0].ID},
	}); err != nil {
		t.Fatal(err)
	}
	publish(t, host, early.Receipts[0])

	view := viewFor(t, host, bob)
	if len(view.Entries) != 2 {
		t.Fatalf("want two entries, got %d", len(view.Entries))
	}
	if view.Entries[0].MessageID != "early" || !view.Entries[0].Retracted {
		t.Fatalf("offline early message should be first tombstone: %#v", view.Entries[0])
	}
	if view.Entries[0].Body.Text != "" {
		t.Fatal("retracted content leaked")
	}
	if view.Entries[1].MessageID != "late" || view.Entries[1].Retracted {
		t.Fatalf("late message ordering/state wrong: %#v", view.Entries[1])
	}
}

func TestCorrectionIsReceiptScoped(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")
	carol := join(t, host, "carol-1", "carol", "carol", "carol@private")

	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob, carol}, "m1", roomdelivery.TextBody("typo"), "")
	publish(t, host, dispatch.Receipts[0])
	if _, err := host.Correct(roomdelivery.CorrectRequest{
		Author:     alice,
		ReceiptIDs: []roomdelivery.ReceiptID{dispatch.Receipts[0].ID},
		Body:       roomdelivery.TextBody("fixed"),
	}); err != nil {
		t.Fatal(err)
	}
	publish(t, host, dispatch.Receipts[1])

	bobView := viewFor(t, host, bob)
	carolView := viewFor(t, host, carol)
	if bobView.Entries[0].Body.Text != "fixed" || !bobView.Entries[0].Corrected {
		t.Fatalf("corrected receipt wrong: %#v", bobView.Entries[0])
	}
	if carolView.Entries[0].Body.Text != "typo" || carolView.Entries[0].Corrected {
		t.Fatalf("correction crossed receipt scope: %#v", carolView.Entries[0])
	}
	if _, err := host.Correct(roomdelivery.CorrectRequest{
		Author:     bob,
		ReceiptIDs: []roomdelivery.ReceiptID{dispatch.Receipts[0].ID},
		Body:       roomdelivery.TextBody("forged"),
	}); !errors.Is(err, roomdelivery.ErrReceiptScope) {
		t.Fatalf("expected scope error for foreign author, got %v", err)
	}
}

func TestRevokedQueuedIdentityReceiptCannotPublish(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	revealID := reveal(t, host, alice, bob, 0)
	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1",
		roomdelivery.TextBody("private").WithIdentity("sender", roomdelivery.IdentityRef{
			Principal:  alice.Principal,
			Generation: alice.Generation,
			Instance:   alice.Instance,
		}), "")
	if _, err := host.Revoke(roomdelivery.RevokeRequest{
		Subject:   alice,
		Recipient: bob,
		RevealID:  revealID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Publish(dispatch.Receipts[0].ID); !errors.Is(err, roomdelivery.ErrIdentityRevoked) {
		t.Fatalf("expected revoked identity error, got %v", err)
	}
	receipt, _ := host.Receipt(dispatch.Receipts[0].ID)
	if receipt.Status != roomdelivery.ReceiptBlocked {
		t.Fatalf("queued receipt should be blocked, got %s", receipt.Status)
	}
	view := viewFor(t, host, bob)
	if len(view.Entries) != 0 {
		t.Fatalf("blocked receipt appeared in recipient view: %#v", view.Entries)
	}
}

func TestFreshRevealDoesNotReopenQueuedReceipt(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	firstReveal := reveal(t, host, alice, bob, 0)
	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1", roomdelivery.TextBody("queued"), "")
	if _, err := host.Revoke(roomdelivery.RevokeRequest{
		Subject:   alice,
		Recipient: bob,
		RevealID:  firstReveal,
	}); err != nil {
		t.Fatal(err)
	}
	reveal(t, host, alice, bob, 0)
	if err := host.Publish(dispatch.Receipts[0].ID); !errors.Is(err, roomdelivery.ErrIdentityRevoked) {
		t.Fatalf("fresh reveal reopened queued receipt: %v", err)
	}

	next := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m2", roomdelivery.TextBody("new"), "")
	publish(t, host, next.Receipts[0])
	view := viewFor(t, host, bob)
	if view.Entries[0].Author.JID != "alice@private" {
		t.Fatalf("new receipt should use fresh reveal: %#v", view.Entries[0].Author)
	}
}

func TestRetractedQueuedReceiptCannotExposeRevokedIdentity(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	revealID := reveal(t, host, alice, bob, 0)
	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1", roomdelivery.TextBody("private"), "")
	if _, err := host.Revoke(roomdelivery.RevokeRequest{
		Subject:   alice,
		Recipient: bob,
		RevealID:  revealID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Retract(roomdelivery.RetractRequest{
		Author:     alice,
		ReceiptIDs: []roomdelivery.ReceiptID{dispatch.Receipts[0].ID},
	}); err != nil {
		t.Fatal(err)
	}
	publish(t, host, dispatch.Receipts[0])
	view := viewFor(t, host, bob)
	if len(view.Entries) != 1 || !view.Entries[0].Retracted {
		t.Fatalf("expected one tombstone: %#v", view.Entries)
	}
	if view.Entries[0].Author.JID != "" {
		t.Fatalf("retracted tombstone exposed revoked JID: %#v", view.Entries[0].Author)
	}
}

func TestQueuedBodyIdentityIsDetectedFromInstanceOnlyReference(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")
	carol := join(t, host, "carol-1", "carol", "carol", "carol@private")

	revealID := reveal(t, host, carol, bob, 0)
	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1",
		roomdelivery.TextBody("contact").WithIdentity("contact", roomdelivery.IdentityRef{
			Instance: carol.Instance,
		}), "")
	if _, err := host.Revoke(roomdelivery.RevokeRequest{
		Subject:   carol,
		Recipient: bob,
		RevealID:  revealID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Publish(dispatch.Receipts[0].ID); !errors.Is(err, roomdelivery.ErrIdentityRevoked) {
		t.Fatalf("instance-only identity reference escaped revocation check: %v", err)
	}
}

func TestTemporaryRevealExpiresBeforeQueuedPublish(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")

	revealVersion := host.Version() + 3
	reveal(t, host, alice, bob, revealVersion)
	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "m1",
		roomdelivery.TextBody("expiring").WithIdentity("sender", roomdelivery.IdentityRef{
			Principal:  alice.Principal,
			Generation: alice.Generation,
			Instance:   alice.Instance,
		}), "")
	if host.Version() >= revealVersion {
		t.Fatalf("test setup crossed reveal expiry: version=%d expires=%d", host.Version(), revealVersion)
	}
	if _, err := host.Rename(roomdelivery.RenameRequest{Member: bob, DisplayName: "bob-renamed"}); err != nil {
		t.Fatal(err)
	}
	if host.Version() < revealVersion {
		t.Fatalf("test setup did not reach expiry: version=%d expires=%d", host.Version(), revealVersion)
	}
	if err := host.Publish(dispatch.Receipts[0].ID); !errors.Is(err, roomdelivery.ErrIdentityRevoked) {
		t.Fatalf("expected expiry to block queued identity, got %v", err)
	}
}

func TestRevealDoesNotCrossSubjectOrReceiverGenerations(t *testing.T) {
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@one")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@one")
	reveal(t, host, alice, bob, 0)

	dispatch := send(t, host, alice, []roomdelivery.MembershipRef{bob}, "visible", roomdelivery.TextBody("visible"), "")
	publish(t, host, dispatch.Receipts[0])
	view := viewFor(t, host, bob)
	if view.Entries[0].Author.JID != "alice@one" {
		t.Fatalf("baseline reveal missing: %#v", view.Entries[0].Author)
	}

	if _, err := host.Kick(roomdelivery.KickRequest{Member: alice}); err != nil {
		t.Fatal(err)
	}
	alice2, err := host.Rejoin(roomdelivery.RejoinRequest{
		Principal:        "alice",
		PreviousInstance: alice.Instance,
		DisplayName:      "alice",
		RealJID:          "alice@two",
		InstanceID:       "alice-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch = send(t, host, alice2, []roomdelivery.MembershipRef{bob}, "subject-gen", roomdelivery.TextBody("subject generation"), "")
	publish(t, host, dispatch.Receipts[0])
	view = viewFor(t, host, bob)
	if view.Entries[1].Author.JID != "" {
		t.Fatalf("new subject generation inherited old reveal: %#v", view.Entries[1].Author)
	}

	if _, err := host.Kick(roomdelivery.KickRequest{Member: bob}); err != nil {
		t.Fatal(err)
	}
	bob2, err := host.Rejoin(roomdelivery.RejoinRequest{
		Principal:        "bob",
		PreviousInstance: bob.Instance,
		DisplayName:      "bob",
		RealJID:          "bob@two",
		InstanceID:       "bob-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch = send(t, host, alice2, []roomdelivery.MembershipRef{bob2}, "receiver-gen", roomdelivery.TextBody("receiver generation"), "")
	publish(t, host, dispatch.Receipts[0])
	view = viewFor(t, host, bob2)
	if view.Entries[0].Author.JID != "" {
		t.Fatalf("new receive instance inherited old reveal: %#v", view.Entries[0].Author)
	}
}

func buildPrivateHistory(t *testing.T, carolJID roomdelivery.RealJID, revealCarol bool) roomdelivery.History {
	t.Helper()
	host := roomdelivery.NewHost("room")
	alice := join(t, host, "alice-1", "alice", "alice", "alice@private")
	bob := join(t, host, "bob-1", "bob", "bob", "bob@private")
	carol := join(t, host, "carol-1", "carol", "carol", carolJID)
	if revealCarol {
		reveal(t, host, carol, bob, 0)
	}

	first := send(t, host, carol, []roomdelivery.MembershipRef{bob}, "m1",
		roomdelivery.TextBody("reach me at "+string(carolJID)).WithIdentity("sender", roomdelivery.IdentityRef{
			Principal:  carol.Principal,
			Generation: carol.Generation,
			Instance:   carol.Instance,
		}), "")
	publish(t, host, first.Receipts[0])
	if _, err := host.Rename(roomdelivery.RenameRequest{Member: carol, DisplayName: "carol-new"}); err != nil {
		t.Fatal(err)
	}
	second := send(t, host, carol, []roomdelivery.MembershipRef{bob}, "m2", roomdelivery.TextBody("quoted"), "")
	publish(t, host, second.Receipts[0])
	third := send(t, host, carol, []roomdelivery.MembershipRef{bob}, "m3", roomdelivery.TextBody("reply"), "m2")
	publish(t, host, third.Receipts[0])
	if _, err := host.Send(roomdelivery.SendRequest{
		Author:      carol,
		Targets:     []roomdelivery.MembershipRef{bob},
		MessageID:   "count",
		MessageType: roomdelivery.MessageTypeParticipantCount,
		Body:        roomdelivery.TextBody("participants"),
	}); err != nil {
		t.Fatal(err)
	}
	countReceipts := host.Receipts()
	for _, receipt := range countReceipts {
		if receipt.MessageID == "count" {
			publish(t, host, receipt)
		}
	}
	errorReceipt, err := host.SendTo(roomdelivery.SendRequest{
		Author:      carol,
		MessageID:   "error",
		MessageType: roomdelivery.MessageTypeError,
		Body:        roomdelivery.TextBody("delivery failed"),
		Failure: &roomdelivery.Failure{
			Code: "unreachable",
			Text: "could not reach " + string(carolJID),
			Subject: &roomdelivery.IdentityRef{
				Principal:  carol.Principal,
				Generation: carol.Generation,
				Instance:   carol.Instance,
			},
		},
	}, bob)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, host, errorReceipt)
	_ = alice
	return host.History()
}

func TestUnauthorizedRealJIDChangesDoNotAlterVisibleOutput(t *testing.T) {
	left := buildPrivateHistory(t, "carol@one", false)
	right := left.WithRealJID("carol-1", "carol@two")
	leftView, err := roomdelivery.RebuildRecipientView(left, roomdelivery.MembershipRef{
		RoomID: "room", Instance: "bob-1", Principal: "bob", Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	rightView, err := roomdelivery.RebuildRecipientView(right, roomdelivery.MembershipRef{
		RoomID: "room", Instance: "bob-1", Principal: "bob", Generation: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	leftJSON, err := roomdelivery.CanonicalJSON(leftView)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := roomdelivery.CanonicalJSON(rightView)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("unauthorized JID changed visible output\nleft:  %s\nright: %s", leftJSON, rightJSON)
	}
}

func TestAuthorizedRealJIDIsVisibleAndChangesOutput(t *testing.T) {
	left := buildPrivateHistory(t, "carol@one", true)
	right := buildPrivateHistory(t, "carol@two", true)
	bob := roomdelivery.MembershipRef{RoomID: "room", Instance: "bob-1", Principal: "bob", Generation: 1}
	leftView, err := roomdelivery.RebuildRecipientView(left, bob)
	if err != nil {
		t.Fatal(err)
	}
	rightView, err := roomdelivery.RebuildRecipientView(right, bob)
	if err != nil {
		t.Fatal(err)
	}
	if leftView.Entries[0].Author.JID != "carol@one" || rightView.Entries[0].Author.JID != "carol@two" {
		t.Fatalf("authorized JIDs not visible: %#v %#v", leftView.Entries[0].Author, rightView.Entries[0].Author)
	}
	if reflect.DeepEqual(leftView, rightView) {
		t.Fatal("authorized JID changes should alter visible output")
	}
}
