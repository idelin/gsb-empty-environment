package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mustAuthorizer(t *testing.T, rules ...Rule) *Authorizer {
	t.Helper()
	authorizer, err := NewAuthorizerWithRules(rules...)
	if err != nil {
		t.Fatalf("NewAuthorizerWithRules: %v", err)
	}
	return authorizer
}

func mustSubscribe(t *testing.T, authorizer *Authorizer, tenant, exchange, binding string) *Subscription {
	t.Helper()
	subscription, err := authorizer.Subscribe(tenant, exchange, binding)
	if err != nil {
		t.Fatalf("Subscribe(%q, %q, %q): %v", tenant, exchange, binding, err)
	}
	return subscription
}

func requireDenied(t *testing.T, authorizer *Authorizer, tenant, exchange, binding string, target error) {
	t.Helper()
	_, err := authorizer.Subscribe(tenant, exchange, binding)
	if err == nil {
		t.Fatalf("Subscribe(%q, %q, %q) unexpectedly succeeded", tenant, exchange, binding)
	}
	if !errors.Is(err, ErrDenied) {
		t.Errorf("Subscribe(%q, %q, %q) error %v does not match ErrDenied", tenant, exchange, binding, err)
	}
	if target != nil && !errors.Is(err, target) {
		t.Errorf("Subscribe(%q, %q, %q) error %v does not match %v", tenant, exchange, binding, err, target)
	}
}

func receiveOne(t *testing.T, subscription *Subscription) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	return event
}

func requireNoEvent(t *testing.T, subscription *Subscription) {
	t.Helper()
	event, ok, err := subscription.TryReceive()
	if err != nil {
		t.Fatalf("TryReceive: %v", err)
	}
	if ok {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestHashEscalationCannotSubscribe(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created"},
	})

	for _, binding := range []string{"#", "tenant-a.#", "tenant-a.orders.#", "tenant-a.orders.*"} {
		requireDenied(t, authorizer, "tenant-a", "orders", binding, ErrPattern)
	}

	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")
	if delivered := authorizer.Publish(Event{
		Tenant:     "tenant-a",
		Exchange:   "orders",
		RoutingKey: "tenant-a.orders.created",
		Payload:    "authorized",
	}); delivered != 1 {
		t.Fatalf("Publish delivered %d events, want 1", delivered)
	}

	for _, event := range []Event{
		{Tenant: "tenant-b", Exchange: "orders", RoutingKey: "tenant-a.orders.created", Payload: "other-tenant"},
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders.created.eu", Payload: "deeper"},
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders.createdx", Payload: "partial-segment"},
	} {
		if delivered := authorizer.Publish(event); delivered != 0 {
			t.Errorf("Publish(%+v) delivered %d events, want 0", event, delivered)
		}
	}

	event := receiveOne(t, subscription)
	if event.Payload != "authorized" {
		t.Fatalf("payload = %v, want authorized", event.Payload)
	}
	requireNoEvent(t, subscription)
}

func TestPrefixExpansionCannotSubscribe(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.*"},
	})

	for _, binding := range []string{"#", "tenant-a.#", "tenant-a.orders.#", "tenant-a.orders.created.#", "tenant-a.*"} {
		requireDenied(t, authorizer, "tenant-a", "orders", binding, ErrPattern)
	}

	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", 1); delivered != 1 {
		t.Fatalf("authorized publish delivered %d, want 1", delivered)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.cancelled", 2); delivered != 0 {
		t.Fatalf("unrelated publish delivered %d, want 0", delivered)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created.eu", 3); delivered != 0 {
		t.Fatalf("prefix-expanded publish delivered %d, want 0", delivered)
	}

	if event := receiveOne(t, subscription); event.Payload != 1 {
		t.Fatalf("payload = %v, want 1", event.Payload)
	}
	requireNoEvent(t, subscription)
}

func TestCrossExchangeIsolation(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t,
		Rule{
			Tenant:   "tenant-a",
			Exchange: "orders",
			Patterns: []string{"tenant-a.orders.*"},
		},
		Rule{
			Tenant:   "tenant-a",
			Exchange: "payments",
			Patterns: []string{"tenant-a.payments.*"},
		},
	)

	requireDenied(t, authorizer, "tenant-a", "audit", "tenant-a.audit.created", ErrExchange)

	orders := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")
	payments := mustSubscribe(t, authorizer, "tenant-a", "payments", "tenant-a.payments.created")

	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", "orders"); delivered != 1 {
		t.Fatalf("orders publish delivered %d, want 1", delivered)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "payments", "tenant-a.orders.created", "wrong-exchange"); delivered != 0 {
		t.Fatalf("cross-exchange publish delivered %d, want 0", delivered)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "payments", "tenant-a.payments.created", "payments"); delivered != 1 {
		t.Fatalf("payments publish delivered %d, want 1", delivered)
	}

	if event := receiveOne(t, orders); event.Payload != "orders" {
		t.Fatalf("orders payload = %v", event.Payload)
	}
	if event := receiveOne(t, payments); event.Payload != "payments" {
		t.Fatalf("payments payload = %v", event.Payload)
	}
	requireNoEvent(t, orders)
	requireNoEvent(t, payments)
}

func TestACLUpdateRevokesInflightSubscription(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created"},
	})

	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")
	firstGeneration := subscription.Generation()

	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", "queued-before-update"); delivered != 1 {
		t.Fatalf("pre-update publish delivered %d, want 1", delivered)
	}

	nextGeneration, err := authorizer.SetRules([]Rule{{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.cancelled"},
	}})
	if err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if nextGeneration <= firstGeneration {
		t.Fatalf("generation did not advance: %d -> %d", firstGeneration, nextGeneration)
	}
	if !subscription.Revoked() {
		t.Fatal("in-flight subscription was not revoked after ACL update")
	}

	event, ok, err := subscription.TryReceive()
	if !errors.Is(err, ErrSubscriptionClosed) || ok {
		t.Fatalf("TryReceive after revoke = (%+v, %v, %v), want closed", event, ok, err)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", "after-update"); delivered != 0 {
		t.Fatalf("post-update publish delivered %d, want 0", delivered)
	}

	cancelled := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.cancelled")
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.cancelled", "new-policy"); delivered != 1 {
		t.Fatalf("new-policy publish delivered %d, want 1", delivered)
	}
	if event := receiveOne(t, cancelled); event.Payload != "new-policy" {
		t.Fatalf("new-policy payload = %v", event.Payload)
	}
}

func TestCompatibleACLUpdateKeepsSubscription(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created"},
	})

	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", "before"); delivered != 1 {
		t.Fatalf("publish before update delivered %d, want 1", delivered)
	}

	generation, err := authorizer.SetRules([]Rule{{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created", "tenant-a.orders.cancelled"},
	}})
	if err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	if subscription.Revoked() {
		t.Fatal("still-authorized subscription was revoked")
	}
	if got := subscription.Generation(); got != generation {
		t.Fatalf("subscription generation = %d, want %d", got, generation)
	}

	if event := receiveOne(t, subscription); event.Payload != "before" {
		t.Fatalf("queued payload = %v, want before", event.Payload)
	}
	if delivered := authorizer.PublishForTenant("tenant-a", "orders", "tenant-a.orders.created", "after"); delivered != 1 {
		t.Fatalf("publish after update delivered %d, want 1", delivered)
	}
	if event := receiveOne(t, subscription); event.Payload != "after" {
		t.Fatalf("post-update payload = %v, want after", event.Payload)
	}
}

func TestExactBindingReceivesOnlyAuthorizedEvents(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.created"},
	})

	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.created")

	authorized := Event{
		Tenant:     "tenant-a",
		Exchange:   "orders",
		RoutingKey: "tenant-a.orders.created",
		Payload:    "authorized",
	}
	if delivered := authorizer.Publish(authorized); delivered != 1 {
		t.Fatalf("authorized publish delivered %d, want 1", delivered)
	}

	unauthorized := []Event{
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders.cancelled"},
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders.created.eu"},
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders.createdx"},
		{Tenant: "tenant-a", Exchange: "orders", RoutingKey: "tenant-a.orders"},
		{Tenant: "tenant-b", Exchange: "orders", RoutingKey: "tenant-a.orders.created"},
		{Tenant: "tenant-a", Exchange: "payments", RoutingKey: "tenant-a.orders.created"},
	}
	for _, event := range unauthorized {
		if delivered := authorizer.Publish(event); delivered != 0 {
			t.Errorf("unauthorized event %+v delivered %d", event, delivered)
		}
	}

	if event := receiveOne(t, subscription); event.Payload != "authorized" {
		t.Fatalf("payload = %v, want authorized", event.Payload)
	}
	requireNoEvent(t, subscription)
}

func TestSingleWildcardMatchesOneSegment(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.*"},
	})
	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.*")

	for _, routingKey := range []string{
		"tenant-a.orders.created",
		"tenant-a.orders.cancelled",
		"tenant-a.orders",
		"tenant-a.orders.created.eu",
		"tenant-a.ordersx.created",
	} {
		authorizer.PublishForTenant("tenant-a", "orders", routingKey, routingKey)
	}

	if event := receiveOne(t, subscription); event.RoutingKey != "tenant-a.orders.created" {
		t.Fatalf("first routing key = %q", event.RoutingKey)
	}
	if event := receiveOne(t, subscription); event.RoutingKey != "tenant-a.orders.cancelled" {
		t.Fatalf("second routing key = %q", event.RoutingKey)
	}
	requireNoEvent(t, subscription)
}

func TestMultiWildcardMatchesZeroOrMoreSegments(t *testing.T) {
	t.Parallel()

	authorizer := mustAuthorizer(t, Rule{
		Tenant:   "tenant-a",
		Exchange: "orders",
		Patterns: []string{"tenant-a.orders.#"},
	})
	subscription := mustSubscribe(t, authorizer, "tenant-a", "orders", "tenant-a.orders.#")

	for _, routingKey := range []string{
		"tenant-a.orders",
		"tenant-a.orders.created",
		"tenant-a.orders.created.eu.1",
		"tenant-a.ordersx.created",
	} {
		authorizer.PublishForTenant("tenant-a", "orders", routingKey, routingKey)
	}

	for _, want := range []string{
		"tenant-a.orders",
		"tenant-a.orders.created",
		"tenant-a.orders.created.eu.1",
	} {
		if event := receiveOne(t, subscription); event.RoutingKey != want {
			t.Fatalf("routing key = %q, want %q", event.RoutingKey, want)
		}
	}
	requireNoEvent(t, subscription)
}
