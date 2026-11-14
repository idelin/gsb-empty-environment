package netsec_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0505"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestManager(t *testing.T, ttl time.Duration, clock *fakeClock) *netsec.Manager {
	t.Helper()
	manager, err := netsec.NewManager(netsec.Options{
		TokenTTL: ttl,
		Clock:    clock.Now,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func mustRequest(t *testing.T, manager *netsec.Manager, list, address, requester string) netsec.Confirmation {
	t.Helper()
	confirmation, err := manager.RequestSubscription(context.Background(), netsec.SubscriptionRequest{
		List:      list,
		Address:   address,
		Requester: requester,
	})
	if err != nil {
		t.Fatalf("RequestSubscription(%q, %q, %q): %v", list, address, requester, err)
	}
	return confirmation
}

func mustConfirm(t *testing.T, manager *netsec.Manager, confirmation netsec.Confirmation) netsec.Subscription {
	t.Helper()
	subscription, err := manager.ConfirmSubscription(context.Background(), netsec.ConfirmationRequest{
		Token:     confirmation.Token,
		List:      confirmation.List,
		Address:   confirmation.Address,
		Requester: confirmation.Requester,
	})
	if err != nil {
		t.Fatalf("ConfirmSubscription: %v", err)
	}
	return subscription
}

func confirm(t *testing.T, manager *netsec.Manager, token, list, address, requester string) error {
	t.Helper()
	_, err := manager.ConfirmSubscription(context.Background(), netsec.ConfirmationRequest{
		Token:     token,
		List:      list,
		Address:   address,
		Requester: requester,
	})
	return err
}

func TestLegitimateDoubleOptInOnlyDeliversToConfirmedAddress(t *testing.T) {
	start := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	manager := newTestManager(t, 30*time.Minute, clock)

	confirmation := mustRequest(t, manager, " News.Announce ", " Alice@Example.COM ", " account-42 ")
	if confirmation.List != "news.announce" {
		t.Fatalf("canonical list = %q, want news.announce", confirmation.List)
	}
	if confirmation.Address != "alice@example.com" {
		t.Fatalf("canonical address = %q, want alice@example.com", confirmation.Address)
	}
	if confirmation.Requester != "account-42" {
		t.Fatalf("canonical requester = %q, want account-42", confirmation.Requester)
	}
	if want := start.Add(30 * time.Minute); !confirmation.ExpiresAt.Equal(want) {
		t.Fatalf("expires at %v, want %v", confirmation.ExpiresAt, want)
	}
	if confirmation.Token == "" {
		t.Fatal("confirmation token is empty")
	}

	if err := manager.AuthorizeDelivery(context.Background(), "news.announce", "alice@example.com"); !errors.Is(err, netsec.ErrNotConfirmed) {
		t.Fatalf("delivery before confirmation = %v, want ErrNotConfirmed", err)
	}

	subscription := mustConfirm(t, manager, confirmation)
	if subscription.List != "news.announce" || subscription.Address != "alice@example.com" {
		t.Fatalf("unexpected subscription: %+v", subscription)
	}
	if !subscription.ConfirmedAt.Equal(start) {
		t.Fatalf("confirmed at %v, want %v", subscription.ConfirmedAt, start)
	}

	if !manager.CanDeliver(context.Background(), "NEWS.ANNOUNCE", "ALICE@example.com") {
		t.Fatal("confirmed address should be authorized for its list")
	}
	if manager.CanDeliver(context.Background(), "news.other", "alice@example.com") {
		t.Fatal("address must not be authorized for another list")
	}
	if manager.CanDeliver(context.Background(), "news.announce", "bob@example.com") {
		t.Fatal("another address must not be authorized")
	}

	if err := confirm(t, manager, confirmation.Token, confirmation.List, confirmation.Address, confirmation.Requester); !errors.Is(err, netsec.ErrTokenConsumed) {
		t.Fatalf("second token use = %v, want ErrTokenConsumed", err)
	}
	if _, err := manager.RequestSubscription(context.Background(), netsec.SubscriptionRequest{
		List:      "news.announce",
		Address:   "alice@example.com",
		Requester: "account-42",
	}); !errors.Is(err, netsec.ErrAlreadySubscribed) {
		t.Fatalf("duplicate request = %v, want ErrAlreadySubscribed", err)
	}
}

func TestForgedConfirmationRejected(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)
	confirmation := mustRequest(t, manager, "security", "victim@example.com", "session-victim")

	tampered := []byte(confirmation.Token)
	if tampered[0] == 'A' {
		tampered[0] = 'B'
	} else {
		tampered[0] = 'A'
	}

	for name, token := range map[string]string{
		"empty":      "",
		"random":     strings.Repeat("Z", 43),
		"tampered":   string(tampered),
		"not-base64": "***not-a-token***",
		"oversized":  strings.Repeat("A", 4096),
	} {
		t.Run(name, func(t *testing.T) {
			err := confirm(t, manager, token, "security", "victim@example.com", "session-victim")
			if !errors.Is(err, netsec.ErrTokenInvalid) {
				t.Fatalf("forged confirmation = %v, want ErrTokenInvalid", err)
			}
			if manager.CanDeliver(context.Background(), "security", "victim@example.com") {
				t.Fatal("forged confirmation authorized delivery")
			}
		})
	}

	// Failed forgeries must not burn the real token.
	mustConfirm(t, manager, confirmation)
	if !manager.CanDeliver(context.Background(), "security", "victim@example.com") {
		t.Fatal("legitimate confirmation should still succeed after forged attempts")
	}
}

func TestCrossListConfirmationRejected(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)
	confirmation := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")

	err := confirm(t, manager, confirmation.Token, "list-b", "alice@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrListMismatch) {
		t.Fatalf("cross-list confirmation = %v, want ErrListMismatch", err)
	}
	if manager.CanDeliver(context.Background(), "list-a", "alice@example.com") {
		t.Fatal("cross-list attempt must not confirm list-a")
	}
	if manager.CanDeliver(context.Background(), "list-b", "alice@example.com") {
		t.Fatal("cross-list attempt must not confirm list-b")
	}

	mustConfirm(t, manager, confirmation)
	if !manager.CanDeliver(context.Background(), "list-a", "alice@example.com") {
		t.Fatal("token should remain usable for its bound list")
	}
	if manager.CanDeliver(context.Background(), "list-b", "alice@example.com") {
		t.Fatal("confirmation leaked into another list")
	}
}

func TestAddressForgeryRejected(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)
	confirmation := mustRequest(t, manager, "list-a", "victim@example.com", "session-alice")

	err := confirm(t, manager, confirmation.Token, "list-a", "attacker@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrAddressMismatch) {
		t.Fatalf("address-swapped confirmation = %v, want ErrAddressMismatch", err)
	}
	if manager.CanDeliver(context.Background(), "list-a", "attacker@example.com") {
		t.Fatal("attacker address was authorized")
	}
	if manager.CanDeliver(context.Background(), "list-a", "victim@example.com") {
		t.Fatal("victim address was authorized before its confirmation")
	}

	mustConfirm(t, manager, confirmation)
	if manager.CanDeliver(context.Background(), "list-a", "attacker@example.com") {
		t.Fatal("confirmation authorized an address other than the token's address")
	}
	if !manager.CanDeliver(context.Background(), "list-a", "victim@example.com") {
		t.Fatal("legitimate address was not authorized")
	}
}

func TestForwardedConfirmationRejectedByRequesterBinding(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)
	confirmation := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")

	err := confirm(t, manager, confirmation.Token, "list-a", "alice@example.com", "session-bob")
	if !errors.Is(err, netsec.ErrRequesterMismatch) {
		t.Fatalf("forwarded confirmation = %v, want ErrRequesterMismatch", err)
	}
	if manager.CanDeliver(context.Background(), "list-a", "alice@example.com") {
		t.Fatal("forwarded confirmation authorized delivery")
	}

	mustConfirm(t, manager, confirmation)
	if !manager.CanDeliver(context.Background(), "list-a", "alice@example.com") {
		t.Fatal("original requester should still be able to confirm")
	}
}

func TestExpiredConfirmationRejected(t *testing.T) {
	start := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	manager := newTestManager(t, 10*time.Minute, clock)

	early := mustRequest(t, manager, "early", "alice@example.com", "session-alice")
	clock.Advance(10*time.Minute - time.Second)
	mustConfirm(t, manager, early)
	clock.Advance(time.Second)

	late := mustRequest(t, manager, "late", "alice@example.com", "session-alice")
	clock.Advance(10 * time.Minute)
	err := confirm(t, manager, late.Token, "late", "alice@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrTokenExpired) {
		t.Fatalf("expired confirmation = %v, want ErrTokenExpired", err)
	}
	if manager.CanDeliver(context.Background(), "late", "alice@example.com") {
		t.Fatal("expired token authorized delivery")
	}

	clock.Advance(time.Hour)
	err = confirm(t, manager, late.Token, "late", "alice@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrTokenExpired) {
		t.Fatalf("expired token after time passed = %v, want ErrTokenExpired", err)
	}

	replacement := mustRequest(t, manager, "late", "alice@example.com", "session-alice")
	if !replacement.ExpiresAt.Equal(clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("replacement expiry = %v, want %v", replacement.ExpiresAt, clock.Now().Add(10*time.Minute))
	}
	mustConfirm(t, manager, replacement)
	if !manager.CanDeliver(context.Background(), "late", "alice@example.com") {
		t.Fatal("fresh confirmation should be accepted")
	}
}

func TestConcurrentConfirmationConsumesTokenOnce(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)
	confirmation := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")

	const workers = 64
	errs := make(chan error, workers)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	done.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer done.Done()
			start.Wait()
			errs <- confirm(t, manager, confirmation.Token, "list-a", "alice@example.com", "session-alice")
		}()
	}
	start.Done()
	done.Wait()
	close(errs)

	successes := 0
	consumed := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, netsec.ErrTokenConsumed):
			consumed++
		default:
			t.Errorf("unexpected concurrent confirmation error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumptions = %d, want 1", successes)
	}
	if consumed != workers-1 {
		t.Fatalf("consumed rejections = %d, want %d", consumed, workers-1)
	}
}

func TestUnsubscribeRevokesPendingTokenAndIsListScoped(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)

	confirmedA := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")
	mustConfirm(t, manager, confirmedA)
	pendingB := mustRequest(t, manager, "list-b", "alice@example.com", "session-alice")

	if err := manager.Unsubscribe(context.Background(), "list-a", "alice@example.com"); err != nil {
		t.Fatalf("Unsubscribe(list-a): %v", err)
	}
	if err := manager.AuthorizeDelivery(context.Background(), "list-a", "alice@example.com"); !errors.Is(err, netsec.ErrUnsubscribed) {
		t.Fatalf("delivery after unsubscribe = %v, want ErrUnsubscribed", err)
	}

	// list-b was not unsubscribed, and its pending token must remain valid.
	mustConfirm(t, manager, pendingB)
	if !manager.CanDeliver(context.Background(), "list-b", "alice@example.com") {
		t.Fatal("unsubscribing list-a revoked list-b")
	}

	// A fresh double opt-in restores list-a, but only after confirmation.
	renewA := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")
	if err := manager.AuthorizeDelivery(context.Background(), "list-a", "alice@example.com"); !errors.Is(err, netsec.ErrUnsubscribed) {
		t.Fatalf("delivery before renewed confirmation = %v, want ErrUnsubscribed", err)
	}
	mustConfirm(t, manager, renewA)
	if !manager.CanDeliver(context.Background(), "list-a", "alice@example.com") {
		t.Fatal("renewed confirmation did not restore delivery")
	}

	// Unsubscribe revokes a token that was issued but not yet confirmed.
	pendingC := mustRequest(t, manager, "list-c", "alice@example.com", "session-alice")
	if err := manager.Unsubscribe(context.Background(), "list-c", "alice@example.com"); err != nil {
		t.Fatalf("Unsubscribe(list-c): %v", err)
	}
	err := confirm(t, manager, pendingC.Token, "list-c", "alice@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrTokenRevoked) {
		t.Fatalf("confirmation after unsubscribe = %v, want ErrTokenRevoked", err)
	}
	if manager.CanDeliver(context.Background(), "list-c", "alice@example.com") {
		t.Fatal("revoked confirmation authorized delivery")
	}
}

func TestComplaintRevokesAllSubscriptionsAndPendingTokens(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)

	confirmedB := mustRequest(t, manager, "list-b", "alice@example.com", "session-alice")
	mustConfirm(t, manager, confirmedB)
	pendingA := mustRequest(t, manager, "list-a", "alice@example.com", "session-alice")

	if err := manager.Complaint(context.Background(), "list-a", "alice@example.com"); err != nil {
		t.Fatalf("Complaint: %v", err)
	}

	err := confirm(t, manager, pendingA.Token, "list-a", "alice@example.com", "session-alice")
	if !errors.Is(err, netsec.ErrTokenRevoked) {
		t.Fatalf("confirmation after complaint = %v, want ErrTokenRevoked", err)
	}
	for _, list := range []string{"list-a", "list-b", "list-c"} {
		if err := manager.AuthorizeDelivery(context.Background(), list, "alice@example.com"); !errors.Is(err, netsec.ErrComplained) {
			t.Fatalf("delivery on %s after complaint = %v, want ErrComplained", list, err)
		}
	}

	if _, err := manager.RequestSubscription(context.Background(), netsec.SubscriptionRequest{
		List:      "list-c",
		Address:   "alice@example.com",
		Requester: "session-alice",
	}); !errors.Is(err, netsec.ErrComplained) {
		t.Fatalf("new request after complaint = %v, want ErrComplained", err)
	}

	// A later unsubscribe must not clear the hard complaint suppression.
	if err := manager.Unsubscribe(context.Background(), "list-b", "alice@example.com"); err != nil {
		t.Fatalf("Unsubscribe after complaint: %v", err)
	}
	if err := manager.AuthorizeDelivery(context.Background(), "list-b", "alice@example.com"); !errors.Is(err, netsec.ErrComplained) {
		t.Fatalf("delivery after complaint and unsubscribe = %v, want ErrComplained", err)
	}
}

func TestOnlyConsentingAddressReceivesDelivery(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	manager := newTestManager(t, time.Hour, clock)

	consenting := mustRequest(t, manager, "announce", "alice@example.com", "session-alice")
	mustConfirm(t, manager, consenting)

	// Bob was named by an attacker, but never confirmed.
	mustRequest(t, manager, "announce", "bob@example.com", "attacker-session")

	// Carol unsubscribed after confirming.
	carol := mustRequest(t, manager, "announce", "carol@example.com", "session-carol")
	mustConfirm(t, manager, carol)
	if err := manager.Unsubscribe(context.Background(), "announce", "carol@example.com"); err != nil {
		t.Fatalf("Unsubscribe(carol): %v", err)
	}

	// Dave complained.
	dave := mustRequest(t, manager, "announce", "dave@example.com", "session-dave")
	mustConfirm(t, manager, dave)
	if err := manager.Complaint(context.Background(), "announce", "dave@example.com"); err != nil {
		t.Fatalf("Complaint(dave): %v", err)
	}

	for address, want := range map[string]bool{
		"alice@example.com":       true,
		"alice+other@example.com": false,
		"bob@example.com":         false,
		"carol@example.com":       false,
		"dave@example.com":        false,
		"mallory@example.com":     false,
	} {
		if got := manager.CanDeliver(context.Background(), "announce", address); got != want {
			t.Errorf("CanDeliver(%q) = %v, want %v", address, got, want)
		}
	}
}

func TestInvalidInputRejected(t *testing.T) {
	manager, err := netsec.NewManager(netsec.Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	requests := []netsec.SubscriptionRequest{
		{List: "", Address: "alice@example.com", Requester: "session"},
		{List: "bad list", Address: "alice@example.com", Requester: "session"},
		{List: "list-a", Address: "not-an-address", Requester: "session"},
		{List: "list-a", Address: "Alice <alice@example.com>", Requester: "session"},
		{List: "list-a", Address: "alice@example.com, bob@example.com", Requester: "session"},
		{List: "list-a", Address: "alice@example.com", Requester: ""},
	}
	for _, request := range requests {
		if _, err := manager.RequestSubscription(context.Background(), request); err == nil {
			t.Errorf("RequestSubscription(%+v) unexpectedly succeeded", request)
		}
	}

	if _, err := netsec.NewManager(netsec.Options{TokenTTL: -time.Second}); err == nil {
		t.Fatal("NewManager accepted a negative token TTL")
	}
	if _, err := netsec.NewManager(netsec.Options{TokenTTL: 31 * 24 * time.Hour}); err == nil {
		t.Fatal("NewManager accepted an excessive token TTL")
	}
}
