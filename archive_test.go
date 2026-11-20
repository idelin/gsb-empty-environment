package newsindex_test

import (
	"errors"
	"reflect"
	"testing"

	newsindex "prescreen.local/ddd-10559-v2"
)

const (
	team  newsindex.Group       = "team"
	vault newsindex.Group       = "vault"
	alice newsindex.PrincipalID = "alice"
	bob   newsindex.PrincipalID = "bob"
)

func commit(t *testing.T, archive *newsindex.Archive, batch *newsindex.Batch) newsindex.CommitResult {
	t.Helper()
	result, err := archive.Commit(batch)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return result
}

func page(t *testing.T, archive *newsindex.Archive, user newsindex.PrincipalID, limit int, cursor string) newsindex.Page {
	t.Helper()
	result, err := archive.Page(user, newsindex.PageRequest{Limit: limit, Cursor: cursor})
	if err != nil {
		t.Fatalf("Page(%s): %v", user, err)
	}
	return result
}

func message(t *testing.T, archive *newsindex.Archive, user newsindex.PrincipalID, id newsindex.MessageID) newsindex.MessageView {
	t.Helper()
	result, err := archive.Get(user, id)
	if err != nil {
		t.Fatalf("Get(%s, %s): %v", user, id, err)
	}
	return result
}

func ids(page newsindex.Page) []newsindex.MessageID {
	result := make([]newsindex.MessageID, len(page.Items))
	for i, item := range page.Items {
		result[i] = item.ID
	}
	return result
}

func TestMultiGroupFragmentsAndLastPublicAssociation(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "m1", "public one").
		Put(team, "m1", "team one").
		Put(newsindex.PublicGroup, "m2", "public two").
		Grant(bob, team))

	publicOnly := message(t, archive, alice, "m1")
	if got, want := publicOnly.Fragments, []newsindex.FragmentView{{Group: newsindex.PublicGroup, Body: "public one"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alice fragments = %#v, want %#v", got, want)
	}

	both := message(t, archive, bob, "m1")
	if got, want := both.Fragments, []newsindex.FragmentView{
		{Group: newsindex.PublicGroup, Body: "public one"},
		{Group: team, Body: "team one"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bob fragments = %#v, want %#v", got, want)
	}

	commit(t, archive, newsindex.NewBatch().Remove(newsindex.PublicGroup, "m1"))

	_, missingErr := archive.Get(alice, "does-not-exist")
	_, hiddenErr := archive.Get(alice, "m1")
	if !errors.Is(hiddenErr, newsindex.ErrMessageNotFound) || !errors.Is(missingErr, newsindex.ErrMessageNotFound) ||
		missingErr.Error() != hiddenErr.Error() {
		t.Fatalf("hidden lookup = %v, missing lookup = %v; want identical not-found results", hiddenErr, missingErr)
	}
	if got := ids(page(t, archive, alice, 10, "")); !reflect.DeepEqual(got, []newsindex.MessageID{"m2"}) {
		t.Fatalf("alice page = %v, want [m2]", got)
	}
	if got := message(t, archive, bob, "m1"); len(got.Fragments) != 1 || got.Fragments[0].Group != team {
		t.Fatalf("bob should still see the team fragment, got %#v", got)
	}

	commit(t, archive, newsindex.NewBatch().Remove(team, "m1"))
	if _, err := archive.Get(bob, "m1"); !errors.Is(err, newsindex.ErrMessageNotFound) {
		t.Fatalf("bob lookup after last association = %v, want ErrMessageNotFound", err)
	}

	// Re-publishing the last removed ID starts a new publication order.
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "m1", "republished"))
	if got := ids(page(t, archive, alice, 10, "")); !reflect.DeepEqual(got, []newsindex.MessageID{"m2", "m1"}) {
		t.Fatalf("republished order = %v, want [m2 m1]", got)
	}
}

func TestOrderUsesEarliestFragmentVisibleToThatPrincipal(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(team, "m2", "team two").
		Put(newsindex.PublicGroup, "m1", "public one").
		Put(newsindex.PublicGroup, "m3", "public three").
		Grant(bob, team))

	if got, want := ids(page(t, archive, alice, 10, "")), []newsindex.MessageID{"m1", "m3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alice order = %v, want %v", got, want)
	}
	if got, want := ids(page(t, archive, bob, 10, "")), []newsindex.MessageID{"m2", "m1", "m3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bob order = %v, want %v", got, want)
	}

	// m2's private fragment was published first, but that order is invisible
	// to Alice. Her first visible fragment for m2 is this new public one, so
	// it must sort after m1 and m3 rather than jumping to the front.
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "m2", "public two"))
	if got, want := ids(page(t, archive, alice, 10, "")), []newsindex.MessageID{"m1", "m3", "m2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alice order after public m2 = %v, want %v", got, want)
	}
	if got, want := ids(page(t, archive, bob, 10, "")), []newsindex.MessageID{"m2", "m1", "m3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bob order after public m2 = %v, want %v", got, want)
	}
}

func TestReplyEdgesAreVisibleOnlyWhenBothEndpointsAre(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "A", "a").
		Put(newsindex.PublicGroup, "B", "b").
		Put(newsindex.PublicGroup, "C", "c").
		Put(team, "D", "d").
		Grant(bob, team))

	commit(t, archive, newsindex.NewBatch().
		Link("A", "B").
		Link("B", "A").                          // cycle
		LinkIn(newsindex.PublicGroup, "A", "B"). // duplicate pair, different group
		LinkIn(team, "A", "B").                  // duplicate pair, another group
		Link("C", "A").
		Link("B", "B"). // self reply
		Link("A", "D"))

	a := message(t, archive, alice, "A")
	if got, want := a.Replies, []newsindex.MessageID{"B"}; !reflect.DeepEqual(got, want) || a.ReplyCount != 1 {
		t.Fatalf("A replies = %v (count %d), want %v (count 1)", got, a.ReplyCount, want)
	}
	if got, want := a.ReferencedBy, []newsindex.MessageID{"B", "C"}; !reflect.DeepEqual(got, want) || a.ReferencedByCount != 2 {
		t.Fatalf("A referenced by = %v (count %d), want %v (count 2)", got, a.ReferencedByCount, want)
	}
	b := message(t, archive, alice, "B")
	if got, want := b.Replies, []newsindex.MessageID{"A", "B"}; !reflect.DeepEqual(got, want) || b.ReplyCount != 2 {
		t.Fatalf("B replies = %v (count %d), want %v (count 2)", got, b.ReplyCount, want)
	}
	if got, want := b.ReferencedBy, []newsindex.MessageID{"A", "B"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("B referenced by = %v, want %v", got, want)
	}

	bobsA := message(t, archive, bob, "A")
	if got, want := bobsA.Replies, []newsindex.MessageID{"B", "D"}; !reflect.DeepEqual(got, want) || bobsA.ReplyCount != 2 {
		t.Fatalf("bob A replies = %v (count %d), want %v (count 2)", got, bobsA.ReplyCount, want)
	}

	// Removing one group association of a duplicated edge leaves the pair.
	commit(t, archive, newsindex.NewBatch().UnlinkIn(newsindex.PublicGroup, "A", "B"))
	if got := message(t, archive, alice, "A").ReplyCount; got != 1 {
		t.Fatalf("A reply count after partial unlink = %d, want 1", got)
	}
	commit(t, archive, newsindex.NewBatch().UnlinkIn(team, "A", "B"))
	if got := message(t, archive, alice, "A").ReplyCount; got != 1 {
		t.Fatalf("A reply count after second partial unlink = %d, want 1", got)
	}
	commit(t, archive, newsindex.NewBatch().Unlink("A", "B"))
	if got := message(t, archive, alice, "A").ReplyCount; got != 0 {
		t.Fatalf("A reply count after final unlink = %d, want 0", got)
	}

	// Hiding an endpoint hides every incident edge and recomputes counts.
	commit(t, archive, newsindex.NewBatch().Remove(newsindex.PublicGroup, "B"))
	a = message(t, archive, alice, "A")
	if got := a.ReferencedBy; !reflect.DeepEqual(got, []newsindex.MessageID{"C"}) || a.ReferencedByCount != 1 {
		t.Fatalf("A referenced by after B was hidden = %v (count %d), want [C] (count 1)", got, a.ReferencedByCount)
	}
}

func TestGrantingBothEndpointsMakesTheEdgeVisibleAtomically(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(team, "P", "parent").
		Put(team, "R", "reply").
		Link("P", "R"))
	if got := ids(page(t, archive, alice, 10, "")); len(got) != 0 {
		t.Fatalf("alice sees unauthorized messages: %v", got)
	}

	commit(t, archive, newsindex.NewBatch().Grant(alice, team))
	parent := message(t, archive, alice, "P")
	if got, want := parent.Replies, []newsindex.MessageID{"R"}; !reflect.DeepEqual(got, want) || parent.ReplyCount != 1 {
		t.Fatalf("parent replies = %v (count %d), want %v (count 1)", got, parent.ReplyCount, want)
	}
	reply := message(t, archive, alice, "R")
	if got, want := reply.ReferencedBy, []newsindex.MessageID{"P"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reply referenced by = %v, want %v", got, want)
	}
}

func TestCursorBindsPrincipalAuthorizationAndVisibleSnapshot(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "m1", "one").
		Put(newsindex.PublicGroup, "m2", "two").
		Put(newsindex.PublicGroup, "m3", "three").
		Put(newsindex.PublicGroup, "m4", "four").
		Put(newsindex.PublicGroup, "m5", "five"))

	first := page(t, archive, alice, 2, "")
	if got, want := ids(first), []newsindex.MessageID{"m1", "m2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first page = %v, want %v", got, want)
	}
	if first.Next == "" || first.Snapshot == "" {
		t.Fatalf("first page has no cursor or snapshot: %#v", first)
	}
	if _, err := archive.GetAt(alice, first.Snapshot, "m5"); err != nil {
		t.Fatalf("GetAt on pinned snapshot: %v", err)
	}

	// A hidden commit must not alter an already issued continuation.
	commit(t, archive, newsindex.NewBatch().
		Put(vault, "hidden", "private body 1").
		Link("hidden", "m1").
		Put(vault, "hidden2", "private body 2"))

	second := page(t, archive, alice, 2, first.Next)
	if got, want := ids(second), []newsindex.MessageID{"m3", "m4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second page after hidden commit = %v, want %v", got, want)
	}
	if second.Snapshot != first.Snapshot {
		t.Fatalf("hidden commit changed visible snapshot token: %q -> %q", first.Snapshot, second.Snapshot)
	}

	// Visible commits do not rewrite the snapshot a cursor is pinned to.
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "m6", "six"))
	third := page(t, archive, alice, 2, second.Next)
	if got, want := ids(third), []newsindex.MessageID{"m5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("third page on pinned snapshot = %v, want %v", got, want)
	}
	if third.Next != "" {
		t.Fatalf("original snapshot should be exhausted, got next %q", third.Next)
	}
	if got, want := ids(page(t, archive, alice, 10, "")), []newsindex.MessageID{"m1", "m2", "m3", "m4", "m5", "m6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("latest page = %v, want %v", got, want)
	}
	if _, err := archive.GetAt(alice, first.Snapshot, "m6"); !errors.Is(err, newsindex.ErrMessageNotFound) {
		t.Fatalf("GetAt old snapshot for new message = %v, want ErrMessageNotFound", err)
	}

	// Same cursor, different principal.
	if _, err := archive.Page(bob, newsindex.PageRequest{Limit: 2, Cursor: first.Next}); !errors.Is(err, newsindex.ErrCursorPrincipalMismatch) {
		t.Fatalf("cross-principal cursor error = %v, want ErrCursorPrincipalMismatch", err)
	}

	// Authorization generation changes invalidate old cursors for that user.
	bobPage := page(t, archive, bob, 2, "")
	commit(t, archive, newsindex.NewBatch().
		Put(team, "private-early", "private").
		Grant(bob, team))
	if _, err := archive.Page(bob, newsindex.PageRequest{Limit: 2, Cursor: bobPage.Next}); !errors.Is(err, newsindex.ErrStaleCursor) {
		t.Fatalf("cursor after grant error = %v, want ErrStaleCursor", err)
	}
	if got, want := ids(page(t, archive, alice, 10, "")), []newsindex.MessageID{"m1", "m2", "m3", "m4", "m5", "m6"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("another principal's grant changed alice's view: %v", got)
	}

	if _, err := archive.Page(alice, newsindex.PageRequest{Limit: 2, Cursor: first.Next + "x"}); !errors.Is(err, newsindex.ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v, want ErrInvalidCursor", err)
	}
}

func TestRevocationInvalidatesCursorAndRepaginatesWithoutSkipping(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(team, "t1", "team one").
		Put(newsindex.PublicGroup, "p1", "public one").
		Put(team, "t2", "team two").
		Put(newsindex.PublicGroup, "p2", "public two").
		Put(team, "t3", "team three").
		Put(newsindex.PublicGroup, "p3", "public three").
		Grant(bob, team))

	before := page(t, archive, bob, 2, "")
	if got, want := ids(before), []newsindex.MessageID{"t1", "p1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before revocation = %v, want %v", got, want)
	}

	commit(t, archive, newsindex.NewBatch().Revoke(bob, team))
	if _, err := archive.Page(bob, newsindex.PageRequest{Limit: 2, Cursor: before.Next}); !errors.Is(err, newsindex.ErrStaleCursor) {
		t.Fatalf("cursor after revocation error = %v, want ErrStaleCursor", err)
	}

	first := page(t, archive, bob, 2, "")
	second := page(t, archive, bob, 2, first.Next)
	if got, want := ids(first), []newsindex.MessageID{"p1", "p2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first post-revocation page = %v, want %v", got, want)
	}
	if got, want := ids(second), []newsindex.MessageID{"p3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second post-revocation page = %v, want %v", got, want)
	}
	if second.Next != "" {
		t.Fatalf("unexpected continuation after last visible message: %q", second.Next)
	}

	// Regranting restores the original first-publication order, but old
	// cursors stay stale because the authorization generation moved again.
	commit(t, archive, newsindex.NewBatch().Grant(bob, team))
	if _, err := archive.Page(bob, newsindex.PageRequest{Limit: 2, Cursor: first.Next}); !errors.Is(err, newsindex.ErrStaleCursor) {
		t.Fatalf("cursor after regrant error = %v, want ErrStaleCursor", err)
	}
	all := page(t, archive, bob, 10, "")
	if got, want := ids(all), []newsindex.MessageID{"t1", "p1", "t2", "p2", "t3", "p3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-regrant view = %v, want %v", got, want)
	}
}

func TestAuthorizationChangeWithIdenticalViewStillReissuesValidCursor(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "m1", "one").
		Put(newsindex.PublicGroup, "m2", "two").
		Put(newsindex.PublicGroup, "m3", "three"))

	before := page(t, archive, alice, 1, "")
	if before.AuthGeneration != 0 {
		t.Fatalf("initial auth generation = %d, want 0", before.AuthGeneration)
	}

	// Granting an empty group changes authorization generation without
	// changing the visible view or its snapshot token.
	commit(t, archive, newsindex.NewBatch().Grant(alice, vault))
	if _, err := archive.Page(alice, newsindex.PageRequest{Limit: 1, Cursor: before.Next}); !errors.Is(err, newsindex.ErrStaleCursor) {
		t.Fatalf("old cursor error = %v, want ErrStaleCursor", err)
	}

	fresh := page(t, archive, alice, 1, "")
	if fresh.AuthGeneration != 1 || fresh.Snapshot != before.Snapshot {
		t.Fatalf("fresh page = %#v, want generation 1 and unchanged token %q", fresh, before.Snapshot)
	}
	next := page(t, archive, alice, 1, fresh.Next)
	if got, want := ids(next), []newsindex.MessageID{"m2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("continuation = %v, want %v", got, want)
	}
	if next.AuthGeneration != 1 || next.Next == "" {
		t.Fatalf("continuation = %#v, want generation 1 with another cursor", next)
	}
}

func TestPrivateEditsAndHiddenInsertionsDoNotChangePublicOutput(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "m1", "public one").
		Put(newsindex.PublicGroup, "m2", "public two").
		Put(team, "m1", "team one").
		Grant(bob, team))

	before := page(t, archive, alice, 1, "")
	beforeChanges, err := archive.Changes(alice, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Add a private-only message that sorts before m1, edit m1's private
	// fragment, and link the hidden message to m1.
	commit(t, archive, newsindex.NewBatch().
		Put(team, "hidden-early", "private early").
		Put(team, "m1", "team one edited").
		Link("hidden-early", "m1"))

	after := page(t, archive, alice, 1, "")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("private edits changed alice page:\nbefore %#v\nafter  %#v", before, after)
	}
	afterChanges, err := archive.Changes(alice, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeChanges, afterChanges) {
		t.Fatalf("private edits changed alice notifications:\nbefore %#v\nafter  %#v", beforeChanges, afterChanges)
	}

	// The authorized private reader must still see the edit.
	if got := message(t, archive, bob, "m1").Fragments; got[1].Body != "team one edited" {
		t.Fatalf("bob team fragment = %#v, want edited body", got)
	}

	// Removing the hidden message and m1's private fragment is also inert
	// for the public reader.
	commit(t, archive, newsindex.NewBatch().
		Remove(team, "hidden-early").
		Remove(team, "m1"))
	if got := page(t, archive, alice, 1, ""); !reflect.DeepEqual(got, before) {
		t.Fatalf("hidden deletion changed alice page: %#v, want %#v", got, before)
	}
}

func TestSameBatchRemoveAndReaddPreservesOrderAndProducesNoChange(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "m1", "one").
		Put(newsindex.PublicGroup, "m2", "two").
		Put(newsindex.PublicGroup, "m3", "three"))

	commit(t, archive, newsindex.NewBatch().
		Remove(newsindex.PublicGroup, "m2").
		Put(newsindex.PublicGroup, "m2", "two"))

	if got, want := ids(page(t, archive, alice, 10, "")), []newsindex.MessageID{"m1", "m2", "m3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order after transient remove/readd = %v, want %v", got, want)
	}
	changes, err := archive.Changes(alice, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("net no-op batch produced notifications: %#v", changes)
	}
}

func TestBatchNotificationsUseOnlyFinalVisibleDifference(t *testing.T) {
	archive := newsindex.New()

	// A message is briefly public and then private inside one batch. Alice
	// must not receive the transient public body; Bob sees only the final
	// private view.
	commit(t, archive, newsindex.NewBatch().
		Put(newsindex.PublicGroup, "x", "secret draft").
		Remove(newsindex.PublicGroup, "x").
		Put(team, "x", "final private body").
		Grant(bob, team))

	aliceChanges, err := archive.Changes(alice, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceChanges) != 0 {
		t.Fatalf("alice saw transient public content: %#v", aliceChanges)
	}
	if _, err := archive.Get(alice, "x"); !errors.Is(err, newsindex.ErrMessageNotFound) {
		t.Fatalf("alice Get(x) = %v, want ErrMessageNotFound", err)
	}

	bobChanges, err := archive.Changes(bob, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobChanges) != 1 || len(bobChanges[0].Changes) != 1 {
		t.Fatalf("bob changes = %#v, want one notification with one change", bobChanges)
	}
	bobChange := bobChanges[0].Changes[0]
	if bobChange.Kind != newsindex.ChangeAdded || bobChange.Message == nil {
		t.Fatalf("bob change = %#v, want added message", bobChange)
	}
	if got, want := bobChange.Message.Fragments, []newsindex.FragmentView{{Group: team, Body: "final private body"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bob notification fragments = %#v, want %#v", got, want)
	}

	// Editing a visible fragment then hiding it in the same batch is seen by
	// Alice only as a removal, never with the new private body.
	commit(t, archive, newsindex.NewBatch().
		Put(team, "x", "edited before hide").
		Put(newsindex.PublicGroup, "x", "new public body").
		Remove(newsindex.PublicGroup, "x").
		Put(team, "x", "private after edit"))
	aliceChanges, err = archive.Changes(alice, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceChanges) != 0 {
		t.Fatalf("alice should see no change for an always-private edit: %#v", aliceChanges)
	}

	// A private fragment that becomes public in the same batch must notify
	// only the final public body to Alice.
	commit(t, archive, newsindex.NewBatch().
		Put(team, "z", "private draft").
		Put(newsindex.PublicGroup, "z", "public final"))
	aliceChanges, err = archive.Changes(alice, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceChanges) != 1 || len(aliceChanges[0].Changes) != 1 || aliceChanges[0].Changes[0].Message == nil {
		t.Fatalf("alice changes = %#v, want one added message", aliceChanges)
	}
	if got, want := aliceChanges[0].Changes[0].Message.Fragments, []newsindex.FragmentView{{Group: newsindex.PublicGroup, Body: "public final"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alice notification leaked private fragment: %#v, want %#v", got, want)
	}

	// Hidden-only commits produce no notifications.
	commit(t, archive, newsindex.NewBatch().
		Put(vault, "hidden", "hidden body").
		Link("hidden", "z").
		Put(vault, "hidden", "hidden body edited"))
	aliceChanges, err = archive.Changes(alice, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceChanges) != 0 {
		t.Fatalf("hidden commit produced alice notifications: %#v", aliceChanges)
	}

	// Content updates produce exactly one update entry for the changed
	// message.
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "z", "public final v2"))
	aliceChanges, err = archive.Changes(alice, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceChanges) != 1 || len(aliceChanges[0].Changes) != 1 ||
		aliceChanges[0].Changes[0].Kind != newsindex.ChangeUpdated ||
		aliceChanges[0].Changes[0].ID != "z" {
		t.Fatalf("update notification = %#v, want one update of z", aliceChanges)
	}
}

func TestBatchPublicThenHiddenNotifiesRemovalOnly(t *testing.T) {
	archive := newsindex.New()
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "y", "public y"))

	// The message had a public view before the batch. Moving it to a group
	// Alice cannot see must produce a removal, never the replacement body.
	commit(t, archive, newsindex.NewBatch().
		Put(team, "y", "secret replacement").
		Remove(newsindex.PublicGroup, "y"))

	changes, err := archive.Changes(alice, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || len(changes[0].Changes) != 1 {
		t.Fatalf("alice changes = %#v, want one removal", changes)
	}
	change := changes[0].Changes[0]
	if change.Kind != newsindex.ChangeRemoved || change.ID != "y" || change.Message != nil {
		t.Fatalf("alice change = %#v, want bodyless removal of y", change)
	}
	if _, err := archive.Get(alice, "y"); !errors.Is(err, newsindex.ErrMessageNotFound) {
		t.Fatalf("alice Get(y) = %v, want ErrMessageNotFound", err)
	}
}

func TestSubscriptionPollAdvancesPastEmptyCommits(t *testing.T) {
	archive := newsindex.New()
	subscription, err := archive.Subscribe(alice, 0)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, archive, newsindex.NewBatch().Put(newsindex.PublicGroup, "m1", "one"))
	first, err := subscription.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Changes[0].ID != "m1" {
		t.Fatalf("first poll = %#v, want m1", first)
	}
	commit(t, archive, newsindex.NewBatch().Put(vault, "hidden", "private"))
	second, err := subscription.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("hidden commit notifications = %#v, want none", second)
	}
	if got, want := subscription.NextSnapshot(), archive.CurrentSnapshot()+1; got != want {
		t.Fatalf("subscription next = %d, want %d", got, want)
	}
}

func TestMalformedInputsAndMissingMessages(t *testing.T) {
	archive := newsindex.New()
	if _, err := archive.Commit(nil); !errors.Is(err, newsindex.ErrNilBatch) {
		t.Fatalf("nil batch error = %v", err)
	}
	if _, err := archive.Commit(newsindex.NewBatch().Put("", "m", "body")); !errors.Is(err, newsindex.ErrInvalidOperation) {
		t.Fatalf("invalid operation error = %v", err)
	}
	if _, err := archive.Page(alice, newsindex.PageRequest{Limit: 0}); !errors.Is(err, newsindex.ErrInvalidPageSize) {
		t.Fatalf("invalid page size error = %v", err)
	}
	if _, err := archive.Get(alice, "missing"); !errors.Is(err, newsindex.ErrMessageNotFound) {
		t.Fatalf("missing message error = %v", err)
	}
	if _, err := archive.Changes(alice, 1); !errors.Is(err, newsindex.ErrFutureSnapshot) {
		t.Fatalf("future changes error = %v", err)
	}
	if _, err := archive.Subscribe(alice, 1); !errors.Is(err, newsindex.ErrFutureSnapshot) {
		t.Fatalf("future subscribe error = %v", err)
	}
}
